package alerting

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/notify"
)

const maxAlertHistory = 100

// maxAlertDeliveries bounds concurrent webhook/channel deliveries. A burst of
// alerts while a destination hangs would otherwise start a goroutine (and a
// socket) per alert per channel; deliveries over the cap are dropped and
// logged, the alert itself stays in the history.
const maxAlertDeliveries = 16

// maxChannelBody bounds the message body handed to Slack / Telegram / email.
// Telegram rejects a sendMessage text over 4096 characters, and the title,
// source and server-identity lines share that budget; cron_failed carries the
// job's whole command line, which a tenant can make arbitrarily long, so an
// over-long alert was refused by the API and lost with only a log warning
// (F13300). The history and the generic webhook keep the full text.
const maxChannelBody = 3500

// Alert represents a single alert event.
type Alert struct {
	Time    time.Time `json:"time"`
	Level   string    `json:"level"` // "info", "warning", "critical"
	Type    string    `json:"type"`  // "domain_down", "cert_expiry", "rate_limit", "error_spike"
	Host    string    `json:"host"`
	Message string    `json:"message"`
}

// Alerter sends notifications on important events and keeps a ring buffer
// of recent alerts for the admin dashboard.
type Alerter struct {
	webhookURL string

	// channels are the Slack / Telegram / email destinations configured under
	// global.alerting. They were accepted by the settings API, stored, shown
	// in the panel and delivered to nobody: this package never imported
	// internal/notify, which implements and tests all three.
	channels []notify.Channel

	logger  *logger.Logger
	mu      sync.Mutex
	history []Alert
	pos     int
	full    bool
	enabled bool
	client  *http.Client

	// urlSafetyCheck and dialControl give the legacy webhook_url the same SSRF
	// policy as every other admin-configured webhook (internal/notify,
	// internal/webhook): checked before the request, on every redirect hop,
	// and at dial time against DNS rebinding. Tests that target loopback
	// servers set both to nil before the first Alert.
	urlSafetyCheck func(string) error
	dialControl    func(network, address string, c syscall.RawConn) error

	// sendChannel delivers one message to one channel: notify.Send in
	// production, replaced by tests (the channel senders are SSRF-checked, so a
	// test cannot point them at a loopback server).
	sendChannel func(notify.Channel, notify.Message) error

	// deliverSlots caps concurrent deliveries per destination
	// (maxAlertDeliveries each), so one hung destination cannot use up the
	// slots of the healthy ones. Destinations are the webhook and the channel
	// types, a small fixed set, so the total stays bounded.
	slotMu       sync.Mutex
	deliverSlots map[string]chan struct{}

	// Rate limit tracking for error_spike detection.
	errorWindow    []errorEntry
	errorWindowErr int // running count of error entries in errorWindow
	errorWindowMu  sync.Mutex
	lastSpike      time.Time // guarded by mu: when the last error_spike alert was claimed
}

type errorEntry struct {
	time  time.Time
	isErr bool
}

// New creates a new Alerter.
func New(enabled bool, webhookURL string, channels []notify.Channel, log *logger.Logger) *Alerter {
	a := &Alerter{
		webhookURL:     webhookURL,
		channels:       channels,
		logger:         log,
		enabled:        enabled,
		history:        make([]Alert, 0, maxAlertHistory),
		urlSafetyCheck: config.IsWebhookURLSafe,
		dialControl:    config.SafeDialControl,
		sendChannel:    notify.Send,
		deliverSlots:   make(map[string]chan struct{}),
	}
	a.client = &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, _ []*http.Request) error {
			if a.urlSafetyCheck == nil {
				return nil
			}
			return a.urlSafetyCheck(req.URL.String())
		},
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout: 10 * time.Second,
				Control: func(network, address string, c syscall.RawConn) error {
					if a.dialControl == nil {
						return nil
					}
					return a.dialControl(network, address, c)
				},
			}).DialContext,
		},
	}
	return a
}

// Update replaces the delivery settings after a config reload. They were fixed
// when the Alerter was built, so a changed Slack/Telegram/email destination, a
// rotated webhook URL, or alerting switched on or off in the panel never took
// effect until restart (F2860).
func (a *Alerter) Update(enabled bool, webhookURL string, channels []notify.Channel) {
	a.mu.Lock()
	a.enabled = enabled
	a.webhookURL = webhookURL
	a.channels = channels
	a.mu.Unlock()
}

// settings returns a consistent snapshot of the delivery settings.
func (a *Alerter) settings() (enabled bool, webhookURL string, channels []notify.Channel) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.enabled, a.webhookURL, a.channels
}

// Alert records an alert in the ring buffer and sends it via webhook if configured.
func (a *Alerter) Alert(alert Alert) {
	enabled, webhookURL, channels := a.settings()
	if !enabled {
		return
	}

	if alert.Time.IsZero() {
		alert.Time = time.Now()
	}

	a.mu.Lock()
	if len(a.history) < maxAlertHistory {
		a.history = append(a.history, alert)
		if len(a.history) == maxAlertHistory {
			a.full = true
		}
	} else {
		a.history[a.pos] = alert
		a.pos = (a.pos + 1) % maxAlertHistory
	}
	a.mu.Unlock()

	a.logger.Warn("alert",
		"level", alert.Level,
		"type", alert.Type,
		"host", alert.Host,
		"message", alert.Message,
	)

	if webhookURL != "" {
		a.deliver("webhook", alert.Type, func() {
			if err := a.sendWebhook(alert); err != nil {
				a.logger.Warn("webhook delivery failed", "error", err)
			}
		})
	}

	// Fan out to the configured channels. Each send is its own goroutine: an
	// unreachable SMTP server must not delay Slack, and none of them may
	// delay the caller — Alert is invoked from request-path recorders and
	// certificate renewal alike.
	for _, ch := range channels {
		if !ch.Enabled {
			continue
		}
		ch := ch
		a.deliver(ch.Type, alert.Type, func() {
			if err := a.sendChannel(ch, notify.Message{
				Level:  alert.Level,
				Title:  alertTitle(alert.Type),
				Body:   truncateForChannel(alert.Message, maxChannelBody),
				Source: alert.Host,
			}); err != nil {
				a.logger.Warn("alert channel delivery failed",
					"channel", ch.Type, "type", alert.Type, "error", err)
			}
		})
	}
}

// deliver runs fn on its own goroutine while fewer than maxAlertDeliveries
// are in flight for dest; otherwise the delivery is dropped (the caller must never
// block — Alert runs on request paths).
func (a *Alerter) deliver(dest, typ string, fn func()) {
	a.slotMu.Lock()
	if a.deliverSlots == nil {
		a.deliverSlots = make(map[string]chan struct{})
	}
	slots := a.deliverSlots[dest]
	if slots == nil {
		slots = make(chan struct{}, maxAlertDeliveries)
		a.deliverSlots[dest] = slots
	}
	a.slotMu.Unlock()
	select {
	case slots <- struct{}{}:
	default:
		a.logger.Warn("alert delivery dropped: too many deliveries in flight", "destination", dest, "type", typ)
		return
	}
	go func() {
		defer func() { <-slots }()
		fn()
	}()
}

// alertTitle turns the stable machine type into the human headline shown on
// Telegram / Slack / email. The type string itself stays on the wire and in
// the history API so filters and dedup keep working.
func alertTitle(typ string) string {
	switch typ {
	case "error_spike":
		return "5xx error spike"
	case "domain_down":
		return "Domain down"
	case "cert_expiry":
		return "Certificate expiring"
	case "rate_limit":
		return "Rate limit"
	case "cron_failed":
		return "Cron job failed"
	case "php_crashed":
		return "PHP crashed"
	default:
		if strings.HasPrefix(typ, "bandwidth_") {
			return "Bandwidth limit"
		}
		return typ
	}
}

// RecordRequest records a request result for error spike detection.
// Call this for every request; it tracks a 5-minute sliding window.
func (a *Alerter) RecordRequest(isError bool) {
	if enabled, _, _ := a.settings(); !enabled {
		return
	}

	now := time.Now()
	cutoff := now.Add(-5 * time.Minute)

	a.errorWindowMu.Lock()
	// Append new entry, keeping the running error count in sync (so we don't
	// re-sum the whole window — up to maxWindowSize entries — on every request).
	a.errorWindow = append(a.errorWindow, errorEntry{time: now, isErr: isError})
	if isError {
		a.errorWindowErr++
	}

	// Prune old entries
	start := 0
	for start < len(a.errorWindow) && a.errorWindow[start].time.Before(cutoff) {
		if a.errorWindow[start].isErr {
			a.errorWindowErr--
		}
		start++
	}
	if start > 0 {
		// Reslice instead of copying: this runs on every request, and a
		// copy of the whole window per expired entry made each request
		// allocate O(window) bytes (F4300). append reallocates when the
		// shrunk capacity runs out, so the memory is still released.
		a.errorWindow = a.errorWindow[start:]
	}
	// Cap the window size to prevent unbounded growth under high load.
	const maxWindowSize = 100000
	if len(a.errorWindow) > maxWindowSize {
		drop := len(a.errorWindow) - maxWindowSize
		for _, e := range a.errorWindow[:drop] {
			if e.isErr {
				a.errorWindowErr--
			}
		}
		a.errorWindow = a.errorWindow[drop:]
	}

	total := len(a.errorWindow)
	errors := a.errorWindowErr
	a.errorWindowMu.Unlock()

	// Fire alert if error rate > 10% and we have a meaningful sample
	if total >= 10 && float64(errors)/float64(total) > 0.10 {
		// Deduplicate: only alert once per minute. The slot is claimed under
		// the same lock as the check; concurrent recorders used to all pass
		// the check before any of them recorded the alert (F8200).
		a.mu.Lock()
		shouldAlert := now.Sub(a.lastSpike) >= time.Minute
		for i := 0; shouldAlert && i < len(a.history); i++ {
			h := a.history[i]
			if h.Type == "error_spike" && now.Sub(h.Time) < time.Minute {
				shouldAlert = false
			}
		}
		if shouldAlert {
			a.lastSpike = now
		}
		a.mu.Unlock()

		if shouldAlert {
			pct := float64(errors) / float64(total) * 100
			a.Alert(Alert{
				Level: "warning",
				Type:  "error_spike",
				// Spell out what "error" means (HTTP 5xx only) and that the
				// window is site-wide — otherwise the Telegram headline reads
				// like a cryptic metric dump.
				Message: itoa(errors) + " of " + itoa(total) + " requests (" + ftoa(pct) +
					"%) returned HTTP 5xx in the last 5 minutes across all sites. " +
					"Typical causes: PHP/app crash, upstream timeout, or a bad deploy.",
			})
		}
	}
}

// Alerts returns the most recent alerts (up to 100), newest first.
func (a *Alerter) Alerts() []Alert {
	a.mu.Lock()
	defer a.mu.Unlock()

	if len(a.history) == 0 {
		return nil
	}

	// When history is still growing (len < maxAlertHistory), entries are at
	// history[0]..history[len-1]; iterate newest-first from the end.
	// When full, entries are at history[0..pos-1] and history[pos..cap-1];
	// newest is at (pos-1+cap)%cap, then wrap backward.
	result := make([]Alert, 0, maxAlertHistory)
	if !a.full {
		for i := len(a.history) - 1; i >= 0; i-- {
			result = append(result, a.history[i])
		}
	} else {
		for i := 0; i < maxAlertHistory; i++ {
			idx := (a.pos - 1 - i + maxAlertHistory) % maxAlertHistory
			result = append(result, a.history[idx])
		}
	}

	return result
}

func (a *Alerter) sendWebhook(alert Alert) error {
	_, webhookURL, _ := a.settings()
	if a.urlSafetyCheck != nil {
		if err := a.urlSafetyCheck(webhookURL); err != nil {
			// A URL that fails to parse (a pasted trailing newline) comes back
			// as a *url.Error that prints the whole credential-bearing URL.
			return fmt.Errorf("webhook URL not allowed: %w", redactURLError(err))
		}
	}

	payload, err := json.Marshal(alert)
	if err != nil {
		a.logger.Error("failed to marshal alert for webhook", "error", err)
		return fmt.Errorf("marshal alert for webhook: %w", err)
	}

	resp, err := a.client.Post(webhookURL, "application/json", bytes.NewReader(payload))
	if err != nil {
		// The URL is a credential (Slack hook path, ?token=): *url.Error
		// prints it, so keep only the operation and the cause.
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = fmt.Errorf("%s: %w", uerr.Op, uerr.Err)
		}
		a.logger.Error("webhook delivery failed", "error", err)
		return fmt.Errorf("webhook delivery failed: %w", err)
	}
	// Only the status matters; do not read whatever the receiver streams.
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	resp.Body.Close()

	if resp.StatusCode >= 400 {
		a.logger.Error("webhook returned error", "status", resp.StatusCode)
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}
	return nil
}

// redactURLError removes the request URL from a *url.Error's text and keeps
// the cause. Webhook URLs are credentials (Slack hook path, ?token=), and
// url.Parse failures print them verbatim (F9700).
func redactURLError(err error) error {
	var uerr *url.Error
	if err == nil || !errors.As(err, &uerr) {
		return err
	}
	msg := strings.ReplaceAll(err.Error(), strconv.Quote(uerr.URL), `"[redacted URL]"`)
	msg = strings.ReplaceAll(msg, uerr.URL, "[redacted URL]")
	return &redactedURLError{msg: msg, cause: uerr.Err}
}

type redactedURLError struct {
	msg   string
	cause error
}

func (e *redactedURLError) Error() string { return e.msg }
func (e *redactedURLError) Unwrap() error { return e.cause }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	if n < 0 {
		return "-" + itoa(-n)
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}

func ftoa(f float64) string {
	// 1-decimal format. Truncating the binary float printed 11.1 as "11.0".
	return strconv.FormatFloat(f, 'f', 1, 64)
}

// truncateForChannel shortens s to at most max bytes on a rune boundary and
// marks the cut.
func truncateForChannel(s string, max int) string {
	if len(s) <= max {
		return s
	}
	const marker = "… (truncated)"
	cut := max - len(marker)
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + marker
}

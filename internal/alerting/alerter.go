package alerting

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/notify"
)

const maxAlertHistory = 100

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

	// Rate limit tracking for error_spike detection.
	errorWindow    []errorEntry
	errorWindowErr int // running count of error entries in errorWindow
	errorWindowMu  sync.Mutex
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

// Alert records an alert in the ring buffer and sends it via webhook if configured.
func (a *Alerter) Alert(alert Alert) {
	if !a.enabled {
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

	if a.webhookURL != "" {
		go func() {
			if err := a.sendWebhook(alert); err != nil {
				a.logger.Warn("webhook delivery failed", "error", err, "url", a.webhookURL)
			}
		}()
	}

	// Fan out to the configured channels. Each send is its own goroutine: an
	// unreachable SMTP server must not delay Slack, and none of them may
	// delay the caller — Alert is invoked from request-path recorders and
	// certificate renewal alike.
	for _, ch := range a.channels {
		if !ch.Enabled {
			continue
		}
		go func(ch notify.Channel) {
			if err := notify.Send(ch, notify.Message{
				Level:  alert.Level,
				Title:  alertTitle(alert.Type),
				Body:   alert.Message,
				Source: alert.Host,
			}); err != nil {
				a.logger.Warn("alert channel delivery failed",
					"channel", ch.Type, "type", alert.Type, "error", err)
			}
		}(ch)
	}
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
	if !a.enabled {
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
		a.errorWindow = append([]errorEntry(nil), a.errorWindow[start:]...)
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
		copy(a.errorWindow, a.errorWindow[drop:])
		a.errorWindow = a.errorWindow[:maxWindowSize]
	}

	total := len(a.errorWindow)
	errors := a.errorWindowErr
	a.errorWindowMu.Unlock()

	// Fire alert if error rate > 10% and we have a meaningful sample
	if total >= 10 && float64(errors)/float64(total) > 0.10 {
		// Deduplicate: only alert once per minute
		a.mu.Lock()
		shouldAlert := true
		for i := 0; i < len(a.history); i++ {
			h := a.history[i]
			if h.Type == "error_spike" && now.Sub(h.Time) < time.Minute {
				shouldAlert = false
				break
			}
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
	if a.urlSafetyCheck != nil {
		if err := a.urlSafetyCheck(a.webhookURL); err != nil {
			return fmt.Errorf("webhook URL not allowed: %w", err)
		}
	}

	payload, err := json.Marshal(alert)
	if err != nil {
		a.logger.Error("failed to marshal alert for webhook", "error", err)
		return fmt.Errorf("marshal alert for webhook: %w", err)
	}

	resp, err := a.client.Post(a.webhookURL, "application/json", bytes.NewReader(payload))
	if err != nil {
		a.logger.Error("webhook delivery failed", "error", err, "url", a.webhookURL)
		return fmt.Errorf("webhook delivery failed: %w", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if resp.StatusCode >= 400 {
		a.logger.Error("webhook returned error", "status", resp.StatusCode, "url", a.webhookURL)
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}
	return nil
}

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
	// Simple 1-decimal format
	whole := int(f)
	frac := int((f - float64(whole)) * 10)
	if frac < 0 {
		frac = -frac
	}
	return itoa(whole) + "." + itoa(frac)
}

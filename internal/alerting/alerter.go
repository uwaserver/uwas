package alerting

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

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
	return &Alerter{
		webhookURL: webhookURL,
		channels:   channels,
		logger:     log,
		enabled:    enabled,
		history:    make([]Alert, 0, maxAlertHistory),
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
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
				Title:  alert.Type,
				Body:   alert.Message,
				Source: alert.Host,
			}); err != nil {
				a.logger.Warn("alert channel delivery failed",
					"channel", ch.Type, "type", alert.Type, "error", err)
			}
		}(ch)
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
		a.errorWindow = append([]errorEntry(nil), a.errorWindow[drop:]...)
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
				Level:   "warning",
				Type:    "error_spike",
				Message: "Error rate " + ftoa(pct) + "% in last 5 minutes (" + itoa(errors) + "/" + itoa(total) + " requests)",
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

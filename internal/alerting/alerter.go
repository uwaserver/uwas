package alerting

import (
	"bytes"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/uwaserver/uwas/internal/logger"
)

const maxAlertHistory = 100

// Alert represents a single alert event.
type Alert struct {
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`   // "info", "warning", "critical"
	Type    string    `json:"type"`    // "domain_down", "cert_expiry", "rate_limit", "error_spike"
	Host    string    `json:"host"`
	Message string    `json:"message"`
}

// Alerter sends notifications on important events and keeps a ring buffer
// of recent alerts for the admin dashboard.
type Alerter struct {
	webhookURL string
	logger     *logger.Logger
	mu         sync.Mutex
	history    []Alert
	pos        int
	full       bool
	enabled    bool
	client     *http.Client

	// Rate limit tracking for error_spike detection.
	errorWindow   []errorEntry
	errorWindowMu sync.Mutex

}

type errorEntry struct {
	time  time.Time
	isErr bool
}

// New creates a new Alerter.
func New(enabled bool, webhookURL string, log *logger.Logger) *Alerter {
	return &Alerter{
		webhookURL: webhookURL,
		logger:     log,
		enabled:    enabled,
		history:    make([]Alert, maxAlertHistory),
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
	a.history[a.pos] = alert
	a.pos = (a.pos + 1) % maxAlertHistory
	if a.pos == 0 {
		a.full = true
	}
	a.mu.Unlock()

	a.logger.Warn("alert",
		"level", alert.Level,
		"type", alert.Type,
		"host", alert.Host,
		"message", alert.Message,
	)

	if a.webhookURL != "" {
		go a.sendWebhook(alert)
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
	// Append new entry
	a.errorWindow = append(a.errorWindow, errorEntry{time: now, isErr: isError})

	// Prune old entries
	start := 0
	for start < len(a.errorWindow) && a.errorWindow[start].time.Before(cutoff) {
		start++
	}
	if start > 0 {
		a.errorWindow = a.errorWindow[start:]
	}

	// Calculate error rate
	var total, errors int
	for _, e := range a.errorWindow {
		total++
		if e.isErr {
			errors++
		}
	}
	a.errorWindowMu.Unlock()

	// Fire alert if error rate > 10% and we have a meaningful sample
	if total >= 10 && float64(errors)/float64(total) > 0.10 {
		// Deduplicate: only alert once per minute
		a.mu.Lock()
		shouldAlert := true
		for i := 0; i < maxAlertHistory; i++ {
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

	var count int
	if a.full {
		count = maxAlertHistory
	} else {
		count = a.pos
	}
	if count == 0 {
		return nil
	}

	result := make([]Alert, 0, count)
	// Walk backward from most recent to oldest
	for i := count - 1; i >= 0; i-- {
		var idx int
		if a.full {
			idx = (a.pos - 1 - (count - 1 - i) + maxAlertHistory) % maxAlertHistory
		} else {
			idx = i
		}
		if !a.history[idx].Time.IsZero() {
			result = append(result, a.history[idx])
		}
	}

	return result
}

func (a *Alerter) sendWebhook(alert Alert) {
	payload, err := json.Marshal(alert)
	if err != nil {
		a.logger.Error("failed to marshal alert for webhook", "error", err)
		return
	}

	resp, err := a.client.Post(a.webhookURL, "application/json", bytes.NewReader(payload))
	if err != nil {
		a.logger.Error("webhook delivery failed", "error", err, "url", a.webhookURL)
		return
	}
	resp.Body.Close()

	if resp.StatusCode >= 400 {
		a.logger.Error("webhook returned error", "status", resp.StatusCode, "url", a.webhookURL)
	}
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

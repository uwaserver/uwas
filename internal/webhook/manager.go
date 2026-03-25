// Package webhook provides event-driven webhook delivery system.
// Fires webhooks on domain.add, domain.delete, cert.renewed, backup.completed, etc.
package webhook

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"sync"
	"time"
)

// EventType represents a webhook event type.
type EventType string

const (
	EventDomainAdd       EventType = "domain.add"
	EventDomainDelete    EventType = "domain.delete"
	EventDomainUpdate    EventType = "domain.update"
	EventCertRenewed     EventType = "cert.renewed"
	EventCertExpiry      EventType = "cert.expiry"
	EventBackupCompleted EventType = "backup.completed"
	EventBackupFailed    EventType = "backup.failed"
	EventPHPCrashed      EventType = "php.crashed"
	EventSecurityBlocked EventType = "security.blocked"
	EventCronFailed      EventType = "cron.failed"
	EventLoginSuccess    EventType = "login.success"
	EventLoginFailed     EventType = "login.failed"
	EventTest            EventType = "test"
)

// Event represents a webhook event payload.
type Event struct {
	ID        string    `json:"id"`
	Type      EventType `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	Data      any       `json:"data"`
}

// WebhookConfig defines a webhook endpoint configuration.
type WebhookConfig struct {
	URL       string            `json:"url" yaml:"url"`
	Events    []EventType       `json:"events" yaml:"events"`       // empty = all events
	Headers   map[string]string `json:"headers" yaml:"headers"`     // custom headers
	Secret    string            `json:"secret" yaml:"secret"`       // for HMAC signature
	RetryMax  int               `json:"retry_max" yaml:"retry_max"` // max retries, default 3
	Timeout   time.Duration     `json:"timeout" yaml:"timeout"`     // default 30s
	Enabled   bool              `json:"enabled" yaml:"enabled"`
}

// Manager handles webhook event delivery.
type Manager struct {
	mu        sync.RWMutex
	webhooks  []WebhookConfig
	client    *http.Client
	queue     chan *queuedEvent
	dataDir   string
	logger    Logger
}

// Logger interface for logging.
type Logger interface {
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

type queuedEvent struct {
	webhook   WebhookConfig
	event     Event
	attempts  int
}

// NewManager creates a new webhook manager.
func NewManager(dataDir string, logger Logger) *Manager {
	m := &Manager{
		webhooks: make([]WebhookConfig, 0),
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		queue:   make(chan *queuedEvent, 1000),
		dataDir: dataDir,
		logger:  logger,
	}

	// Start worker goroutine
	go m.worker()

	return m
}

// Close stops the webhook manager worker goroutine.
func (m *Manager) Close() {
	close(m.queue)
}

// UpdateWebhooks updates the webhook configurations.
func (m *Manager) UpdateWebhooks(configs []WebhookConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.webhooks = configs
}

// Fire sends a webhook event to all matching webhooks.
func (m *Manager) Fire(eventType EventType, data any) {
	event := Event{
		ID:        generateID(),
		Type:      eventType,
		Timestamp: time.Now(),
		Data:      data,
	}

	m.mu.RLock()
	webhooks := make([]WebhookConfig, len(m.webhooks))
	copy(webhooks, m.webhooks)
	m.mu.RUnlock()

	for _, wh := range webhooks {
		if !wh.Enabled {
			continue
		}
		if !matchesEvent(wh.Events, eventType) {
			continue
		}

		qe := &queuedEvent{
			webhook:  wh,
			event:    event,
			attempts: 0,
		}

		select {
		case m.queue <- qe:
		default:
			m.logger.Error("webhook queue full, dropping event", "event", eventType)
		}
	}
}

// FireTo sends a webhook event directly to a specific URL, bypassing subscription matching.
func (m *Manager) FireTo(url string, eventType EventType, data any) {
	event := Event{
		ID:        generateID(),
		Type:      eventType,
		Timestamp: time.Now(),
		Data:      data,
	}

	// Find the webhook config for this URL to get secret/headers, or use defaults
	m.mu.RLock()
	var wh WebhookConfig
	found := false
	for _, w := range m.webhooks {
		if w.URL == url {
			wh = w
			found = true
			break
		}
	}
	m.mu.RUnlock()

	if !found {
		wh = WebhookConfig{URL: url, Enabled: true, RetryMax: 3, Timeout: 30 * time.Second}
	}

	qe := &queuedEvent{webhook: wh, event: event}
	select {
	case m.queue <- qe:
	default:
		m.logger.Error("webhook queue full, dropping test event", "url", url)
	}
}

// worker processes the webhook queue.
func (m *Manager) worker() {
	for qe := range m.queue {
		m.deliver(qe)
	}
}

// deliver sends a webhook with retry logic.
func (m *Manager) deliver(qe *queuedEvent) {
	maxRetries := qe.webhook.RetryMax
	if maxRetries == 0 {
		maxRetries = 3
	}

	timeout := qe.webhook.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	client := &http.Client{Timeout: timeout}

	payload, err := json.Marshal(qe.event)
	if err != nil {
		m.logger.Error("failed to marshal webhook payload", "error", err)
		return
	}

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff: 1s, 2s, 4s...
			time.Sleep(time.Duration(1<<uint(attempt-1)) * time.Second)
		}

		req, err := http.NewRequest("POST", qe.webhook.URL, bytes.NewReader(payload))
		if err != nil {
			m.logger.Error("failed to create webhook request", "error", err)
			continue
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-UWAS-Event", string(qe.event.Type))
		req.Header.Set("X-UWAS-Event-ID", qe.event.ID)
		req.Header.Set("X-UWAS-Attempt", fmt.Sprintf("%d", attempt+1))

		// Add custom headers
		for k, v := range qe.webhook.Headers {
			req.Header.Set(k, v)
		}

		// Add HMAC signature if secret is configured
		if qe.webhook.Secret != "" {
			sig := sign(payload, qe.webhook.Secret)
			req.Header.Set("X-UWAS-Signature", sig)
		}

		resp, err := client.Do(req)
		if err != nil {
			m.logger.Warn("webhook delivery failed",
				"event", qe.event.Type,
				"url", qe.webhook.URL,
				"attempt", attempt+1,
				"error", err,
			)
			continue
		}
		resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			m.logger.Debug("webhook delivered",
				"event", qe.event.Type,
				"url", qe.webhook.URL,
				"attempt", attempt+1,
			)
			return
		}

		m.logger.Warn("webhook returned error status",
			"event", qe.event.Type,
			"url", qe.webhook.URL,
			"attempt", attempt+1,
			"status", resp.StatusCode,
		)
	}

	m.logger.Error("webhook delivery exhausted all retries",
		"event", qe.event.Type,
		"url", qe.webhook.URL,
		"retries", maxRetries,
	)
}

// matchesEvent checks if an event type matches the configured events.
// Empty events list means match all.
func matchesEvent(configured []EventType, event EventType) bool {
	if len(configured) == 0 {
		return true
	}
	for _, e := range configured {
		if e == event {
			return true
		}
	}
	return false
}

// generateID generates a unique event ID.
func generateID() string {
	return fmt.Sprintf("%d_%d", time.Now().UnixNano(), time.Now().Unix())
}

// sign creates an HMAC-SHA256 signature for the payload.
func sign(payload []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// dataDir returns the data directory for persistence.
func (m *Manager) historyFile() string {
	if m.dataDir == "" {
		return ""
	}
	return filepath.Join(m.dataDir, "webhook_history.json")
}

package webhook

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// --- historyFile ---

func TestHistoryFile(t *testing.T) {
	m := &Manager{dataDir: "/tmp/webhooks", logger: &testLogger{}}
	got := m.historyFile()
	if got == "" {
		t.Error("historyFile should return a path when dataDir is set")
	}
}

func TestHistoryFileEmpty(t *testing.T) {
	m := &Manager{dataDir: "", logger: &testLogger{}}
	got := m.historyFile()
	if got != "" {
		t.Errorf("historyFile should return empty when dataDir is empty, got %q", got)
	}
}

// --- Fire: queue full ---

func TestFireQueueFull(t *testing.T) {
	// Create a manager with a tiny queue that we can fill
	m := &Manager{
		webhooks: []WebhookConfig{
			{URL: "http://example.com/hook", Enabled: true, RetryMax: 1, Timeout: 1 * time.Second},
		},
		client: &http.Client{Timeout: 1 * time.Second},
		queue:  make(chan *queuedEvent, 1), // capacity 1
		logger: &testLogger{},
	}
	// Don't start the worker so the queue fills up

	// First Fire should succeed (fills the queue)
	m.Fire(EventDomainAdd, map[string]any{"host": "a.com"})

	// Second Fire should drop due to full queue (exercises the default branch)
	m.Fire(EventDomainAdd, map[string]any{"host": "b.com"})

	// Drain the queue
	select {
	case <-m.queue:
	default:
	}
}

// --- FireTo: with existing webhook config ---

func TestFireToWithExistingConfig(t *testing.T) {
	var mu sync.Mutex
	var receivedSig string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		receivedSig = r.Header.Get("X-UWAS-Signature")
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer srv.Close()

	m := newTestManager()
	defer m.Close()

	// Configure a webhook with a secret for this URL
	m.UpdateWebhooks([]WebhookConfig{
		{URL: srv.URL, Secret: "my-secret", Enabled: true, RetryMax: 1, Timeout: 5 * time.Second},
	})

	// FireTo should find the existing config and use its secret
	m.FireTo(srv.URL, EventTest, map[string]any{"msg": "hello"})
	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if receivedSig == "" {
		t.Error("FireTo with existing config should use the secret for HMAC signing")
	}
}

// --- FireTo: queue full ---

func TestFireToQueueFull(t *testing.T) {
	m := &Manager{
		webhooks: []WebhookConfig{},
		client:   &http.Client{Timeout: 1 * time.Second},
		queue:    make(chan *queuedEvent, 1), // capacity 1
		logger:   &testLogger{},
	}
	// Don't start worker

	// First FireTo fills the queue
	m.FireTo("http://example.com/hook", EventTest, nil)

	// Second FireTo should drop
	m.FireTo("http://example.com/hook", EventTest, nil)

	select {
	case <-m.queue:
	default:
	}
}

// --- deliver: default timeout and retryMax ---

func TestDeliverDefaultTimeoutAndRetry(t *testing.T) {
	var mu sync.Mutex
	var attempts int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attempts++
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer srv.Close()

	m := newTestManager()
	defer m.Close()

	// Use zero Timeout and zero RetryMax to exercise the default paths
	m.UpdateWebhooks([]WebhookConfig{
		{URL: srv.URL, Enabled: true, Timeout: 0, RetryMax: 0},
	})

	m.Fire(EventTest, map[string]any{"msg": "defaults"})
	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if attempts < 1 {
		t.Errorf("expected at least 1 attempt, got %d", attempts)
	}
}

// --- deliver: all retries exhausted with non-2xx ---

func TestDeliverExhaustedRetries(t *testing.T) {
	var mu sync.Mutex
	var attempts int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attempts++
		mu.Unlock()
		w.WriteHeader(500) // always fail
	}))
	defer srv.Close()

	m := newTestManager()
	defer m.Close()

	m.UpdateWebhooks([]WebhookConfig{
		{URL: srv.URL, Enabled: true, RetryMax: 1, Timeout: 2 * time.Second},
	})

	m.Fire(EventTest, map[string]any{"msg": "exhaust"})
	time.Sleep(3 * time.Second)

	mu.Lock()
	defer mu.Unlock()
	// RetryMax=1 means initial attempt + 1 retry = 2 total attempts
	if attempts != 2 {
		t.Errorf("expected 2 attempts (initial + 1 retry), got %d", attempts)
	}
}

// --- deliver: connection refused (network error) ---

func TestDeliverConnectionRefused(t *testing.T) {
	m := newTestManager()
	defer m.Close()

	m.UpdateWebhooks([]WebhookConfig{
		{URL: "http://127.0.0.1:1/unreachable", Enabled: true, RetryMax: 0, Timeout: 1 * time.Second},
	})

	m.Fire(EventTest, map[string]any{"msg": "refused"})
	time.Sleep(2 * time.Second)
	// Should not panic; retries exhausted
}

// --- deliver: verify all standard headers ---

func TestDeliverStandardHeaders(t *testing.T) {
	var receivedHeaders http.Header
	var receivedBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header.Clone()
		receivedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	m := newTestManager()
	defer m.Close()

	m.UpdateWebhooks([]WebhookConfig{
		{URL: srv.URL, Enabled: true, RetryMax: 0, Timeout: 5 * time.Second},
	})

	m.Fire(EventDomainAdd, map[string]any{"host": "example.com"})
	time.Sleep(500 * time.Millisecond)

	if receivedHeaders.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", receivedHeaders.Get("Content-Type"))
	}
	if receivedHeaders.Get("X-UWAS-Event") != string(EventDomainAdd) {
		t.Errorf("X-UWAS-Event = %q, want %q", receivedHeaders.Get("X-UWAS-Event"), EventDomainAdd)
	}
	if receivedHeaders.Get("X-UWAS-Event-ID") == "" {
		t.Error("X-UWAS-Event-ID should be set")
	}
	if receivedHeaders.Get("X-UWAS-Attempt") != "1" {
		t.Errorf("X-UWAS-Attempt = %q, want 1", receivedHeaders.Get("X-UWAS-Attempt"))
	}

	// Verify the body is valid JSON with the correct event type
	var ev Event
	if err := json.Unmarshal(receivedBody, &ev); err != nil {
		t.Fatalf("failed to unmarshal webhook body: %v", err)
	}
	if ev.Type != EventDomainAdd {
		t.Errorf("event type = %q, want %q", ev.Type, EventDomainAdd)
	}
}

// --- deliver: invalid URL that causes NewRequest to fail ---

func TestDeliverInvalidURL(t *testing.T) {
	m := newTestManager()
	defer m.Close()

	// A URL with control characters should cause http.NewRequest to fail
	m.UpdateWebhooks([]WebhookConfig{
		{URL: "http://invalid\x7f.com/hook", Enabled: true, RetryMax: 1, Timeout: 1 * time.Second},
	})

	m.Fire(EventTest, nil)
	time.Sleep(1 * time.Second)
	// Should not panic; http.NewRequest error is logged and retried
}

// --- generateID ---

func TestGenerateID(t *testing.T) {
	id1 := generateID()
	id2 := generateID()
	if id1 == "" || id2 == "" {
		t.Error("generateID should return non-empty strings")
	}
}

// --- Multiple webhooks: some match, some don't ---

func TestFireMultipleWebhooks(t *testing.T) {
	var mu sync.Mutex
	var count1, count2 int

	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		count1++
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer srv1.Close()

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		count2++
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer srv2.Close()

	m := newTestManager()
	defer m.Close()

	m.UpdateWebhooks([]WebhookConfig{
		{URL: srv1.URL, Events: []EventType{EventDomainAdd}, Enabled: true, RetryMax: 0, Timeout: 5 * time.Second},
		{URL: srv2.URL, Events: []EventType{EventDomainDelete}, Enabled: true, RetryMax: 0, Timeout: 5 * time.Second},
	})

	m.Fire(EventDomainAdd, nil)
	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if count1 != 1 {
		t.Errorf("srv1 should receive 1 event, got %d", count1)
	}
	if count2 != 0 {
		t.Errorf("srv2 should receive 0 events (wrong type), got %d", count2)
	}
}

// --- matchesEvent: empty events list with various events ---

func TestMatchesEventEmptyListAll(t *testing.T) {
	events := []EventType{
		EventDomainAdd, EventDomainDelete, EventDomainUpdate,
		EventCertRenewed, EventCertExpiry, EventBackupCompleted,
		EventBackupFailed, EventPHPCrashed, EventSecurityBlocked,
		EventCronFailed, EventLoginSuccess, EventLoginFailed, EventTest,
	}
	for _, ev := range events {
		if !matchesEvent(nil, ev) {
			t.Errorf("matchesEvent(nil, %q) = false, want true", ev)
		}
	}
}

// --- deliver: retry with exponential backoff timing ---

func TestDeliverRetryBackoffTiming(t *testing.T) {
	var mu sync.Mutex
	var timestamps []time.Time

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		timestamps = append(timestamps, time.Now())
		mu.Unlock()
		w.WriteHeader(500) // always fail
	}))
	defer srv.Close()

	m := newTestManager()
	defer m.Close()

	m.UpdateWebhooks([]WebhookConfig{
		{URL: srv.URL, Enabled: true, RetryMax: 2, Timeout: 2 * time.Second},
	})

	start := time.Now()
	m.Fire(EventTest, nil)
	time.Sleep(6 * time.Second) // 0 + 1s + 2s + buffer

	mu.Lock()
	defer mu.Unlock()
	if len(timestamps) < 3 {
		t.Errorf("expected 3 attempts, got %d", len(timestamps))
		return
	}

	// Verify first attempt is near-instant
	if timestamps[0].Sub(start) > 500*time.Millisecond {
		t.Error("first attempt should be near-instant")
	}
}

// --- UpdateWebhooks replaces config ---

func TestUpdateWebhooksReplaces(t *testing.T) {
	m := newTestManager()
	defer m.Close()

	m.UpdateWebhooks([]WebhookConfig{
		{URL: "http://a.com", Enabled: true},
		{URL: "http://b.com", Enabled: true},
	})

	m.mu.RLock()
	count1 := len(m.webhooks)
	m.mu.RUnlock()
	if count1 != 2 {
		t.Errorf("expected 2 webhooks, got %d", count1)
	}

	m.UpdateWebhooks([]WebhookConfig{
		{URL: "http://c.com", Enabled: true},
	})

	m.mu.RLock()
	count2 := len(m.webhooks)
	m.mu.RUnlock()
	if count2 != 1 {
		t.Errorf("expected 1 webhook after update, got %d", count2)
	}
}

// --- Close stops the worker ---

func TestCloseStopsWorker(t *testing.T) {
	m := newTestManager()
	m.Close()
	// After close, the queue channel should be closed
	// Sending should panic, but we just verify it doesn't hang
}

// --- deliver: json.Marshal error (unmarshalable data) ---

func TestDeliverMarshalError(t *testing.T) {
	m := newTestManager()
	defer m.Close()

	// Directly deliver a queuedEvent with unmarshalable data (channel type)
	qe := &queuedEvent{
		webhook: WebhookConfig{
			URL:      "http://example.com/hook",
			Enabled:  true,
			RetryMax: 0,
			Timeout:  1 * time.Second,
		},
		event: Event{
			ID:        "test-marshal-err",
			Type:      EventTest,
			Timestamp: time.Now(),
			Data:      make(chan int), // channels can't be JSON marshaled
		},
	}

	// Call deliver directly - should not panic, just log error
	m.deliver(qe)
}

// --- Fire with nil data ---

func TestFireNilData(t *testing.T) {
	var received bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = true
		body, _ := io.ReadAll(r.Body)
		var ev Event
		json.Unmarshal(body, &ev)
		if ev.Data != nil {
			// data was null in JSON
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	m := newTestManager()
	defer m.Close()

	m.UpdateWebhooks([]WebhookConfig{
		{URL: srv.URL, Enabled: true, RetryMax: 0, Timeout: 5 * time.Second},
	})

	m.Fire(EventTest, nil)
	time.Sleep(500 * time.Millisecond)

	if !received {
		t.Error("webhook should be delivered even with nil data")
	}
}

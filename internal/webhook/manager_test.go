package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type testLogger struct{}

func (l *testLogger) Debug(msg string, args ...any) {}
func (l *testLogger) Info(msg string, args ...any)  {}
func (l *testLogger) Warn(msg string, args ...any)  {}
func (l *testLogger) Error(msg string, args ...any) {}

func newTestManager() *Manager {
	return NewManager("", &testLogger{})
}

func TestFire(t *testing.T) {
	var mu sync.Mutex
	var received []Event

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var ev Event
		json.Unmarshal(body, &ev)
		mu.Lock()
		received = append(received, ev)
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer srv.Close()

	m := newTestManager()
	defer m.Close()

	m.UpdateWebhooks([]WebhookConfig{
		{URL: srv.URL, Enabled: true, RetryMax: 1, Timeout: 5 * time.Second},
	})

	m.Fire(EventDomainAdd, map[string]any{"host": "example.com"})

	// Wait for delivery
	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 {
		t.Fatalf("expected 1 event, got %d", len(received))
	}
	if received[0].Type != EventDomainAdd {
		t.Errorf("expected event type %q, got %q", EventDomainAdd, received[0].Type)
	}
}

func TestFireEventFiltering(t *testing.T) {
	var mu sync.Mutex
	var count int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		count++
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer srv.Close()

	m := newTestManager()
	defer m.Close()

	// Only subscribe to domain.delete
	m.UpdateWebhooks([]WebhookConfig{
		{URL: srv.URL, Events: []EventType{EventDomainDelete}, Enabled: true, RetryMax: 1, Timeout: 5 * time.Second},
	})

	m.Fire(EventDomainAdd, map[string]any{"host": "a.com"})       // should NOT match
	m.Fire(EventDomainDelete, map[string]any{"host": "b.com"})    // should match

	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if count != 1 {
		t.Errorf("expected 1 delivery (only domain.delete), got %d", count)
	}
}

func TestFireDisabledWebhook(t *testing.T) {
	var mu sync.Mutex
	var count int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		count++
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer srv.Close()

	m := newTestManager()
	defer m.Close()

	m.UpdateWebhooks([]WebhookConfig{
		{URL: srv.URL, Enabled: false, RetryMax: 1, Timeout: 5 * time.Second},
	})

	m.Fire(EventDomainAdd, map[string]any{"host": "a.com"})

	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if count != 0 {
		t.Errorf("expected 0 deliveries for disabled webhook, got %d", count)
	}
}

func TestHMACSignature(t *testing.T) {
	secret := "my-secret-key"
	var receivedSig string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedSig = r.Header.Get("X-UWAS-Signature")
		w.WriteHeader(200)
	}))
	defer srv.Close()

	m := newTestManager()
	defer m.Close()

	m.UpdateWebhooks([]WebhookConfig{
		{URL: srv.URL, Secret: secret, Enabled: true, RetryMax: 1, Timeout: 5 * time.Second},
	})

	m.Fire(EventTest, map[string]any{"msg": "hello"})
	time.Sleep(500 * time.Millisecond)

	if receivedSig == "" {
		t.Fatal("expected X-UWAS-Signature header, got empty")
	}

	// Verify it starts with sha256=
	if len(receivedSig) < 8 || receivedSig[:7] != "sha256=" {
		t.Fatalf("signature should start with 'sha256=', got %q", receivedSig)
	}
}

func TestSignFunction(t *testing.T) {
	payload := []byte(`{"type":"test"}`)
	secret := "secret123"

	result := sign(payload, secret)

	// Verify using standard HMAC
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if result != expected {
		t.Errorf("sign() = %q, want %q", result, expected)
	}
}

func TestRetryOnFailure(t *testing.T) {
	var mu sync.Mutex
	var attempts int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attempts++
		n := attempts
		mu.Unlock()
		if n <= 2 {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	m := newTestManager()
	defer m.Close()

	m.UpdateWebhooks([]WebhookConfig{
		{URL: srv.URL, Enabled: true, RetryMax: 3, Timeout: 5 * time.Second},
	})

	m.Fire(EventTest, map[string]any{"msg": "retry-test"})
	time.Sleep(8 * time.Second) // backoff: 0 + 1s + 2s + buffer

	mu.Lock()
	defer mu.Unlock()
	if attempts < 3 {
		t.Errorf("expected at least 3 attempts (2 failures + 1 success), got %d", attempts)
	}
}

func TestFireTo(t *testing.T) {
	var received bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = true
		w.WriteHeader(200)
	}))
	defer srv.Close()

	m := newTestManager()
	defer m.Close()

	// No webhooks configured — FireTo should still deliver
	m.FireTo(srv.URL, EventTest, map[string]any{"msg": "direct"})
	time.Sleep(500 * time.Millisecond)

	if !received {
		t.Error("expected FireTo to deliver to URL even without configured webhooks")
	}
}

func TestMatchesEvent(t *testing.T) {
	tests := []struct {
		name       string
		configured []EventType
		event      EventType
		want       bool
	}{
		{"empty matches all", nil, EventDomainAdd, true},
		{"exact match", []EventType{EventDomainAdd}, EventDomainAdd, true},
		{"no match", []EventType{EventDomainDelete}, EventDomainAdd, false},
		{"multiple with match", []EventType{EventDomainAdd, EventDomainDelete}, EventDomainDelete, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchesEvent(tt.configured, tt.event); got != tt.want {
				t.Errorf("matchesEvent(%v, %q) = %v, want %v", tt.configured, tt.event, got, tt.want)
			}
		})
	}
}

func TestHeaders(t *testing.T) {
	var receivedHeaders http.Header

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header.Clone()
		w.WriteHeader(200)
	}))
	defer srv.Close()

	m := newTestManager()
	defer m.Close()

	m.UpdateWebhooks([]WebhookConfig{
		{
			URL:     srv.URL,
			Enabled: true,
			Headers: map[string]string{"X-Custom": "hello"},
			RetryMax: 1,
			Timeout: 5 * time.Second,
		},
	})

	m.Fire(EventTest, nil)
	time.Sleep(500 * time.Millisecond)

	if receivedHeaders.Get("X-Custom") != "hello" {
		t.Errorf("expected X-Custom header 'hello', got %q", receivedHeaders.Get("X-Custom"))
	}
	if receivedHeaders.Get("X-UWAS-Event") != "test" {
		t.Errorf("expected X-UWAS-Event header 'test', got %q", receivedHeaders.Get("X-UWAS-Event"))
	}
}

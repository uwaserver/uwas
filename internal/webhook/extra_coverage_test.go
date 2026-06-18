package webhook

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"syscall"
	"testing"
	"time"
)

// --- NewManager: history-path debug branch (non-empty dataDir) ---

func TestNewManagerWithDataDir(t *testing.T) {
	// Restore globals to a safe state for the constructor.
	webhookURLSafetyCheck = func(string) error { return nil }
	webhookDialControl = nil

	m := NewManager(t.TempDir(), &testLogger{})
	defer m.Close()

	if m.historyFile() == "" {
		t.Error("expected non-empty history file path for non-empty dataDir")
	}
}

// --- Fire: early return when closed ---

func TestFireAfterClose(t *testing.T) {
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
	m.UpdateWebhooks([]WebhookConfig{
		{URL: srv.URL, Enabled: true, RetryMax: 0, Timeout: 5 * time.Second},
	})
	m.Close()

	// Fire after Close must return immediately without queueing.
	m.Fire(EventTest, map[string]any{"msg": "after-close"})

	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if count != 0 {
		t.Errorf("expected 0 deliveries after Close, got %d", count)
	}
}

// --- FireTo: early return when closed ---

func TestFireToAfterClose(t *testing.T) {
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
	m.Close()

	m.FireTo(srv.URL, EventTest, nil)

	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if count != 0 {
		t.Errorf("expected 0 deliveries for FireTo after Close, got %d", count)
	}
}

// --- FireTo: SSRF block path ---

func TestFireToSSRFBlocked(t *testing.T) {
	// Construct a manager whose urlSafe hook always rejects.
	webhookURLSafetyCheck = func(string) error { return errors.New("blocked by policy") }
	webhookDialControl = nil

	m := NewManager("", &testLogger{})
	defer m.Close()

	// FireTo should detect the unsafe URL and not enqueue anything.
	m.FireTo("http://169.254.169.254/latest/meta-data", EventTest, nil)

	// Nothing should be queued.
	select {
	case <-m.queue:
		t.Error("FireTo should not enqueue an SSRF-blocked URL")
	default:
	}

	// Reset for other tests.
	webhookURLSafetyCheck = func(string) error { return nil }
}

// --- deliver: SSRF block before delivery ---

func TestDeliverSSRFBlocked(t *testing.T) {
	var hit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.WriteHeader(200)
	}))
	defer srv.Close()

	m := &Manager{
		logger:  &testLogger{},
		urlSafe: func(string) error { return errors.New("blocked") },
	}

	qe := &queuedEvent{
		webhook: WebhookConfig{URL: srv.URL, Enabled: true, RetryMax: 0, Timeout: time.Second},
		event:   Event{ID: "x", Type: EventTest, Timestamp: time.Now()},
	}
	// deliver should short-circuit on the SSRF check.
	m.deliver(qe)

	if hit {
		t.Error("deliver should not reach the server when URL is SSRF-blocked")
	}
}

// --- deliver: dialControl set (DNS-rebinding control path) ---

func TestDeliverWithDialControl(t *testing.T) {
	received := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- struct{}{}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	// A permissive dial control that allows loopback so delivery succeeds,
	// while exercising the m.dialControl != nil branch in deliver().
	m := &Manager{
		logger:      &testLogger{},
		urlSafe:     func(string) error { return nil },
		dialControl: func(_, _ string, _ syscall.RawConn) error { return nil },
	}

	qe := &queuedEvent{
		webhook: WebhookConfig{URL: srv.URL, Enabled: true, RetryMax: 0, Timeout: 5 * time.Second},
		event:   Event{ID: "dc", Type: EventTest, Timestamp: time.Now()},
	}
	m.deliver(qe)

	select {
	case <-received:
	case <-time.After(2 * time.Second):
		t.Fatal("expected delivery with dialControl set")
	}
}

// --- deliver: redirect re-validation rejects internal target ---

func TestDeliverRedirectRevalidation(t *testing.T) {
	// Target server redirects to a "blocked" host. The CheckRedirect hook
	// uses checkURLSafe, which we make reject the redirect target.
	var blockedHost = "http://blocked.internal/secret"

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, blockedHost, http.StatusFound)
	}))
	defer redirector.Close()

	m := &Manager{
		logger: &testLogger{},
		urlSafe: func(u string) error {
			if u == blockedHost {
				return errors.New("redirect target blocked")
			}
			return nil
		},
	}

	qe := &queuedEvent{
		webhook: WebhookConfig{URL: redirector.URL, Enabled: true, RetryMax: 0, Timeout: 5 * time.Second},
		event:   Event{ID: "rd", Type: EventTest, Timestamp: time.Now()},
	}
	// The redirect should be blocked by CheckRedirect; deliver treats it as a
	// failed attempt (client.Do returns an error). Should not panic.
	m.deliver(qe)
}

// NOTE on sendToQueue's recover branch:
// The deferred recover in sendToQueue only swallows a panic whose value is the
// literal Go *string* "send on closed channel". In practice the runtime's
// closed-channel panic value is a runtime error type (runtime.plainError), not
// a plain string, so the type assertion `r.(string)` fails and the recover
// re-panics. The "recovered/return" branch is therefore genuinely unreachable
// without modifying production code, so it is intentionally left uncovered.

package webhook

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// A negative RetryMax means "no retries": the event must still get exactly
// one delivery attempt instead of being silently dropped.
func TestDeliverNegativeRetryMaxStillAttemptsOnce(t *testing.T) {
	for _, retry := range []int{-1, -1000} {
		var hits atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
		}))
		m := &Manager{logger: &testLogger{}, urlSafe: func(string) error { return nil }}
		m.deliver(&queuedEvent{
			webhook: WebhookConfig{URL: srv.URL, Enabled: true, RetryMax: retry, Timeout: 5 * time.Second},
			event:   Event{ID: "neg", Type: EventTest},
		})
		srv.Close()
		if got := hits.Load(); got != 1 {
			t.Errorf("RetryMax=%d: attempts = %d, want 1", retry, got)
		}
	}
}

// Close must abandon a delivery that is waiting in retry backoff, so no
// attempt reaches the endpoint after Close and the worker does not outlive it.
func TestCloseAbandonsRetryBackoff(t *testing.T) {
	hits := make(chan struct{}, 64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits <- struct{}{}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	m := NewManager("", &testLogger{})
	m.urlSafe = func(string) error { return nil }
	m.dialControl = nil

	returned := make(chan struct{})
	go func() {
		m.deliver(&queuedEvent{
			webhook: WebhookConfig{URL: srv.URL, Enabled: true, RetryMax: 30, Timeout: 5 * time.Second},
			event:   Event{ID: "close", Type: EventTest},
		})
		close(returned)
	}()

	<-hits // attempt 1 reached the endpoint; deliver now enters backoff
	m.Close()

	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("deliver still running after Close")
	}
	if n := len(hits); n != 0 {
		t.Fatalf("%d delivery attempt(s) after Close, want 0", n)
	}
}

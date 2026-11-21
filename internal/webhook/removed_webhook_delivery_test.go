package webhook

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func removedWebhookManager() *Manager {
	return &Manager{
		queue:   make(chan *queuedEvent, 8),
		done:    make(chan struct{}),
		logger:  &testLogger{},
		urlSafe: func(string) error { return nil },
	}
}

// An event queued for a webhook that is then removed, disabled or
// unsubscribed must not be POSTed to it; an unchanged reload still delivers
// (F64800).
func TestQueuedEventSkipsRemovedWebhook(t *testing.T) {
	enabled := func(u string) []WebhookConfig {
		return []WebhookConfig{{URL: u, Enabled: true, Timeout: 5 * time.Second}}
	}
	cases := []struct {
		name  string
		after func(u string) []WebhookConfig
		want  int32
	}{
		{"removed", func(string) []WebhookConfig { return nil }, 0},
		{"disabled", func(u string) []WebhookConfig { return []WebhookConfig{{URL: u}} }, 0},
		{"unsubscribed", func(u string) []WebhookConfig {
			return []WebhookConfig{{URL: u, Enabled: true, Events: []EventType{EventCertRenewed}}}
		}, 0},
		{"unchanged reload", enabled, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
			defer srv.Close()
			m := removedWebhookManager()
			m.UpdateWebhooks(enabled(srv.URL))
			m.Fire(EventLoginSuccess, map[string]any{"user": "u"})
			qe := <-m.queue
			m.UpdateWebhooks(tc.after(srv.URL))
			m.deliver(qe)
			if got := hits.Load(); got != tc.want {
				t.Fatalf("POSTs = %d, want %d", got, tc.want)
			}
		})
	}
}

// A webhook removed while a delivery waits in retry backoff gets no retry.
// The removal completes inside the handler before attempt 1 answers 500.
func TestRetrySkipsWebhookRemovedDuringBackoff(t *testing.T) {
	var hits atomic.Int32
	m := removedWebhookManager()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			m.UpdateWebhooks(nil)
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	m.UpdateWebhooks([]WebhookConfig{{URL: srv.URL, Enabled: true, RetryMax: 1, Timeout: 5 * time.Second}})
	m.Fire(EventDomainAdd, nil)
	m.deliver(<-m.queue)
	if got := hits.Load(); got != 1 {
		t.Fatalf("POSTs = %d, want 1 (no retry after removal)", got)
	}
}

// FireTo targets an explicit URL (the admin test endpoint), so it does not
// depend on a registration.
func TestFireToIgnoresRegistration(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer srv.Close()
	m := removedWebhookManager()
	m.FireTo(srv.URL, EventTest, nil)
	m.deliver(<-m.queue)
	if got := hits.Load(); got != 1 {
		t.Fatalf("POSTs = %d, want 1", got)
	}
}

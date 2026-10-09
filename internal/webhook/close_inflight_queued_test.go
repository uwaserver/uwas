package webhook

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// Close must cancel a delivery attempt that is already in flight instead of
// leaving the worker blocked until the endpoint answers or times out.
func TestCloseCancelsInFlightDelivery(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release) // runs before srv.Close so a stuck handler can finish

	m := NewManager("", &testLogger{})
	m.urlSafe = func(string) error { return nil }
	m.dialControl = nil

	returned := make(chan struct{})
	go func() {
		m.deliver(&queuedEvent{
			webhook: WebhookConfig{URL: srv.URL, Enabled: true, RetryMax: 3, Timeout: 20 * time.Second},
			event:   Event{ID: "inflight", Type: EventTest},
		})
		close(returned)
	}()

	<-entered
	m.Close()

	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("deliver still blocked on the in-flight request after Close")
	}
}

// An event still waiting in the queue when Close runs must not be POSTed.
func TestCloseDropsQueuedEvents(t *testing.T) {
	block := make(chan struct{})
	busy := make(chan struct{}, webhookWorkers)
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		busy <- struct{}{}
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	defer slow.Close()
	defer close(block) // runs before slow.Close so stuck handlers can finish
	var late atomic.Int32
	lateSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		late.Add(1)
	}))
	defer lateSrv.Close()

	m := NewManager("", &testLogger{})
	m.urlSafe = func(string) error { return nil }
	m.dialControl = nil

	m.UpdateWebhooks([]WebhookConfig{{URL: slow.URL, Enabled: true, RetryMax: -1, Timeout: 20 * time.Second}})
	for i := 0; i < webhookWorkers; i++ {
		m.Fire(EventTest, i)
	}
	for i := 0; i < webhookWorkers; i++ {
		<-busy // every worker is inside a POST; the next event can only queue
	}
	m.UpdateWebhooks([]WebhookConfig{{URL: lateSrv.URL, Enabled: true, RetryMax: -1, Timeout: 20 * time.Second}})
	m.Fire(EventTest, "queued")

	// Run deliver on the queued event directly after Close: it is what a
	// worker would do once it pops the event.
	m.Close()
	m.deliver(&queuedEvent{
		webhook: WebhookConfig{URL: lateSrv.URL, Enabled: true, RetryMax: -1, Timeout: 20 * time.Second},
		event:   Event{ID: "queued", Type: EventTest},
	})
	if n := late.Load(); n != 0 {
		t.Fatalf("%d POST(s) after Close, want 0", n)
	}
}

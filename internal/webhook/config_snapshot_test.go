package webhook

import "testing"

func TestWebhookRegistrationOwnsConfig(t *testing.T) {
	cfg := []WebhookConfig{{URL: "https://original.test", Enabled: true, Events: []EventType{EventTest}, Headers: map[string]string{"X-Label": "original"}, Secret: "original"}}
	m := &Manager{queue: make(chan *queuedEvent, 8), logger: &testLogger{}}
	m.UpdateWebhooks(cfg)
	m.Fire(EventTest, nil)
	if len(m.queue) != 1 {
		t.Fatal("control event missing")
	}
	queued := <-m.queue
	release, done := make(chan struct{}), make(chan struct{})
	go func() {
		<-release
		cfg[0].URL = "https://changed.test"
		cfg[0].Enabled = false
		cfg[0].Events[0] = EventDomainAdd
		cfg[0].Headers["X-Label"] = "changed"
		cfg[0].Secret = "changed"
		close(done)
	}()
	close(release)
	<-done
	if queued.webhook.URL != "https://original.test" || queued.webhook.Headers["X-Label"] != "original" || queued.webhook.Events[0] != EventTest || queued.webhook.Secret != "original" {
		t.Fatal("queued config changed", queued.webhook)
	}
	m.Fire(EventTest, nil)
	if len(m.queue) != 1 {
		t.Fatal("registered subscription changed")
	}
	<-m.queue
	m.Fire(EventDomainAdd, nil)
	if len(m.queue) != 0 {
		t.Fatal("foreign event matched")
	}
	m.UpdateWebhooks(nil)
	m.Fire(EventTest, nil)
	if len(m.queue) != 0 || m.webhooks != nil {
		t.Fatal("clear failed")
	}
	m.UpdateWebhooks([]WebhookConfig{{URL: "https://new.test", Enabled: true}})
	m.Fire(EventDomainAdd, nil)
	if len(m.queue) != 1 {
		t.Fatal("empty events must match all")
	}
	fresh := <-m.queue
	if fresh.webhook.URL != "https://new.test" || fresh.webhook.Events != nil || fresh.webhook.Headers != nil {
		t.Fatal("fresh/nil config", fresh.webhook)
	}
	m.UpdateWebhooks([]WebhookConfig{})
	if m.webhooks == nil {
		t.Fatal("empty slice lost")
	}
}

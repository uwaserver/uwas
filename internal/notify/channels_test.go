package notify

import (
	"testing"
)

func TestSendDisabledChannel(t *testing.T) {
	ch := Channel{
		Type:    "webhook",
		Enabled: false,
		Config:  map[string]string{"url": "http://example.com/hook"},
	}
	msg := Message{
		Level:  "info",
		Title:  "Test",
		Body:   "Test body",
		Source: "test",
	}

	err := Send(ch, msg)
	if err != nil {
		t.Errorf("Send with disabled channel should return nil, got: %v", err)
	}
}

func TestSendUnknownChannelType(t *testing.T) {
	ch := Channel{
		Type:    "unknown_type",
		Enabled: true,
		Config:  map[string]string{},
	}
	msg := Message{
		Level:  "info",
		Title:  "Test",
		Body:   "Test body",
		Source: "test",
	}

	err := Send(ch, msg)
	if err == nil {
		t.Error("Send with unknown channel type should return error")
	}
}

func TestMessageStruct(t *testing.T) {
	msg := Message{
		Level:  "critical",
		Title:  "Server Down",
		Body:   "The server is not responding",
		Source: "health_check",
	}

	if msg.Level != "critical" {
		t.Errorf("expected Level 'critical', got %q", msg.Level)
	}
	if msg.Title != "Server Down" {
		t.Errorf("expected Title 'Server Down', got %q", msg.Title)
	}
	if msg.Body != "The server is not responding" {
		t.Errorf("expected Body 'The server is not responding', got %q", msg.Body)
	}
	if msg.Source != "health_check" {
		t.Errorf("expected Source 'health_check', got %q", msg.Source)
	}
}

func TestChannelStruct(t *testing.T) {
	ch := Channel{
		Type:    "slack",
		Enabled: true,
		Config: map[string]string{
			"webhook_url": "https://hooks.slack.com/test",
		},
	}

	if ch.Type != "slack" {
		t.Errorf("expected Type 'slack', got %q", ch.Type)
	}
	if !ch.Enabled {
		t.Error("expected Enabled=true")
	}
	if ch.Config["webhook_url"] != "https://hooks.slack.com/test" {
		t.Errorf("expected webhook_url in config, got %q", ch.Config["webhook_url"])
	}
}

func TestSendDisabledChannelAllTypes(t *testing.T) {
	types := []string{"webhook", "slack", "telegram", "email"}
	msg := Message{Level: "info", Title: "Test", Body: "body", Source: "test"}

	for _, typ := range types {
		ch := Channel{Type: typ, Enabled: false}
		err := Send(ch, msg)
		if err != nil {
			t.Errorf("Send with disabled %s channel should return nil, got: %v", typ, err)
		}
	}
}

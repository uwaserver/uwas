package notify

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/smtp"
	"strings"
	"testing"
)

// --- Existing tests ---

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

// --- New comprehensive tests ---

func testMsg() Message {
	return Message{
		Level:  "info",
		Title:  "Test Alert",
		Body:   "Something happened",
		Source: "unit_test",
	}
}

// sendWebhook tests

func TestSendWebhookSuccess(t *testing.T) {
	var received Message
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &received)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	msg := testMsg()
	err := sendWebhook(srv.URL, msg)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if received.Title != msg.Title {
		t.Errorf("expected title %q, got %q", msg.Title, received.Title)
	}
	if received.Level != msg.Level {
		t.Errorf("expected level %q, got %q", msg.Level, received.Level)
	}
	if received.Body != msg.Body {
		t.Errorf("expected body %q, got %q", msg.Body, received.Body)
	}
	if received.Source != msg.Source {
		t.Errorf("expected source %q, got %q", msg.Source, received.Source)
	}
}

func TestSendWebhookHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	err := sendWebhook(srv.URL, testMsg())
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	if !strings.Contains(err.Error(), "webhook returned 500") {
		t.Errorf("expected 'webhook returned 500', got: %v", err)
	}
}

func TestSendWebhookNetworkError(t *testing.T) {
	err := sendWebhook("http://127.0.0.1:1/nonexistent", testMsg())
	if err == nil {
		t.Fatal("expected network error")
	}
}

// sendSlack tests

func TestSendSlackSuccess(t *testing.T) {
	levels := []struct {
		level string
		emoji string
	}{
		{"info", "\u2139\ufe0f"},
		{"warning", "\u26a0\ufe0f"},
		{"critical", "\U0001f6a8"},
	}

	for _, tc := range levels {
		t.Run(tc.level, func(t *testing.T) {
			var receivedText string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				var payload map[string]string
				json.Unmarshal(body, &payload)
				receivedText = payload["text"]
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()

			msg := testMsg()
			msg.Level = tc.level
			err := sendSlack(srv.URL, msg)
			if err != nil {
				t.Fatalf("expected no error, got: %v", err)
			}
			if !strings.Contains(receivedText, tc.emoji) {
				t.Errorf("expected text to contain emoji %q for level %q, got: %s", tc.emoji, tc.level, receivedText)
			}
			if !strings.Contains(receivedText, msg.Title) {
				t.Errorf("expected text to contain title %q, got: %s", msg.Title, receivedText)
			}
		})
	}
}

func TestSendSlackNetworkError(t *testing.T) {
	err := sendSlack("http://127.0.0.1:1/nonexistent", testMsg())
	if err == nil {
		t.Fatal("expected network error")
	}
}

// sendTelegram tests

func TestSendTelegramSuccess(t *testing.T) {
	levels := []struct {
		level string
		emoji string
	}{
		{"info", "\u2139\ufe0f"},
		{"warning", "\u26a0\ufe0f"},
		{"critical", "\U0001f6a8"},
	}

	for _, tc := range levels {
		t.Run(tc.level, func(t *testing.T) {
			var receivedPayload map[string]string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				json.Unmarshal(body, &receivedPayload)
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()

			origBase := telegramAPIBase
			telegramAPIBase = srv.URL
			t.Cleanup(func() { telegramAPIBase = origBase })

			msg := testMsg()
			msg.Level = tc.level
			err := sendTelegram("testbot123", "chat456", msg)
			if err != nil {
				t.Fatalf("expected no error, got: %v", err)
			}
			if receivedPayload["chat_id"] != "chat456" {
				t.Errorf("expected chat_id 'chat456', got %q", receivedPayload["chat_id"])
			}
			if receivedPayload["parse_mode"] != "HTML" {
				t.Errorf("expected parse_mode 'HTML', got %q", receivedPayload["parse_mode"])
			}
			text := receivedPayload["text"]
			if !strings.Contains(text, tc.emoji) {
				t.Errorf("expected text to contain emoji %q for level %q, got: %s", tc.emoji, tc.level, text)
			}
			if !strings.Contains(text, msg.Title) {
				t.Errorf("expected text to contain title %q, got: %s", msg.Title, text)
			}
		})
	}
}

func TestSendTelegramNetworkError(t *testing.T) {
	origBase := telegramAPIBase
	telegramAPIBase = "http://127.0.0.1:1"
	t.Cleanup(func() { telegramAPIBase = origBase })

	err := sendTelegram("token", "chat", testMsg())
	if err == nil {
		t.Fatal("expected network error")
	}
}

// sendEmail tests

func TestSendEmailMissingHost(t *testing.T) {
	cfg := map[string]string{
		"to": "admin@example.com",
	}
	err := sendEmail(cfg, testMsg())
	if err == nil {
		t.Fatal("expected error for missing smtp_host")
	}
	if !strings.Contains(err.Error(), "smtp_host and to are required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSendEmailMissingTo(t *testing.T) {
	cfg := map[string]string{
		"smtp_host": "mail.example.com",
	}
	err := sendEmail(cfg, testMsg())
	if err == nil {
		t.Fatal("expected error for missing to")
	}
	if !strings.Contains(err.Error(), "smtp_host and to are required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSendEmailSuccess(t *testing.T) {
	origFn := smtpSendMailFn
	t.Cleanup(func() { smtpSendMailFn = origFn })

	var capturedAddr string
	var capturedFrom string
	var capturedTo []string
	var capturedBody []byte
	var capturedAuth smtp.Auth

	smtpSendMailFn = func(addr string, a smtp.Auth, from string, to []string, msg []byte) error {
		capturedAddr = addr
		capturedAuth = a
		capturedFrom = from
		capturedTo = to
		capturedBody = msg
		return nil
	}

	cfg := map[string]string{
		"smtp_host": "mail.example.com",
		"smtp_port": "465",
		"smtp_user": "user@example.com",
		"smtp_pass": "secret",
		"from":      "alerts@example.com",
		"to":        "admin@example.com",
	}
	msg := testMsg()
	err := sendEmail(cfg, msg)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if capturedAddr != "mail.example.com:465" {
		t.Errorf("expected addr 'mail.example.com:465', got %q", capturedAddr)
	}
	if capturedFrom != "alerts@example.com" {
		t.Errorf("expected from 'alerts@example.com', got %q", capturedFrom)
	}
	if len(capturedTo) != 1 || capturedTo[0] != "admin@example.com" {
		t.Errorf("expected to ['admin@example.com'], got %v", capturedTo)
	}
	if capturedAuth == nil {
		t.Error("expected auth to be set when smtp_user is provided")
	}
	bodyStr := string(capturedBody)
	if !strings.Contains(bodyStr, "[UWAS] INFO: Test Alert") {
		t.Errorf("expected subject in body, got: %s", bodyStr)
	}
	if !strings.Contains(bodyStr, msg.Body) {
		t.Errorf("expected message body in email, got: %s", bodyStr)
	}
}

func TestSendEmailDefaultPort(t *testing.T) {
	origFn := smtpSendMailFn
	t.Cleanup(func() { smtpSendMailFn = origFn })

	var capturedAddr string
	smtpSendMailFn = func(addr string, a smtp.Auth, from string, to []string, msg []byte) error {
		capturedAddr = addr
		return nil
	}

	cfg := map[string]string{
		"smtp_host": "mail.example.com",
		"smtp_user": "user@example.com",
		"smtp_pass": "secret",
		"from":      "alerts@example.com",
		"to":        "admin@example.com",
	}
	err := sendEmail(cfg, testMsg())
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if capturedAddr != "mail.example.com:587" {
		t.Errorf("expected default port 587, got addr %q", capturedAddr)
	}
}

func TestSendEmailFromDefaultsToUser(t *testing.T) {
	origFn := smtpSendMailFn
	t.Cleanup(func() { smtpSendMailFn = origFn })

	var capturedFrom string
	smtpSendMailFn = func(addr string, a smtp.Auth, from string, to []string, msg []byte) error {
		capturedFrom = from
		return nil
	}

	cfg := map[string]string{
		"smtp_host": "mail.example.com",
		"smtp_user": "user@example.com",
		"smtp_pass": "secret",
		"to":        "admin@example.com",
		// "from" intentionally omitted
	}
	err := sendEmail(cfg, testMsg())
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if capturedFrom != "user@example.com" {
		t.Errorf("expected from to default to smtp_user 'user@example.com', got %q", capturedFrom)
	}
}

func TestSendEmailWithoutAuth(t *testing.T) {
	origFn := smtpSendMailFn
	t.Cleanup(func() { smtpSendMailFn = origFn })

	var capturedAuth smtp.Auth
	smtpSendMailFn = func(addr string, a smtp.Auth, from string, to []string, msg []byte) error {
		capturedAuth = a
		return nil
	}

	cfg := map[string]string{
		"smtp_host": "mail.example.com",
		"from":      "alerts@example.com",
		"to":        "admin@example.com",
		// no smtp_user / smtp_pass
	}
	err := sendEmail(cfg, testMsg())
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if capturedAuth != nil {
		t.Error("expected auth to be nil when no smtp_user is provided")
	}
}

func TestSendEmailSMTPFailure(t *testing.T) {
	origFn := smtpSendMailFn
	t.Cleanup(func() { smtpSendMailFn = origFn })

	smtpSendMailFn = func(addr string, a smtp.Auth, from string, to []string, msg []byte) error {
		return fmt.Errorf("SMTP connection refused")
	}

	cfg := map[string]string{
		"smtp_host": "mail.example.com",
		"smtp_user": "user@example.com",
		"smtp_pass": "secret",
		"from":      "alerts@example.com",
		"to":        "admin@example.com",
	}
	err := sendEmail(cfg, testMsg())
	if err == nil {
		t.Fatal("expected SMTP error")
	}
	if !strings.Contains(err.Error(), "SMTP connection refused") {
		t.Errorf("unexpected error: %v", err)
	}
}

// Full Send() dispatch tests for each channel type

func TestSendDispatchWebhook(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ch := Channel{
		Type:    "webhook",
		Enabled: true,
		Config:  map[string]string{"url": srv.URL},
	}
	err := Send(ch, testMsg())
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !called {
		t.Error("expected webhook handler to be called")
	}
}

func TestSendDispatchSlack(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ch := Channel{
		Type:    "slack",
		Enabled: true,
		Config:  map[string]string{"webhook_url": srv.URL},
	}
	err := Send(ch, testMsg())
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !called {
		t.Error("expected slack handler to be called")
	}
}

func TestSendDispatchTelegram(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	origBase := telegramAPIBase
	telegramAPIBase = srv.URL
	t.Cleanup(func() { telegramAPIBase = origBase })

	ch := Channel{
		Type:    "telegram",
		Enabled: true,
		Config:  map[string]string{"bot_token": "tok", "chat_id": "123"},
	}
	err := Send(ch, testMsg())
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !called {
		t.Error("expected telegram handler to be called")
	}
}

func TestSendDispatchEmail(t *testing.T) {
	origFn := smtpSendMailFn
	t.Cleanup(func() { smtpSendMailFn = origFn })

	var called bool
	smtpSendMailFn = func(addr string, a smtp.Auth, from string, to []string, msg []byte) error {
		called = true
		return nil
	}

	ch := Channel{
		Type:    "email",
		Enabled: true,
		Config: map[string]string{
			"smtp_host": "mail.example.com",
			"smtp_user": "user@example.com",
			"smtp_pass": "pass",
			"from":      "alerts@example.com",
			"to":        "admin@example.com",
		},
	}
	err := Send(ch, testMsg())
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !called {
		t.Error("expected email handler to be called")
	}
}

func TestSendEmailMultipleRecipients(t *testing.T) {
	origFn := smtpSendMailFn
	t.Cleanup(func() { smtpSendMailFn = origFn })

	var capturedTo []string
	smtpSendMailFn = func(addr string, a smtp.Auth, from string, to []string, msg []byte) error {
		capturedTo = to
		return nil
	}

	cfg := map[string]string{
		"smtp_host": "mail.example.com",
		"from":      "alerts@example.com",
		"to":        "admin@example.com,ops@example.com",
	}
	err := sendEmail(cfg, testMsg())
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(capturedTo) != 2 {
		t.Fatalf("expected 2 recipients, got %d", len(capturedTo))
	}
	if capturedTo[0] != "admin@example.com" || capturedTo[1] != "ops@example.com" {
		t.Errorf("unexpected recipients: %v", capturedTo)
	}
}

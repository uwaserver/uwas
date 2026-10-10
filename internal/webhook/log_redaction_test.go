package webhook

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type capLogger struct {
	mu   sync.Mutex
	logs []string
}

func (l *capLogger) add(msg string, args []any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.logs = append(l.logs, msg+" "+fmt.Sprint(args...))
}
func (l *capLogger) Debug(msg string, args ...any) { l.add(msg, args) }
func (l *capLogger) Info(msg string, args ...any)  { l.add(msg, args) }
func (l *capLogger) Warn(msg string, args ...any)  { l.add(msg, args) }
func (l *capLogger) Error(msg string, args ...any) { l.add(msg, args) }
func (l *capLogger) all() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.logs, "\n")
}

// F1000: the receiver URL is a credential (Slack hook path, ?token=); delivery
// logs must keep the cause but not the URL path/query.
func TestDeliveryLogsOmitReceiverCredential(t *testing.T) {
	const secret = "T0K3N-s3cr3t"
	srv := httptest.NewServer(nil)
	base := srv.URL
	srv.Close() // connection refused

	run := func(path string) string {
		lg := &capLogger{}
		m := NewManager("", lg)
		defer m.Close()
		m.urlSafe, m.dialControl = func(string) error { return nil }, nil
		wh := WebhookConfig{URL: base + path, Enabled: true, RetryMax: -1, Timeout: time.Second}
		m.deliver(&queuedEvent{webhook: wh, event: Event{ID: "1", Type: EventTest}})
		return lg.all()
	}

	out := run("/services/" + secret + "?token=" + secret)
	if strings.Contains(out, secret) || !strings.Contains(out, "connection refused") {
		t.Fatalf("EXPECTED: log keeps cause, omits credential\nACTUAL: %s\nPROBLEM CONFIRMED", out)
	}
	// control: same dead receiver without credential still logs the cause
	if c := run("/hook"); !strings.Contains(c, "connection refused") {
		t.Fatalf("control failed: %s", c)
	}
	// SSRF-blocked path also must not leak the credential
	lg := &capLogger{}
	m := NewManager("", lg)
	defer m.Close()
	m.urlSafe = func(string) error { return fmt.Errorf("blocked") }
	m.FireTo("http://10.0.0.1/services/"+secret, EventTest, nil)
	if strings.Contains(lg.all(), secret) || !strings.Contains(lg.all(), "blocked") {
		t.Fatalf("EXPECTED: SSRF log omits credential\nACTUAL: %s\nPROBLEM CONFIRMED", lg.all())
	}
	t.Log("PROBLEM NOT REPRODUCED")
}

func TestLogURLKeepsOnlySchemeAndHost(t *testing.T) {
	cases := map[string]string{
		"https://hooks.example.com/services/A/B/C?token=x": "https://hooks.example.com",
		"http://h:8080/p": "http://h:8080",
		"::not a url":     "(unparsable url)",
		"":                "(unparsable url)",
	}
	for in, want := range cases {
		if got := logURL(in); got != want {
			t.Errorf("logURL(%q) = %q, want %q", in, got, want)
		}
	}
	t.Log("FIX VERIFIED")
}

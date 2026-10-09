package alerting

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// The legacy global.alerting.webhook_url used to be posted with a bare
// http.Client, so an internal URL — or an external receiver that 302s to one —
// reached loopback / metadata targets the notify and webhook packages refuse.
func TestSendWebhookRefusesInternalTarget(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	}))
	defer srv.Close()

	a := New(true, srv.URL, nil, testLogger())
	if err := a.sendWebhook(Alert{Type: "t"}); err == nil {
		t.Fatal("expected loopback webhook URL to be refused")
	}

	// URL check bypassed (DNS rebinding): the dial-time control still refuses.
	b := New(true, srv.URL, nil, testLogger())
	b.urlSafetyCheck = nil
	if err := b.sendWebhook(Alert{Type: "t"}); err == nil {
		t.Fatal("expected dial-time control to refuse loopback")
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("internal target received %d requests, want 0", n)
	}
}

func TestSendWebhookRefusesRedirectToInternalTarget(t *testing.T) {
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	}))
	defer redir.Close()

	a := New(true, redir.URL, nil, testLogger())
	a.dialControl = nil
	a.urlSafetyCheck = func(u string) error {
		if strings.HasPrefix(u, redir.URL) {
			return nil // stands in for a public receiver
		}
		return config.IsWebhookURLSafe(u)
	}
	err := a.sendWebhook(Alert{Type: "t"})
	if err == nil || !strings.Contains(err.Error(), "SSRF") {
		t.Fatalf("redirect to metadata IP not refused: %v", err)
	}
}

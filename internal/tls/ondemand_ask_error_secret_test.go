package uwastls

import (
	"bytes"
	"crypto/tls"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// The on-demand ask URL usually carries a shared secret in its query. Neither
// the log line nor the error returned from the handshake may copy it (F2112).
func TestOnDemandAskFailureOmitsURLSecret(t *testing.T) {
	run := func(ask string) (logged, returned string) {
		t.Helper()
		var buf bytes.Buffer
		lg := logger.New("error", "text")
		lg.Logger = slog.New(slog.NewTextHandler(&buf, nil))
		m := NewManager(config.ACMEConfig{
			Email: "test@example.com", CAURL: "https://acme.example.com/directory",
			Storage: t.TempDir(), OnDemand: true, OnDemandAsk: ask,
		}, nil, lg)
		_, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: "error-domain.com"})
		if err == nil {
			t.Fatal("expected an ask failure")
		}
		return buf.String(), err.Error()
	}

	// Control: the failure and its cause are still reported.
	l, e := run("http://127.0.0.1:1/check")
	if !strings.Contains(l, "on-demand ask failed") || !strings.Contains(l, "connection refused") ||
		!strings.Contains(e, "on-demand ask error") {
		t.Fatalf("control: logged=%q returned=%q", l, e)
	}

	for _, ask := range []string{
		"http://127.0.0.1:1/check?token=ASKSECRET123",
		"http://ops:ASKSECRET123@127.0.0.1:1/check",
		"http://127.0.0.1:1/check?a=1&key=ASKSECRET123&b=2",
	} {
		l, e := run(ask)
		if strings.Contains(l, "ASKSECRET123") || strings.Contains(e, "ASKSECRET123") {
			t.Errorf("%s: secret leaked: logged=%s returned=%s", ask, strings.TrimSpace(l), e)
		}
	}

	// A rejecting ask endpoint still produces the rejection error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	if _, e := run(srv.URL + "/check?token=ASKSECRET123"); !strings.Contains(e, "rejected") || strings.Contains(e, "ASKSECRET123") {
		t.Errorf("rejected ask: returned=%q", e)
	}
}

// A malformed ask URL's parse error also quotes the raw string.
func TestOnDemandAskInvalidURLOmitsSecret(t *testing.T) {
	m := NewManager(config.ACMEConfig{
		Email: "test@example.com", CAURL: "https://acme.example.com/directory",
		Storage: t.TempDir(), OnDemand: true, OnDemandAsk: "http://127.0.0.1:1/check?token=ASKSECRET123\x7f",
	}, nil, logger.New("error", "text"))
	_, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: "error-domain.com"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "ASKSECRET123") {
		t.Errorf("invalid-URL error leaks the secret: %v", err)
	}
}

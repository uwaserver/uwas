package server

import (
	"crypto/tls"
	"net/http/httptest"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// handleHTTP redirects to HTTPS when a cert is loaded. The router serves
// "Host: secure.example.com.", the :80 form and the derived www variant as the
// same domain, so the redirect must not depend on how the client spelled it:
// a cert lookup on the raw Host string left those requests on plain HTTP (F2560).
func TestHTTPSRedirectIndependentOfHostSpelling(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{WorkerCount: "1", LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{
			{Host: "secure.example.com", Root: t.TempDir(), Type: "static", SSL: config.SSLConfig{Mode: "auto"}},
			{Host: "*.wild.example.org", Root: t.TempDir(), Type: "static", SSL: config.SSLConfig{Mode: "auto"}},
			{Host: "nocert.example.net", Root: t.TempDir(), Type: "static", SSL: config.SSLConfig{Mode: "auto"}},
		},
	}
	s := New(cfg, logger.New("error", "text"))
	s.tlsMgr.RegisterCert("secure.example.com", &tls.Certificate{})
	s.tlsMgr.RegisterCert("one.wild.example.org", &tls.Certificate{}) // per-host cert under a wildcard domain

	status := func(host string) (int, string) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/page?x=1", nil)
		req.Host = host
		s.handleHTTP(rec, req)
		return rec.Code, rec.Header().Get("Location")
	}

	for _, h := range []string{
		"secure.example.com", "SECURE.example.com", "secure.example.com.",
		"secure.example.com:80", "www.secure.example.com",
	} {
		if code, loc := status(h); code != 301 || loc != "https://secure.example.com/page?x=1" {
			t.Errorf("Host %q: status=%d Location=%q, want 301 to https://secure.example.com/page?x=1", h, code, loc)
		}
	}
	// A per-host cert under a wildcard domain still triggers the redirect.
	if code, _ := status("one.wild.example.org"); code != 301 {
		t.Errorf("wildcard domain with per-host cert: status=%d, want 301", code)
	}
	// Without a cert the site stays reachable over plain HTTP, whatever the spelling.
	for _, h := range []string{"nocert.example.net", "NOCERT.example.net.", "nocert.example.net:80"} {
		if code, _ := status(h); code == 301 {
			t.Errorf("Host %q: redirected without a loaded cert", h)
		}
	}
}

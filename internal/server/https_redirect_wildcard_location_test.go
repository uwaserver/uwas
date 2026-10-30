package server

import (
	"crypto/tls"
	"net/http/httptest"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// F47600: the plain-HTTP redirect to HTTPS targets the domain's configured
// host (never the client's Host, which could be forged to aim the redirect at
// an arbitrary site). For a wildcard domain that configured host is the
// pattern itself, so http://a.wild.example.org/p was answered
// "301 Location: https://*.wild.example.org/p" — a URL no browser can follow —
// and every plain-HTTP request to a wildcard SSL site ended on it. The
// existing wildcard case only checked the status code. A wildcard domain has
// no single configured host, so the redirect keeps the request's own host,
// which the wildcard match already proved is inside the domain's suffix.
func TestHTTPSRedirectWildcardDomainKeepsRequestHost(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{WorkerCount: "1", LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{
			{Host: "*.wild.example.org", Root: t.TempDir(), Type: "static", SSL: config.SSLConfig{Mode: "auto", ForceSSL: true}},
			{Host: "secure.example.com", Root: t.TempDir(), Type: "static", SSL: config.SSLConfig{Mode: "auto"}},
			{Host: "main.example.net", Aliases: []string{"*.al.example.net"}, Root: t.TempDir(), Type: "static", SSL: config.SSLConfig{Mode: "auto", ForceSSL: true}},
		},
	}
	s := New(cfg, logger.New("error", "text"))
	s.tlsMgr.RegisterCert("secure.example.com", &tls.Certificate{})

	redirect := func(host string) (int, string) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/page?x=1", nil)
		req.Host = host
		s.handleHTTP(rec, req)
		return rec.Code, rec.Header().Get("Location")
	}

	t.Run("wildcard domain", func(t *testing.T) {
		for host, want := range map[string]string{
			"a.wild.example.org":      "https://a.wild.example.org/page?x=1",
			"A.Wild.Example.ORG":      "https://a.wild.example.org/page?x=1",
			"a.wild.example.org.":     "https://a.wild.example.org/page?x=1",
			"a.wild.example.org:80":   "https://a.wild.example.org/page?x=1",
			"deep.a.wild.example.org": "https://deep.a.wild.example.org/page?x=1",
		} {
			code, loc := redirect(host)
			t.Logf("EXPECTED: 301 %s\nACTUAL: %d %s", want, code, loc)
			if code != 301 || loc != want {
				t.Errorf("PROBLEM CONFIRMED: Host %q: status=%d Location=%q, want 301 %q", host, code, loc, want)
			}
		}
	})

	// Control: an exact host still redirects to its configured name, whatever
	// spelling or www variant the client used (F2560 behaviour is unchanged).
	t.Run("exact host unchanged", func(t *testing.T) {
		for _, host := range []string{"secure.example.com", "SECURE.example.com:80", "www.secure.example.com"} {
			if code, loc := redirect(host); code != 301 || loc != "https://secure.example.com/page?x=1" {
				t.Errorf("Host %q: status=%d Location=%q, want 301 https://secure.example.com/page?x=1", host, code, loc)
			}
		}
	})

	// Control: aliases of an exact domain keep redirecting to the configured
	// host (only a wildcard domain has no single host to target).
	t.Run("alias of exact domain unchanged", func(t *testing.T) {
		for _, host := range []string{"main.example.net", "x.al.example.net"} {
			if code, loc := redirect(host); code != 301 || loc != "https://main.example.net/page?x=1" {
				t.Errorf("Host %q: status=%d Location=%q, want 301 https://main.example.net/page?x=1", host, code, loc)
			}
		}
	})
}

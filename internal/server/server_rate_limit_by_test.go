package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/middleware"
)

// security.rate_limit.by was dead configuration: SPECIFICATION.md documents
// `by: ip | header:X-Forwarded-For` and nothing read it, so every limiter
// keyed on the client address whatever the domain asked for.

func rateFixture(t *testing.T, rl config.RateLimitConfig, trusted []string) http.Handler {
	t.Helper()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("yaz: %v", err)
	}
	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1", LogLevel: "error", LogFormat: "text",
			TrustedProxies: trusted,
		},
		Domains: []config.Domain{{
			Host:     "rl.test",
			Type:     "static",
			Root:     root,
			SSL:      config.SSLConfig{Mode: "off"},
			Security: config.SecurityConfig{RateLimit: rl},
		}},
	}
	s := New(cfg, logger.New("error", "text"))
	t.Cleanup(func() { s.cancel() })
	return s.buildMiddlewareChain()
}

// rateRequest sends one request with a given RemoteAddr and headers.
func rateRequest(h http.Handler, remoteAddr string, headers map[string]string) int {
	req := httptest.NewRequest(http.MethodGet, "/index.html", nil)
	req.Host = "rl.test"
	req.RemoteAddr = remoteAddr
	req.Header.Set("User-Agent", "uwas-test")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

// Behind a trusted proxy, two different clients must get their own budgets.
// This already works — the real-IP middleware rewrites r.RemoteAddr from
// X-Forwarded-For before dispatch, so the limiter's key is the client, not
// the proxy. Pinned here because the `by` work below moves key construction,
// and this is the property that must survive the move.
func TestRateLimitHonoursTrustedProxies(t *testing.T) {
	h := rateFixture(t,
		config.RateLimitConfig{Requests: 2, Window: config.Duration{}},
		[]string{"10.0.0.1/32"})

	// Client A exhausts its budget through the proxy.
	for i := 0; i < 2; i++ {
		if code := rateRequest(h, "10.0.0.1:1234", map[string]string{"X-Forwarded-For": "203.0.113.10"}); code != http.StatusOK {
			t.Fatalf("client A request %d: status %d", i+1, code)
		}
	}
	if code := rateRequest(h, "10.0.0.1:1234", map[string]string{"X-Forwarded-For": "203.0.113.10"}); code != http.StatusTooManyRequests {
		t.Fatalf("client A was not limited: status %d", code)
	}

	// Client B, through the same proxy, must still be served.
	if code := rateRequest(h, "10.0.0.1:1234", map[string]string{"X-Forwarded-For": "203.0.113.99"}); code != http.StatusOK {
		t.Errorf("client B status = %d, want 200 — the limit is keyed on the proxy address, so every visitor shares one bucket", code)
	}
}

// by: header:<name> must key on that header.
func TestRateLimitByHeader(t *testing.T) {
	h := rateFixture(t,
		config.RateLimitConfig{Requests: 2, By: "header:X-API-Key"},
		nil)

	for i := 0; i < 2; i++ {
		if code := rateRequest(h, "203.0.113.1:5000", map[string]string{"X-API-Key": "anahtar-a"}); code != http.StatusOK {
			t.Fatalf("client A request %d: status %d", i+1, code)
		}
	}
	if code := rateRequest(h, "203.0.113.1:5000", map[string]string{"X-API-Key": "anahtar-a"}); code != http.StatusTooManyRequests {
		t.Fatalf("client A was not limited: status %d", code)
	}

	// Same IP, different key: its own budget.
	if code := rateRequest(h, "203.0.113.1:5000", map[string]string{"X-API-Key": "anahtar-b"}); code != http.StatusOK {
		t.Errorf("client B status = %d, want 200 — by: header is not applied", code)
	}
}

// by: ip (and an unset by) must keep keying on the client address.
func TestRateLimitByIPIsDefault(t *testing.T) {
	for _, by := range []string{"", "ip"} {
		h := rateFixture(t, config.RateLimitConfig{Requests: 2, By: by}, nil)

		for i := 0; i < 2; i++ {
			if code := rateRequest(h, "203.0.113.1:5000", nil); code != http.StatusOK {
				t.Fatalf("by=%q newRequest %d: durum %d", by, i+1, code)
			}
		}
		if code := rateRequest(h, "203.0.113.1:5000", nil); code != http.StatusTooManyRequests {
			t.Errorf("by=%q the limit was not applied: status %d", by, code)
		}
		if code := rateRequest(h, "203.0.113.2:5000", nil); code != http.StatusOK {
			t.Errorf("by=%q another address was blocked: status %d", by, code)
		}
	}
}

// A request missing the keying header must not share one bucket with every
// other header-less request: that would be a trivial way to exhaust the limit
// for everyone. It falls back to the client address.
func TestRateLimitMissingHeaderFallsBackToIP(t *testing.T) {
	h := rateFixture(t, config.RateLimitConfig{Requests: 2, By: "header:X-API-Key"}, nil)

	for i := 0; i < 2; i++ {
		if code := rateRequest(h, "203.0.113.1:5000", nil); code != http.StatusOK {
			t.Fatalf("newRequest %d: durum %d", i+1, code)
		}
	}
	if code := rateRequest(h, "203.0.113.1:5000", nil); code != http.StatusTooManyRequests {
		t.Fatalf("a request without the header was not limited: status %d", code)
	}
	if code := rateRequest(h, "203.0.113.2:5000", nil); code != http.StatusOK {
		t.Errorf("another address was blocked: status %d — header-less requests share one bucket", code)
	}
}

// Startup and reload share buildDomainRateLimiters, so `by` cannot be applied
// in one path and forgotten in the other — which would make it work until the
// first config reload.
func TestBuildDomainRateLimitersCarriesKeyBy(t *testing.T) {
	limiters := buildDomainRateLimiters(t.Context(), []config.Domain{{
		Host:     "rl.test",
		Security: config.SecurityConfig{RateLimit: config.RateLimitConfig{Requests: 2, By: "header:X-API-Key"}},
	}}, nil, logger.New("error", "text"))

	rl := limiters["rl.test"]
	if rl == nil {
		t.Fatal("no limiter was built")
	}

	a := httptest.NewRequest(http.MethodGet, "/", nil)
	a.RemoteAddr = "203.0.113.1:5000"
	a.Header.Set("X-API-Key", "anahtar-a")
	b := httptest.NewRequest(http.MethodGet, "/", nil)
	b.RemoteAddr = "203.0.113.1:5000"
	b.Header.Set("X-API-Key", "anahtar-b")

	if rl.Key(a) == rl.Key(b) {
		t.Error("by: header did not carry — two keys from one address share a bucket")
	}
}

// A domain with no rate limit must produce no limiter.
func TestBuildDomainRateLimitersSkipsUnlimited(t *testing.T) {
	limiters := buildDomainRateLimiters(t.Context(), []config.Domain{{Host: "plain.test"}}, nil, logger.New("error", "text"))
	if len(limiters) != 0 {
		t.Errorf("a limiter was built for a domain with no limit: %v", limiters)
	}
}

func TestKnownRateLimitKey(t *testing.T) {
	for _, ok := range []string{"", "ip", "IP", " ip ", "header:X-API-Key", "HEADER:X-Api-Key"} {
		if !middleware.KnownRateLimitKey(ok) {
			t.Errorf("%q was not recognised", ok)
		}
	}
	for _, kotu := range []string{"header:", "header", "cookie:sid", "nonsense"} {
		if middleware.KnownRateLimitKey(kotu) {
			t.Errorf("%q was recognised", kotu)
		}
	}
}

// An unrecognised value must still produce a working limiter, keyed per
// client address — not a nil limiter or a shared bucket.
func TestUnknownKeyByStillLimitsPerAddress(t *testing.T) {
	h := rateFixture(t, config.RateLimitConfig{Requests: 2, By: "cookie:sid"}, nil)

	for i := 0; i < 2; i++ {
		if code := rateRequest(h, "203.0.113.1:5000", nil); code != http.StatusOK {
			t.Fatalf("newRequest %d: durum %d", i+1, code)
		}
	}
	if code := rateRequest(h, "203.0.113.1:5000", nil); code != http.StatusTooManyRequests {
		t.Errorf("the limit was not applied with an unrecognised by: status %d", code)
	}
	if code := rateRequest(h, "203.0.113.2:5000", nil); code != http.StatusOK {
		t.Errorf("another address was blocked: status %d — everything fell into one bucket", code)
	}
}

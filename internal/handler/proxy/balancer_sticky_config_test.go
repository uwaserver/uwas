package proxy

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// proxy.sticky was dead configuration. The documented block —
// type / cookie_name / ttl — sat alongside `algorithm`, but nothing read it:
// affinity was reachable only through the undocumented `algorithm: sticky`,
// and the cookie name and TTL were hardcoded to "uwas_sticky"/3600.

func testBackends(t *testing.T, hosts ...string) []*Backend {
	t.Helper()
	out := make([]*Backend, 0, len(hosts))
	for _, h := range hosts {
		u, err := url.Parse("http://" + h)
		if err != nil {
			t.Fatalf("url: %v", err)
		}
		out = append(out, &Backend{URL: u, Weight: 1})
	}
	return out
}

func TestStickyUsesConfiguredCookieName(t *testing.T) {
	b := NewBalancerFor(config.ProxyConfig{
		Algorithm: "round_robin",
		Sticky:    config.StickyConfig{Type: "cookie", CookieName: "UWAS_UPSTREAM", TTL: 600},
	}, testLogger())

	sb, ok := b.(*StickyBalancer)
	if !ok {
		t.Fatalf("sticky.type=cookie did not produce a StickyBalancer: %T", b)
	}
	if sb.CookieName != "UWAS_UPSTREAM" {
		t.Errorf("CookieName = %q, want UWAS_UPSTREAM", sb.CookieName)
	}
	if sb.TTL != 600 {
		t.Errorf("TTL = %d, want 600", sb.TTL)
	}

	// The configured name must be the one actually read off the request.
	backends := testBackends(t, "10.0.0.1:80", "10.0.0.2:80")
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: "UWAS_UPSTREAM", Value: "10.0.0.2:80"})
	if got := sb.Select(backends, r); got == nil || got.URL.Host != "10.0.0.2:80" {
		t.Errorf("the backend pinned by the cookie was not chosen: %v", got)
	}

	// ...and the one written back.
	w := httptest.NewRecorder()
	SetStickyCookie(w, sb.CookieName, "10.0.0.2:80", sb.TTL, false)
	c := w.Result().Cookies()
	if len(c) != 1 || c[0].Name != "UWAS_UPSTREAM" || c[0].MaxAge != 600 {
		t.Errorf("the cookie written = %+v", c)
	}
}

// An unset field must keep the value the code has always used, so a config
// that names sticky without tuning it behaves as before.
func TestStickyDefaultsWhenFieldsOmitted(t *testing.T) {
	b := NewBalancerFor(config.ProxyConfig{
		Algorithm: "round_robin",
		Sticky:    config.StickyConfig{Type: "cookie"},
	}, testLogger())

	sb, ok := b.(*StickyBalancer)
	if !ok {
		t.Fatalf("want *StickyBalancer, got %T", b)
	}
	if sb.CookieName != DefaultStickyCookieName || sb.TTL != DefaultStickyTTL {
		t.Errorf("the defaults were broken: %q / %d", sb.CookieName, sb.TTL)
	}
}

// sticky layers over the algorithm; it does not replace it. A request with no
// cookie must be placed by least_conn, not by round-robin.
func TestStickyFallsBackToConfiguredAlgorithm(t *testing.T) {
	b := NewBalancerFor(config.ProxyConfig{
		Algorithm: "least_conn",
		Sticky:    config.StickyConfig{Type: "cookie"},
	}, testLogger())

	sb, ok := b.(*StickyBalancer)
	if !ok {
		t.Fatalf("want *StickyBalancer, got %T", b)
	}
	if _, ok := sb.Fallback.(*LeastConn); !ok {
		t.Fatalf("fallback = %T, want *LeastConn — sticky is replacing the algorithm", sb.Fallback)
	}

	backends := testBackends(t, "10.0.0.1:80", "10.0.0.2:80")
	backends[0].ActiveConns.Add(5) // the first backend is busy
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := sb.Select(backends, r); got == nil || got.URL.Host != "10.0.0.2:80" {
		t.Errorf("a request with no cookie was not placed by least_conn: %v", got)
	}
}

// A cookie naming a backend that is no longer in the pool must not strand
// the request.
func TestStickyIgnoresStaleCookie(t *testing.T) {
	b := NewBalancerFor(config.ProxyConfig{
		Algorithm: "round_robin",
		Sticky:    config.StickyConfig{Type: "cookie", CookieName: "s"},
	}, testLogger())

	backends := testBackends(t, "10.0.0.1:80")
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: "s", Value: "10.9.9.9:80"})
	if got := b.Select(backends, r); got == nil || got.URL.Host != "10.0.0.1:80" {
		t.Errorf("a cookie pinned to a vanished backend stranded the request: %v", got)
	}
}

func TestStickyTypeIP(t *testing.T) {
	b := NewBalancerFor(config.ProxyConfig{
		Algorithm: "round_robin",
		Sticky:    config.StickyConfig{Type: "ip"},
	}, testLogger())
	if _, ok := b.(*IPHash); !ok {
		t.Errorf("sticky.type=ip → %T, want *IPHash", b)
	}
}

// header affinity is documented but the config carries no header name; it
// must fall back to the algorithm rather than silently pretending.
func TestStickyTypeHeaderFallsBack(t *testing.T) {
	b := NewBalancerFor(config.ProxyConfig{
		Algorithm: "least_conn",
		Sticky:    config.StickyConfig{Type: "header"},
	}, testLogger())
	if _, ok := b.(*LeastConn); !ok {
		t.Errorf("sticky.type=header → %T, want *LeastConn", b)
	}
}

func TestStickyUnknownTypeFallsBack(t *testing.T) {
	b := NewBalancerFor(config.ProxyConfig{
		Algorithm: "ip_hash",
		Sticky:    config.StickyConfig{Type: "nonsense"},
	}, testLogger())
	if _, ok := b.(*IPHash); !ok {
		t.Errorf("bilinmeyen tip → %T, want *IPHash", b)
	}
}

// No sticky block at all must leave the algorithm untouched.
func TestNoStickyBlockKeepsAlgorithm(t *testing.T) {
	for _, alg := range []string{"round_robin", "least_conn", "ip_hash", "uri_hash", "random", ""} {
		b := NewBalancerFor(config.ProxyConfig{Algorithm: alg}, testLogger())
		if _, ok := b.(*StickyBalancer); ok {
			t.Errorf("algorithm=%q produced a StickyBalancer with no sticky block", alg)
		}
	}
}

// The historical `algorithm: sticky` spelling must keep working, and must
// pick up cookie_name/ttl if the block is also present — without nesting a
// sticky balancer inside itself.
func TestLegacyAlgorithmSticky(t *testing.T) {
	b := NewBalancerFor(config.ProxyConfig{Algorithm: "sticky"}, testLogger())
	sb, ok := b.(*StickyBalancer)
	if !ok {
		t.Fatalf("algorithm=sticky → %T", b)
	}
	if sb.CookieName != DefaultStickyCookieName || sb.TTL != DefaultStickyTTL {
		t.Errorf("the legacy spelling lost the defaults: %q / %d", sb.CookieName, sb.TTL)
	}

	b2 := NewBalancerFor(config.ProxyConfig{
		Algorithm: "sticky",
		Sticky:    config.StickyConfig{Type: "cookie", CookieName: "x", TTL: 30},
	}, testLogger())
	sb2, ok := b2.(*StickyBalancer)
	if !ok {
		t.Fatalf("want *StickyBalancer, got %T", b2)
	}
	if sb2.CookieName != "x" || sb2.TTL != 30 {
		t.Errorf("the overrides were not applied: %q / %d", sb2.CookieName, sb2.TTL)
	}
	if _, nested := sb2.Fallback.(*StickyBalancer); nested {
		t.Error("the sticky balancer nested inside itself")
	}
}

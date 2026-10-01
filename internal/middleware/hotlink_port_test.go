package middleware

import (
	"net/http/httptest"
	"testing"

	"github.com/uwaserver/uwas/internal/logger"
)

// Regression: hotlink referer matching compared a host:port authority against
// a bare domain, 403'ing legitimate traffic.
//
// isAllowedReferer passed url.URL.Host — which net/url defines as the host
// INCLUDING any ":port" — into domainSuffixMatch, which compares a bare
// domain. A referer of https://example.com:8443/page therefore arrived as
// "example.com:8443", matching neither the exact domain nor the
// ".example.com" suffix, so the guard blocked it. The port is not part of the
// domain identity (hotlink.go's own doc: "domain-suffix matching: example.com
// allows example.com and sub.example.com"), so a site on a non-default port
// 403'd its own legitimate hotlinks.

// hotlinkAllows runs the production HotlinkGuard for a protected asset and
// reports whether the request was allowed through.
func hotlinkAllows(allowedReferers []string, reqHost, referer string) bool {
	guard := HotlinkGuard(logger.New("error", "text"), allowedReferers, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/images/photo.jpg", nil)
	req.Host = reqHost
	req.Header.Set("Referer", referer)
	return guard(rec, req)
}

// A configured allowed-referer written as a bare domain must still allow a
// referer on that same domain reached on a non-default port.
func TestHotlinkGuard_AllowsAllowedDomainOnNonDefaultPort(t *testing.T) {
	cases := []struct {
		name    string
		referer string
	}{
		{"exact domain with port", "https://example.com:8443/page"},
		{"subdomain with port", "https://sub.example.com:8443/page"},
		{"www subdomain with port", "https://www.example.com:8443/page"},
		{"http scheme with port", "http://example.com:3000/page"},
		{"allowed referer carrying a port", "https://example.com:9000/page"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// The "allowed referer carrying a port" case configures the
			// allowed entry with a port too, so it must match exactly.
			allowed := []string{"example.com"}
			if c.name == "allowed referer carrying a port" {
				allowed = []string{"example.com:9000"}
			}
			if !hotlinkAllows(allowed, "cdn.other.test", c.referer) {
				t.Errorf("guard 403'd legitimate referer %q (allowed=%v); the "+
					"port is not part of the domain identity", c.referer, allowed)
			}
		})
	}
}

// The same-host allowance documented at hotlink.go ("Also allow if the referer
// is from the same host as the request") must hold on a non-default-port site,
// where r.Host carries the port.
func TestHotlinkGuard_SameHostRefererOnNonDefaultPort(t *testing.T) {
	if !hotlinkAllows(nil, "example.com:8443", "https://example.com:8443/gallery") {
		t.Error("guard 403'd a same-host referer on a non-default-port site")
	}
}

// stripPort must be a no-op on a bare domain and must not mangle a host that
// has no port to strip.
func TestStripPort(t *testing.T) {
	cases := []struct{ in, want string }{
		{"example.com:8443", "example.com"},
		{"example.com", "example.com"},
		{"example.com:80", "example.com"},
		{"[::1]:8080", "::1"},
		{"", ""},
		// A colon-bearing value that is not host:port is returned intact.
		{"not a host", "not a host"},
	}
	for _, c := range cases {
		if got := stripPort(c.in); got != c.want {
			t.Errorf("stripPort(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The security behaviour the guard exists for must survive the fix: a port
// must not become a way to slip a domain-suffix attack past it.
func TestHotlinkGuard_StillBlocksDomainSuffixAttackWithPort(t *testing.T) {
	attacks := []string{
		"https://example.com.evil.com:8443/page",
		"https://attacker.example.com.evil.com/page",
		"https://example.com.attacker.net:8443/page",
		"https://notexample.com:8443/page",
		"https://evil.test:8443/page",
	}
	for _, referer := range attacks {
		t.Run(referer, func(t *testing.T) {
			if hotlinkAllows([]string{"example.com"}, "example.com", referer) {
				t.Errorf("guard ALLOWED domain-suffix attack %q — must block", referer)
			}
		})
	}
}

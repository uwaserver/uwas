package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/uwaserver/uwas/internal/logger"
)

func TestHotlinkProtectionBlocksExternalReferer(t *testing.T) {
	guard := HotlinkGuard(logger.New("error", "text"), []string{"example.com"}, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/images/photo.jpg", nil)
	req.Header.Set("Referer", "https://evil.test/page")
	req.Host = "example.com"
	if guard(rec, req) {
		t.Fatal("guard allowed external referer")
	}

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

// ---------------------------------------------------------------------------
// R29: Substring referer check bypassed by domain-suffix attacks
// hotlink.go:44 uses strings.Contains(refLower, ref) which is substring
// matching, not domain matching. "https://example.com.evil.com/" contains
// "example.com" and bypasses the guard. Fix: parse referer origin, use
// domain-suffix matching.
// ---------------------------------------------------------------------------

func TestHotlinkGuard_RejectsDomainSuffixAttack(t *testing.T) {
	guard := HotlinkGuard(logger.New("error", "text"), []string{"example.com"}, nil)

	for _, tc := range []struct {
		name    string
		referer string
		wantOK  bool
	}{
		// Legitimate same-site request — must ALLOW
		{name: "exact domain", referer: "https://example.com/page", wantOK: true},
		{name: "subdomain", referer: "https://sub.example.com/page", wantOK: true},
		{name: "www subdomain", referer: "https://www.example.com/page", wantOK: true},

		// Domain-suffix attack — must BLOCK
		{name: "evil.com contains example.com", referer: "https://example.com.evil.com/page", wantOK: false},
		{name: "attacker.example.com.evil.com", referer: "https://attacker.example.com.evil.com/page", wantOK: false},
		{name: "example.com.attacker.net", referer: "https://example.com.attacker.net/page", wantOK: false},

		// Substring false-positive — must BLOCK (not a subdomain)
		{name: "notexample.com contains example.com", referer: "https://notexample.com/page", wantOK: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/images/photo.jpg", nil)
			req.Header.Set("Referer", tc.referer)
			req.Host = "example.com"
			got := guard(rec, req)
			if got != tc.wantOK {
				if tc.wantOK {
					t.Errorf("guard blocked legitimate %q", tc.referer)
				} else {
					t.Errorf("guard ALLOWED domain-suffix attack %q — must block", tc.referer)
				}
			}
		})
	}
}

func TestHotlinkProtectionAllowsSameHostAndUnprotectedPath(t *testing.T) {
	guard := HotlinkGuard(logger.New("error", "text"), nil, nil)

	for _, tc := range []struct {
		name    string
		path    string
		referer string
	}{
		{name: "same host", path: "/images/photo.jpg", referer: "https://example.com/page"},
		{name: "unprotected extension", path: "/page.html", referer: "https://evil.test/page"},
		{name: "no referer", path: "/images/photo.jpg"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", tc.path, nil)
			req.Host = "example.com"
			if tc.referer != "" {
				req.Header.Set("Referer", tc.referer)
			}
			if !guard(rec, req) {
				t.Fatal("guard blocked allowed request")
			}
		})
	}
}

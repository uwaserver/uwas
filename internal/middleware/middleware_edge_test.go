package middleware

import (
	"net"
	"testing"
)

// --- accesslog.go: redactReferer (93.8%) ---
// Test redactReferer with no query string (early return path).
func TestRedactRefererNoQueryString(t *testing.T) {
	ref := "https://example.com/page"
	got := redactReferer(ref)
	if got != ref {
		t.Errorf("expected %q, got %q", ref, got)
	}
}

func TestRedactRefererWithQuery(t *testing.T) {
	ref := "https://example.com/page?token=abc&page=1"
	got := redactReferer(ref)
	// The token parameter should be redacted
	if got == ref {
		t.Error("expected redaction of sensitive query param")
	}
	if contains(got, "token=abc") {
		t.Errorf("token not redacted: %q", got)
	}
}

func TestRedactRefererSensitiveParamDetection(t *testing.T) {
	// Test isSensitiveQueryParam with various inputs
	cases := map[string]bool{
		"token":     true,
		"api_key":   true,
		"password":  true,
		"page":      false,
		"name":      false,
		"access":    false,
		"secretkey": true,
	}
	for name, want := range cases {
		got := isSensitiveQueryParam(name)
		if got != want {
			t.Errorf("isSensitiveQueryParam(%q) = %v, want %v", name, got, want)
		}
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// --- realip.go: extractRealIP (90.9%) ---
// Uncovered: return "" when parts is empty (line 98 — effectively dead code
// since strings.Split always returns at least one element, but test for completeness).

func TestExtractRealIPAllTrusted(t *testing.T) {
	_, cidr, _ := net.ParseCIDR("10.0.0.0/8")
	trusted := []*net.IPNet{cidr}

	// All IPs are trusted → returns leftmost
	ip := extractRealIP("10.0.0.1, 10.0.0.2", trusted)
	if ip != "10.0.0.1" {
		t.Errorf("expected leftmost trusted IP, got %q", ip)
	}
}

func TestExtractRealIPUntrustedFound(t *testing.T) {
	_, cidr, _ := net.ParseCIDR("10.0.0.0/8")
	trusted := []*net.IPNet{cidr}

	// Walk right-to-left: 192.168.1.1 is not trusted → return it
	ip := extractRealIP("10.0.0.1, 192.168.1.1", trusted)
	if ip != "192.168.1.1" {
		t.Errorf("expected rightmost untrusted IP, got %q", ip)
	}
}

func TestExtractRealIPEmpty(t *testing.T) {
	ip := extractRealIP("", nil)
	if ip != "" {
		t.Errorf("expected empty for empty input, got %q", ip)
	}
}

func TestExtractRealIPInvalidEntry(t *testing.T) {
	_, cidr, _ := net.ParseCIDR("10.0.0.0/8")
	trusted := []*net.IPNet{cidr}

	// Invalid IP entry is skipped; all remaining IPs (10.0.0.1) are trusted
	// so the function returns the leftmost entry (which is the invalid one)
	ip := extractRealIP("not-an-ip, 10.0.0.1", trusted)
	if ip != "not-an-ip" {
		t.Errorf("expected leftmost entry when all IPs are trusted/skipped, got %q", ip)
	}
}

// --- isAPContentType ---
func TestIsAPContentTypeEdge(t *testing.T) {
	if !isAPContentType("application/json") {
		t.Error("application/json should be AP content type")
	}
	if !isAPContentType("multipart/form-data") {
		t.Error("multipart/form-data should be AP content type")
	}
	if !isAPContentType("application/vnd.api+json") {
		t.Error("+json suffix should be AP content type")
	}
	if isAPContentType("text/plain") {
		t.Error("text/plain should not be AP content type")
	}
	// Test with charset parameter
	if !isAPContentType("application/json; charset=utf-8") {
		t.Error("application/json with charset should be AP content type")
	}
}

package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRealIPRejectsLoopbackForwardedIP: a trusted proxy forwarding a
// client-supplied loopback address must not rewrite RemoteAddr — otherwise
// a remote client could buy BotGuard/GeoIP localhost trust with a header.
func TestRealIPRejectsLoopbackForwardedIP(t *testing.T) {
	cases := []struct {
		name   string
		header string
		value  string
	}{
		{"CF-Connecting-IP loopback", "CF-Connecting-IP", "127.0.0.1"},
		{"X-Real-IP loopback", "X-Real-IP", "127.0.0.1"},
		{"X-Real-IP v6 loopback", "X-Real-IP", "::1"},
		{"X-Real-IP unspecified", "X-Real-IP", "0.0.0.0"},
		{"XFF loopback", "X-Forwarded-For", "127.0.0.1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var seen string
			h := RealIP([]string{"10.0.0.1"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen = r.RemoteAddr
			}))
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = "10.0.0.1:12345"
			req.Header.Set(tc.header, tc.value)
			h.ServeHTTP(httptest.NewRecorder(), req)

			if strings.HasPrefix(seen, "127.") || strings.HasPrefix(seen, "[::1]") ||
				strings.HasPrefix(seen, "::1") || strings.HasPrefix(seen, "0.0.0.0") {
				t.Fatalf("forwarded loopback rewrote RemoteAddr to %q", seen)
			}
		})
	}

	// Sanity: a legitimate public IP is still honored.
	var seen string
	h := RealIP([]string{"10.0.0.1"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.RemoteAddr
	}))
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("X-Real-IP", "203.0.113.9")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if !strings.HasPrefix(seen, "203.0.113.9") {
		t.Fatalf("public forwarded IP not honored, RemoteAddr=%q", seen)
	}
}

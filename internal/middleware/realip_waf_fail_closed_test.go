package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/uwaserver/uwas/internal/logger"
)

// A trusted proxy that appends the client as "ip:port" or "unknown" must not
// let the client's own leftmost X-Forwarded-For value become its address.
func TestRealIPPortSuffixedForwardedEntryNotSpoofable(t *testing.T) {
	trusted := []string{"10.0.0.0/8"}
	cases := map[string]string{
		"6.6.6.6, 203.0.113.9:51234": "203.0.113.9:0",
		"6.6.6.6, [2001:db8::9]:443": "[2001:db8::9]:0",
		"6.6.6.6, unknown":           "10.0.0.2:4000",
	}
	for xff, want := range cases {
		var got string
		h := RealIP(trusted)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = r.RemoteAddr }))
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = "10.0.0.2:4000"
		r.Header.Set("X-Forwarded-For", xff)
		h.ServeHTTP(httptest.NewRecorder(), r)
		if got != want {
			t.Errorf("RealIP xff=%q: RemoteAddr=%q, want %q", xff, got, want)
		}

		r2 := httptest.NewRequest("GET", "/", nil)
		r2.RemoteAddr = "10.0.0.2:4000"
		r2.Header.Set("X-Forwarded-For", xff)
		if k := clientIP(&RateLimiter{trustedProxies: parseCIDRs(trusted)}, r2); k == "6.6.6.6" {
			t.Errorf("rate-limit key for xff=%q is the spoofed 6.6.6.6", xff)
		}
	}
}

// One malformed %-escape must not exempt the encoded payload beside it.
func TestDomainWAFMalformedEscapeDoesNotBypass(t *testing.T) {
	g := DomainWAFGuard(logger.New("error", "text"), nil, nil, nil)
	for _, q := range []string{
		"q=1%20union%20select%20pass%20from%20users&x=%zz",
		"q=%3Cscript%3Ealert(1)%3C/script%3E&x=%",
	} {
		r := httptest.NewRequest("GET", "/search?"+q, nil)
		if g(httptest.NewRecorder(), r) {
			t.Errorf("WAF passed %q", q)
		}
	}
}

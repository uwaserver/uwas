package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// A trusted proxy that only appends the client to X-Forwarded-For forwards a
// client-set X-Real-IP verbatim. With XFF present, X-Real-IP is believed only
// when it agrees with the hop the proxy appended (F2740), as CF-Connecting-IP
// already must.
func TestRealIPXRealIPNeedsXFFAgreement(t *testing.T) {
	resolve := func(xff []string, xri string) string {
		var got string
		h := RealIP([]string{"10.0.0.0/8"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = r.RemoteAddr }))
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = "10.0.0.2:4000"
		for _, v := range xff {
			r.Header.Add("X-Forwarded-For", v)
		}
		if xri != "" {
			r.Header.Set("X-Real-IP", xri)
		}
		h.ServeHTTP(httptest.NewRecorder(), r)
		return got
	}
	cases := []struct {
		name string
		xff  []string
		xri  string
		want string
	}{
		{"x-real-ip alone", nil, "198.51.100.7", "198.51.100.7:0"},
		{"agrees with single hop", []string{"198.51.100.7"}, "198.51.100.7", "198.51.100.7:0"},
		{"agrees with rightmost untrusted", []string{"203.0.113.1, 198.51.100.7, 10.0.0.9"}, "198.51.100.7", "198.51.100.7:0"},
		{"client-set differs from appended hop", []string{"6.6.6.6, 198.51.100.7"}, "6.6.6.6", "198.51.100.7:0"},
		{"client-set differs, split header lines", []string{"6.6.6.6", "198.51.100.7"}, "6.6.6.6", "198.51.100.7:0"},
		{"client-set victim address", []string{"198.51.100.7"}, "203.0.113.200", "198.51.100.7:0"},
		{"xff unparseable: peer address kept", []string{"unknown"}, "6.6.6.6", "10.0.0.2:4000"},
		{"loopback x-real-ip still rejected", []string{"198.51.100.7"}, "127.0.0.1", "198.51.100.7:0"},
	}
	for _, c := range cases {
		if got := resolve(c.xff, c.xri); got != c.want {
			t.Errorf("%s: RemoteAddr=%q, want %q", c.name, got, c.want)
		}
	}
}

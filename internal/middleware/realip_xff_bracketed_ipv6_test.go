package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// A trusted proxy may write an IPv6 hop as "[addr]" with no port. The entry
// parsed as nothing, so the walk gave up and the client kept the proxy's
// address (F2831).
func TestRealIPXFFBracketedIPv6(t *testing.T) {
	resolve := func(xff string) string {
		var got string
		h := RealIP([]string{"10.0.0.0/8"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = r.RemoteAddr }))
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = "10.0.0.2:4000"
		r.Header.Set("X-Forwarded-For", xff)
		h.ServeHTTP(httptest.NewRecorder(), r)
		return got
	}
	const proxy = "10.0.0.2:4000"
	for _, c := range []struct{ name, xff, want string }{
		{"plain v6", "2001:db8::7", "[2001:db8::7]:0"},
		{"v6 with port", "[2001:db8::7]:443", "[2001:db8::7]:0"},
		{"v4 with port", "203.0.113.9:51234", "203.0.113.9:0"},
		{"bracketed v6 no port", "[2001:db8::7]", "[2001:db8::7]:0"},
		{"bracketed v6 after client junk", "6.6.6.6, [2001:db8::7]", "[2001:db8::7]:0"},
		{"bracketed v6 then trusted hop", "[2001:db8::7], 10.9.9.9", "[2001:db8::7]:0"},
		{"unterminated bracket", "[2001:db8::7", proxy},
		{"empty brackets", "[]", proxy},
		{"bracketed junk ends the walk", "6.6.6.6, [unknown]", proxy},
		{"bracketed loopback not trusted", "[::1]", proxy},
	} {
		if got := resolve(c.xff); got != c.want {
			t.Errorf("%s: XFF %q -> %s, want %s", c.name, c.xff, got, c.want)
		}
	}
}

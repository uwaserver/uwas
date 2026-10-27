package middleware

// F37301: BotGuard's contract is "localhost requests are always allowed
// (internal health checks, API calls)", and RealIP's is that a client can never
// buy loopback trust with a forwarded value. But a request relayed by a
// reverse proxy on the same host reaches BotGuard with the proxy's own
// loopback RemoteAddr whenever the client address could not be resolved: the
// proxy is not a configured trusted proxy, or the forwarding headers disagree
// (a verbatim, client-set X-Forwarded-For beside the proxy's X-Real-IP is
// refused, and the fallback is the proxy's address). Every such request was
// treated as internal, so a listed scanner or an empty User-Agent walked past
// the guard. A request that carries forwarding headers was relayed; it is not
// a local request.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/uwaserver/uwas/internal/logger"
)

func TestBotGuardDoesNotExemptRelayedLoopbackRequests(t *testing.T) {
	cases := []struct {
		name    string
		trusted []string
		headers map[string]string
		want    int
	}{
		// Control: a genuine local request is exempt, as documented.
		{"local request, no forwarding headers", []string{"127.0.0.1/32"}, nil, http.StatusOK},
		{"local request, no trusted proxies", nil, nil, http.StatusOK},
		// Control: the client resolves, so it is judged as itself.
		{"resolved client", []string{"127.0.0.1/32"}, map[string]string{"X-Real-IP": "198.51.100.7"}, http.StatusForbidden},
		// The defect: relayed, client unresolvable, loopback peer.
		{"conflicting X-Forwarded-For beside X-Real-IP", []string{"127.0.0.1/32"},
			map[string]string{"X-Real-IP": "198.51.100.7", "X-Forwarded-For": "bogus"}, http.StatusForbidden},
		{"proxy on loopback not configured as trusted", nil,
			map[string]string{"X-Forwarded-For": "198.51.100.7"}, http.StatusForbidden},
		{"Cloudflare tunnel on loopback, not trusted", nil,
			map[string]string{"CF-Connecting-IP": "198.51.100.7"}, http.StatusForbidden},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			chain := RealIP(c.trusted)(BotGuard(logger.New("error", "text"), nil)(ok200()))
			r := httptest.NewRequest("GET", "/wp-login.php", nil)
			r.RemoteAddr = "127.0.0.1:5000"
			r.Header.Set("User-Agent", "sqlmap/1.7")
			for k, v := range c.headers {
				r.Header.Set(k, v)
			}
			w := httptest.NewRecorder()
			chain.ServeHTTP(w, r)
			t.Logf("EXPECTED: %d\nACTUAL: %d", c.want, w.Code)
			if w.Code != c.want {
				t.Fatalf("PROBLEM CONFIRMED: %d, want %d", w.Code, c.want)
			}
		})
	}
}

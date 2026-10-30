package admin

// F44600: POST /api/v1/auth/bootstrap creates the first admin and returns its
// API key to whoever asks first. The no-credentials mode refuses a request that
// a web page or a relaying proxy could have sent (F15800, F16101): a loopback
// listener is reachable from any page the operator visits, because a DNS-rebound
// name arrives from the loopback peer with a Host header the page chose. The
// bootstrap route skipped that vetting, so on the recommended loopback-only
// setup a page the operator merely visits could create the first admin (and read
// its API key, the page being same-origin after the rebind), and a same-host
// reverse proxy forwarding a remote client was indistinguishable from the
// operator's own browser.
//
// A peer that is not loopback keeps the documented first-come behaviour
// (TestAuthBootstrapSuccessThroughMiddleware); only a loopback peer that
// presents a foreign Host, or that carries forwarding headers, is refused.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/auth"
)

// strongPassword passes the password policy (the request is accepted and a
// bcrypt hash is made); weakPassword is refused by policy with 400 before any
// hashing, which is how a control shows the request got past the vetting
// without the cost of a real first-admin creation.
const (
	strongPassword = "S3cure-Passw0rd!"
	weakPassword   = "short"
)

func bootstrapAttempt(t *testing.T, remoteAddr, host, password string, hdr map[string]string) (int, bool) {
	t.Helper()
	s := testServer()
	s.config.Global.WebRoot = t.TempDir()
	s.config.Global.Users.Enabled = true
	s.config.Global.Admin.APIKey = ""

	req := httptest.NewRequest("POST", "/api/v1/auth/bootstrap",
		strings.NewReader(`{"username":"admin","email":"admin@example.com","password":"`+password+`"}`))
	req.RemoteAddr = remoteAddr
	if host != "" {
		req.Host = host
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	s.authMiddleware(s.mux).ServeHTTP(rec, req)

	created := false
	if mgr, ok := s.authMgr.(*auth.Manager); ok {
		defer mgr.Stop()
		_, created = mgr.GetUser("admin")
	}
	return rec.Code, created
}

func TestBootstrapRefusesLoopbackPeerWithForeignHostOrForwarding(t *testing.T) {
	refused := []struct {
		name string
		host string
		hdr  map[string]string
	}{
		{"dns-rebound name (page the operator visits)", "attacker.example:9443", nil},
		{"same-host proxy keeps the public Host", "panel.example.com", nil},
		{"same-host proxy forwards a client (X-Forwarded-For)", "127.0.0.1:9443", map[string]string{"X-Forwarded-For": "203.0.113.9"}},
		{"same-host proxy forwards a client (X-Real-IP)", "localhost:9443", map[string]string{"X-Real-IP": "203.0.113.9"}},
		{"same-host proxy forwards a client (Forwarded)", "localhost:9443", map[string]string{"Forwarded": "for=203.0.113.9"}},
		{"same-host proxy forwards a client (CF-Connecting-IP)", "localhost:9443", map[string]string{"CF-Connecting-IP": "203.0.113.9"}},
	}
	for _, c := range refused {
		t.Run(c.name, func(t *testing.T) {
			code, created := bootstrapAttempt(t, "127.0.0.1:50000", c.host, strongPassword, c.hdr)
			t.Logf("EXPECTED: 403 and no admin\nACTUAL: %d, admin created = %v", code, created)
			if code != http.StatusForbidden || created {
				t.Fatalf("PROBLEM CONFIRMED: loopback peer, Host %q, headers %v -> %d (admin created: %v)",
					c.host, c.hdr, code, created)
			}
		})
	}
}

func TestBootstrapStillAcceptsTheOperatorsOwnBrowserAndRemotePeers(t *testing.T) {
	accepted := []struct {
		name, remote, host string
	}{
		{"loopback peer, 127.0.0.1 Host", "127.0.0.1:50000", "127.0.0.1:9443"},
		{"loopback peer, localhost Host", "127.0.0.1:50000", "localhost:9443"},
		{"loopback peer, ::1", "[::1]:50000", "[::1]:9443"},
		{"ssh tunnel, *.localhost Host", "127.0.0.1:50000", "panel.localhost:9443"},
		// Documented first-come behaviour for a listener that is not loopback.
		{"remote peer keeps working", "10.0.0.1:1234", "203.0.113.5:9443"},
	}
	for _, c := range accepted {
		t.Run(c.name, func(t *testing.T) {
			code, _ := bootstrapAttempt(t, c.remote, c.host, weakPassword, nil)
			if code != http.StatusBadRequest {
				t.Fatalf("control: %s -> %d, want 400 (past the vetting, refused by password policy)", c.name, code)
			}
		})
	}
}

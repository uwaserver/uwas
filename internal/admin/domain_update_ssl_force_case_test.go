package admin

import (
	"net/http"
	"testing"

	"github.com/uwaserver/uwas/internal/auth"
	"github.com/uwaserver/uwas/internal/config"
)

// F2350: Update located ssl and force_ssl in the raw body by exact key while
// encoding/json fills the struct from keys of any case, so a case variant was
// accepted (200) but force_ssl was silently not applied.
func TestUpdateSSLForceKeyCaseInsensitive(t *testing.T) {
	run := func(t *testing.T, startForce bool, body string) bool {
		t.Helper()
		e := newHostConflictEnv(t, []config.Domain{{Host: "a.com", Type: "static", SSL: config.SSLConfig{ForceSSL: startForce}}})
		e.s.configMu.Lock()
		e.s.config.Domains[0].SSL.ForceSSL = startForce
		e.s.configMu.Unlock()
		if code := e.do(http.MethodPut, "/api/v1/domains/a.com", body, auth.RoleAdmin); code != http.StatusOK {
			t.Fatalf("status = %d, want 200", code)
		}
		e.s.configMu.RLock()
		defer e.s.configMu.RUnlock()
		return e.s.config.Domains[0].SSL.ForceSSL
	}

	cases := []struct {
		name  string
		start bool
		body  string
		want  bool
	}{
		{"control lowercase enable", false, `{"ssl":{"mode":"off","force_ssl":true}}`, true},
		{"SSL.Force_SSL enable", false, `{"SSL":{"mode":"off","Force_SSL":true}}`, true},
		{"ssl.FORCE_SSL enable", false, `{"ssl":{"mode":"off","FORCE_SSL":true}}`, true},
		{"SSL.force_ssl enable", false, `{"SSL":{"mode":"off","force_ssl":true}}`, true},
		{"SSL.Force_SSL explicit false clears", true, `{"SSL":{"mode":"off","Force_SSL":false}}`, false},
		{"force_ssl absent keeps stored value", true, `{"ssl":{"mode":"off"}}`, true},
		{"no ssl key keeps stored value", true, `{"cache":{"enabled":true}}`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := run(t, c.start, c.body); got != c.want {
				t.Errorf("force_ssl = %v, want %v", got, c.want)
			}
		})
	}
}

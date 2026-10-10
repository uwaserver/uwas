package admin

import (
	"net/http"
	"path/filepath"
	"testing"

	"github.com/uwaserver/uwas/internal/auth"
	"github.com/uwaserver/uwas/internal/config"
)

// F1480: Update and the raw editor refuse privilege-sensitive fields from
// non-admins; Add must too. A reseller assigned a not-yet-created hostname
// could otherwise create it with root at a sibling tenant's docroot (still
// under web_root), an access_log path or a webhook secret of its choosing.
func TestResellerAddRefusesPrivilegedFields(t *testing.T) {
	newEnv := func(t *testing.T) (*hostConflictEnv, string) {
		e := newHostConflictEnv(t, []config.Domain{{Host: "victim.com", Type: "static"}})
		return e, e.s.config.Domains[0].Root
	}
	stored := func(e *hostConflictEnv, host string) bool {
		e.s.configMu.RLock()
		defer e.s.configMu.RUnlock()
		for _, d := range e.s.config.Domains {
			if d.Host == host {
				return true
			}
		}
		return false
	}
	add := func(e *hostConflictEnv, role auth.Role, host, body string) int {
		return e.do(http.MethodPost, "/api/v1/domains", body, role, host)
	}

	t.Run("reseller refused", func(t *testing.T) {
		e, victimRoot := newEnv(t)
		for _, v := range []struct{ name, host, body string }{
			{"root of a sibling docroot", "a.example", `{"host":"a.example","type":"static","root":"` + victimRoot + `","ssl":{"mode":"off"}}`},
			{"type app", "b.example", `{"host":"b.example","type":"app","app":{"command":"id"},"ssl":{"mode":"off"}}`},
			{"type proxy", "g.example", `{"host":"g.example","type":"proxy","proxy":{"upstreams":[{"address":"http://127.0.0.1:1"}]},"ssl":{"mode":"off"}}`},
			{"access_log path", "c.example", `{"host":"c.example","type":"static","access_log":{"path":"/etc/cron.d/x"},"ssl":{"mode":"off"}}`},
			{"internal_aliases", "d.example", `{"host":"d.example","type":"static","internal_aliases":["/etc"],"ssl":{"mode":"off"}}`},
			{"webhook_secret", "f.example", `{"host":"f.example","type":"static","webhook_secret":"x","ssl":{"mode":"off"}}`},
		} {
			if code := add(e, auth.RoleReseller, v.host, v.body); code != http.StatusForbidden {
				t.Errorf("%s: status = %d, want 403", v.name, code)
			}
			if stored(e, v.host) {
				t.Errorf("%s: a refused request still created the domain", v.name)
			}
		}
	})

	t.Run("reseller plain add and default root allowed", func(t *testing.T) {
		e, _ := newEnv(t)
		if code := add(e, auth.RoleReseller, "ok.example", `{"host":"ok.example","type":"static","ssl":{"mode":"off"}}`); code != http.StatusCreated && code != http.StatusOK {
			t.Errorf("plain add: status = %d, want 2xx", code)
		}
		def := filepath.Join(filepath.Dir(filepath.Dir(e.s.config.Domains[0].Root)), "own.example", "public_html")
		if code := add(e, auth.RoleReseller, "own.example", `{"host":"own.example","type":"static","root":"`+def+`","ssl":{"mode":"off"}}`); code != http.StatusCreated && code != http.StatusOK {
			t.Errorf("explicit default root: status = %d, want 2xx", code)
		}
	})

	t.Run("admin keeps full control", func(t *testing.T) {
		e, victimRoot := newEnv(t)
		body := `{"host":"adm.example","type":"static","root":"` + victimRoot + `","webhook_secret":"x","ssl":{"mode":"off"}}`
		if code := add(e, auth.RoleAdmin, "adm.example", body); code != http.StatusCreated && code != http.StatusOK {
			t.Errorf("admin add: status = %d, want 2xx", code)
		}
		if !stored(e, "adm.example") {
			t.Error("admin domain was not created")
		}
	})
}

package admin

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/uwaserver/uwas/internal/auth"
	"github.com/uwaserver/uwas/internal/config"
)

// F1420: locations[].root / locations[].proxy_pass and error_pages let a
// domain serve files or reach hosts outside its docroot as the server, so
// non-admins must not introduce or edit them through Add, Update or the raw
// YAML editor (they may keep what an admin set).
func TestResellerCannotSetWebAccessFields(t *testing.T) {
	newEnv := func(t *testing.T) *hostConflictEnv {
		return newHostConflictEnv(t, []config.Domain{{Host: "reseller.com", Type: "static"}})
	}
	stored := func(e *hostConflictEnv, host string) *config.Domain {
		if path, err := e.s.domainFilePath(host); err == nil {
			if data, err := os.ReadFile(path); err == nil {
				var fd config.Domain
				if yaml.Unmarshal(data, &fd) == nil && fd.Host == host {
					return &fd
				}
			}
		}
		e.s.configMu.RLock()
		defer e.s.configMu.RUnlock()
		for _, d := range e.s.config.Domains {
			if d.Host == host {
				dd := d
				return &dd
			}
		}
		return nil
	}
	sensitive := func(d *config.Domain) bool {
		if d == nil {
			return false
		}
		for _, l := range d.Locations {
			if l.Root != "" || l.ProxyPass != "" {
				return true
			}
		}
		return len(d.ErrorPages) > 0
	}
	update := func(e *hostConflictEnv, role auth.Role, body string) int {
		return e.do(http.MethodPut, "/api/v1/domains/reseller.com", body, role, "reseller.com")
	}
	raw := func(e *hostConflictEnv, role auth.Role, y string) int {
		b, _ := json.Marshal(map[string]string{"content": y})
		return e.do(http.MethodPut, "/api/v1/config/domains/reseller.com/raw", string(b), role, "reseller.com")
	}
	rawBase := func(e *hostConflictEnv) string {
		return "host: reseller.com\ntype: static\nroot: " + e.s.config.Domains[0].Root + "\nssl:\n  mode: \"off\"\n"
	}

	t.Run("reseller refused", func(t *testing.T) {
		e := newEnv(t)
		cases := []struct {
			name string
			do   func() int
		}{
			{"update locations.root", func() int { return update(e, auth.RoleReseller, `{"locations":[{"match":"/x/","root":"/etc"}]}`) }},
			{"update locations.proxy_pass", func() int {
				return update(e, auth.RoleReseller, `{"locations":[{"match":"/x/","proxy_pass":"http://169.254.169.254"}]}`)
			}},
			{"raw locations.root", func() int {
				return raw(e, auth.RoleReseller, rawBase(e)+"locations:\n  - match: /x/\n    root: /etc\n")
			}},
			{"raw error_pages", func() int {
				return raw(e, auth.RoleReseller, rawBase(e)+"error_pages:\n  404: ../../../../etc/passwd\n")
			}},
			{"add locations.root", func() int {
				return e.do(http.MethodPost, "/api/v1/domains",
					`{"host":"mine.reseller.com","type":"static","ssl":{"mode":"off"},"locations":[{"match":"/x/","root":"/etc"}]}`,
					auth.RoleReseller, "reseller.com", "mine.reseller.com")
			}},
		}
		for _, c := range cases {
			if code := c.do(); code != http.StatusForbidden {
				t.Errorf("%s: status = %d, want 403", c.name, code)
			}
		}
		if sensitive(stored(e, "reseller.com")) || sensitive(stored(e, "mine.reseller.com")) {
			t.Error("a refused request still stored a web-access field")
		}
	})

	t.Run("admin allowed", func(t *testing.T) {
		e := newEnv(t)
		if code := update(e, auth.RoleAdmin, `{"locations":[{"match":"/x/","root":"/srv/shared"}]}`); code != http.StatusOK {
			t.Fatalf("admin update: status = %d, want 200", code)
		}
		if !sensitive(stored(e, "reseller.com")) {
			t.Error("admin location root was not stored")
		}
	})

	t.Run("reseller keeps admin-set and edits harmless fields", func(t *testing.T) {
		e := newEnv(t)
		if code := update(e, auth.RoleAdmin, `{"locations":[{"match":"/x/","root":"/srv/shared"}]}`); code != http.StatusOK {
			t.Fatalf("admin update: status = %d", code)
		}
		// Re-submitting the identical location plus a harmless one is fine.
		body := `{"locations":[{"match":"/x/","root":"/srv/shared"},{"match":"/a/","cache_control":"public, max-age=60"}]}`
		if code := update(e, auth.RoleReseller, body); code != http.StatusOK {
			t.Errorf("reseller keeping admin location: status = %d, want 200", code)
		}
		// Editing the admin-set root is refused.
		if code := update(e, auth.RoleReseller, `{"locations":[{"match":"/x/","root":"/etc"}]}`); code != http.StatusForbidden {
			t.Errorf("reseller changing location root: status = %d, want 403", code)
		}
		// Locations without root/proxy_pass are the reseller's to manage.
		if code := update(e, auth.RoleReseller, `{"locations":[{"match":"/a/","cache_control":"no-store"}]}`); code != http.StatusOK {
			t.Errorf("reseller harmless locations: status = %d, want 200", code)
		}
	})
}

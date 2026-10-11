package admin

import (
	"net/http"
	"path/filepath"
	"testing"

	"github.com/uwaserver/uwas/internal/auth"
	"github.com/uwaserver/uwas/internal/config"
)

// F2290: Update refuses privilege-sensitive keys from non-admins by looking
// for the exact lowercase key in the raw body, but encoding/json fills
// config.Domain from keys of any case, so {"Root": ...} used to slip past the
// guard and point a reseller's domain at a sibling tenant's docroot.
func TestResellerUpdateCaseVariantKeysRefused(t *testing.T) {
	newEnv := func(t *testing.T) (*hostConflictEnv, string) {
		e := newHostConflictEnv(t, []config.Domain{
			{Host: "reseller.com", Type: "static"},
			{Host: "victim.com", Type: "static"},
		})
		victim := filepath.Join(filepath.Dir(filepath.Dir(e.s.config.Domains[0].Root)), "victim.com", "public_html")
		return e, victim
	}
	stored := func(e *hostConflictEnv) config.Domain {
		e.s.configMu.RLock()
		defer e.s.configMu.RUnlock()
		return e.s.config.Domains[0]
	}
	put := func(e *hostConflictEnv, role auth.Role, body string) int {
		return e.do(http.MethodPut, "/api/v1/domains/reseller.com", body, role, "reseller.com")
	}

	t.Run("reseller refused", func(t *testing.T) {
		e, victim := newEnv(t)
		own := stored(e).Root
		bodies := map[string]string{
			"lowercase root (control)": `{"root":"` + victim + `"}`,
			"Root":                     `{"Root":"` + victim + `"}`,
			"ROOT":                     `{"ROOT":"` + victim + `"}`,
			"Webhook_Secret":           `{"Webhook_Secret":"attacker-chosen"}`,
			"Access_Log":               `{"Access_Log":{"path":"/var/log/owned.log"}}`,
			"Proxy":                    `{"Proxy":{"upstreams":[{"address":"http://127.0.0.1:1"}]}}`,
			"Type":                     `{"Type":"proxy"}`,
			"IP":                       `{"Ip":"203.0.113.9"}`,
			"Internal_Aliases":         `{"Internal_Aliases":["/etc"]}`,
		}
		for name, body := range bodies {
			if code := put(e, auth.RoleReseller, body); code != http.StatusForbidden {
				t.Errorf("%s: status = %d, want 403", name, code)
			}
		}
		d := stored(e)
		if d.Root != own || d.WebhookSecret != "" || d.Type != "static" || d.IP != "" || len(d.InternalAliases) != 0 {
			t.Errorf("a refused request still changed the domain: %+v", d)
		}
	})

	t.Run("harmless case variants and admin still work", func(t *testing.T) {
		e, victim := newEnv(t)
		if code := put(e, auth.RoleReseller, `{"Cache":{"enabled":true}}`); code != http.StatusOK {
			t.Errorf("reseller harmless Cache: status = %d, want 200", code)
		}
		if code := put(e, auth.RoleAdmin, `{"Root":"`+victim+`"}`); code != http.StatusOK {
			t.Errorf("admin Root: status = %d, want 200", code)
		}
		if got := stored(e).Root; got != victim {
			t.Errorf("admin root not stored: %q", got)
		}
	})
}

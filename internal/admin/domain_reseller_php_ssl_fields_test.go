package admin

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/uwaserver/uwas/internal/auth"
	"github.com/uwaserver/uwas/internal/config"
)

// F1510: php.fpm_address (any FastCGI endpoint) and ssl.cert / ssl.key /
// ssl.client_ca (files the server reads) are set server-side (PHP assignment,
// certificate upload), so non-admins must not introduce or change them through
// Add, Update or the raw YAML editor; they may keep what is stored.
func TestResellerCannotSetPHPAndSSLPathFields(t *testing.T) {
	newEnv := func(t *testing.T) *hostConflictEnv {
		return newHostConflictEnv(t, []config.Domain{{Host: "reseller.com", Type: "php"}})
	}
	stored := func(e *hostConflictEnv) config.Domain {
		e.s.configMu.RLock()
		defer e.s.configMu.RUnlock()
		for _, d := range e.s.config.Domains {
			if d.Host == "reseller.com" {
				return d
			}
		}
		return config.Domain{}
	}
	update := func(e *hostConflictEnv, role auth.Role, body string) int {
		return e.do(http.MethodPut, "/api/v1/domains/reseller.com", body, role, "reseller.com")
	}
	raw := func(e *hostConflictEnv, role auth.Role, y string) int {
		b, _ := json.Marshal(map[string]string{"content": y})
		return e.do(http.MethodPut, "/api/v1/config/domains/reseller.com/raw", string(b), role, "reseller.com")
	}
	rawBase := func(e *hostConflictEnv) string {
		return "host: reseller.com\ntype: php\nroot: " + e.s.config.Domains[0].Root + "\nssl:\n  mode: \"off\"\n"
	}

	t.Run("reseller refused", func(t *testing.T) {
		e := newEnv(t)
		cases := []struct {
			name string
			do   func() int
		}{
			{"update php.fpm_address", func() int {
				return update(e, auth.RoleReseller, `{"php":{"fpm_address":"unix:/run/php/other.sock"}}`)
			}},
			{"update ssl.cert", func() int {
				return update(e, auth.RoleReseller, `{"ssl":{"mode":"manual","cert":"/etc/shadow","key":"/etc/shadow"}}`)
			}},
			{"update ssl.client_ca", func() int {
				return update(e, auth.RoleReseller, `{"ssl":{"client_ca":"/etc/passwd","client_auth":"require"}}`)
			}},
			{"raw php.fpm_address", func() int {
				return raw(e, auth.RoleReseller, rawBase(e)+"php:\n  fpm_address: 127.0.0.1:9000\n")
			}},
			{"add ssl.cert", func() int {
				return e.do(http.MethodPost, "/api/v1/domains",
					`{"host":"mine.reseller.com","type":"php","ssl":{"mode":"manual","cert":"/etc/shadow","key":"/etc/shadow"}}`,
					auth.RoleReseller, "reseller.com", "mine.reseller.com")
			}},
		}
		for _, c := range cases {
			if code := c.do(); code != http.StatusForbidden {
				t.Errorf("%s: status = %d, want 403", c.name, code)
			}
		}
		d := stored(e)
		if d.PHP.FPMAddress != "" || d.SSL.Cert != "" || d.SSL.Key != "" || d.SSL.ClientCA != "" {
			t.Errorf("a refused request still stored a field: php=%+v ssl=%+v", d.PHP, d.SSL)
		}
	})

	t.Run("admin allowed, reseller keeps stored values", func(t *testing.T) {
		e := newEnv(t)
		if code := update(e, auth.RoleAdmin, `{"php":{"fpm_address":"127.0.0.1:9100"},"ssl":{"mode":"manual","cert":"/srv/c.pem","key":"/srv/k.pem"}}`); code != http.StatusOK {
			t.Fatalf("admin update: status = %d, want 200", code)
		}
		d := stored(e)
		if d.PHP.FPMAddress != "127.0.0.1:9100" || d.SSL.Cert != "/srv/c.pem" {
			t.Fatalf("admin values not stored: php=%+v ssl=%+v", d.PHP, d.SSL)
		}
		// Re-submitting the stored values, omitting them, and changing a harmless field are fine.
		for name, body := range map[string]string{
			"resubmit": `{"php":{"fpm_address":"127.0.0.1:9100"},"ssl":{"cert":"/srv/c.pem","key":"/srv/k.pem"}}`,
			"omitted":  `{"cache":{"enabled":true}}`,
		} {
			if code := update(e, auth.RoleReseller, body); code != http.StatusOK {
				t.Errorf("reseller %s: status = %d, want 200", name, code)
			}
		}
		if code := update(e, auth.RoleReseller, `{"php":{"fpm_address":"127.0.0.1:9200"}}`); code != http.StatusForbidden {
			t.Errorf("reseller changing stored fpm_address: status = %d, want 403", code)
		}
		if got := stored(e); got.PHP.FPMAddress != "127.0.0.1:9100" || got.SSL.Cert != "/srv/c.pem" {
			t.Errorf("stored values changed: php=%+v ssl=%+v", got.PHP, got.SSL)
		}
	})
}

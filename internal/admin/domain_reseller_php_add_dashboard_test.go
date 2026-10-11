package admin

import (
	"net/http"
	"testing"

	"github.com/uwaserver/uwas/internal/auth"
	"github.com/uwaserver/uwas/internal/config"
)

// F2950: the panel's PHP and WordPress templates post php.fpm_address on every
// Add. F1510 refused that field for non-admins, which made creating a PHP site
// from the panel impossible for a reseller. Add assigns the pool itself, so the
// client's value is dropped instead; ssl cert paths stay refused.
func TestResellerPHPAddWithDashboardPayload(t *testing.T) {
	e := newHostConflictEnv(t, []config.Domain{{Host: "victim.com", Type: "static"}})
	add := func(role auth.Role, host, body string) int {
		return e.do(http.MethodPost, "/api/v1/domains", body, role, host)
	}
	stored := func(host string) (config.Domain, bool) {
		e.s.configMu.RLock()
		defer e.s.configMu.RUnlock()
		for _, d := range e.s.config.Domains {
			if d.Host == host {
				return d, true
			}
		}
		return config.Domain{}, false
	}

	panel := `{"host":"shop.example","type":"php","ssl":{"mode":"off","force_ssl":false},"canonical_host":"apex",` +
		`"htaccess":{"mode":"import"},"php":{"fpm_address":"127.0.0.1:9000","index_files":["index.php","index.html"]}}`
	if code := add(auth.RoleReseller, "shop.example", panel); code != http.StatusCreated {
		t.Fatalf("reseller add with the panel payload: status = %d, want 201", code)
	}
	d, ok := stored("shop.example")
	if !ok {
		t.Fatal("domain was not created")
	}
	if d.PHP.FPMAddress != "" {
		t.Errorf("reseller-chosen fpm_address was stored: %q", d.PHP.FPMAddress)
	}
	if len(d.PHP.IndexFiles) != 2 {
		t.Errorf("index_files = %v, want the two the panel sent", d.PHP.IndexFiles)
	}

	// Cert paths and the other guarded fields are still refused.
	if code := add(auth.RoleReseller, "evil.example",
		`{"host":"evil.example","type":"php","ssl":{"mode":"manual","cert":"/etc/shadow","key":"/etc/shadow"}}`); code != http.StatusForbidden {
		t.Errorf("reseller add with ssl.cert: status = %d, want 403", code)
	}
	// An admin's own value is untouched.
	if code := add(auth.RoleAdmin, "adm.example",
		`{"host":"adm.example","type":"php","ssl":{"mode":"off"},"php":{"fpm_address":"127.0.0.1:9000"}}`); code != http.StatusCreated {
		t.Fatalf("admin add: status = %d, want 201", code)
	}
	if a, _ := stored("adm.example"); a.PHP.FPMAddress != "127.0.0.1:9000" {
		t.Errorf("admin fpm_address = %q, want it kept", a.PHP.FPMAddress)
	}
}

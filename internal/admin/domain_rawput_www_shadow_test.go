package admin

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/uwaserver/uwas/internal/auth"
	"github.com/uwaserver/uwas/internal/config"
)

// F60000: the raw YAML editor's non-admin guards looked the stored domain up
// with the "www." prefix ignored and took the first match. A legacy redirect
// record "www.reseller.com" listed before the site was compared against the
// edited site, so every privilege-sensitive field (type, root, ...) looked
// changed and a reseller's harmless save of their own site was refused.
func TestRawPutResellerEditsSiteNotWWWRedirectRecord(t *testing.T) {
	site := config.Domain{Host: "reseller.com", Type: "static"}
	www := config.Domain{
		Host: "www.reseller.com", Type: "redirect",
		Redirect: config.RedirectConfig{Target: "https://reseller.com", Status: 301},
	}
	raw := func(order []config.Domain) int {
		e := newHostConflictEnv(t, order)
		var root string
		for _, d := range e.s.config.Domains {
			if d.Host == "reseller.com" {
				root = d.Root
			}
		}
		y := "host: reseller.com\ntype: static\nroot: " + root + "\nssl:\n  mode: \"off\"\n"
		b, _ := json.Marshal(map[string]string{"content": y})
		return e.do(http.MethodPut, "/api/v1/config/domains/reseller.com/raw", string(b), auth.RoleReseller, "reseller.com")
	}
	if code := raw([]config.Domain{site, www}); code != http.StatusOK {
		t.Fatalf("control (site first) = %d, want 200", code)
	}
	if code := raw([]config.Domain{www, site}); code != http.StatusOK {
		t.Fatalf("redirect record first = %d, want 200 (guards compared against the www redirect record)", code)
	}
}

package admin

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/auth"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/router"
)

type hostConflictEnv struct {
	s       *Server
	cfgPath string
}

func newHostConflictEnv(t *testing.T, domains []config.Domain) *hostConflictEnv {
	t.Helper()
	dir := t.TempDir()
	webRoot := filepath.Join(dir, "www")
	for i := range domains {
		if domains[i].Type == "static" {
			root := filepath.Join(webRoot, domains[i].Host, "public_html")
			if err := os.MkdirAll(root, 0o755); err != nil {
				t.Fatal(err)
			}
			domains[i].Root = root
		}
		domains[i].SSL.Mode = "off"
	}
	s := testServerFromConfig(t, &config.Config{
		Global:  config.GlobalConfig{WebRoot: webRoot, LogLevel: "info", LogFormat: "text"},
		Domains: domains,
	})
	s.authMgr = newMockAuthManager()
	s.configPath = filepath.Join(dir, "uwas.yaml")
	if err := s.persistConfig(); err != nil {
		t.Fatal(err)
	}
	return &hostConflictEnv{s: s, cfgPath: s.configPath}
}

func (e *hostConflictEnv) do(method, path, body string, role auth.Role, domains ...string) int {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r = r.WithContext(auth.WithUser(r.Context(), &auth.User{ID: "u", Username: "u", Role: role, Enabled: true, Domains: domains}))
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.s.mux.ServeHTTP(rec, r)
	return rec.Code
}

func (e *hostConflictEnv) routeOf(host string) string {
	e.s.configMu.RLock()
	doms := append([]config.Domain(nil), e.s.config.Domains...)
	e.s.configMu.RUnlock()
	if d := router.NewVHostRouter(doms).Lookup(host); d != nil {
		return d.Host
	}
	return ""
}

// TestDomainAddUpdateRejectsAliasOwnedHostnames pins that Add and Update
// refuse a hostname another domain already owns as an alias. Accepting it
// let the later domain take over that alias's traffic (the router registers
// last-writer-wins) and left a config that config.Load rejects.
func TestDomainAddUpdateRejectsAliasOwnedHostnames(t *testing.T) {
	e := newHostConflictEnv(t, []config.Domain{
		{Host: "victim.com", Type: "static", Aliases: []string{"shop.victim.com"}},
		{Host: "reseller.com", Type: "static"},
	})
	cases := []struct {
		name, method, path, body string
		role                     auth.Role
		domains                  []string
	}{
		{"add alias", http.MethodPost, "/api/v1/domains", `{"host":"newsite.com","type":"static","aliases":["shop.victim.com"],"ssl":{"mode":"off"}}`, auth.RoleReseller, []string{"reseller.com", "newsite.com"}},
		{"add host", http.MethodPost, "/api/v1/domains", `{"host":"shop.victim.com","type":"static","ssl":{"mode":"off"}}`, auth.RoleAdmin, nil},
		{"rename", http.MethodPut, "/api/v1/domains/reseller.com", `{"host":"shop.victim.com"}`, auth.RoleAdmin, nil},
	}
	for _, c := range cases {
		if code := e.do(c.method, c.path, c.body, c.role, c.domains...); code != http.StatusConflict {
			t.Errorf("%s: status = %d, want 409", c.name, code)
		}
	}
	if got := e.routeOf("shop.victim.com"); got != "victim.com" {
		t.Errorf("shop.victim.com routes to %q, want victim.com", got)
	}
	if _, err := config.Load(e.cfgPath); err != nil {
		t.Fatalf("persisted config no longer loads: %v", err)
	}
}

// TestDomainAddUpdateLegacyWWWRedirect pins that a rejected Add leaves a
// legacy www redirect domain in place, and that replacing one (Add of the
// apex, or Update of it) also removes its domains.d file so the persisted
// config still loads.
func TestDomainAddUpdateLegacyWWWRedirect(t *testing.T) {
	wwwRedirect := config.Domain{Host: "www.example.com", Type: "redirect", Redirect: config.RedirectConfig{Target: "https://example.com", Status: 301}}

	t.Run("duplicate add leaves config unchanged", func(t *testing.T) {
		e := newHostConflictEnv(t, []config.Domain{{Host: "example.com", Type: "static"}, wwwRedirect})
		if code := e.do(http.MethodPost, "/api/v1/domains", `{"host":"example.com","type":"static","ssl":{"mode":"off"}}`, auth.RoleAdmin); code != http.StatusConflict {
			t.Fatalf("status = %d, want 409", code)
		}
		e.s.configMu.RLock()
		n := len(e.s.config.Domains)
		e.s.configMu.RUnlock()
		if n != 2 {
			t.Fatalf("rejected add changed the live domain list: %d domains, want 2", n)
		}
	})

	t.Run("add apex replaces redirect on disk", func(t *testing.T) {
		e := newHostConflictEnv(t, []config.Domain{{Host: "keep.org", Type: "static"}, wwwRedirect})
		if code := e.do(http.MethodPost, "/api/v1/domains", `{"host":"example.com","type":"static","ssl":{"mode":"off"}}`, auth.RoleAdmin); code != http.StatusCreated {
			t.Fatalf("status = %d, want 201", code)
		}
		if _, err := config.Load(e.cfgPath); err != nil {
			t.Fatalf("persisted config no longer loads: %v", err)
		}
	})

	t.Run("update apex replaces redirect on disk", func(t *testing.T) {
		e := newHostConflictEnv(t, []config.Domain{{Host: "example.com", Type: "static"}, wwwRedirect})
		if code := e.do(http.MethodPut, "/api/v1/domains/example.com", `{"cache":{"enabled":true}}`, auth.RoleAdmin); code != http.StatusOK {
			t.Fatalf("status = %d, want 200", code)
		}
		if _, err := config.Load(e.cfgPath); err != nil {
			t.Fatalf("persisted config no longer loads: %v", err)
		}
	})
}

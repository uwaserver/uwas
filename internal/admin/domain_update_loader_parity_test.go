package admin

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// TestResellerDomainUpdateCannotBreakConfigLoad pins that a non-admin domain
// update the loader would reject is refused, so it can never be persisted to
// domains.d and stop the server from starting for every tenant.
func TestResellerDomainUpdateCannotBreakConfigLoad(t *testing.T) {
	dir := t.TempDir()
	webRoot := filepath.Join(dir, "www")
	root := filepath.Join(webRoot, "reseller.com", "public_html")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "uwas.yaml")
	s := testServerFromConfig(t, &config.Config{
		Global:  config.GlobalConfig{WebRoot: webRoot, LogLevel: "info", LogFormat: "text"},
		Domains: []config.Domain{{Host: "reseller.com", Type: "static", Root: root, SSL: config.SSLConfig{Mode: "off"}}},
	})
	s.authMgr = newMockAuthManager()
	s.configPath = cfgPath
	if err := s.persistConfig(); err != nil {
		t.Fatal(err)
	}

	put := func(body string) int {
		r := withResellerContext(httptest.NewRequest(http.MethodPut, "/api/v1/domains/reseller.com", strings.NewReader(body)))
		r.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		s.mux.ServeHTTP(rec, r)
		return rec.Code
	}
	for name, body := range map[string]string{
		"cache rule glob":     `{"cache":{"enabled":true,"rules":[{"match":"*.html","ttl":60}]}}`,
		"ssl.min_version 1.4": `{"ssl":{"mode":"off","min_version":"1.4"}}`,
		"compression zstd":    `{"compression":{"enabled":true,"algorithms":["zstd"]}}`,
	} {
		if code := put(body); code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, code)
		}
		if _, err := config.Load(cfgPath); err != nil {
			t.Fatalf("%s: persisted config no longer loads: %v", name, err)
		}
	}

	if code := put(`{"cache":{"enabled":true,"rules":[{"match":"\\.html$","ttl":60}]}}`); code != http.StatusOK {
		t.Fatalf("valid update status = %d, want 200", code)
	}
	if cfg, err := config.Load(cfgPath); err != nil || len(cfg.Domains[0].Cache.Rules) != 1 {
		t.Fatalf("valid update not persisted loadably: err=%v", err)
	}
}

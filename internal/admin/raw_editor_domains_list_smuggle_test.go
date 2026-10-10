package admin

// Regression test for F770: the raw domain editor validates the submitted YAML as ONE
// config.Domain (yaml.Unmarshal into Domain ignores an unknown top-level
// `domains:` key) but writes the raw bytes verbatim to domains.d. The loader's
// loadDomainFile tries the `domains:` wrapper FIRST and, when it is non-empty,
// returns that list and ignores the top-level fields. So the domain the
// server actually loads is not the one the editor checked: a non-admin can
// smuggle a root outside web_root (or any other guarded field) past
// rawPutForbiddenChange / the web-root check.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

func TestRawEditorCannotSmuggleDomainsList(t *testing.T) {
	dir := t.TempDir()
	webRoot := filepath.Join(dir, "www")
	root := filepath.Join(webRoot, "reseller.com", "public_html")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(dir, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
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
	// Production reload re-reads the config through config.Load.
	s.SetReloadFunc(func() error { _, err := config.Load(cfgPath); return err })

	put := func(content string) int {
		body, _ := json.Marshal(map[string]string{"content": content})
		r := withResellerContext(httptest.NewRequest(http.MethodPut, "/api/v1/config/domains/reseller.com/raw", strings.NewReader(string(body))))
		r.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		s.mux.ServeHTTP(rec, r)
		return rec.Code
	}
	loadedRoot := func() string {
		cfg, err := config.Load(cfgPath)
		if err != nil {
			return "LOAD-ERROR: " + err.Error()
		}
		for _, d := range cfg.Domains {
			if d.Host == "reseller.com" {
				return d.Root
			}
		}
		return "MISSING"
	}

	problems := 0
	base := fmt.Sprintf("host: reseller.com\ntype: static\nroot: %s\nssl:\n  mode: \"off\"\n", root)

	// Control 1: the raw-editor guards (forbidden-field / web-root) reject a
	// direct out-of-root root from a non-admin.
	direct := fmt.Sprintf("host: reseller.com\ntype: static\nroot: %s\nssl:\n  mode: \"off\"\n", outside)
	if code := put(direct); code != http.StatusForbidden && code != http.StatusBadRequest {
		fmt.Printf("CONTROL FAILED: direct out-of-root root status=%d want 403/400\n", code)
		t.Fatalf("invalid proof")
	}
	// Control 2: a plain in-root edit is saved and loaded as checked.
	if code := put(base); code != http.StatusOK || loadedRoot() != root {
		fmt.Printf("CONTROL FAILED: plain edit status=%d loadedRoot=%s\n", code, loadedRoot())
		t.Fatalf("invalid proof")
	}

	// Case: same checked top-level domain plus a `domains:` list carrying the
	// out-of-root root for the same host.
	smuggle := base + fmt.Sprintf("domains:\n  - host: reseller.com\n    type: static\n    root: %s\n    ssl:\n      mode: \"off\"\n", outside)
	code := put(smuggle)
	got := loadedRoot()
	if code == http.StatusOK && got == outside {
		problems++
	}

	if problems > 0 {
		t.Fatalf("raw editor persisted a domains list the loader applied: status=%d loaded root=%s", code, got)
	}
}

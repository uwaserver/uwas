package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// TestHtaccessFilesMatchDenyApplied pins that <FilesMatch>/<Files> deny
// blocks from a migrated site's .htaccess are enforced for file requests.
// Before the fix the blocks were parsed into the RuleSet and then silently
// ignored — files an operator explicitly denied (database dumps, backups,
// logs) were served to anyone with the URL.
func TestHtaccessFilesMatchDenyApplied(t *testing.T) {
	root := t.TempDir()
	ht := `<FilesMatch "\.(sql|bak)$">
Require all denied
</FilesMatch>
<Files "dump.log">
Deny from all
</Files>
`
	if err := os.WriteFile(filepath.Join(root, ".htaccess"), []byte(ht), 0o644); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"secret.sql": "SELECT * FROM users;",
		"notes.bak":  "backup data",
		"dump.log":   "log line",
		"real.html":  "ok",
		"notes.txt":  "harmless",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := &config.Config{
		Global: config.GlobalConfig{WorkerCount: "1", LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{{
			Host:     "ht.test",
			Type:     "static",
			Root:     root,
			SSL:      config.SSLConfig{Mode: "off"},
			Htaccess: config.HtaccessConfig{Mode: "import"},
		}},
	}
	s := New(cfg, logger.New("error", "text"))
	h := s.buildMiddlewareChain()

	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Host = "ht.test"
		req.Header.Set("User-Agent", "uwas-test")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	// Denied by FilesMatch + Require all denied.
	rec := get("/secret.sql")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("GET /secret.sql = %d, want 403 — the FilesMatch deny block was ignored (body: %q)", rec.Code, rec.Body.String())
	}
	rec = get("/notes.bak")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("GET /notes.bak = %d, want 403 — the FilesMatch deny block was ignored", rec.Code)
	}

	// Denied by Files + Deny from all (Apache 2.2 form).
	rec = get("/dump.log")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("GET /dump.log = %d, want 403 — the Files deny block was ignored", rec.Code)
	}

	// Controls: non-matching and regular files are still served.
	for _, path := range []string{"/notes.txt", "/real.html"} {
		rec = get(path)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200 (deny must not over-block)", path, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "SELECT") {
			t.Fatalf("GET %s leaked denied content", path)
		}
	}
}

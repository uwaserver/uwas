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

// TestHtaccessErrorDocumentApplied pins that ErrorDocument directives from a
// migrated site's .htaccess are actually rendered for UWAS-generated error
// responses. Before the fix the directive was parsed into the htaccess cache
// entry (entry.errorPages) and then silently dropped — renderDomainError only
// consulted domain.ErrorPages from YAML config, so requests for missing files
// showed the generic UWAS 404 page instead of the site's own.
func TestHtaccessErrorDocumentApplied(t *testing.T) {
	root := t.TempDir()
	ht := "ErrorDocument 404 /my404.html\n"
	if err := os.WriteFile(filepath.Join(root, ".htaccess"), []byte(ht), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "my404.html"), []byte("<h1>MY CUSTOM 404</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "real.html"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
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

	get := func(host, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Host = host
		req.Header.Set("User-Agent", "uwas-test")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	// Missing file → 404 with the htaccess-declared custom page body.
	rec := get("ht.test", "/missing-page")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /missing-page = %d, want 404 (body: %q)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "MY CUSTOM 404") {
		t.Fatalf("GET /missing-page = 404 but without the htaccess ErrorDocument body (body: %q)", rec.Body.String())
	}

	// Control: a real file is still served normally.
	rec3 := get("ht.test", "/real.html")
	if rec3.Code != http.StatusOK || !strings.Contains(rec3.Body.String(), "ok") {
		t.Fatalf("GET /real.html = %d (%q), want 200 ok", rec3.Code, rec3.Body.String())
	}

	// Control: a domain WITHOUT .htaccess keeps the generic styled 404.
	root2 := t.TempDir()
	cfg.Domains[0].Root = root2
	cfg.Domains[0].Host = "other.test"
	s2 := New(cfg, logger.New("error", "text"))
	h2 := s2.buildMiddlewareChain()
	req := httptest.NewRequest(http.MethodGet, "/missing-2", nil)
	req.Host = "other.test"
	req.Header.Set("User-Agent", "uwas-test")
	rec2 := httptest.NewRecorder()
	h2.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("GET /missing-2 = %d, want 404", rec2.Code)
	}
	if strings.Contains(rec2.Body.String(), "MY CUSTOM 404") {
		t.Fatal("domain without .htaccess must not serve the other domain's error page")
	}
	if !strings.Contains(rec2.Body.String(), "UWAS") {
		t.Fatalf("generic 404 body missing the default page (body: %q)", rec2.Body.String())
	}
}

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

// TestHtaccessDirectoryIndexApplied pins that DirectoryIndex directives from
// a migrated site's .htaccess actually steer directory resolution. Before the
// fix the directive was parsed into the RuleSet and then silently dropped —
// a site declaring "DirectoryIndex home.html" still got the built-in index
// order and requests for / served the wrong page.
func TestHtaccessDirectoryIndexApplied(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".htaccess"), []byte("DirectoryIndex home.html\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "home.html"), []byte("<h1>HOME PAGE</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<h1>DEFAULT INDEX</h1>"), 0o644); err != nil {
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

	// GET / must resolve through the htaccess DirectoryIndex order.
	rec := get("ht.test", "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200 (body: %q)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "HOME PAGE") {
		t.Fatalf("GET / served %q — the htaccess DirectoryIndex was ignored, want home.html content", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "DEFAULT INDEX") {
		t.Fatalf("GET / served the built-in index order despite htaccess DirectoryIndex")
	}

	// Controls: direct requests still serve each file, and an unlisted
	// real file stays reachable.
	if rec2 := get("ht.test", "/home.html"); !strings.Contains(rec2.Body.String(), "HOME PAGE") {
		t.Fatalf("GET /home.html = %d (%q)", rec2.Code, rec2.Body.String())
	}
	if rec3 := get("ht.test", "/index.html"); !strings.Contains(rec3.Body.String(), "DEFAULT INDEX") {
		t.Fatalf("GET /index.html = %d (%q)", rec3.Code, rec3.Body.String())
	}
	if rec4 := get("ht.test", "/real.html"); rec4.Code != http.StatusOK {
		t.Fatalf("GET /real.html = %d, want 200", rec4.Code)
	}
}

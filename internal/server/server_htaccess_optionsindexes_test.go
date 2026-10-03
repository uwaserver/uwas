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

// TestHtaccessOptionsIndexesApplied pins that htaccess "Options +Indexes"
// enables directory listings for a domain whose YAML config keeps
// directory_listing off. The directive was parsed into
// RuleSet.DirectoryListing (a *bool) but the dispatch gate only read
// domain.DirectoryListing, so the per-directory Apache override was ignored
// and listing requests 404'd. Controls: DirectoryIndex still wins over the
// listing for the doc root, and plain files still serve.
func TestHtaccessOptionsIndexesApplied(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".htaccess"), []byte("Options +Indexes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<h1>FRONT</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	filesDir := filepath.Join(root, "files")
	if err := os.MkdirAll(filesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filesDir, "alpha.txt"), []byte("alpha"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filesDir, "beta.txt"), []byte("beta"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Global: config.GlobalConfig{WorkerCount: "1", LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{{
			Host:     "ix.test",
			Type:     "static",
			Root:     root,
			SSL:      config.SSLConfig{Mode: "off"},
			Htaccess: config.HtaccessConfig{Mode: "import"},
			// DirectoryListing deliberately false: the htaccess directive is
			// the only thing that should turn listings on for /files/.
		}},
	}
	s := New(cfg, logger.New("error", "text"))
	h := s.buildMiddlewareChain()

	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("User-Agent", "uwas-test") // botguard blocks empty UAs
		req.Host = "ix.test"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}

	w := get("/files/")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /files/ = %d, want 200 — htaccess Options +Indexes was ignored (body: %.120s)", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "alpha.txt") || !strings.Contains(body, "beta.txt") {
		t.Fatalf("directory listing is missing the entries (body: %.200s)", body)
	}

	// Control: the doc root keeps its index — DirectoryIndex resolves before
	// the listing is considered ("index before autoindex").
	if w2 := get("/"); w2.Code != http.StatusOK || !strings.Contains(w2.Body.String(), "FRONT") {
		t.Fatalf("GET / = %d body %.120s — index.html must still win over the listing", w2.Code, w2.Body.String())
	}
	// Control: plain files inside the listing-enabled directory still serve.
	if w3 := get("/files/alpha.txt"); w3.Code != http.StatusOK || !strings.Contains(w3.Body.String(), "alpha") {
		t.Fatalf("GET /files/alpha.txt = %d — plain files must still serve", w3.Code)
	}
}

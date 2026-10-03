package server

// Redirect and RedirectMatch directives in an imported .htaccess were parsed
// into the RuleSet (with full status mapping: permanent/temp/seeother/gone)
// but never applied by applyHtaccess — a migrated site's redirects silently
// no-op'd and the visitor got the static handler's response instead.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

func TestHtaccessRedirectsApplied(t *testing.T) {
	root := t.TempDir()
	ht := "Redirect 301 /old.html /new.html\n" +
		"RedirectMatch 302 ^/legacy/(.*)$ /modern/$1\n"
	if err := os.WriteFile(filepath.Join(root, ".htaccess"), []byte(ht), 0o644); err != nil {
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

	// Plain Redirect: prefix match, 301 to the mapped path.
	req := httptest.NewRequest(http.MethodGet, "/old.html", nil)
	req.Host = "ht.test"
	req.Header.Set("User-Agent", "uwas-test")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("GET /old.html = %d, want 301 — htaccess Redirect was silently ignored (body: %q)", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/new.html" {
		t.Errorf("Location = %q, want /new.html", loc)
	}

	// RedirectMatch: regex match with $1 backref in the target.
	req2 := httptest.NewRequest(http.MethodGet, "/legacy/docs/a", nil)
	req2.Host = "ht.test"
	req2.Header.Set("User-Agent", "uwas-test")
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusFound {
		t.Fatalf("GET /legacy/docs/a = %d, want 302 — htaccess RedirectMatch was silently ignored", rec2.Code)
	}
	if loc := rec2.Header().Get("Location"); loc != "/modern/docs/a" {
		t.Errorf("Location = %q, want /modern/docs/a", loc)
	}

	// Control: a real file is still served normally.
	req3 := httptest.NewRequest(http.MethodGet, "/real.html", nil)
	req3.Host = "ht.test"
	req3.Header.Set("User-Agent", "uwas-test")
	rec3 := httptest.NewRecorder()
	h.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Errorf("GET /real.html = %d, want 200 (control)", rec3.Code)
	}
}

package server

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// Apache matches .htaccess RewriteRule patterns against the path without the
// per-directory prefix, so "^backup/" must deny /backup/db.sql. Matching the
// raw "/backup/db.sql" made every standard ^-anchored deny rule fail open.
func TestHtaccessRewriteMatchesPerDirPath(t *testing.T) {
	cases := []struct {
		name, rule, path string
		status           int
		body             string
	}{
		{"apache deny", "RewriteRule ^backup/ - [F]", "/backup/db.sql", 403, ""},
		{"apache rewrite", "RewriteRule ^old-page$ /new.html [L]", "/old-page", 200, "NEW"},
		{"relative target", "RewriteCond %{REQUEST_FILENAME} !-f\nRewriteRule ^ new.html [L]", "/a/b", 200, "NEW"},
		{"legacy leading slash", "RewriteRule ^/backup/ - [F]", "/backup/db.sql", 403, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			os.MkdirAll(filepath.Join(dir, "backup"), 0o755)
			os.WriteFile(filepath.Join(dir, "backup", "db.sql"), []byte("DB-DUMP"), 0o644)
			os.WriteFile(filepath.Join(dir, "new.html"), []byte("NEW"), 0o644)
			os.WriteFile(filepath.Join(dir, ".htaccess"), []byte("RewriteEngine On\n"+c.rule+"\n"), 0o644)
			s := newDispatchTestServer(t, []config.Domain{{
				Host: "ht.test", Type: "static", Root: dir,
				SSL:      config.SSLConfig{Mode: "off"},
				Htaccess: config.HtaccessConfig{Mode: "import"},
			}})
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", c.path, nil)
			req.Host = "ht.test"
			s.handleRequest(rec, req)
			if rec.Code != c.status || !strings.Contains(rec.Body.String(), c.body) {
				t.Fatalf("GET %s: status=%d body=%.40q, want %d containing %q", c.path, rec.Code, rec.Body.String(), c.status, c.body)
			}
		})
	}
}

package server

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// F18601 (characterization, no defect): hostile file names and query strings
// never reach the listing page unescaped.
func TestDirectoryListingEscapesHostileNames(t *testing.T) {
	root := filepath.Join(t.TempDir(), "site")
	dir := filepath.Join(root, "list")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	names := []string{`<script>alert(1)<`, `a"b`, `a'b`, "a\u202eb", "a&b", "a%20b", "new\nline", "x\"><img src=x onerror=alert(1)>", strings.Repeat("n", 200), "ok.txt"}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := newDispatchTestServer(t, []config.Domain{{Host: "s.test", Type: "static", Root: root,
		SSL: config.SSLConfig{Mode: "off"}, DirectoryListing: true}})
	get := func(p string) string {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", p, nil)
		req.Host = "s.test"
		req.Header.Set("User-Agent", "uwas-test")
		s.handleRequest(rec, req)
		if rec.Code != 200 {
			t.Fatalf("%s -> %d", p, rec.Code)
		}
		return rec.Body.String()
	}
	body := get("/list/?C=M;O=D\"><script>alert(2)</script>")
	for _, bad := range []string{"<script>", "<img", `onerror=alert(1)>`, `href="/list/a"b"`} {
		if strings.Contains(body, bad) {
			t.Errorf("listing contains unescaped %q", bad)
		}
	}
	if !strings.Contains(body, "ok.txt") || !strings.Contains(body, "&lt;script&gt;") {
		t.Errorf("listing lost the control entries:\n%s", body)
	}
}

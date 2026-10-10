package admin

import (
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDomainAddPlaceholderNeverFollowsSymlink(t *testing.T) {
	add := func(host string, plant func(root string)) string {
		s := testServer()
		webRoot := t.TempDir()
		s.config.Global.WebRoot = webRoot
		root := filepath.Join(webRoot, host, "public_html")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		plant(root)
		body := strings.NewReader(fmt.Sprintf(`{"host":%q,"type":"static","root":%q,"ssl":{"mode":"auto"}}`, host, root))
		rec := httptest.NewRecorder()
		s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/domains", body))
		if rec.Code != 201 {
			t.Fatalf("add %s: %d %s", host, rec.Code, rec.Body.String())
		}
		return root
	}
	read := func(p string) string { b, _ := os.ReadFile(p); return string(b) }

	root := add("plain.example.com", func(string) {})
	if !strings.Contains(read(filepath.Join(root, "index.html")), "plain.example.com") {
		t.Error("plain docroot: placeholder not written")
	}
	root = add("keep.example.com", func(r string) { os.WriteFile(filepath.Join(r, "index.html"), []byte("mine"), 0o644) })
	if read(filepath.Join(root, "index.html")) != "mine" {
		t.Error("existing index.html overwritten")
	}
	out := filepath.Join(t.TempDir(), "x")
	add("dangling.example.com", func(r string) { os.Symlink(out, filepath.Join(r, "index.html")) })
	if _, err := os.Lstat(out); err == nil {
		t.Error("dangling symlink followed")
	}
	victim := filepath.Join(t.TempDir(), "victim")
	os.WriteFile(victim, []byte("secret"), 0o600)
	add("live.example.com", func(r string) { os.Symlink(victim, filepath.Join(r, "index.html")) })
	if read(victim) != "secret" {
		t.Error("symlink target overwritten")
	}
}

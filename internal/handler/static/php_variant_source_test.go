package static

// F48600: Serve answered 403 for backup and editor copies of a script
// (wp-config.php.bak, a.php~, a.phps) because only a name ending in ".php" is
// handed to FastCGI and a static answer would be the script's source. The
// other extensions PHP is routinely mapped to (.phtml, .pht, .php3-.php8) were
// not on that list: Apache and cPanel run them as PHP, UWAS ran nothing and
// sent the raw source of a template or include to anyone who asked for it.
//
// F48602: the directory listing advertised every one of those names with a
// link that Serve then refuses.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/router"
)

func TestStaticRefusesPHPVariantSource(t *testing.T) {
	dir := t.TempDir()
	const source = "<?php $dbpass = 'hunter2';"
	for _, name := range []string{"tmpl.phtml", "tmpl.PHTML", "a.pht", "a.php3", "a.php4", "a.php5", "a.php7", "a.php8"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Controls: names that merely look similar stay ordinary static files.
	for name, body := range map[string]string{
		"notes.txt": "NOTES", "a.php6x": "X", "phtml.txt": "T", "a.phtmlx": "Y", "a.php9": "Z",
		"composer.phar": "PHAR", "inc.inc": "INC",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h := New()
	get := func(name string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/"+name, nil)
		h.Serve(&router.RequestContext{
			Request: req, Response: router.NewResponseWriter(rec),
			ResolvedPath: filepath.Join(dir, name), DocumentRoot: dir,
		})
		return rec
	}

	for _, name := range []string{"tmpl.phtml", "tmpl.PHTML", "a.pht", "a.php3", "a.php4", "a.php5", "a.php7", "a.php8"} {
		rec := get(name)
		t.Logf("EXPECTED: 403, no source\nACTUAL: %s -> %d %q", name, rec.Code, rec.Body.String())
		if rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), "hunter2") {
			t.Errorf("PROBLEM CONFIRMED: GET /%s = %d %q, want 403 without the script source", name, rec.Code, rec.Body.String())
		}
	}
	for name, want := range map[string]string{
		"notes.txt": "NOTES", "a.php6x": "X", "phtml.txt": "T", "a.phtmlx": "Y", "a.php9": "Z",
		"composer.phar": "PHAR", "inc.inc": "INC",
	} {
		if rec := get(name); rec.Code != http.StatusOK || rec.Body.String() != want {
			t.Errorf("control: GET /%s = %d %q, want 200 %q", name, rec.Code, rec.Body.String(), want)
		}
	}
}

func TestDirListingOmitsNamesServeRefuses(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"wp-config.php.bak", "old.php~", "src.phps", "tmpl.phtml", "legacy.php5",
		"readme.txt", "app.js", "index.php", "report.pdf",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ServeDirListing(&router.RequestContext{Request: req, Response: router.NewResponseWriter(rec)}, dir, "/")
	body := rec.Body.String()
	t.Logf("EXPECTED: refused names absent from the listing\nACTUAL: %s", body)
	for _, hidden := range []string{"wp-config.php.bak", "old.php~", "src.phps", "tmpl.phtml", "legacy.php5"} {
		if strings.Contains(body, hidden) {
			t.Errorf("PROBLEM CONFIRMED: listing advertises %q, which the static handler answers with 403", hidden)
		}
	}
	for _, shown := range []string{"readme.txt", "app.js", "index.php", "report.pdf"} {
		if !strings.Contains(body, shown) {
			t.Errorf("control: listing lost %q", shown)
		}
	}
}

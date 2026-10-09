package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// TestImageOptVariantMustStayInDocRoot pins F261: the WebP/AVIF swap opens
// resolved+"."+fmt, a different file from the one ResolveRequest contained.
// A symlinked pic.jpg.webp pointing outside the doc root must be skipped and
// the original image served instead.
func TestImageOptVariantMustStayInDocRoot(t *testing.T) {
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret"), []byte("TOP-SECRET"), 0o644)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "pic.jpg"), []byte("jpeg-original"), 0o644)
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(dir, "pic.jpg.webp")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	os.WriteFile(filepath.Join(dir, "ok.jpg"), []byte("jpeg-ok"), 0o644)
	os.WriteFile(filepath.Join(dir, "ok.jpg.webp"), []byte("webp-inside"), 0o644)

	s := newDispatchTestServer(t, []config.Domain{{
		Host: "img.test", Type: "static", Root: dir, SSL: config.SSLConfig{Mode: "off"},
		ImageOptimization: config.ImageOptimizationConfig{Enabled: true, Formats: []string{"webp"}},
	}})
	get := func(p string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", p, nil)
		req.Host = "img.test"
		req.Header.Set("Accept", "image/webp,image/*")
		s.handleRequest(rec, req)
		return rec
	}
	if rec := get("/pic.jpg"); strings.Contains(rec.Body.String(), "TOP-SECRET") || rec.Body.String() != "jpeg-original" {
		t.Errorf("outside variant: status=%d body=%q, want the original jpeg", rec.Code, rec.Body.String())
	}
	if rec := get("/ok.jpg"); rec.Body.String() != "webp-inside" {
		t.Errorf("in-root variant: body=%q, want webp-inside", rec.Body.String())
	}
}

// TestHtaccessParseFailureFailsClosed pins F262: an .htaccess that exceeds the
// parser's limits must not silently drop its <Files> denies. Requests answer
// 500 (as Apache does for a broken .htaccess) until the file is fixed or
// removed.
func TestHtaccessParseFailureFailsClosed(t *testing.T) {
	var b strings.Builder
	b.WriteString("<Files \"secret.sql\">\nRequire all denied\n</Files>\n")
	for i := 0; i < 205; i++ {
		fmt.Fprintf(&b, "Header set X-Pad-%d \"1\"\n", i)
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "secret.sql"), []byte("DB-DUMP"), 0o644)
	os.WriteFile(filepath.Join(dir, "page.html"), []byte("hello"), 0o644)
	ht := filepath.Join(dir, ".htaccess")
	os.WriteFile(ht, []byte(b.String()), 0o644)

	s := newDispatchTestServer(t, []config.Domain{{
		Host: "ht.test", Type: "static", Root: dir, SSL: config.SSLConfig{Mode: "off"},
		Htaccess: config.HtaccessConfig{Mode: "import"},
	}})
	get := func(p string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", p, nil)
		req.Host = "ht.test"
		s.handleRequest(rec, req)
		return rec
	}
	if rec := get("/secret.sql"); rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "DB-DUMP") {
		t.Errorf("broken .htaccess: status=%d body=%q, want 500 without the dump", rec.Code, rec.Body.String())
	}
	os.Remove(ht)
	if rec := get("/page.html"); rec.Code != http.StatusOK {
		t.Errorf("after removing the broken .htaccess: status=%d, want 200", rec.Code)
	}
}

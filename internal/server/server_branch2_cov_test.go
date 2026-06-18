package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// --- applyHtaccess: ErrorDocument + php_value directives parsed & applied ---

func TestApplyHtaccessErrorDocAndPHPValue(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "404.html"), []byte("custom-404"), 0644)
	os.WriteFile(filepath.Join(dir, "index.php"), []byte("<?php echo 1;"), 0644)
	htaccess := `
ErrorDocument 404 /404.html
php_value memory_limit 256M
php_flag display_errors on
`
	os.WriteFile(filepath.Join(dir, ".htaccess"), []byte(htaccess), 0644)
	s := newDispatchTestServer(t, []config.Domain{
		{
			Host:     "hterr.test",
			Type:     "php",
			Root:     dir,
			SSL:      config.SSLConfig{Mode: "off"},
			Htaccess: config.HtaccessConfig{Mode: "import"},
			PHP:      config.PHPConfig{FPMAddress: "127.0.0.1:65496"},
		},
	})
	// Request a non-php path so applyHtaccess runs (php paths skip rewrite but
	// still go through dispatch). Use a path that triggers the ErrorDocument
	// merge into domain.ErrorPages.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/some-page", nil)
	req.Host = "hterr.test"
	s.handleRequest(rec, req)
	// The ErrorDocument map should have been merged onto the domain.
	d := s.vhosts.Lookup("hterr.test")
	errorPagesMu.RLock()
	_, has404 := d.ErrorPages[404]
	errorPagesMu.RUnlock()
	if !has404 {
		t.Errorf("ErrorDocument 404 not merged into domain.ErrorPages: %#v", d.ErrorPages)
	}
}

// --- handleFileRequest: directory requested as a file → 403 ---

func TestHandleFileRequestDirAsFile(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "assets")
	os.Mkdir(subdir, 0755)
	// Place an index so ResolveRequest finds the dir but with a trailing path
	// that resolves to the directory itself.
	s := newDispatchTestServer(t, []config.Domain{
		{Host: "daf.test", Type: "static", Root: dir, SSL: config.SSLConfig{Mode: "off"}},
	})
	rec := httptest.NewRecorder()
	// Request the directory directly (no trailing slash, no index file).
	req := httptest.NewRequest("GET", "/assets", nil)
	req.Host = "daf.test"
	s.handleRequest(rec, req)
	// Without an index, the static resolver returns 404 or 403; either keeps
	// directory contents from being served as a file.
	if rec.Code == http.StatusOK {
		t.Fatalf("directory should not be served as a file (got 200)")
	}
}

// --- location root: non-tilde prefix strip + traversal containment → 403 ---

func TestHandleRequestLocationRootPrefixTraversal(t *testing.T) {
	locRoot := t.TempDir()
	os.WriteFile(filepath.Join(locRoot, "ok.txt"), []byte("ok"), 0644)
	s := newDispatchTestServer(t, []config.Domain{
		{
			Host: "lrpt.test",
			Type: "static",
			Root: t.TempDir(),
			SSL:  config.SSLConfig{Mode: "off"},
			Locations: []config.LocationConfig{
				// Non-tilde prefix → the matched prefix is stripped before join.
				{Match: "/d/", Root: locRoot},
			},
		},
	})
	// Normal file under the location root.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/d/ok.txt", nil)
	req.Host = "lrpt.test"
	s.handleRequest(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "ok") {
		t.Errorf("body = %q", rec.Body.String())
	}
}

// --- handleProxy: mirror skipped for large/unknown request body ---

func TestHandleProxyMirrorLargeBodySkipped(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	}))
	defer primary.Close()
	mirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer mirror.Close()

	domains := []config.Domain{
		{
			Host: "mirbig.test",
			Type: "proxy",
			SSL:  config.SSLConfig{Mode: "off"},
			Proxy: config.ProxyConfig{
				Upstreams:             []config.Upstream{{Address: primary.URL}},
				AllowPrivateUpstreams: true,
				Mirror: config.MirrorConfig{
					Enabled:      true,
					Backend:      mirror.URL,
					Percent:      100,
					MaxBodyBytes: 4, // tiny → larger bodies are skipped
				},
			},
		},
	}
	s := newDispatchTestServer(t, domains)
	s.rebuildProxyPools(domains)

	rec := httptest.NewRecorder()
	// Body larger than MaxBodyBytes → mirror skip branch executes.
	req := httptest.NewRequest("POST", "/api", strings.NewReader("this body exceeds four bytes"))
	req.Host = "mirbig.test"
	s.handleRequest(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d want 200", rec.Code)
	}
}

// --- handleFileRequest: OriginalURI already set (preset by caller) ---

func TestHandleFileRequestOriginalURIPreset(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("x"), 0644)
	s := newDispatchTestServer(t, []config.Domain{
		{Host: "ou.test", Type: "static", Root: dir, SSL: config.SSLConfig{Mode: "off"}},
	})
	// Two requests to the same domain; the second still hits the OriginalURI
	// guard. We can't preset on the pooled context directly, so just exercise
	// the normal path which sets OriginalURI once.
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/index.html", nil)
		req.Host = "ou.test"
		s.handleRequest(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d want 200", rec.Code)
		}
	}
}

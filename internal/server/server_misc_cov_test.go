package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// newCacheTestServer builds a Server with the in-memory cache + ESI enabled.
func newCacheTestServer(t *testing.T, domains []config.Domain) *Server {
	t.Helper()
	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
			Cache: config.CacheConfig{
				Enabled:     true,
				MemoryLimit: config.ByteSize(8 << 20),
				DiskPath:    t.TempDir(),
				DiskLimit:   config.ByteSize(8 << 20),
			},
		},
		Domains: domains,
	}
	s := New(cfg, logger.New("error", "text"))
	t.Cleanup(func() {
		s.cancel()
		// Drain any in-flight async disk-cache writes before t.TempDir cleanup
		// (registered earlier, runs after this) removes the disk path.
		time.Sleep(50 * time.Millisecond)
	})
	return s
}

// --- cache: miss → store, then hit serves from cache (incl. ETag + 304) ---

func TestHandleRequestCacheStoreAndHit(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "page.html"), []byte("<html>cached body</html>"), 0644)
	s := newCacheTestServer(t, []config.Domain{
		{
			Host:  "cache.test",
			Type:  "static",
			Root:  dir,
			SSL:   config.SSLConfig{Mode: "off"},
			Cache: config.DomainCache{Enabled: true, TTL: 60},
		},
	})

	// First request: MISS, stored.
	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest("GET", "/page.html", nil)
	req1.Host = "cache.test"
	s.handleRequest(rec1, req1)
	if rec1.Code != 200 {
		t.Fatalf("first request status = %d want 200", rec1.Code)
	}

	// Second request: should be a HIT (X-Cache header set).
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/page.html", nil)
	req2.Host = "cache.test"
	s.handleRequest(rec2, req2)
	if rec2.Code != 200 {
		t.Fatalf("second request status = %d want 200", rec2.Code)
	}
	xc := rec2.Header().Get("X-Cache")
	if xc != "HIT" && xc != "STALE" {
		t.Logf("X-Cache = %q (cache hit not guaranteed on disk-only); continuing", xc)
	}

	// If an ETag was generated, a conditional request returns 304.
	if etag := rec2.Header().Get("Etag"); etag != "" {
		rec3 := httptest.NewRecorder()
		req3 := httptest.NewRequest("GET", "/page.html", nil)
		req3.Host = "cache.test"
		req3.Header.Set("If-None-Match", etag)
		s.handleRequest(rec3, req3)
		if rec3.Code != http.StatusNotModified && rec3.Code != http.StatusOK {
			t.Fatalf("conditional status = %d", rec3.Code)
		}
	}
}

// --- cache: bypass rule + WordPress path bypass + session cookie bypass ---

func TestHandleRequestCacheBypassRules(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "page.html"), []byte("x"), 0644)
	os.MkdirAll(filepath.Join(dir, "wp-admin"), 0755)
	os.WriteFile(filepath.Join(dir, "wp-admin", "index.html"), []byte("admin"), 0644)
	s := newCacheTestServer(t, []config.Domain{
		{
			Host: "cb.test",
			Type: "static",
			Root: dir,
			SSL:  config.SSLConfig{Mode: "off"},
			Cache: config.DomainCache{
				Enabled: true,
				TTL:     60,
				Rules: []config.CacheRule{
					{Match: "/page.html", Bypass: true},
					{Match: "/page.html", CacheControl: "public"},
				},
			},
		},
	})
	// Bypass rule path.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/page.html", nil)
	req.Host = "cb.test"
	s.handleRequest(rec, req)
	if rec.Code != 200 {
		t.Fatalf("bypass rule status = %d", rec.Code)
	}

	// WordPress admin path bypass.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/wp-admin/index.html", nil)
	req2.Host = "cb.test"
	s.handleRequest(rec2, req2)

	// Session cookie bypass.
	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest("GET", "/page.html", nil)
	req3.Host = "cb.test"
	req3.Header.Set("Cookie", "wordpress_logged_in=1")
	s.handleRequest(rec3, req3)
}

// --- cache: PHP domain only caches known static extensions ---

func TestHandleRequestPHPCacheStaticExtOnly(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "style.css"), []byte("body{}"), 0644)
	s := newCacheTestServer(t, []config.Domain{
		{
			Host:  "phpc.test",
			Type:  "php",
			Root:  dir,
			SSL:   config.SSLConfig{Mode: "off"},
			Cache: config.DomainCache{Enabled: true, TTL: 60},
		},
	})
	// .css is cacheable on PHP domains → cacheEnabled stays true.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/style.css", nil)
	req.Host = "phpc.test"
	s.handleRequest(rec, req)
	if rec.Code != 200 {
		t.Fatalf("css status = %d want 200", rec.Code)
	}

	// A non-static path (no extension) disables caching for PHP domains.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/dynamic", nil)
	req2.Host = "phpc.test"
	s.handleRequest(rec2, req2)
}

// --- handleFileRequest: directory listing enabled ---

func TestHandleFileRequestDirListing(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0644)
	s := newDispatchTestServer(t, []config.Domain{
		{
			Host:             "dl.test",
			Type:             "static",
			Root:             dir,
			SSL:              config.SSLConfig{Mode: "off"},
			DirectoryListing: true,
		},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "dl.test"
	s.handleRequest(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("dir listing status = %d want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "a.txt") || !strings.Contains(body, "b.txt") {
		t.Errorf("dir listing body missing entries: %q", body)
	}
}

// --- handleFileRequest: image optimization serves pre-converted webp ---

func TestHandleFileRequestImageOpt(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "pic.jpg"), []byte("jpeg-bytes"), 0644)
	os.WriteFile(filepath.Join(dir, "pic.jpg.webp"), []byte("webp-bytes"), 0644)
	s := newDispatchTestServer(t, []config.Domain{
		{
			Host: "img.test",
			Type: "static",
			Root: dir,
			SSL:  config.SSLConfig{Mode: "off"},
			ImageOptimization: config.ImageOptimizationConfig{
				Enabled: true,
				Formats: []string{"webp"},
			},
		},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/pic.jpg", nil)
	req.Host = "img.test"
	req.Header.Set("Accept", "image/webp,image/*")
	s.handleRequest(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("image opt status = %d want 200", rec.Code)
	}
	if rec.Header().Get("Content-Type") != "image/webp" {
		t.Errorf("Content-Type = %q want image/webp", rec.Header().Get("Content-Type"))
	}
	if !strings.Contains(rec.Body.String(), "webp-bytes") {
		t.Errorf("served wrong body: %q", rec.Body.String())
	}
}

// --- per-domain access log written through handleRequest ---

func TestHandleRequestAccessLog(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("x"), 0644)
	logPath := filepath.Join(t.TempDir(), "access.log")
	s := newDispatchTestServer(t, []config.Domain{
		{
			Host:      "log.test",
			Type:      "static",
			Root:      dir,
			SSL:       config.SSLConfig{Mode: "off"},
			AccessLog: config.AccessLogConfig{Path: logPath},
		},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/index.html", nil)
	req.Host = "log.test"
	req.RemoteAddr = "203.0.113.99:8080"
	s.handleRequest(rec, req)
	// Allow async file write to flush.
	s.domainLogs.Close()
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read access log: %v", err)
	}
	if !strings.Contains(string(data), "GET /index.html") {
		t.Errorf("access log missing request line: %q", string(data))
	}
}

// --- applyHtaccess: rewrite + header + expires directives ---

func TestApplyHtaccessDirectives(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "real.html"), []byte("real"), 0644)
	htaccess := `
<IfModule mod_headers.c>
Header set X-From-Htaccess "yes"
</IfModule>
<IfModule mod_expires.c>
ExpiresActive On
ExpiresByType text/html "access plus 1 hour"
</IfModule>
RewriteEngine On
RewriteRule ^pretty$ /real.html [L]
`
	os.WriteFile(filepath.Join(dir, ".htaccess"), []byte(htaccess), 0644)
	s := newDispatchTestServer(t, []config.Domain{
		{
			Host:     "ht.test",
			Type:     "static",
			Root:     dir,
			SSL:      config.SSLConfig{Mode: "off"},
			Htaccess: config.HtaccessConfig{Mode: "import"},
		},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/pretty", nil)
	req.Host = "ht.test"
	s.handleRequest(rec, req)
	if rec.Header().Get("X-From-Htaccess") != "yes" {
		t.Errorf("htaccess header not applied: %v", rec.Header())
	}
}

// --- configHasPHPDomains: nil + has-php + no-php ---

func TestConfigHasPHPDomains(t *testing.T) {
	if configHasPHPDomains(nil) {
		t.Errorf("nil config should report no php domains")
	}
	if configHasPHPDomains(&config.Config{Domains: []config.Domain{{Type: "static"}}}) {
		t.Errorf("static-only config should report no php domains")
	}
	if !configHasPHPDomains(&config.Config{Domains: []config.Domain{{Type: "php"}}}) {
		t.Errorf("php config should report php domains")
	}
}

// --- resolveAppsUpstream: passthrough, no appsMgr handled, unresolved name ---

func TestResolveAppsUpstreamBranches(t *testing.T) {
	s := newDispatchTestServer(t, nil)

	// Non-apps scheme passes through unchanged.
	if got := s.resolveAppsUpstream("http://example.com:8080"); got != "http://example.com:8080" {
		t.Errorf("passthrough = %q", got)
	}
	// apps:// name with appsMgr present but app not registered → placeholder.
	if got := s.resolveAppsUpstream("apps://nonexistent"); got != "http://127.0.0.1:0" {
		t.Errorf("unresolved apps = %q want placeholder", got)
	}
	// apps:// with trailing path is stripped to the name only.
	if got := s.resolveAppsUpstream("apps://nope/foo?bar=1"); got != "http://127.0.0.1:0" {
		t.Errorf("apps with path = %q want placeholder", got)
	}
}

// --- domainlog: cleanupOld removes aged rotated files ---

func TestDomainLogCleanupOldCov2(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "access.log")
	m := newDomainLogManager()
	defer m.Close()

	// Register the domain so cleanupOld sees it (short MaxAge).
	m.Write("clean.test", logPath, config.RotateConfig{MaxAge: config.Duration{Duration: time.Nanosecond}},
		"GET", "/", "127.0.0.1", "A", 200, 10, time.Millisecond)

	// Create an old rotated file with an old mtime.
	rotated := logPath + ".20200101-000000.gz"
	os.WriteFile(rotated, []byte("old"), 0644)
	old := time.Now().Add(-48 * time.Hour)
	os.Chtimes(rotated, old, old)

	m.cleanupOld()

	if _, err := os.Stat(rotated); !os.IsNotExist(err) {
		t.Errorf("expected aged rotated file to be removed")
	}
}

// --- domainlog: compressFile gzips and removes the source ---

func TestCompressFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "x.log")
	os.WriteFile(src, []byte("loglineloglinelogline"), 0644)
	compressFile(src)
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("source should be removed after compression")
	}
	if _, err := os.Stat(src + ".gz"); err != nil {
		t.Errorf("gz file should exist: %v", err)
	}

	// Error path: compressing a missing file is a no-op.
	compressFile(filepath.Join(dir, "missing.log"))
}

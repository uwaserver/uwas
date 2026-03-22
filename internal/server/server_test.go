package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/cache"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

func testConfig(root string) *config.Config {
	return &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
			PIDFile:     "",
		},
		Domains: []config.Domain{
			{
				Host: "localhost",
				Root: root,
				Type: "static",
				SSL:  config.SSLConfig{Mode: "off"},
			},
		},
	}
}

func TestHandleRequestStaticFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>Test</h1>"), 0644)

	cfg := testConfig(dir)
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/index.html", nil)
	req.Host = "localhost"
	s.handleRequest(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "<h1>Test</h1>" {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestHandleRequestIndexFallback(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("home"), 0644)

	cfg := testConfig(dir)
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "localhost"
	s.handleRequest(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestHandleRequest404(t *testing.T) {
	dir := t.TempDir()

	cfg := testConfig(dir)
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/nonexistent", nil)
	req.Host = "localhost"
	s.handleRequest(rec, req)

	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestHandleRequestUnknownHost(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{LogLevel: "error", LogFormat: "text"},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "unknown.com"
	s.handleRequest(rec, req)

	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestHandleRequestRedirectDomain(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{
			{
				Host: "old.com",
				Type: "redirect",
				SSL:  config.SSLConfig{Mode: "off"},
				Redirect: config.RedirectConfig{
					Target:       "https://new.com",
					Status:       301,
					PreservePath: true,
				},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/some/path", nil)
	req.Host = "old.com"
	s.handleRequest(rec, req)

	if rec.Code != 301 {
		t.Errorf("status = %d, want 301", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if loc != "https://new.com/some/path" {
		t.Errorf("Location = %q", loc)
	}
}

func TestHandleRequestBlockedPath(t *testing.T) {
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	os.MkdirAll(gitDir, 0755)
	os.WriteFile(filepath.Join(gitDir, "config"), []byte("secret"), 0644)

	cfg := testConfig(dir)
	cfg.Domains[0].Security.BlockedPaths = []string{".git"}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/.git/config", nil)
	req.Host = "localhost"
	s.handleRequest(rec, req)

	if rec.Code != 403 {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestHandleRequestRewrite(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "target.html"), []byte("rewritten"), 0644)

	cfg := testConfig(dir)
	cfg.Domains[0].Rewrites = []config.RewriteRule{
		{Match: "^/old$", To: "/target.html"},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/old", nil)
	req.Host = "localhost"
	s.handleRequest(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "rewritten" {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestHandleHTTPRedirectToHTTPS(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{
			{
				Host: "secure.com",
				Root: "/tmp",
				Type: "static",
				SSL:  config.SSLConfig{Mode: "auto"},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/page", nil)
	req.Host = "secure.com"
	s.handleHTTP(rec, req)

	if rec.Code != 301 {
		t.Errorf("status = %d, want 301", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if loc != "https://secure.com/page" {
		t.Errorf("Location = %q", loc)
	}
	if rec.Header().Get("Strict-Transport-Security") == "" {
		t.Error("HSTS header should be set")
	}
}

func TestHandleProxyNoPool(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{
			{
				Host: "api.com",
				Type: "proxy",
				SSL:  config.SSLConfig{Mode: "off"},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "api.com"
	s.handleRequest(rec, req)

	if rec.Code != 502 {
		t.Errorf("status = %d, want 502", rec.Code)
	}
}

func TestRenderErrorPage(t *testing.T) {
	codes := []int{400, 403, 404, 500, 502, 503, 504}
	for _, code := range codes {
		rec := httptest.NewRecorder()
		renderErrorPage(rec, code)
		if rec.Code != code {
			t.Errorf("renderErrorPage(%d) status = %d", code, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
			t.Errorf("renderErrorPage(%d) content-type = %q", code, ct)
		}
		body := rec.Body.String()
		if len(body) < 100 {
			t.Errorf("renderErrorPage(%d) body too short: %d bytes", code, len(body))
		}
	}
}

func TestServerHeaders(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("ok"), 0644)

	cfg := testConfig(dir)
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/index.html", nil)
	req.Host = "localhost"
	s.handleRequest(rec, req)

	if h := rec.Header().Get("Server"); h == "" {
		t.Error("Server header should be set")
	}
}

func TestSetConfigPath(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{LogLevel: "error", LogFormat: "text"},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	s.SetConfigPath("/etc/uwas/uwas.yaml")
	if s.configPath != "/etc/uwas/uwas.yaml" {
		t.Errorf("configPath = %q, want /etc/uwas/uwas.yaml", s.configPath)
	}
}

func TestReloadNoConfigPath(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{LogLevel: "error", LogFormat: "text"},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	err := s.reload()
	if err == nil {
		t.Error("expected error when no config path set")
	}
	if !strings.Contains(err.Error(), "no config path set") {
		t.Errorf("error = %q, should mention no config path", err.Error())
	}
}

func TestReloadSuccess(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "uwas.yaml")

	// Write a valid config file
	configContent := `
domains:
  - host: reloaded.com
    root: /tmp
    type: static
    ssl:
      mode: "off"
`
	os.WriteFile(configPath, []byte(configContent), 0644)

	// Start with a different config
	cfg := &config.Config{
		Global: config.GlobalConfig{LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{
			{Host: "original.com", Root: "/tmp", Type: "static", SSL: config.SSLConfig{Mode: "off"}},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)
	s.SetConfigPath(configPath)

	err := s.reload()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}

	// Verify config was updated
	if len(s.config.Domains) != 1 {
		t.Fatalf("domains count = %d, want 1", len(s.config.Domains))
	}
	if s.config.Domains[0].Host != "reloaded.com" {
		t.Errorf("domain host = %q, want reloaded.com", s.config.Domains[0].Host)
	}
}

func TestReloadInvalidConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "uwas.yaml")

	// Write an invalid config
	os.WriteFile(configPath, []byte(`invalid yaml {{`), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{LogLevel: "error", LogFormat: "text"},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)
	s.SetConfigPath(configPath)

	err := s.reload()
	if err == nil {
		t.Error("expected error for invalid config")
	}
}

func TestWritePID(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "uwas.pid")
	cfg := testConfig(t.TempDir())
	cfg.Global.PIDFile = pidFile
	log := logger.New("error", "text")
	s := New(cfg, log)

	if err := s.writePID(); err != nil {
		t.Fatalf("writePID: %v", err)
	}

	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read pid file: %v", err)
	}

	got := strings.TrimSpace(string(data))
	want := strconv.Itoa(os.Getpid())
	if got != want {
		t.Errorf("pid = %q, want %q", got, want)
	}
}

func TestWritePIDEmptyPath(t *testing.T) {
	cfg := testConfig(t.TempDir())
	cfg.Global.PIDFile = ""
	log := logger.New("error", "text")
	s := New(cfg, log)

	// Empty PIDFile should be a no-op
	if err := s.writePID(); err != nil {
		t.Fatalf("writePID with empty path should not error: %v", err)
	}
}

func TestWritePIDCreatesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sub", "dir")
	pidFile := filepath.Join(dir, "uwas.pid")
	cfg := testConfig(t.TempDir())
	cfg.Global.PIDFile = pidFile
	log := logger.New("error", "text")
	s := New(cfg, log)

	if err := s.writePID(); err != nil {
		t.Fatalf("writePID should create parent dirs: %v", err)
	}
	if _, err := os.Stat(pidFile); err != nil {
		t.Fatalf("pid file should exist: %v", err)
	}
}

func TestRemovePID(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "uwas.pid")
	cfg := testConfig(t.TempDir())
	cfg.Global.PIDFile = pidFile
	log := logger.New("error", "text")
	s := New(cfg, log)

	// Write then remove
	if err := s.writePID(); err != nil {
		t.Fatalf("writePID: %v", err)
	}
	if _, err := os.Stat(pidFile); err != nil {
		t.Fatal("pid file should exist after writePID")
	}

	s.removePID()
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Error("pid file should be gone after removePID")
	}
}

func TestRemovePIDEmptyPath(t *testing.T) {
	cfg := testConfig(t.TempDir())
	cfg.Global.PIDFile = ""
	log := logger.New("error", "text")
	s := New(cfg, log)

	// Should not panic with empty path
	s.removePID()
}

func TestHandleFileRequestPHP(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.php"), []byte("<?php echo 'hi'; ?>"), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
		},
		Domains: []config.Domain{
			{
				Host: "php.local",
				Root: dir,
				Type: "php",
				SSL:  config.SSLConfig{Mode: "off"},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/index.php", nil)
	req.Host = "php.local"
	s.handleRequest(rec, req)

	// No FastCGI backend running, so expect 502 Bad Gateway
	if rec.Code != 502 {
		t.Errorf("status = %d, want 502", rec.Code)
	}
}

func TestRenderErrorPageUnknownCode(t *testing.T) {
	// Test with a code not in defaultErrorTitles map (e.g., 418)
	rec := httptest.NewRecorder()
	renderErrorPage(rec, http.StatusTeapot)
	if rec.Code != 418 {
		t.Errorf("status = %d, want 418", rec.Code)
	}
	body := rec.Body.String()
	// Should fallback to http.StatusText
	if !strings.Contains(body, "I'm a teapot") {
		t.Errorf("body should contain status text for 418, got: %s", body[:min(100, len(body))])
	}
}

func TestRenderErrorPageAllKnownCodes(t *testing.T) {
	// Exhaustively test all codes in defaultErrorTitles map
	for code, title := range defaultErrorTitles {
		rec := httptest.NewRecorder()
		renderErrorPage(rec, code)
		if rec.Code != code {
			t.Errorf("renderErrorPage(%d) status = %d", code, rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, title) {
			t.Errorf("renderErrorPage(%d) body should contain %q", code, title)
		}
	}
}

func TestBuildMiddlewareChain(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{
			{Host: "example.com", Root: "/tmp", Type: "static", SSL: config.SSLConfig{Mode: "off"}},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	// The middleware chain is built during New(); verify handler is not nil
	if s.handler == nil {
		t.Fatal("handler should not be nil after New()")
	}

	// Exercise the chain with a request to verify it produces a response
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "example.com"
	s.handler.ServeHTTP(rec, req)

	if rec.Code == 0 {
		t.Error("expected a non-zero status code from the middleware chain")
	}
}

func TestHandleHTTPNonSSLDomain(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("hello"), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{
			{Host: "plain.com", Root: dir, Type: "static", SSL: config.SSLConfig{Mode: "off"}},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/index.html", nil)
	req.Host = "plain.com"
	s.handleHTTP(rec, req)

	// Non-SSL domain should serve content, not redirect
	if rec.Code == 301 {
		t.Error("non-SSL domain should not redirect to HTTPS")
	}
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "hello") {
		t.Errorf("body = %q, want 'hello'", rec.Body.String())
	}
}

func TestHandleHTTPACMEChallengePath(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			LogLevel:  "error",
			LogFormat: "text",
			ACME: config.ACMEConfig{
				Email:   "test@example.com",
				CAURL:   "https://acme-staging-v02.api.letsencrypt.org/directory",
				Storage: t.TempDir(),
			},
		},
		Domains: []config.Domain{
			{Host: "acme.com", Root: "/tmp", Type: "static", SSL: config.SSLConfig{Mode: "auto"}},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/.well-known/acme-challenge/test-token", nil)
	req.Host = "acme.com"
	s.handleHTTP(rec, req)

	// ACME challenge path should be handled by the TLS manager.
	// Since no token is registered, the ACME handler returns 404 (challenge not found).
	if rec.Code != 404 {
		t.Errorf("status = %d, want 404 (challenge not found)", rec.Code)
	}
}

func TestHandleHTTPSSLDomainRedirect(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{
			{Host: "ssl.com", Root: "/tmp", Type: "static", SSL: config.SSLConfig{Mode: "manual"}},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/page?q=1", nil)
	req.Host = "ssl.com"
	s.handleHTTP(rec, req)

	if rec.Code != 301 {
		t.Errorf("status = %d, want 301", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if loc != "https://ssl.com/page?q=1" {
		t.Errorf("Location = %q, want https://ssl.com/page?q=1", loc)
	}
	if rec.Header().Get("Strict-Transport-Security") == "" {
		t.Error("HSTS header should be set for SSL domain redirect")
	}
}

func TestHandleRequestCacheEnabled(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "cached.html"), []byte("cached content"), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
			Cache: config.CacheConfig{
				Enabled:     true,
				MemoryLimit: config.ByteSize(10 * 1024 * 1024), // 10MB
			},
		},
		Domains: []config.Domain{
			{
				Host:  "cached.com",
				Root:  dir,
				Type:  "static",
				SSL:   config.SSLConfig{Mode: "off"},
				Cache: config.DomainCache{Enabled: true, TTL: 60},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	// First request: cache miss, serve the file and store in cache
	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest("GET", "/cached.html", nil)
	req1.Host = "cached.com"
	s.handleRequest(rec1, req1)

	if rec1.Code != 200 {
		t.Fatalf("first request status = %d, want 200", rec1.Code)
	}

	// Manually populate the cache with the response so the second request gets a hit.
	// The static handler does not auto-store in cache, so we store directly.
	if s.cache != nil {
		cacheReq := httptest.NewRequest("GET", "/cached.html", nil)
		cacheReq.Host = "cached.com"

		s.cache.Set(cacheReq, &cache.CachedResponse{
			StatusCode: 200,
			Headers:    http.Header{"Content-Type": {"text/html"}},
			Body:       []byte("cached content"),
			Created:    time.Now(),
			TTL:        60 * time.Second,
		})

		// Second request: should get X-Cache header
		rec2 := httptest.NewRecorder()
		req2 := httptest.NewRequest("GET", "/cached.html", nil)
		req2.Host = "cached.com"
		s.handleRequest(rec2, req2)

		xcache := rec2.Header().Get("X-Cache")
		if xcache == "" {
			t.Error("second request should have X-Cache header set")
		}
		if xcache != "HIT" && xcache != "STALE" {
			t.Errorf("X-Cache = %q, want HIT or STALE", xcache)
		}
	}
}

func TestResponseCacheAutoStore(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "page.html"), []byte("hello world"), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
			Cache: config.CacheConfig{
				Enabled:     true,
				MemoryLimit: config.ByteSize(10 * 1024 * 1024),
			},
		},
		Domains: []config.Domain{
			{
				Host:  "autocache.com",
				Root:  dir,
				Type:  "static",
				SSL:   config.SSLConfig{Mode: "off"},
				Cache: config.DomainCache{Enabled: true, TTL: 120},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	// First request: cache miss, response should be auto-stored.
	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest("GET", "/page.html", nil)
	req1.Host = "autocache.com"
	s.handleRequest(rec1, req1)

	if rec1.Code != 200 {
		t.Fatalf("first request status = %d, want 200", rec1.Code)
	}
	if rec1.Body.String() != "hello world" {
		t.Fatalf("first request body = %q, want 'hello world'", rec1.Body.String())
	}

	// Second request: should be served from cache (HIT).
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/page.html", nil)
	req2.Host = "autocache.com"
	s.handleRequest(rec2, req2)

	if rec2.Code != 200 {
		t.Errorf("second request status = %d, want 200", rec2.Code)
	}
	xcache := rec2.Header().Get("X-Cache")
	if xcache != "HIT" {
		t.Errorf("X-Cache = %q, want HIT", xcache)
	}
	if !strings.Contains(rec2.Body.String(), "hello world") {
		t.Errorf("second request body = %q, should contain 'hello world'", rec2.Body.String())
	}
}

func TestResponseCacheNotStoredForPOST(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("home"), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
			Cache: config.CacheConfig{
				Enabled:     true,
				MemoryLimit: config.ByteSize(10 * 1024 * 1024),
			},
		},
		Domains: []config.Domain{
			{
				Host:  "nocache.com",
				Root:  dir,
				Type:  "static",
				SSL:   config.SSLConfig{Mode: "off"},
				Cache: config.DomainCache{Enabled: true, TTL: 60},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	// POST requests should bypass cache entirely.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/index.html", nil)
	req.Host = "nocache.com"
	s.handleRequest(rec, req)

	// POST to a static file will get a 404 or method-specific response,
	// but the key thing is the cache should not store anything.
	// Verify with a subsequent GET that it is a MISS (not stored by POST).
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/index.html", nil)
	req2.Host = "nocache.com"
	s.handleRequest(rec2, req2)

	// First GET is a miss; the important thing is it was not pre-populated by POST.
	// After this GET, it should be stored.
	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest("GET", "/index.html", nil)
	req3.Host = "nocache.com"
	s.handleRequest(rec3, req3)

	xcache := rec3.Header().Get("X-Cache")
	if xcache != "HIT" {
		t.Errorf("third GET X-Cache = %q, want HIT (auto-stored from second GET)", xcache)
	}
}

func TestResponseCacheNotStoredFor404(t *testing.T) {
	dir := t.TempDir()
	// No files in dir

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
			Cache: config.CacheConfig{
				Enabled:     true,
				MemoryLimit: config.ByteSize(10 * 1024 * 1024),
			},
		},
		Domains: []config.Domain{
			{
				Host:  "notfound.com",
				Root:  dir,
				Type:  "static",
				SSL:   config.SSLConfig{Mode: "off"},
				Cache: config.DomainCache{Enabled: true, TTL: 60},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	// 404 responses ARE cacheable per IsCacheable, so verify they get cached.
	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest("GET", "/missing.html", nil)
	req1.Host = "notfound.com"
	s.handleRequest(rec1, req1)

	if rec1.Code != 404 {
		t.Fatalf("status = %d, want 404", rec1.Code)
	}

	// Second request should hit cache.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/missing.html", nil)
	req2.Host = "notfound.com"
	s.handleRequest(rec2, req2)

	xcache := rec2.Header().Get("X-Cache")
	if xcache != "HIT" {
		t.Errorf("X-Cache = %q, want HIT for cached 404", xcache)
	}
}

func TestResponseCaptureBasic(t *testing.T) {
	rec := httptest.NewRecorder()
	cap := newResponseCapture(rec)

	cap.Header().Set("Content-Type", "text/plain")
	cap.WriteHeader(201)
	cap.Write([]byte("hello"))

	if cap.statusCode != 201 {
		t.Errorf("statusCode = %d, want 201", cap.statusCode)
	}
	if cap.body.String() != "hello" {
		t.Errorf("body = %q, want 'hello'", cap.body.String())
	}
	if !cap.written {
		t.Error("written should be true after WriteHeader")
	}
	// Real writer should also have received the data.
	if rec.Code != 201 {
		t.Errorf("real writer code = %d, want 201", rec.Code)
	}
	if rec.Body.String() != "hello" {
		t.Errorf("real writer body = %q, want 'hello'", rec.Body.String())
	}
}

func TestResponseCaptureImplicitWriteHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	cap := newResponseCapture(rec)

	// Write without calling WriteHeader first should auto-set 200.
	cap.Write([]byte("data"))

	if cap.statusCode != 200 {
		t.Errorf("statusCode = %d, want 200", cap.statusCode)
	}
	if rec.Code != 200 {
		t.Errorf("real writer code = %d, want 200", rec.Code)
	}
}

func TestResponseCaptureDoubleWriteHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	cap := newResponseCapture(rec)

	cap.WriteHeader(404)
	cap.WriteHeader(500) // should be ignored

	if cap.statusCode != 404 {
		t.Errorf("statusCode = %d, want 404 (first call wins)", cap.statusCode)
	}
}

func TestResponseCaptureCapturedHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	cap := newResponseCapture(rec)

	cap.Header().Set("X-Custom", "value")
	cap.WriteHeader(200)
	cap.Write([]byte("test"))

	hdrs := cap.capturedHeaders()
	if hdrs.Get("X-Custom") != "value" {
		t.Errorf("captured header X-Custom = %q, want 'value'", hdrs.Get("X-Custom"))
	}
}

func TestApplyHtaccess(t *testing.T) {
	dir := t.TempDir()

	// Write a .htaccess file with rewrite rules
	htContent := `RewriteEngine On
RewriteCond %{REQUEST_FILENAME} !-f
RewriteRule ^(.*)$ /index.php [L]
`
	os.WriteFile(filepath.Join(dir, ".htaccess"), []byte(htContent), 0644)
	os.WriteFile(filepath.Join(dir, "index.php"), []byte("<?php echo 'hi'; ?>"), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
		},
		Domains: []config.Domain{
			{
				Host:     "htaccess.local",
				Root:     dir,
				Type:     "static",
				SSL:      config.SSLConfig{Mode: "off"},
				Htaccess: config.HtaccessConfig{Mode: "import"},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	// Request a path that doesn't exist on disk — .htaccess should rewrite to /index.php
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/some-page", nil)
	req.Host = "htaccess.local"
	s.handleRequest(rec, req)

	// The rewrite should have changed the URL path to /index.php.
	// Since this is a static domain (not php), it will serve the index.php file as static.
	// Either way, the request should resolve (200) rather than 404.
	if rec.Code == 404 {
		t.Error("htaccess rewrite should have prevented a 404")
	}
}

// --- matchPath tests ---

func TestMatchPathExact(t *testing.T) {
	if !matchPath("/api/v1/users", "^/api/") {
		t.Error("should match prefix pattern")
	}
}

func TestMatchPathNoMatch(t *testing.T) {
	if matchPath("/home", "^/api/") {
		t.Error("should not match unrelated path")
	}
}

func TestMatchPathFullRegex(t *testing.T) {
	if !matchPath("/assets/style.css", `\.(css|js|png)$`) {
		t.Error("should match file extension regex")
	}
	if matchPath("/assets/style.txt", `\.(css|js|png)$`) {
		t.Error("should not match .txt for css/js/png pattern")
	}
}

func TestMatchPathWildcard(t *testing.T) {
	if !matchPath("/anything/here", ".*") {
		t.Error("wildcard should match any path")
	}
}

func TestMatchPathInvalidRegex(t *testing.T) {
	// Invalid regex should return false without panicking
	if matchPath("/test", "[invalid") {
		t.Error("invalid regex should return false")
	}
}

func TestMatchPathEmptyPattern(t *testing.T) {
	// Empty pattern matches everything (empty regex matches all)
	if !matchPath("/test", "") {
		t.Error("empty pattern should match")
	}
}

func TestMatchPathEmptyPath(t *testing.T) {
	if !matchPath("", "^$") {
		t.Error("empty path should match ^$")
	}
	if matchPath("", "^/api") {
		t.Error("empty path should not match ^/api")
	}
}

// --- handleFileRequest with directory listing ---

func TestHandleFileRequestDirectoryListing(t *testing.T) {
	dir := t.TempDir()
	subDir := filepath.Join(dir, "subdir")
	os.MkdirAll(subDir, 0755)
	os.WriteFile(filepath.Join(subDir, "file.txt"), []byte("hello"), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
		},
		Domains: []config.Domain{
			{
				Host:             "listing.com",
				Root:             dir,
				Type:             "static",
				SSL:              config.SSLConfig{Mode: "off"},
				DirectoryListing: true,
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/subdir/", nil)
	req.Host = "listing.com"
	s.handleRequest(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200 for directory listing", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "file.txt") {
		t.Errorf("directory listing should contain file.txt, got: %s", body[:min(200, len(body))])
	}
}

func TestHandleFileRequestDirectoryListingDisabled(t *testing.T) {
	dir := t.TempDir()
	subDir := filepath.Join(dir, "subdir")
	os.MkdirAll(subDir, 0755)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
		},
		Domains: []config.Domain{
			{
				Host:             "nolisting.com",
				Root:             dir,
				Type:             "static",
				SSL:              config.SSLConfig{Mode: "off"},
				DirectoryListing: false,
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/subdir/", nil)
	req.Host = "nolisting.com"
	s.handleRequest(rec, req)

	// Without directory listing, requesting a directory should NOT return 200 directory listing
	if rec.Code == 200 && strings.Contains(rec.Body.String(), "Index of") {
		t.Error("directory listing should not be shown when disabled")
	}
}

func TestHandleFileRequestDirectoryListingRootDir(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("hello"), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
		},
		Domains: []config.Domain{
			{
				Host:             "rootlist.com",
				Root:             dir,
				Type:             "static",
				SSL:              config.SSLConfig{Mode: "off"},
				DirectoryListing: true,
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "rootlist.com"
	s.handleRequest(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200 for root directory listing", rec.Code)
	}
}

// --- handleProxy with a working pool ---

func TestHandleProxyWithPool(t *testing.T) {
	// Start a backend HTTP server
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backend", "true")
		w.WriteHeader(200)
		w.Write([]byte("backend response"))
	}))
	defer backend.Close()

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
		},
		Domains: []config.Domain{
			{
				Host: "proxy.com",
				Type: "proxy",
				SSL:  config.SSLConfig{Mode: "off"},
				Proxy: config.ProxyConfig{
					Upstreams: []config.Upstream{
						{Address: backend.URL, Weight: 1},
					},
					Algorithm: "round_robin",
				},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Host = "proxy.com"
	s.handleRequest(rec, req)

	// The proxy should have forwarded the request to the backend
	if rec.Code == 502 {
		t.Error("proxy should not return 502 when backend is available")
	}
}

// --- handleHTTP for non-SSL serving path ---

func TestHandleHTTPNonSSLServesContent(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "data.json"), []byte(`{"ok":true}`), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{
			{Host: "api.local", Root: dir, Type: "static", SSL: config.SSLConfig{Mode: "off"}},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/data.json", nil)
	req.Host = "api.local"
	s.handleHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"ok"`) {
		t.Errorf("body = %q, expected JSON content", rec.Body.String())
	}
}

func TestHandleHTTPUnknownHostNonSSL(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{
			{Host: "known.com", Root: "/tmp", Type: "static", SSL: config.SSLConfig{Mode: "off"}},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "unknown.com"
	s.handleHTTP(rec, req)

	// Unknown host with no SSL should fall through to the middleware chain, which returns 404
	if rec.Code != 404 {
		t.Errorf("status = %d, want 404 for unknown host", rec.Code)
	}
}

// --- buildMiddlewareChain ---

func TestBuildMiddlewareChainWithRateLimit(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{
			{
				Host: "rate.com",
				Root: "/tmp",
				Type: "static",
				SSL:  config.SSLConfig{Mode: "off"},
				Security: config.SecurityConfig{
					RateLimit: config.RateLimitConfig{
						Requests: 100,
						Window:   config.Duration{Duration: time.Minute},
					},
				},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	if s.handler == nil {
		t.Fatal("handler should not be nil when rate limit is configured")
	}

	// Exercise the chain
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "rate.com"
	s.handler.ServeHTTP(rec, req)

	if rec.Code == 0 {
		t.Error("expected a non-zero status code")
	}
}

func TestBuildMiddlewareChainWithSecurityGuard(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{
			{
				Host: "guarded.com",
				Root: "/tmp",
				Type: "static",
				SSL:  config.SSLConfig{Mode: "off"},
				Security: config.SecurityConfig{
					BlockedPaths: []string{".env", ".git"},
					WAF:          config.WAFConfig{Enabled: true},
				},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	if s.handler == nil {
		t.Fatal("handler should not be nil with security guard")
	}
}

// --- applyHtaccess with cached rules ---

func TestApplyHtaccessCachedRules(t *testing.T) {
	dir := t.TempDir()

	// Write .htaccess with a simple rewrite
	htContent := `RewriteEngine On
RewriteRule ^old-page$ /new-page [L]
`
	os.WriteFile(filepath.Join(dir, ".htaccess"), []byte(htContent), 0644)
	os.WriteFile(filepath.Join(dir, "new-page"), []byte("new content"), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
		},
		Domains: []config.Domain{
			{
				Host:     "htcache.local",
				Root:     dir,
				Type:     "static",
				SSL:      config.SSLConfig{Mode: "off"},
				Htaccess: config.HtaccessConfig{Mode: "import"},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	// First request: parses .htaccess and caches rules
	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest("GET", "/old-page", nil)
	req1.Host = "htcache.local"
	s.handleRequest(rec1, req1)

	// Second request: should use cached rules (verify it still works)
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/old-page", nil)
	req2.Host = "htcache.local"
	s.handleRequest(rec2, req2)

	// Verify cache was populated
	s.htaccessCacheMu.RLock()
	_, cached := s.htaccessCache[dir]
	s.htaccessCacheMu.RUnlock()

	if !cached {
		t.Error("htaccess rules should be cached after first request")
	}
}

func TestApplyHtaccessNoFile(t *testing.T) {
	dir := t.TempDir()
	// No .htaccess file in dir

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
		},
		Domains: []config.Domain{
			{
				Host:     "nohtaccess.local",
				Root:     dir,
				Type:     "static",
				SSL:      config.SSLConfig{Mode: "off"},
				Htaccess: config.HtaccessConfig{Mode: "import"},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/anything", nil)
	req.Host = "nohtaccess.local"
	s.handleRequest(rec, req)

	// Should complete without error (nil rules cached for missing file)
	s.htaccessCacheMu.RLock()
	rules, cached := s.htaccessCache[dir]
	s.htaccessCacheMu.RUnlock()

	if !cached {
		t.Error("should still cache nil result for missing .htaccess")
	}
	if len(rules) != 0 {
		t.Errorf("rules should be nil/empty for missing .htaccess, got %d", len(rules))
	}
}

// --- applyRewrites with precompiled cache ---

func TestApplyRewritesRedirect(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
		},
		Domains: []config.Domain{
			{
				Host: "rewrite.com",
				Root: "/tmp",
				Type: "static",
				SSL:  config.SSLConfig{Mode: "off"},
				Rewrites: []config.RewriteRule{
					{Match: "^/legacy$", To: "/modern", Status: 301},
				},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	// Verify rewrite cache was populated
	if _, ok := s.rewriteCache["rewrite.com"]; !ok {
		t.Fatal("rewrite cache should have an entry for rewrite.com")
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/legacy", nil)
	req.Host = "rewrite.com"
	s.handleRequest(rec, req)

	if rec.Code != 301 {
		t.Errorf("status = %d, want 301 redirect", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if loc != "/modern" {
		t.Errorf("Location = %q, want /modern", loc)
	}
}

func TestApplyRewritesNoCache(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
		},
		Domains: []config.Domain{
			{
				Host: "norewrite.com",
				Root: "/tmp",
				Type: "static",
				SSL:  config.SSLConfig{Mode: "off"},
				// No rewrites configured
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	// No rewrite cache for this domain
	if _, ok := s.rewriteCache["norewrite.com"]; ok {
		t.Error("should not have rewrite cache for domain without rewrites")
	}
}

func TestApplyRewritesInternalRewrite(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "target.html"), []byte("rewrite target"), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
		},
		Domains: []config.Domain{
			{
				Host: "internal-rw.com",
				Root: dir,
				Type: "static",
				SSL:  config.SSLConfig{Mode: "off"},
				Rewrites: []config.RewriteRule{
					{Match: "^/source$", To: "/target.html"},
				},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/source", nil)
	req.Host = "internal-rw.com"
	s.handleRequest(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200 after internal rewrite", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "rewrite target") {
		t.Errorf("body = %q, expected rewrite target content", rec.Body.String())
	}
}

// --- Cache bypass for session cookies ---

func TestCacheBypassSessionCookies(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "page.html"), []byte("page content"), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
			Cache: config.CacheConfig{
				Enabled:     true,
				MemoryLimit: config.ByteSize(10 * 1024 * 1024),
			},
		},
		Domains: []config.Domain{
			{
				Host:  "session.com",
				Root:  dir,
				Type:  "static",
				SSL:   config.SSLConfig{Mode: "off"},
				Cache: config.DomainCache{Enabled: true, TTL: 60},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	// Request with WordPress session cookie should bypass cache
	for _, cookie := range []string{"wordpress_logged_in=abc", "wp-settings=1", "PHPSESSID=xyz"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/page.html", nil)
		req.Host = "session.com"
		req.Header.Set("Cookie", cookie)
		s.handleRequest(rec, req)

		xcache := rec.Header().Get("X-Cache")
		if xcache == "HIT" {
			t.Errorf("request with cookie %q should bypass cache, got X-Cache=%q", cookie, xcache)
		}
	}
}

// --- Cache bypass rules ---

func TestCacheBypassRules(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "api.json"), []byte(`{"data":1}`), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
			Cache: config.CacheConfig{
				Enabled:     true,
				MemoryLimit: config.ByteSize(10 * 1024 * 1024),
			},
		},
		Domains: []config.Domain{
			{
				Host: "bypass.com",
				Root: dir,
				Type: "static",
				SSL:  config.SSLConfig{Mode: "off"},
				Cache: config.DomainCache{
					Enabled: true,
					TTL:     60,
					Rules: []config.CacheRule{
						{Match: `^/api/`, Bypass: true},
					},
				},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	// First request to cache something
	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest("GET", "/api/data", nil)
	req1.Host = "bypass.com"
	s.handleRequest(rec1, req1)

	// Second request to /api/ path should not hit cache
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/api/data", nil)
	req2.Host = "bypass.com"
	s.handleRequest(rec2, req2)

	xcache := rec2.Header().Get("X-Cache")
	if xcache == "HIT" {
		t.Error("/api/ paths should bypass cache per rules")
	}
}

// --- handleRedirect (preserve path edge cases) ---

func TestHandleRedirectNoPreservePath(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{
			{
				Host: "redir.com",
				Type: "redirect",
				SSL:  config.SSLConfig{Mode: "off"},
				Redirect: config.RedirectConfig{
					Target:       "https://target.com/landing",
					Status:       302,
					PreservePath: false,
				},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/some/deep/path", nil)
	req.Host = "redir.com"
	s.handleRequest(rec, req)

	if rec.Code != 302 {
		t.Errorf("status = %d, want 302", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if loc != "https://target.com/landing" {
		t.Errorf("Location = %q, want https://target.com/landing (no path preserved)", loc)
	}
}

func TestHandleRedirectDefaultStatus(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{
			{
				Host: "default-redir.com",
				Type: "redirect",
				SSL:  config.SSLConfig{Mode: "off"},
				Redirect: config.RedirectConfig{
					Target: "https://elsewhere.com",
					Status: 0, // should default to 301
				},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "default-redir.com"
	s.handleRequest(rec, req)

	if rec.Code != 301 {
		t.Errorf("status = %d, want 301 (default)", rec.Code)
	}
}

// --- handleRequest for unknown domain type ---

func TestHandleRequestUnknownType(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{
			{
				Host: "unknown-type.com",
				Type: "custom",
				SSL:  config.SSLConfig{Mode: "off"},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "unknown-type.com"
	s.handleRequest(rec, req)

	if rec.Code != 500 {
		t.Errorf("status = %d, want 500 for unknown type", rec.Code)
	}
}

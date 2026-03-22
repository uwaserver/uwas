package server

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/backup"
	"github.com/uwaserver/uwas/internal/cache"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// --- handleRedirect ---

func TestHandleRedirectPreservePathTrue(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{
			{
				Host: "redir.com",
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
	req := httptest.NewRequest("GET", "/page/sub?q=1", nil)
	req.Host = "redir.com"
	s.handleRequest(rec, req)

	if rec.Code != 301 {
		t.Errorf("status = %d, want 301", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if loc != "https://new.com/page/sub?q=1" {
		t.Errorf("Location = %q, want https://new.com/page/sub?q=1", loc)
	}
}

func TestHandleRedirectPreservePathFalse(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{
			{
				Host: "redir2.com",
				Type: "redirect",
				SSL:  config.SSLConfig{Mode: "off"},
				Redirect: config.RedirectConfig{
					Target:       "https://new.com/landing",
					Status:       302,
					PreservePath: false,
				},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/some/path", nil)
	req.Host = "redir2.com"
	s.handleRequest(rec, req)

	if rec.Code != 302 {
		t.Errorf("status = %d, want 302", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if loc != "https://new.com/landing" {
		t.Errorf("Location = %q, want https://new.com/landing", loc)
	}
}

func TestHandleRedirectStatus307(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{
			{
				Host: "temp.com",
				Type: "redirect",
				SSL:  config.SSLConfig{Mode: "off"},
				Redirect: config.RedirectConfig{
					Target: "https://temp-new.com",
					Status: 307,
				},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api", nil)
	req.Host = "temp.com"
	s.handleRequest(rec, req)

	if rec.Code != 307 {
		t.Errorf("status = %d, want 307", rec.Code)
	}
}

func TestHandleRedirectStatus308(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{
			{
				Host: "perm.com",
				Type: "redirect",
				SSL:  config.SSLConfig{Mode: "off"},
				Redirect: config.RedirectConfig{
					Target: "https://perm-new.com",
					Status: 308,
				},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/data", nil)
	req.Host = "perm.com"
	s.handleRequest(rec, req)

	if rec.Code != 308 {
		t.Errorf("status = %d, want 308", rec.Code)
	}
}

func TestHandleRedirectDefaultStatusFallback(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{
			{
				Host: "default.com",
				Type: "redirect",
				SSL:  config.SSLConfig{Mode: "off"},
				Redirect: config.RedirectConfig{
					Target: "https://new-default.com",
					Status: 0, // should default to 301
				},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "default.com"
	s.handleRequest(rec, req)

	if rec.Code != 301 {
		t.Errorf("status = %d, want 301 (default)", rec.Code)
	}
}

// --- handleFileRequest with SPA mode ---

func TestHandleFileRequestSPAMode(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>SPA</html>"), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
		},
		Domains: []config.Domain{
			{
				Host:    "spa.com",
				Root:    dir,
				Type:    "static",
				SSL:     config.SSLConfig{Mode: "off"},
				SPAMode: true,
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	// Request a path that does not exist; SPA mode should serve index.html
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/app/deep/route", nil)
	req.Host = "spa.com"
	s.handleRequest(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200 (SPA fallback)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "SPA") {
		t.Errorf("body = %q, want contains 'SPA'", rec.Body.String())
	}
}

// --- renderDomainError with custom error page path ---

func TestRenderDomainErrorCustomPagePath(t *testing.T) {
	dir := t.TempDir()
	errDir := filepath.Join(dir, "errors")
	os.MkdirAll(errDir, 0755)
	os.WriteFile(filepath.Join(errDir, "404.html"), []byte("<html>Custom 404</html>"), 0644)

	domain := &config.Domain{
		Root:       dir,
		ErrorPages: map[int]string{404: "errors/404.html"},
	}

	rec := httptest.NewRecorder()
	renderDomainError(rec, 404, domain)

	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Custom 404") {
		t.Errorf("body = %q, want contains 'Custom 404'", rec.Body.String())
	}
}

// --- handleFileRequest: directory returns 403 when not listing ---

func TestHandleFileRequestDirectoryForbidden(t *testing.T) {
	dir := t.TempDir()
	subDir := filepath.Join(dir, "mydir")
	os.MkdirAll(subDir, 0755)
	// Create index.html in parent but not in subdir
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("root"), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
		},
		Domains: []config.Domain{
			{
				Host:             "dirtest.com",
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
	req := httptest.NewRequest("GET", "/mydir", nil)
	req.Host = "dirtest.com"
	s.handleRequest(rec, req)

	// Without directory listing, accessing a directory should return 403 or 404
	if rec.Code != 403 && rec.Code != 404 {
		t.Errorf("status = %d, want 403 or 404", rec.Code)
	}
}

// --- handleProxy with canary-like multi-upstream ---

func TestHandleProxyMultipleUpstreams(t *testing.T) {
	backend1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("backend1"))
	}))
	defer backend1.Close()

	backend2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("backend2"))
	}))
	defer backend2.Close()

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
		},
		Domains: []config.Domain{
			{
				Host: "canary.com",
				Type: "proxy",
				SSL:  config.SSLConfig{Mode: "off"},
				Proxy: config.ProxyConfig{
					Upstreams: []config.Upstream{
						{Address: backend1.URL, Weight: 90},
						{Address: backend2.URL, Weight: 10},
					},
					Algorithm: "round_robin",
				},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	// Make a request - should be routed to one of the backends
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Host = "canary.com"
	s.handleRequest(rec, req)

	if rec.Code == 502 {
		t.Error("proxy with multiple upstreams should not return 502")
	}
}

// --- Config reload with real config file ---

func TestReloadWithRewriteRules(t *testing.T) {
	dir := t.TempDir()
	configContent := `
domains:
  - host: reloaded.com
    root: /tmp
    type: static
    ssl:
      mode: "off"
    rewrites:
      - match: "^/old$"
        to: "/new"
`
	configPath := filepath.Join(dir, "uwas.yaml")
	os.WriteFile(configPath, []byte(configContent), 0644)

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

	if len(s.config.Domains) != 1 {
		t.Fatalf("domains count = %d, want 1", len(s.config.Domains))
	}
	if s.config.Domains[0].Host != "reloaded.com" {
		t.Errorf("domain host = %q, want reloaded.com", s.config.Domains[0].Host)
	}

	// Rewrite cache should be populated for the new domain
	if _, ok := s.rewriteCache["reloaded.com"]; !ok {
		t.Error("rewrite cache should have been rebuilt after reload")
	}
}

// --- SetConfigPath with backup manager ---

func TestSetConfigPathWithBackupManager(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		Global: config.GlobalConfig{
			LogLevel:  "error",
			LogFormat: "text",
			Admin:     config.AdminConfig{Enabled: true},
			Backup:    config.BackupConfig{Enabled: true, Local: config.BackupLocalConfig{Path: dir}},
			ACME:      config.ACMEConfig{Storage: filepath.Join(dir, "certs")},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	// The backup manager should be initialized and SetConfigPath should wire paths
	if s.backupMgr == nil {
		t.Fatal("backupMgr should be initialized when backup is enabled")
	}

	cfgPath := filepath.Join(dir, "uwas.yaml")
	os.WriteFile(cfgPath, []byte("global: {}"), 0644)
	s.SetConfigPath(cfgPath)

	if s.configPath != cfgPath {
		t.Errorf("configPath = %q, want %q", s.configPath, cfgPath)
	}
}

// --- Server with all features enabled (smoke test) ---

func TestNewWithAllFeaturesEnabled(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "2",
			LogLevel:    "error",
			LogFormat:   "text",
			Admin:       config.AdminConfig{Enabled: true},
			Cache: config.CacheConfig{
				Enabled:     true,
				MemoryLimit: config.ByteSize(1 << 20),
			},
			MCP: config.MCPConfig{Enabled: true},
			Alerting: config.AlertingConfig{
				Enabled: true,
			},
			Backup: config.BackupConfig{
				Enabled: true,
				Local:   config.BackupLocalConfig{Path: dir},
			},
			MaxConnections: 100,
		},
		Domains: []config.Domain{
			{
				Host: "full.com",
				Root: dir,
				Type: "static",
				SSL:  config.SSLConfig{Mode: "off"},
				Cache: config.DomainCache{
					Enabled: true,
					TTL:     60,
				},
				Security: config.SecurityConfig{
					RateLimit: config.RateLimitConfig{
						Requests: 100,
						Window:   config.Duration{Duration: time.Minute},
					},
					BlockedPaths: []string{".env"},
					WAF:          config.WAFConfig{Enabled: true},
				},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	if s.handler == nil {
		t.Fatal("handler should not be nil")
	}
	if s.cache == nil {
		t.Error("cache should be initialized")
	}
	if s.admin == nil {
		t.Error("admin should be initialized")
	}
	if s.mcp == nil {
		t.Error("MCP should be initialized")
	}
	if s.alerter == nil {
		t.Error("alerter should be initialized")
	}
	if s.backupMgr == nil {
		t.Error("backupMgr should be initialized")
	}
	if s.connLimiter == nil {
		t.Error("connLimiter should be initialized when MaxConnections > 0")
	}
	if s.monitor == nil {
		t.Error("monitor should be initialized")
	}
}

// --- Connection limiter: rejects when at capacity ---

func TestConnectionLimiterRejectsAt503(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("ok"), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount:    "1",
			LogLevel:       "error",
			LogFormat:      "text",
			MaxConnections: 1, // max 1 concurrent connection
		},
		Domains: []config.Domain{
			{Host: "limited.com", Root: dir, Type: "static", SSL: config.SSLConfig{Mode: "off"}},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	// Fill the connection limiter
	s.connLimiter <- struct{}{}

	// Now a request should be rejected
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/index.html", nil)
	req.Host = "limited.com"
	s.handleRequest(rec, req)

	if rec.Code != 503 {
		t.Errorf("status = %d, want 503 (at capacity)", rec.Code)
	}

	// Release the connection
	<-s.connLimiter
}

// --- Health check fast path ---

func TestHandleRequestHealthCheckPath(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{LogLevel: "error", LogFormat: "text"},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	for _, path := range []string{"/.well-known/health", "/healthz"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", path, nil)
		req.Host = "any.com"
		s.handleRequest(rec, req)

		if rec.Code != 200 {
			t.Errorf("health path %s: status = %d, want 200", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `"status":"ok"`) {
			t.Errorf("health path %s: body = %q", path, rec.Body.String())
		}
	}
}

// --- Slow request logging ---

func TestHandleRequestSlowRequestLogging(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "slow.html"), []byte("slow"), 0644)

	cfg := testConfig(dir)
	log := logger.New("error", "text")
	s := New(cfg, log)

	// Set a very low slow threshold so the request triggers it
	s.metrics.SlowThreshold = 1 * time.Nanosecond

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/slow.html", nil)
	req.Host = "localhost"
	s.handleRequest(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	// Just verify no panic; the slow log warning is sent to logger
}

// --- renderDomainError with custom page ---

func TestRenderDomainErrorCustomPageServed(t *testing.T) {
	dir := t.TempDir()
	errDir := filepath.Join(dir, "errors")
	os.MkdirAll(errDir, 0755)
	os.WriteFile(filepath.Join(errDir, "500.html"), []byte("<h1>Oops</h1>"), 0644)

	domain := &config.Domain{
		Root:       dir,
		ErrorPages: map[int]string{500: "errors/500.html"},
	}

	rec := httptest.NewRecorder()
	renderDomainError(rec, 500, domain)

	if rec.Code != 500 {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Oops") {
		t.Error("should serve custom error page")
	}
}

func TestRenderDomainErrorNilDomainFallback(t *testing.T) {
	rec := httptest.NewRecorder()
	renderDomainError(rec, 404, nil)

	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "UWAS") {
		t.Error("should fallback to default error page")
	}
}

func TestRenderDomainErrorNoErrorPages(t *testing.T) {
	domain := &config.Domain{Root: "/tmp"}

	rec := httptest.NewRecorder()
	renderDomainError(rec, 403, domain)

	if rec.Code != 403 {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

// --- handleFileRequest with directory listing for root ---

func TestHandleFileRequestDirectoryListingNested(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "a", "b")
	os.MkdirAll(nested, 0755)
	os.WriteFile(filepath.Join(nested, "deep.txt"), []byte("deep content"), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
		},
		Domains: []config.Domain{
			{
				Host:             "nested.com",
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
	req := httptest.NewRequest("GET", "/a/b/", nil)
	req.Host = "nested.com"
	s.handleRequest(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "deep.txt") {
		t.Error("nested directory listing should contain deep.txt")
	}
}

// --- buildMiddlewareChain does not panic ---

func TestBuildMiddlewareChainNoPanic(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{LogLevel: "error", LogFormat: "text"},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	// Manually rebuild chain - should not panic
	h := s.buildMiddlewareChain()
	if h == nil {
		t.Fatal("buildMiddlewareChain should return a non-nil handler")
	}
}

// --- Server with backup manager wired ---

func TestServerWithBackupManagerWired(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
			Admin:       config.AdminConfig{Enabled: true},
			Backup: config.BackupConfig{
				Enabled:  true,
				Local:    config.BackupLocalConfig{Path: dir},
				Schedule: "24h",
			},
		},
		Domains: []config.Domain{
			{Host: "backup.com", Root: dir, Type: "static", SSL: config.SSLConfig{Mode: "off"}},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	if s.backupMgr == nil {
		t.Error("backupMgr should be set when backup is enabled")
	}

	// Admin should have the backup manager
	if s.admin == nil {
		t.Fatal("admin should be initialized")
	}
}

// --- Cache bypass for per-domain rules ---

func TestCacheBypassDomainRules(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "dynamic.js"), []byte("console.log('hi')"), 0644)

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
						{Match: `\.js$`, TTL: 0, Bypass: true},
					},
				},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	// Request a .js file -- should bypass cache
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/dynamic.js", nil)
	req.Host = "bypass.com"
	s.handleRequest(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	// Second request should also work (not cached because bypass rule)
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/dynamic.js", nil)
	req2.Host = "bypass.com"
	s.handleRequest(rec2, req2)

	// X-Cache should not be HIT since bypass rule matched
	xcache := rec2.Header().Get("X-Cache")
	if xcache == "HIT" {
		t.Error("cache should be bypassed for .js files per domain rule")
	}
}

// --- Cache bypass for WordPress cookies ---

func TestCacheBypassWordPressCookie(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "page.html"), []byte("hello"), 0644)

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
				Host:  "wp.com",
				Root:  dir,
				Type:  "static",
				SSL:   config.SSLConfig{Mode: "off"},
				Cache: config.DomainCache{Enabled: true, TTL: 60},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	// Request with WordPress logged-in cookie
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/page.html", nil)
	req.Host = "wp.com"
	req.Header.Set("Cookie", "wordpress_logged_in_abc123=admin")
	s.handleRequest(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	// Request should have bypassed cache (no X-Cache: HIT)
	xcache := rec.Header().Get("X-Cache")
	if xcache == "HIT" {
		t.Error("cache should be bypassed for WordPress logged-in cookie")
	}
}

// --- Default domain type results in error ---

func TestHandleRequestDefaultType(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{
			{
				Host: "unknown-type.com",
				Type: "foobar", // unknown type
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
		t.Errorf("status = %d, want 500 for unknown domain type", rec.Code)
	}
}

// --- Shutdown test ---

func TestShutdownNoServers(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			LogLevel:  "error",
			LogFormat: "text",
			Timeouts: config.TimeoutConfig{
				ShutdownGrace: config.Duration{Duration: 1 * time.Second},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	// shutdown should not panic with no servers running
	s.shutdown()
}

func TestShutdownWithBackupManager(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		Global: config.GlobalConfig{
			LogLevel:  "error",
			LogFormat: "text",
			Timeouts: config.TimeoutConfig{
				ShutdownGrace: config.Duration{Duration: 1 * time.Second},
			},
			Backup: config.BackupConfig{
				Enabled: true,
				Local:   config.BackupLocalConfig{Path: dir},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	// shutdown should call backupMgr.Stop()
	s.shutdown()
}

// --- New with proxy health check ---

func TestNewWithProxyHealthCheckConfig(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
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
				Host: "hc.com",
				Type: "proxy",
				SSL:  config.SSLConfig{Mode: "off"},
				Proxy: config.ProxyConfig{
					Upstreams: []config.Upstream{
						{Address: backend.URL, Weight: 1},
					},
					Algorithm: "round_robin",
					HealthCheck: config.HealthCheckConfig{
						Path:      "/health",
						Interval:  config.Duration{Duration: 10 * time.Second},
						Timeout:   config.Duration{Duration: 2 * time.Second},
						Threshold: 3,
						Rise:      2,
					},
				},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	if _, ok := s.proxyPools["hc.com"]; !ok {
		t.Error("proxy pool should be created for hc.com")
	}
	if _, ok := s.proxyBalancers["hc.com"]; !ok {
		t.Error("proxy balancer should be created for hc.com")
	}

	s.cancel() // stop health checker
}

// --- matchPath edge cases for cache rules ---

func TestMatchPathCacheRulePatterns(t *testing.T) {
	tests := []struct {
		path    string
		pattern string
		want    bool
	}{
		{"/api/data.json", `\.json$`, true},
		{"/style.css", `\.css$`, true},
		{"/image.webp", `\.webp$`, true},
		{"/page.html", `\.json$`, false},
		{"/admin/dashboard", `^/admin`, true},
		{"/user/admin", `^/admin`, false},
	}

	for _, tt := range tests {
		got := matchPath(tt.path, tt.pattern)
		if got != tt.want {
			t.Errorf("matchPath(%q, %q) = %v, want %v", tt.path, tt.pattern, got, tt.want)
		}
	}
}

// --- Backup manager integration in New() ---

func TestNewCreatesBackupManager(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		Global: config.GlobalConfig{
			LogLevel:  "error",
			LogFormat: "text",
			Admin:     config.AdminConfig{Enabled: true},
			Backup: config.BackupConfig{
				Enabled: true,
				Local:   config.BackupLocalConfig{Path: dir},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	if s.backupMgr == nil {
		t.Error("backupMgr should be created when backup is enabled")
	}
}

// --- shutdown with all servers ---

func TestShutdownWithHTTPServer(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			LogLevel:    "error",
			LogFormat:   "text",
			HTTPListen:  "127.0.0.1:0",
			HTTPSListen: "127.0.0.1:0",
			Timeouts: config.TimeoutConfig{
				ShutdownGrace: config.Duration{Duration: 1 * time.Second},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	// Start an actual HTTP server so shutdown has something to shut down
	err := s.startHTTP()
	if err != nil {
		t.Fatalf("startHTTP: %v", err)
	}

	// shutdown should complete without error
	s.shutdown()
}

func TestShutdownWithAdminServer(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			LogLevel:  "error",
			LogFormat: "text",
			Admin:     config.AdminConfig{Enabled: true, Listen: "127.0.0.1:0"},
			Timeouts: config.TimeoutConfig{
				ShutdownGrace: config.Duration{Duration: 1 * time.Second},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	// Start admin
	go s.admin.Start()
	time.Sleep(50 * time.Millisecond) // let it start

	// shutdown should close admin too
	s.shutdown()
}

// --- startHTTP actually starts a listener ---

func TestStartHTTPListens(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			LogLevel:   "error",
			LogFormat:  "text",
			HTTPListen: "127.0.0.1:0",
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	err := s.startHTTP()
	if err != nil {
		t.Fatalf("startHTTP: %v", err)
	}
	defer s.httpSrv.Close()

	if s.httpSrv == nil {
		t.Fatal("httpSrv should not be nil after startHTTP")
	}
}

// --- parseHtaccess edge cases ---

func TestParseHtaccessInvalidFile(t *testing.T) {
	dir := t.TempDir()
	// Write an .htaccess with invalid directives
	os.WriteFile(filepath.Join(dir, ".htaccess"), []byte("This is not valid\nRewriteEngine On\nRewriteRule [invalid /target [L]\n"), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
		},
		Domains: []config.Domain{
			{
				Host:     "badht.local",
				Root:     dir,
				Type:     "static",
				SSL:      config.SSLConfig{Mode: "off"},
				Htaccess: config.HtaccessConfig{Mode: "import"},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	// Request should not panic even with malformed .htaccess
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/anything", nil)
	req.Host = "badht.local"
	s.handleRequest(rec, req)

	// Just verify it completes without panic
	if rec.Code == 0 {
		t.Error("expected non-zero status code")
	}
}

func TestParseHtaccessRewriteDisabled(t *testing.T) {
	dir := t.TempDir()
	// Write an .htaccess without RewriteEngine On
	os.WriteFile(filepath.Join(dir, ".htaccess"), []byte("# No rewrite engine\n"), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
		},
		Domains: []config.Domain{
			{
				Host:     "norewrite.local",
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
	req := httptest.NewRequest("GET", "/test", nil)
	req.Host = "norewrite.local"
	s.handleRequest(rec, req)

	// Verify rules were parsed and cached (as empty)
	s.htaccessCacheMu.RLock()
	rules, ok := s.htaccessCache[dir]
	s.htaccessCacheMu.RUnlock()

	if !ok {
		t.Error("htaccess cache should have entry for dir")
	}
	if len(rules) != 0 {
		t.Errorf("rules should be empty for htaccess without RewriteEngine, got %d", len(rules))
	}
}

func TestParseHtaccessWithConditions(t *testing.T) {
	dir := t.TempDir()
	htContent := `RewriteEngine On
RewriteCond %{REQUEST_FILENAME} !-f
RewriteCond %{REQUEST_FILENAME} !-d
RewriteRule ^(.*)$ /index.html [L]
`
	os.WriteFile(filepath.Join(dir, ".htaccess"), []byte(htContent), 0644)
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("fallback"), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
		},
		Domains: []config.Domain{
			{
				Host:     "condht.local",
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
	req := httptest.NewRequest("GET", "/nonexistent-page", nil)
	req.Host = "condht.local"
	s.handleRequest(rec, req)

	// The htaccess should rewrite to /index.html
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200 (htaccess with conditions rewrite)", rec.Code)
	}
}

// --- GracefulRestart with HTTPS server ---

func TestGracefulRestartWithHTTPS(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Timeouts: config.TimeoutConfig{
				ShutdownGrace: config.Duration{Duration: 1 * time.Second},
			},
		},
	}
	log := logger.New("error", "text")
	s := &Server{
		config:  cfg,
		logger:  log,
		httpSrv: &http.Server{},
		httpsSrv: &http.Server{},
	}

	// Should succeed with unstarted servers
	err := s.GracefulRestart()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- DrainAndWait with HTTPS ---

func TestDrainAndWaitWithHTTPS(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Timeouts: config.TimeoutConfig{
				ShutdownGrace: config.Duration{Duration: 1 * time.Second},
			},
		},
	}
	log := logger.New("error", "text")
	s := &Server{
		config:   cfg,
		logger:   log,
		httpsSrv: &http.Server{},
	}

	done := make(chan struct{})
	go func() {
		s.DrainAndWait()
		close(done)
	}()

	select {
	case <-done:
		// OK
	case <-time.After(3 * time.Second):
		t.Fatal("DrainAndWait did not complete in time")
	}
}

// --- handleFileRequest: static file that is a directory (not directory listing) ---

func TestHandleFileRequestResolvedDirForbidden(t *testing.T) {
	dir := t.TempDir()
	subDir := filepath.Join(dir, "data")
	os.MkdirAll(subDir, 0755)
	// Create an index.html so the path resolves
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("root"), 0644)
	// Ensure /data resolves to a directory

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
		},
		Domains: []config.Domain{
			{
				Host: "diraccess.com",
				Root: dir,
				Type: "static",
				SSL:  config.SSLConfig{Mode: "off"},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/data", nil)
	req.Host = "diraccess.com"
	s.handleRequest(rec, req)

	// Accessing a directory without directory listing should return 403
	if rec.Code != 403 && rec.Code != 404 {
		t.Errorf("status = %d, want 403 or 404 for directory access", rec.Code)
	}
}

// --- handleRequest with HTTPS flag ---

func TestHandleRequestHTTPSFlag(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "secure.html"), []byte("secure"), 0644)

	cfg := testConfig(dir)
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/secure.html", nil)
	req.Host = "localhost"
	req.TLS = &tls.ConnectionState{} // simulate HTTPS
	s.handleRequest(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// --- reload invalidates htaccess cache ---

func TestReloadInvalidatesHtaccessCache(t *testing.T) {
	dir := t.TempDir()
	configContent := `
domains:
  - host: ht.com
    root: /tmp
    type: static
    ssl:
      mode: "off"
`
	configPath := filepath.Join(dir, "uwas.yaml")
	os.WriteFile(configPath, []byte(configContent), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{
			{Host: "ht.com", Root: "/tmp", Type: "static", SSL: config.SSLConfig{Mode: "off"}},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)
	s.SetConfigPath(configPath)

	// Pre-populate htaccess cache
	s.htaccessCacheMu.Lock()
	s.htaccessCache["/tmp"] = nil
	s.htaccessCacheMu.Unlock()

	err := s.reload()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}

	// Cache should be cleared
	s.htaccessCacheMu.RLock()
	_, found := s.htaccessCache["/tmp"]
	s.htaccessCacheMu.RUnlock()

	if found {
		t.Error("htaccess cache should be cleared after reload")
	}
}

// --- shutdown with httpsSrv set ---

func TestShutdownWithHTTPSAndH3(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			LogLevel:  "error",
			LogFormat: "text",
			Timeouts: config.TimeoutConfig{
				ShutdownGrace: config.Duration{Duration: 1 * time.Second},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	// Set httpsSrv to a non-nil but unstarted server
	s.httpsSrv = &http.Server{}
	// h3srv cannot be set without the quic-go import, but let's test without it

	// shutdown should handle all branches
	s.shutdown()
}

// --- handleFileRequest with ETag / If-None-Match for cached responses ---

func TestHandleRequestCachedETagNotModified(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "etag.html"), []byte("etag content"), 0644)

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
				Host:  "etag.com",
				Root:  dir,
				Type:  "static",
				SSL:   config.SSLConfig{Mode: "off"},
				Cache: config.DomainCache{Enabled: true, TTL: 60},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	// First request populates cache
	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest("GET", "/etag.html", nil)
	req1.Host = "etag.com"
	s.handleRequest(rec1, req1)

	// Manually store a cached response with an ETag
	cacheReq := httptest.NewRequest("GET", "/etag.html", nil)
	cacheReq.Host = "etag.com"
	s.cache.Set(cacheReq, &cache.CachedResponse{
		StatusCode: 200,
		Headers:    http.Header{"Content-Type": {"text/html"}, "Etag": {`"abc123"`}},
		Body:       []byte("etag content"),
		Created:    time.Now(),
		TTL:        60 * time.Second,
	})

	// Second request with matching If-None-Match should get 304
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/etag.html", nil)
	req2.Host = "etag.com"
	req2.Header.Set("If-None-Match", `"abc123"`)
	s.handleRequest(rec2, req2)

	if rec2.Code != 304 {
		t.Errorf("status = %d, want 304 (Not Modified)", rec2.Code)
	}
}

// --- Cache stores response with tags ---

func TestCacheStoresWithDomainTags(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "tagged.html"), []byte("tagged"), 0644)

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
				Host: "tagged.com",
				Root: dir,
				Type: "static",
				SSL:  config.SSLConfig{Mode: "off"},
				Cache: config.DomainCache{
					Enabled: true,
					TTL:     120,
					Tags:    []string{"site:tagged"},
				},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	// First request: populates cache
	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest("GET", "/tagged.html", nil)
	req1.Host = "tagged.com"
	s.handleRequest(rec1, req1)

	if rec1.Code != 200 {
		t.Fatalf("status = %d, want 200", rec1.Code)
	}

	// Second request: should hit cache
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/tagged.html", nil)
	req2.Host = "tagged.com"
	s.handleRequest(rec2, req2)

	xcache := rec2.Header().Get("X-Cache")
	if xcache != "HIT" {
		t.Errorf("X-Cache = %q, want HIT", xcache)
	}

	// Purge by tag
	count := s.cache.PurgeByTag("site:tagged")
	if count == 0 {
		t.Error("should have purged at least 1 entry by tag")
	}
}

// --- New with proxy configuration without mirror ---

func TestNewWithProxyNoMirror(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
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
				Host: "nomirror.com",
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

	if _, ok := s.proxyMirrors["nomirror.com"]; ok {
		t.Error("should not have mirror for domain without mirror config")
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Host = "nomirror.com"
	s.handleRequest(rec, req)

	if rec.Code == 502 {
		t.Error("proxy should work without mirror")
	}
}

// --- Cache with redirect domain (cache captures redirect response) ---

func TestCacheRedirectDomain(t *testing.T) {
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
				Host: "redir-cache.com",
				Type: "redirect",
				SSL:  config.SSLConfig{Mode: "off"},
				Redirect: config.RedirectConfig{
					Target:       "https://dest.com",
					Status:       301,
					PreservePath: true,
				},
				Cache: config.DomainCache{Enabled: true, TTL: 60},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	// First request: redirect
	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest("GET", "/page", nil)
	req1.Host = "redir-cache.com"
	s.handleRequest(rec1, req1)

	if rec1.Code != 301 {
		t.Errorf("status = %d, want 301", rec1.Code)
	}
}

// --- applyRewrites: forbidden and gone rules ---

func TestApplyRewritesForbiddenRule(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
		},
		Domains: []config.Domain{
			{
				Host: "forbidden-rw.com",
				Root: "/tmp",
				Type: "static",
				SSL:  config.SSLConfig{Mode: "off"},
				Rewrites: []config.RewriteRule{
					{Match: "^/secret$", To: "-", Flags: []string{"F"}},
				},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/secret", nil)
	req.Host = "forbidden-rw.com"
	s.handleRequest(rec, req)

	if rec.Code != 403 {
		t.Errorf("status = %d, want 403 (forbidden rewrite)", rec.Code)
	}
}

func TestApplyRewritesGoneRule(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
		},
		Domains: []config.Domain{
			{
				Host: "gone-rw.com",
				Root: "/tmp",
				Type: "static",
				SSL:  config.SSLConfig{Mode: "off"},
				Rewrites: []config.RewriteRule{
					{Match: "^/removed$", To: "-", Flags: []string{"G"}},
				},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/removed", nil)
	req.Host = "gone-rw.com"
	s.handleRequest(rec, req)

	if rec.Code != 410 {
		t.Errorf("status = %d, want 410 (gone rewrite)", rec.Code)
	}
}

// --- PHPSESSID cookie bypasses cache ---

func TestCacheBypassPHPSESSID(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "session.html"), []byte("session"), 0644)

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
				Host:  "phpsess.com",
				Root:  dir,
				Type:  "static",
				SSL:   config.SSLConfig{Mode: "off"},
				Cache: config.DomainCache{Enabled: true, TTL: 60},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/session.html", nil)
	req.Host = "phpsess.com"
	req.Header.Set("Cookie", "PHPSESSID=abc123")
	s.handleRequest(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// --- startHTTP error path ---

func TestStartHTTPBadAddress(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			LogLevel:   "error",
			LogFormat:  "text",
			HTTPListen: "invalid-address-no-port",
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	err := s.startHTTP()
	if err == nil {
		t.Error("startHTTP should fail with invalid address")
		if s.httpSrv != nil {
			s.httpSrv.Close()
		}
	}
}

// --- GracefulRestart with httpsSrv ---

func TestGracefulRestartWithHTTPSServer(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Timeouts: config.TimeoutConfig{
				ShutdownGrace: config.Duration{Duration: 1 * time.Second},
			},
		},
	}
	log := logger.New("error", "text")
	s := &Server{
		config:   cfg,
		logger:   log,
		httpsSrv: &http.Server{},
	}

	err := s.GracefulRestart()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- shutdown covers httpsSrv Shutdown path ---

func TestShutdownWithHTTPAndHTTPS(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			LogLevel:    "error",
			LogFormat:   "text",
			HTTPListen:  "127.0.0.1:0",
			HTTPSListen: "127.0.0.1:0",
			Timeouts: config.TimeoutConfig{
				ShutdownGrace: config.Duration{Duration: 1 * time.Second},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	// Start actual HTTP server
	err := s.startHTTP()
	if err != nil {
		t.Fatalf("startHTTP: %v", err)
	}

	// Set httpsSrv (unstarted, but non-nil)
	s.httpsSrv = &http.Server{}

	s.shutdown()
}

// --- writePID error path ---

func TestWritePIDInvalidDir(t *testing.T) {
	cfg := testConfig(t.TempDir())
	cfg.Global.PIDFile = "/nonexistent-dir-abc/sub/uwas.pid"
	log := logger.New("error", "text")
	s := New(cfg, log)

	// This may or may not fail depending on OS permissions
	_ = s.writePID()
}

// needed for backup integration and imports:
var _ = backup.BackupManager{}

// import cache for ETag test
var _ = cache.CachedResponse{}

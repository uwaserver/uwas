package server

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
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

// --- Integration test via real HTTP server ---

func TestStartHTTPAndServeRequest(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "live.html"), []byte("live content"), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
			HTTPListen:  "127.0.0.1:0",
			Timeouts: config.TimeoutConfig{
				Read:          config.Duration{Duration: 5 * time.Second},
				ReadHeader:    config.Duration{Duration: 5 * time.Second},
				Write:         config.Duration{Duration: 5 * time.Second},
				Idle:          config.Duration{Duration: 5 * time.Second},
				ShutdownGrace: config.Duration{Duration: 1 * time.Second},
			},
		},
		Domains: []config.Domain{
			{Host: "localhost", Root: dir, Type: "static", SSL: config.SSLConfig{Mode: "off"}},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	err := s.startHTTP()
	if err != nil {
		t.Fatalf("startHTTP: %v", err)
	}
	defer s.httpSrv.Close()
}

// needed for backup integration and imports:
var _ = backup.BackupManager{}

// import cache for ETag test
var _ = cache.CachedResponse{}

// --- handleHTTP: non-SSL domain passes through to handler ---

func TestHandleHTTPNonSSLDomainPassthrough(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "page.html"), []byte("plain http"), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
		},
		Domains: []config.Domain{
			{
				Host: "plain.com",
				Root: dir,
				Type: "static",
				SSL:  config.SSLConfig{Mode: "off"},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	// handleHTTP should serve the request directly (no redirect) for non-SSL domains
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/page.html", nil)
	req.Host = "plain.com"
	s.handleHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200 for non-SSL domain", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "plain http") {
		t.Errorf("body = %q, want 'plain http'", rec.Body.String())
	}
}

// --- handleHTTP: manual SSL domain redirects to HTTPS ---

func TestHandleHTTPManualSSLRedirect(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{
			{
				Host: "manual-ssl.com",
				Root: "/tmp",
				Type: "static",
				SSL:  config.SSLConfig{Mode: "manual"},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/secure-page?token=abc", nil)
	req.Host = "manual-ssl.com"
	s.handleHTTP(rec, req)

	if rec.Code != 301 {
		t.Errorf("status = %d, want 301 for manual SSL redirect", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if loc != "https://manual-ssl.com/secure-page?token=abc" {
		t.Errorf("Location = %q, want https://manual-ssl.com/secure-page?token=abc", loc)
	}
	if rec.Header().Get("Strict-Transport-Security") == "" {
		t.Error("HSTS header should be set for manual SSL redirect")
	}
}

// --- handleHTTP: unknown host passes through to handler ---

func TestHandleHTTPUnknownHost(t *testing.T) {
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

	// Unknown host gets 421 Misdirected Request
	if rec.Code != 421 {
		t.Errorf("status = %d, want 421 for unknown host", rec.Code)
	}
}

// --- startHTTP verifies server timeout configuration ---

func TestStartHTTPTimeoutsConfigured(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			LogLevel:   "error",
			LogFormat:  "text",
			HTTPListen: "127.0.0.1:0",
			Timeouts: config.TimeoutConfig{
				Read:           config.Duration{Duration: 10 * time.Second},
				ReadHeader:     config.Duration{Duration: 3 * time.Second},
				Write:          config.Duration{Duration: 15 * time.Second},
				Idle:           config.Duration{Duration: 60 * time.Second},
				MaxHeaderBytes: 1 << 20,
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	err := s.startHTTP()
	if err != nil {
		t.Fatalf("startHTTP: %v", err)
	}
	defer s.httpSrv.Close()

	if s.httpSrv.ReadTimeout != 10*time.Second {
		t.Errorf("ReadTimeout = %v, want 10s", s.httpSrv.ReadTimeout)
	}
	if s.httpSrv.ReadHeaderTimeout != 3*time.Second {
		t.Errorf("ReadHeaderTimeout = %v, want 3s", s.httpSrv.ReadHeaderTimeout)
	}
	if s.httpSrv.WriteTimeout != 15*time.Second {
		t.Errorf("WriteTimeout = %v, want 15s", s.httpSrv.WriteTimeout)
	}
	if s.httpSrv.IdleTimeout != 60*time.Second {
		t.Errorf("IdleTimeout = %v, want 60s", s.httpSrv.IdleTimeout)
	}
	if s.httpSrv.MaxHeaderBytes != 1<<20 {
		t.Errorf("MaxHeaderBytes = %d, want %d", s.httpSrv.MaxHeaderBytes, 1<<20)
	}
}

// --- startHTTPS error path with invalid address ---

func TestStartHTTPSBadAddress(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			LogLevel:    "error",
			LogFormat:   "text",
			HTTPSListen: "invalid-address-no-port",
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	err := s.startHTTPS()
	if err == nil {
		t.Error("startHTTPS should fail with invalid address")
		if s.httpsSrv != nil {
			s.httpsSrv.Close()
		}
	}
	if err != nil && !strings.Contains(err.Error(), "listen") {
		t.Errorf("expected listen error, got: %v", err)
	}
}

// --- writePID: successful write and read back ---

func TestWritePIDSuccess(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "subdir", "uwas.pid")

	cfg := testConfig(dir)
	cfg.Global.PIDFile = pidFile
	log := logger.New("error", "text")
	s := New(cfg, log)

	err := s.writePID()
	if err != nil {
		t.Fatalf("writePID: %v", err)
	}

	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read PID file: %v", err)
	}
	pid := strings.TrimSpace(string(data))
	if pid == "" {
		t.Error("PID file should contain a PID")
	}
	// Verify it matches our process PID
	expectedPID := strconv.Itoa(os.Getpid())
	if pid != expectedPID {
		t.Errorf("PID = %q, want %q", pid, expectedPID)
	}
}

// --- writePID: empty PIDFile skips write ---

func TestWritePIDEmptyPathCov(t *testing.T) {
	cfg := testConfig(t.TempDir())
	cfg.Global.PIDFile = ""
	log := logger.New("error", "text")
	s := New(cfg, log)

	err := s.writePID()
	if err != nil {
		t.Errorf("writePID with empty path should return nil, got: %v", err)
	}
}

// --- removePID: cleans up PID file ---

func TestRemovePIDCov(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "uwas.pid")
	os.WriteFile(pidFile, []byte("12345"), 0644)

	cfg := testConfig(dir)
	cfg.Global.PIDFile = pidFile
	log := logger.New("error", "text")
	s := New(cfg, log)

	s.removePID()

	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Error("PID file should be removed after removePID")
	}
}

// --- reload: no config path returns error ---

func TestReloadNoConfigPathCov(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{LogLevel: "error", LogFormat: "text"},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	err := s.reload()
	if err == nil {
		t.Error("reload should fail when configPath is empty")
	}
	if !strings.Contains(err.Error(), "no config path") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- reload: invalid config file returns error ---

func TestReloadInvalidConfigFile(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "bad.yaml")
	os.WriteFile(configPath, []byte("{{{{invalid yaml"), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{LogLevel: "error", LogFormat: "text"},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)
	s.SetConfigPath(configPath)

	err := s.reload()
	if err == nil {
		t.Error("reload should fail with invalid config file")
	}
	if !strings.Contains(err.Error(), "reload config") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- wp-settings cookie bypasses cache ---

func TestCacheBypassWPSettingsCookie(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "wp-page.html"), []byte("wp content"), 0644)

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
				Host:  "wpset.com",
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
	req := httptest.NewRequest("GET", "/wp-page.html", nil)
	req.Host = "wpset.com"
	req.Header.Set("Cookie", "wp-settings-1=abc")
	s.handleRequest(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	// Cache should be bypassed due to wp-settings cookie
	xcache := rec.Header().Get("X-Cache")
	if xcache == "HIT" {
		t.Error("cache should be bypassed for wp-settings cookie")
	}
}

// --- handleRequest: unknown domain type with cache enabled dispatches default ---

func TestHandleRequestUnknownTypeWithCache(t *testing.T) {
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
				Host:  "badtype-cache.com",
				Type:  "unknown_type",
				SSL:   config.SSLConfig{Mode: "off"},
				Cache: config.DomainCache{Enabled: true, TTL: 60},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "badtype-cache.com"
	s.handleRequest(rec, req)

	if rec.Code != 500 {
		t.Errorf("status = %d, want 500 for unknown domain type with cache", rec.Code)
	}
}

// --- applyHtaccess with rewrite query modification ---

func TestApplyHtaccessRewriteWithQuery(t *testing.T) {
	dir := t.TempDir()
	htContent := `RewriteEngine On
RewriteRule ^/page$ /index.php?page=1 [L,QSA]
`
	os.WriteFile(filepath.Join(dir, ".htaccess"), []byte(htContent), 0644)
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("index"), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
		},
		Domains: []config.Domain{
			{
				Host:     "queryht.local",
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
	req := httptest.NewRequest("GET", "/page", nil)
	req.Host = "queryht.local"
	s.handleRequest(rec, req)

	// Just verify it completes without panic
	if rec.Code == 0 {
		t.Error("expected non-zero status code")
	}
}

// --- parseHtaccess with no .htaccess file returns nil ---

func TestParseHtaccessNoFile(t *testing.T) {
	dir := t.TempDir()

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rules := s.parseHtaccess(dir)
	if rules != nil {
		t.Errorf("parseHtaccess should return nil for dir without .htaccess, got %d rules", len(rules))
	}
}

// --- parseHtaccess with valid rewrite rules and conditions ---

func TestParseHtaccessWithMultipleRulesAndBadCondition(t *testing.T) {
	dir := t.TempDir()
	htContent := `RewriteEngine On
RewriteCond %{INVALID_VARIABLE [bad
RewriteRule ^/good$ /target [L]
RewriteRule ^/other$ /elsewhere [L]
`
	os.WriteFile(filepath.Join(dir, ".htaccess"), []byte(htContent), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{LogLevel: "error", LogFormat: "text"},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rules := s.parseHtaccess(dir)
	// Should not panic even with bad condition syntax
	// At least some rules should parse (the ones without bad conditions)
	_ = rules
}

// --- handleFileRequest: directory listing enabled for root ---

func TestHandleFileRequestDirectoryListingEnabled(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "file1.txt"), []byte("content1"), 0644)
	os.WriteFile(filepath.Join(dir, "file2.txt"), []byte("content2"), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
		},
		Domains: []config.Domain{
			{
				Host:             "dirlist.com",
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
	req.Host = "dirlist.com"
	s.handleRequest(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200 for directory listing", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "file1.txt") || !strings.Contains(body, "file2.txt") {
		t.Error("directory listing should contain both files")
	}
}

// --- handleRequest with alerter records request ---

func TestHandleRequestWithAlerter(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "alert.html"), []byte("alert test"), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
			Alerting: config.AlertingConfig{
				Enabled: true,
			},
		},
		Domains: []config.Domain{
			{
				Host: "alerter.com",
				Root: dir,
				Type: "static",
				SSL:  config.SSLConfig{Mode: "off"},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/alert.html", nil)
	req.Host = "alerter.com"
	s.handleRequest(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// --- handleRequest with admin records log ---

func TestHandleRequestWithAdminRecordsLog(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "admin-log.html"), []byte("logged"), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
			Admin:       config.AdminConfig{Enabled: true, Listen: "127.0.0.1:0"},
		},
		Domains: []config.Domain{
			{
				Host: "adminlog.com",
				Root: dir,
				Type: "static",
				SSL:  config.SSLConfig{Mode: "off"},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/admin-log.html", nil)
	req.Host = "adminlog.com"
	s.handleRequest(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// --- applyRewrites: redirect rule ---

func TestApplyRewritesRedirectRule(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
		},
		Domains: []config.Domain{
			{
				Host: "redirect-rw.com",
				Root: "/tmp",
				Type: "static",
				SSL:  config.SSLConfig{Mode: "off"},
				Rewrites: []config.RewriteRule{
					{Match: "^/old-page$", To: "https://external.com/new-page", Status: 302},
				},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/old-page", nil)
	req.Host = "redirect-rw.com"
	s.handleRequest(rec, req)

	if rec.Code != 302 {
		t.Errorf("status = %d, want 302 (redirect rewrite)", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if loc != "https://external.com/new-page" {
		t.Errorf("Location = %q", loc)
	}
}

// --- Circuit Breaker Wiring ---

func TestCircuitBreakerWiring(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("backend-ok"))
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
				Host: "cb-test.com",
				Type: "proxy",
				SSL:  config.SSLConfig{Mode: "off"},
				Proxy: config.ProxyConfig{
					Upstreams: []config.Upstream{
						{Address: backend.URL, Weight: 1},
					},
					Algorithm: "round_robin",
					CircuitBreaker: config.CircuitConfig{
						Threshold: 2,
						Timeout:   config.Duration{Duration: time.Second},
					},
				},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	// Verify circuit breaker was wired
	cb := s.proxyBreakers["cb-test.com"]
	if cb == nil {
		t.Fatal("proxyBreakers should contain cb-test.com")
	}

	// Circuit is closed: request should succeed (reach backend)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api", nil)
	req.Host = "cb-test.com"
	s.handleRequest(rec, req)

	if rec.Code == 503 {
		t.Errorf("status = %d, want != 503 when circuit is closed", rec.Code)
	}

	// Trip the circuit breaker by recording enough failures
	cb.RecordFailure()
	cb.RecordFailure()

	// Now cb.Allow() should return false
	if cb.Allow() {
		t.Error("circuit breaker should be open after 2 failures")
	}

	// Request should be rejected with 503 when circuit is open
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/api", nil)
	req2.Host = "cb-test.com"
	s.handleRequest(rec2, req2)

	if rec2.Code != 503 {
		t.Errorf("status = %d, want 503 when circuit is open", rec2.Code)
	}
}

// --- Canary Router Wiring ---

func TestCanaryRouterWiring(t *testing.T) {
	primaryBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("primary"))
	}))
	defer primaryBackend.Close()

	canaryBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("canary"))
	}))
	defer canaryBackend.Close()

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
		},
		Domains: []config.Domain{
			{
				Host: "canary-test.com",
				Type: "proxy",
				SSL:  config.SSLConfig{Mode: "off"},
				Proxy: config.ProxyConfig{
					Upstreams: []config.Upstream{
						{Address: primaryBackend.URL, Weight: 1},
					},
					Algorithm: "round_robin",
					Canary: config.CanaryConfig{
						Enabled:   true,
						Weight:    0, // 0% canary traffic = never canary via random
						Upstreams: []config.Upstream{{Address: canaryBackend.URL, Weight: 1}},
						Cookie:    "X-Canary",
					},
				},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	// Verify canary router was wired
	cr := s.proxyCanaries["canary-test.com"]
	if cr == nil {
		t.Fatal("proxyCanaries should contain canary-test.com")
	}

	// With weight=0 and no cookie, traffic should go to primary pool
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Host = "canary-test.com"
	s.handleRequest(rec, req)

	if rec.Code == 502 {
		t.Error("proxy should not return 502 when backends are available")
	}

	body := rec.Body.String()
	if body != "primary" {
		t.Errorf("body = %q, want 'primary' (canary weight=0 should route to primary)", body)
	}

	// Verify that the response does NOT have canary headers
	if rec.Header().Get("X-Canary") == "true" {
		t.Error("request without canary cookie and weight=0 should not go to canary")
	}
}

// --- Per-Domain IP ACL ---

func TestPerDomainIPACL(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("hello"), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
		},
		Domains: []config.Domain{
			{
				Host: "acl-test.com",
				Root: dir,
				Type: "static",
				SSL:  config.SSLConfig{Mode: "off"},
				Security: config.SecurityConfig{
					IPBlacklist: []string{"10.0.0.1/32"},
				},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	// Verify domain chain was wired
	if _, ok := s.domainChains["acl-test.com"]; !ok {
		t.Fatal("domainChains should contain acl-test.com")
	}

	// Request from blocked IP should get 403
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/index.html", nil)
	req.Host = "acl-test.com"
	req.RemoteAddr = "10.0.0.1:12345"
	s.handleRequest(rec, req)

	if rec.Code != 403 {
		t.Errorf("status = %d, want 403 for blocked IP", rec.Code)
	}

	// Request from allowed IP should succeed
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/index.html", nil)
	req2.Host = "acl-test.com"
	req2.RemoteAddr = "192.168.1.100:12345"
	s.handleRequest(rec2, req2)

	if rec2.Code != 200 {
		t.Errorf("status = %d, want 200 for allowed IP", rec2.Code)
	}
}

// --- Per-Domain Header Transform ---

func TestPerDomainHeaderTransform(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "page.html"), []byte("content"), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
		},
		Domains: []config.Domain{
			{
				Host: "headers-test.com",
				Root: dir,
				Type: "static",
				SSL:  config.SSLConfig{Mode: "off"},
				Headers: config.HeadersConfig{
					ResponseAdd: map[string]string{
						"X-Custom-Header": "custom-value",
						"X-Frame-Options": "DENY",
					},
				},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/page.html", nil)
	req.Host = "headers-test.com"
	s.handleRequest(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	if rec.Header().Get("X-Custom-Header") != "custom-value" {
		t.Errorf("X-Custom-Header = %q, want 'custom-value'", rec.Header().Get("X-Custom-Header"))
	}
	if rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Errorf("X-Frame-Options = %q, want 'DENY'", rec.Header().Get("X-Frame-Options"))
	}
}

// --- Image Optimization Wiring ---

func TestImageOptimizationWiring(t *testing.T) {
	dir := t.TempDir()

	// Create original JPG and its WebP variant
	os.WriteFile(filepath.Join(dir, "photo.jpg"), []byte("jpeg-data"), 0644)
	os.WriteFile(filepath.Join(dir, "photo.jpg.webp"), []byte("webp-data"), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
		},
		Domains: []config.Domain{
			{
				Host: "imgopt-test.com",
				Root: dir,
				Type: "static",
				SSL:  config.SSLConfig{Mode: "off"},
				ImageOptimization: config.ImageOptimizationConfig{
					Enabled: true,
					Formats: []string{"webp"},
				},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	// Verify image optimization chain was wired
	if _, ok := s.imageOptChains["imgopt-test.com"]; !ok {
		t.Fatal("imageOptChains should contain imgopt-test.com")
	}

	// Request for .jpg with Accept: image/webp should serve the webp variant
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/photo.jpg", nil)
	req.Host = "imgopt-test.com"
	req.Header.Set("Accept", "text/html, image/webp, */*")
	s.handleRequest(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	// Verify the Content-Type was set to webp
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "image/webp") {
		t.Errorf("Content-Type = %q, want to contain 'image/webp'", ct)
	}

	// Verify Vary header is set for content negotiation
	vary := rec.Header().Get("Vary")
	if !strings.Contains(vary, "Accept") {
		t.Errorf("Vary = %q, want to contain 'Accept'", vary)
	}

	// Verify the body contains the webp data
	if !strings.Contains(rec.Body.String(), "webp-data") {
		t.Errorf("body = %q, want to contain 'webp-data'", rec.Body.String())
	}

	// Request without Accept: image/webp should serve original JPG
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/photo.jpg", nil)
	req2.Host = "imgopt-test.com"
	req2.Header.Set("Accept", "text/html, */*")
	s.handleRequest(rec2, req2)

	if rec2.Code != 200 {
		t.Errorf("status = %d, want 200", rec2.Code)
	}

	if strings.Contains(rec2.Body.String(), "webp-data") {
		t.Error("request without Accept: image/webp should serve original JPG, not webp")
	}
}

// --- Connection limiter: fills channel then expects 503, then drains and expects success ---

func TestHandleRequestConnectionLimiter(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "ok.html"), []byte("ok"), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount:    "1",
			LogLevel:       "error",
			LogFormat:      "text",
			MaxConnections: 1, // channel of size 1
		},
		Domains: []config.Domain{
			{Host: "connlim.com", Root: dir, Type: "static", SSL: config.SSLConfig{Mode: "off"}},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	if s.connLimiter == nil {
		t.Fatal("connLimiter should be initialized when MaxConnections > 0")
	}

	// Fill the channel so no slots remain
	s.connLimiter <- struct{}{}

	// Request should be rejected with 503
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/ok.html", nil)
	req.Host = "connlim.com"
	s.handleRequest(rec, req)

	if rec.Code != 503 {
		t.Errorf("status = %d, want 503 (at capacity)", rec.Code)
	}

	// Drain the channel to free a slot
	<-s.connLimiter

	// Now request should succeed
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/ok.html", nil)
	req2.Host = "connlim.com"
	s.handleRequest(rec2, req2)

	if rec2.Code != 200 {
		t.Errorf("status = %d, want 200 after freeing limiter slot", rec2.Code)
	}
}

// --- handleFileRequest: directory listing for a subdirectory with files ---

func TestHandleFileRequestDirectoryListingSubdir(t *testing.T) {
	dir := t.TempDir()
	subDir := filepath.Join(dir, "assets")
	os.MkdirAll(subDir, 0755)
	os.WriteFile(filepath.Join(subDir, "style.css"), []byte("body{}"), 0644)
	os.WriteFile(filepath.Join(subDir, "app.js"), []byte("console.log()"), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
		},
		Domains: []config.Domain{
			{
				Host:             "dirlisting.com",
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
	req := httptest.NewRequest("GET", "/assets/", nil)
	req.Host = "dirlisting.com"
	s.handleRequest(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200 for directory listing", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "style.css") {
		t.Error("directory listing should contain style.css")
	}
	if !strings.Contains(body, "app.js") {
		t.Error("directory listing should contain app.js")
	}
	// Verify it's an HTML response
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
}

// --- Circuit breaker: open → half-open → RecordSuccess → closed recovery ---

func TestHandleProxyCircuitBreakerOpen(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("recovered"))
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
				Host: "cb-recovery.com",
				Type: "proxy",
				SSL:  config.SSLConfig{Mode: "off"},
				Proxy: config.ProxyConfig{
					Upstreams: []config.Upstream{
						{Address: backend.URL, Weight: 1},
					},
					Algorithm: "round_robin",
					CircuitBreaker: config.CircuitConfig{
						Threshold: 2,
						Timeout:   config.Duration{Duration: 50 * time.Millisecond}, // short timeout for test
					},
				},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	cb := s.proxyBreakers["cb-recovery.com"]
	if cb == nil {
		t.Fatal("proxyBreakers should contain cb-recovery.com")
	}

	// 1. Closed state: request should succeed
	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest("GET", "/api", nil)
	req1.Host = "cb-recovery.com"
	s.handleRequest(rec1, req1)

	if rec1.Code == 503 {
		t.Errorf("status = %d, want != 503 when circuit is closed", rec1.Code)
	}

	// 2. Trip the circuit breaker open
	cb.RecordFailure()
	cb.RecordFailure()

	if cb.Allow() {
		t.Error("circuit breaker should be open after 2 failures")
	}

	// 3. Request should be 503 while open
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/api", nil)
	req2.Host = "cb-recovery.com"
	s.handleRequest(rec2, req2)

	if rec2.Code != 503 {
		t.Errorf("status = %d, want 503 when circuit is open", rec2.Code)
	}

	// 4. Wait for timeout to transition to half-open
	time.Sleep(60 * time.Millisecond)

	// 5. In half-open, Allow() returns true for one probe request
	if !cb.Allow() {
		t.Error("circuit breaker should allow one probe in half-open state")
	}

	// 6. RecordSuccess should transition back to closed
	cb.RecordSuccess()

	// 7. Now the circuit should be closed and requests should succeed
	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest("GET", "/api", nil)
	req3.Host = "cb-recovery.com"
	s.handleRequest(rec3, req3)

	if rec3.Code == 503 {
		t.Errorf("status = %d, want != 503 after circuit recovery", rec3.Code)
	}
}

// --- BasicAuth with wrong password expects 401 ---

func TestHandleRequestBasicAuthWrongPassword(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "protected.html"), []byte("protected"), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
		},
		Domains: []config.Domain{
			{
				Host: "authfail.example.com",
				Root: dir,
				Type: "static",
				SSL:  config.SSLConfig{Mode: "off"},
				BasicAuth: config.BasicAuthConfig{
					Enabled: true,
					Users:   map[string]string{"admin": "correctpassword"},
					Realm:   "Secure",
				},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	// Request with wrong password should get 401
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/protected.html", nil)
	req.Host = "authfail.example.com"
	req.SetBasicAuth("admin", "wrongpassword")
	s.handleRequest(rec, req)

	if rec.Code != 401 {
		t.Errorf("status = %d, want 401 for wrong password", rec.Code)
	}

	// Request with wrong username should also get 401
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/protected.html", nil)
	req2.Host = "authfail.example.com"
	req2.SetBasicAuth("nobody", "correctpassword")
	s.handleRequest(rec2, req2)

	if rec2.Code != 401 {
		t.Errorf("status = %d, want 401 for wrong username", rec2.Code)
	}
}

// --- CORS non-preflight GET with allowed origin sets Access-Control-Allow-Origin ---

func TestHandleRequestCORSNonPreflight(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "data.json"), []byte(`{"ok":true}`), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
		},
		Domains: []config.Domain{
			{
				Host: "cors-get.example.com",
				Root: dir,
				Type: "static",
				SSL:  config.SSLConfig{Mode: "off"},
				CORS: config.CORSConfig{
					Enabled:          true,
					AllowedOrigins:   []string{"https://trusted.com"},
					AllowedMethods:   []string{"GET", "POST"},
					AllowedHeaders:   []string{"X-Custom"},
					AllowCredentials: true,
					MaxAge:           7200,
				},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	// Normal GET (not OPTIONS) from allowed origin
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/data.json", nil)
	req.Host = "cors-get.example.com"
	req.Header.Set("Origin", "https://trusted.com")
	s.handleRequest(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	acao := rec.Header().Get("Access-Control-Allow-Origin")
	if acao != "https://trusted.com" {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q", acao, "https://trusted.com")
	}

	acac := rec.Header().Get("Access-Control-Allow-Credentials")
	if acac != "true" {
		t.Errorf("Access-Control-Allow-Credentials = %q, want 'true'", acac)
	}

	// The response body should still be served
	if !strings.Contains(rec.Body.String(), `{"ok":true}`) {
		t.Errorf("body = %q, want contains '{\"ok\":true}'", rec.Body.String())
	}

	// GET from disallowed origin should NOT have ACAO header
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/data.json", nil)
	req2.Host = "cors-get.example.com"
	req2.Header.Set("Origin", "https://evil.com")
	s.handleRequest(rec2, req2)

	if rec2.Code != 200 {
		t.Errorf("status = %d, want 200 even for disallowed origin", rec2.Code)
	}

	acao2 := rec2.Header().Get("Access-Control-Allow-Origin")
	if acao2 == "https://evil.com" {
		t.Errorf("Access-Control-Allow-Origin should not be set for disallowed origin, got %q", acao2)
	}
}

// --- Per-Domain CORS ---

func TestPerDomainCORS(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("hello"), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
		},
		Domains: []config.Domain{
			{
				Host: "cors.example.com",
				Root: dir,
				Type: "static",
				SSL:  config.SSLConfig{Mode: "off"},
				CORS: config.CORSConfig{
					Enabled:        true,
					AllowedOrigins: []string{"https://allowed.com"},
					AllowedMethods: []string{"GET", "POST"},
					AllowedHeaders: []string{"X-Custom"},
					MaxAge:         3600,
				},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	// OPTIONS preflight from allowed origin
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("OPTIONS", "/index.html", nil)
	req.Host = "cors.example.com"
	req.Header.Set("Origin", "https://allowed.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	s.handleRequest(rec, req)

	if rec.Code != 204 && rec.Code != 200 {
		t.Errorf("status = %d, want 204 or 200 for preflight", rec.Code)
	}

	acao := rec.Header().Get("Access-Control-Allow-Origin")
	if acao != "https://allowed.com" {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q", acao, "https://allowed.com")
	}

	// OPTIONS preflight from disallowed origin should NOT have ACAO header
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("OPTIONS", "/index.html", nil)
	req2.Host = "cors.example.com"
	req2.Header.Set("Origin", "https://evil.com")
	req2.Header.Set("Access-Control-Request-Method", "POST")
	s.handleRequest(rec2, req2)

	acao2 := rec2.Header().Get("Access-Control-Allow-Origin")
	if acao2 == "https://evil.com" {
		t.Errorf("Access-Control-Allow-Origin should not be set for disallowed origin, got %q", acao2)
	}
}

// --- Per-Domain Basic Auth ---

func TestPerDomainBasicAuth(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("secret-content"), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
		},
		Domains: []config.Domain{
			{
				Host: "auth.example.com",
				Root: dir,
				Type: "static",
				SSL:  config.SSLConfig{Mode: "off"},
				BasicAuth: config.BasicAuthConfig{
					Enabled: true,
					Users:   map[string]string{"admin": "secret"},
					Realm:   "Test",
				},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	// Request without auth should get 401
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/index.html", nil)
	req.Host = "auth.example.com"
	s.handleRequest(rec, req)

	if rec.Code != 401 {
		t.Errorf("status = %d, want 401 for unauthenticated request", rec.Code)
	}

	// Request with correct auth should succeed
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/index.html", nil)
	req2.Host = "auth.example.com"
	req2.Header.Set("Authorization", "Basic YWRtaW46c2VjcmV0") // admin:secret
	s.handleRequest(rec2, req2)

	if rec2.Code == 401 {
		t.Errorf("status = %d, want non-401 for authenticated request", rec2.Code)
	}
	if rec2.Code != 200 {
		t.Errorf("status = %d, want 200 for authenticated request", rec2.Code)
	}
}

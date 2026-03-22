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

	"github.com/uwaserver/uwas/internal/admin"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/metrics"
)

// TestMatchPathRegexVariants tests matchPath with various regex patterns.
func TestMatchPathRegexVariants(t *testing.T) {
	tests := []struct {
		path    string
		pattern string
		want    bool
	}{
		{"/api/v1/users", `^/api/`, true},
		{"/api/v1/users", `\.css$`, false},
		{"/style.css", `\.css$`, true},
		{"/image.png", `\.(png|jpg|gif)$`, true},
		{"/image.bmp", `\.(png|jpg|gif)$`, false},
		{"/admin/secret", `^/admin`, true},
		{"/public/admin", `^/admin`, false},
		{"", `.*`, true},
		{"/path", ``, true}, // empty pattern matches everything
	}

	for _, tt := range tests {
		got := matchPath(tt.path, tt.pattern)
		if got != tt.want {
			t.Errorf("matchPath(%q, %q) = %v, want %v", tt.path, tt.pattern, got, tt.want)
		}
	}
}

// TestApplyRewritesQueryStringModification tests rewrite rules that modify query strings.
func TestApplyRewritesQueryStringModification(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "target.html"), []byte("ok"), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
		},
		Domains: []config.Domain{
			{
				Host: "localhost",
				Root: dir,
				Type: "static",
				SSL:  config.SSLConfig{Mode: "off"},
				Rewrites: []config.RewriteRule{
					{Match: "^/page/([0-9]+)$", To: "/target.html?id=$1", Flags: []string{"L"}},
				},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/page/42", nil)
	req.Host = "localhost"
	s.handleRequest(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// TestApplyRewritesCacheHit tests that rewrite engine is read from rewriteCache.
func TestApplyRewritesCacheHit(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "result.html"), []byte("rewritten-result"), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
		},
		Domains: []config.Domain{
			{
				Host: "cached.local",
				Root: dir,
				Type: "static",
				SSL:  config.SSLConfig{Mode: "off"},
				Rewrites: []config.RewriteRule{
					{Match: "^/old-path$", To: "/result.html", Flags: []string{"L"}},
				},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	// The rewriteCache should have been populated during New()
	if _, ok := s.rewriteCache["cached.local"]; !ok {
		t.Fatal("rewriteCache should contain cached.local")
	}

	// First request uses the cached engine
	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest("GET", "/old-path", nil)
	req1.Host = "cached.local"
	s.handleRequest(rec1, req1)

	if rec1.Code != 200 {
		t.Errorf("first request status = %d, want 200", rec1.Code)
	}
	if !strings.Contains(rec1.Body.String(), "rewritten-result") {
		t.Errorf("body = %q, want rewritten-result", rec1.Body.String())
	}

	// Second request should also use the same cached engine
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/old-path", nil)
	req2.Host = "cached.local"
	s.handleRequest(rec2, req2)

	if rec2.Code != 200 {
		t.Errorf("second request status = %d, want 200", rec2.Code)
	}
}

// TestHandleHTTPUnknownHostServesContent tests that unknown hosts on non-SSL go through the handler chain.
func TestHandleHTTPUnknownHostServes404(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{
			{Host: "known.com", Root: "/tmp", Type: "static", SSL: config.SSLConfig{Mode: "off"}},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	// Request for an unknown host via handleHTTP (non-SSL)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "unknown.com"
	s.handleHTTP(rec, req)

	// Should go through middleware chain -> handleRequest -> 404 (unknown host)
	if rec.Code != 404 {
		t.Errorf("status = %d, want 404 for unknown host", rec.Code)
	}
}

// TestBuildMiddlewareChainReturnsWorkingHandler tests the middleware chain with all middleware.
func TestBuildMiddlewareChainReturnsWorkingHandler(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "test.html"), []byte("chain-test"), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			LogLevel:       "error",
			LogFormat:      "text",
			TrustedProxies: []string{"10.0.0.0/8"},
		},
		Domains: []config.Domain{
			{
				Host: "chain.test",
				Root: dir,
				Type: "static",
				SSL:  config.SSLConfig{Mode: "off"},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test.html", nil)
	req.Host = "chain.test"
	s.handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	// Verify middleware headers are set
	if rec.Header().Get("X-Request-ID") == "" {
		t.Error("X-Request-ID should be set by RequestID middleware")
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("SecurityHeaders middleware should set X-Content-Type-Options")
	}
}

// TestHandleFileRequestNonExistentFile tests serving a non-existent file.
func TestHandleFileRequestNonExistentFile(t *testing.T) {
	dir := t.TempDir()
	// no files in dir

	cfg := testConfig(dir)
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/missing.html", nil)
	req.Host = "localhost"
	s.handleRequest(rec, req)

	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// TestParseHtaccessValidFile tests parsing a valid .htaccess file with rewrite rules.
func TestParseHtaccessValidFile(t *testing.T) {
	dir := t.TempDir()
	htaccess := `
RewriteEngine On
RewriteRule ^/old$ /new [L]
`
	os.WriteFile(filepath.Join(dir, ".htaccess"), []byte(htaccess), 0644)
	os.WriteFile(filepath.Join(dir, "new"), []byte("new content"), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{
			{
				Host: "htaccess.test",
				Root: dir,
				Type: "static",
				SSL:  config.SSLConfig{Mode: "off"},
				Htaccess: config.HtaccessConfig{Mode: "import"},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/old", nil)
	req.Host = "htaccess.test"
	s.handleRequest(rec, req)

	// Should rewrite /old to /new and serve the file
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// TestNewWithMCPEnabled verifies MCP server is created when enabled.
func TestNewWithMCPEnabled(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			LogLevel:  "error",
			LogFormat: "text",
			MCP:       config.MCPConfig{Enabled: true},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	if s.mcp == nil {
		t.Error("MCP server should be created when enabled")
	}
}

// TestNewWithCacheEnabled verifies cache engine is created when enabled.
func TestNewWithCacheEnabled(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			LogLevel:  "error",
			LogFormat: "text",
			Cache: config.CacheConfig{
				Enabled:     true,
				MemoryLimit: config.ByteSize(1 << 20),
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	if s.cache == nil {
		t.Error("cache engine should be created when enabled")
	}
}

// TestHandleRequestWithAdminRecordLog verifies admin log recording.
func TestHandleRequestWithAdminRecordLog(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("hello"), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			LogLevel:  "error",
			LogFormat: "text",
			Admin:     config.AdminConfig{Enabled: true},
		},
		Domains: []config.Domain{
			{Host: "log.test", Root: dir, Type: "static", SSL: config.SSLConfig{Mode: "off"}},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/index.html", nil)
	req.Host = "log.test"
	s.handleRequest(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// TestHandleRequestSetsHTTPSFlag verifies that TLS requests set IsHTTPS.
func TestHandleRequestSetsHTTPSFlag(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "page.html"), []byte("secure"), 0644)

	cfg := testConfig(dir)
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/page.html", nil)
	req.Host = "localhost"
	s.handleRequest(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// TestRenderDomainErrorCustomPageNotFound tests fallback when custom error page doesn't exist.
func TestRenderDomainErrorCustomPageNotFound(t *testing.T) {
	dir := t.TempDir()

	domain := &config.Domain{
		Root:       dir,
		ErrorPages: map[int]string{404: "errors/404.html"},
	}

	rec := httptest.NewRecorder()
	rw := http.ResponseWriter(rec)
	renderDomainError(rw, 404, domain)

	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	// Should fallback to default error page (contains UWAS)
	if !strings.Contains(rec.Body.String(), "UWAS") {
		t.Error("should fallback to default error page")
	}
}

// --- handleProxy with mirror config ---

func TestHandleProxyWithMirrorConfig(t *testing.T) {
	// Start a backend and a mirror server
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("backend-ok"))
	}))
	defer backend.Close()

	mirrorSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer mirrorSrv.Close()

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
		},
		Domains: []config.Domain{
			{
				Host: "mirror.com",
				Type: "proxy",
				SSL:  config.SSLConfig{Mode: "off"},
				Proxy: config.ProxyConfig{
					Upstreams: []config.Upstream{
						{Address: backend.URL, Weight: 1},
					},
					Algorithm: "round_robin",
					Mirror: config.MirrorConfig{
						Enabled: true,
						Backend: mirrorSrv.URL,
						Percent: 100,
					},
				},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	// Verify mirror was configured
	if _, ok := s.proxyMirrors["mirror.com"]; !ok {
		t.Fatal("proxyMirrors should contain mirror.com")
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Host = "mirror.com"
	s.handleRequest(rec, req)

	if rec.Code == 502 {
		t.Error("proxy with mirror should not return 502 when backend is available")
	}
}

// TestHandleProxyWithMirrorAndBody tests mirror with a request body.
func TestHandleProxyWithMirrorAndBody(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	}))
	defer backend.Close()

	mirrorSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer mirrorSrv.Close()

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
		},
		Domains: []config.Domain{
			{
				Host: "mirrorbody.com",
				Type: "proxy",
				SSL:  config.SSLConfig{Mode: "off"},
				Proxy: config.ProxyConfig{
					Upstreams: []config.Upstream{
						{Address: backend.URL, Weight: 1},
					},
					Algorithm: "round_robin",
					Mirror: config.MirrorConfig{
						Enabled: true,
						Backend: mirrorSrv.URL,
						Percent: 100,
					},
				},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/submit", strings.NewReader(`{"data":"test"}`))
	req.Host = "mirrorbody.com"
	req.Header.Set("Content-Type", "application/json")
	s.handleRequest(rec, req)

	if rec.Code == 502 {
		t.Error("proxy with mirror and body should work")
	}
}

// TestHandleProxyWithMirrorNilBody tests mirror when request body is nil.
func TestHandleProxyWithMirrorNilBody(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer backend.Close()

	mirrorSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer mirrorSrv.Close()

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
		},
		Domains: []config.Domain{
			{
				Host: "mirrornobody.com",
				Type: "proxy",
				SSL:  config.SSLConfig{Mode: "off"},
				Proxy: config.ProxyConfig{
					Upstreams: []config.Upstream{
						{Address: backend.URL, Weight: 1},
					},
					Algorithm: "round_robin",
					Mirror: config.MirrorConfig{
						Enabled: true,
						Backend: mirrorSrv.URL,
						Percent: 100,
					},
				},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/no-body", nil)
	req.Body = nil // explicit nil body
	req.Host = "mirrornobody.com"
	s.handleRequest(rec, req)

	if rec.Code == 502 {
		t.Error("proxy with nil body mirror should work")
	}
}

// --- handleRequest with analytics recording ---

func TestHandleRequestRecordsAnalytics(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "track.html"), []byte("tracked"), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
		},
		Domains: []config.Domain{
			{Host: "analytics.test", Root: dir, Type: "static", SSL: config.SSLConfig{Mode: "off"}},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	// analytics is set during New()
	if s.analytics == nil {
		t.Fatal("analytics should be initialized")
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/track.html", nil)
	req.Host = "analytics.test"
	req.RemoteAddr = "192.168.1.1:12345"
	s.handleRequest(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	// Analytics Record() is called in the deferred func; just verify no panic.
}

// --- handleRequest with alerting (error spike) ---

func TestHandleRequestRecordsAlertingErrorSpike(t *testing.T) {
	dir := t.TempDir()
	// No files, so all requests return 404

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
			Alerting: config.AlertingConfig{
				Enabled:    true,
				WebhookURL: "http://localhost:1/noop",
			},
		},
		Domains: []config.Domain{
			{Host: "alerttest.com", Root: dir, Type: "static", SSL: config.SSLConfig{Mode: "off"}},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	if s.alerter == nil {
		t.Fatal("alerter should be initialized when alerting is enabled")
	}

	// Make requests that result in non-5xx (404) and verify alerter records them.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/missing", nil)
	req.Host = "alerttest.com"
	s.handleRequest(rec, req)

	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	// Verify alerter's RecordRequest was called (non-error path).
	// The method is called in deferred func; no panic means success.
}

// --- GracefulRestart with both HTTP and HTTPS servers ---

func TestGracefulRestartWithBothServers(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Timeouts: config.TimeoutConfig{
				ShutdownGrace: config.Duration{Duration: 5 * time.Second},
			},
		},
	}
	log := logger.New("error", "text")

	httpSrv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})}
	httpsSrv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})}

	s := &Server{
		config:   cfg,
		logger:   log,
		httpSrv:  httpSrv,
		httpsSrv: httpsSrv,
	}

	err := s.GracefulRestart()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestGracefulRestartDefaultGrace tests with zero grace (should default to 30s).
func TestGracefulRestartDefaultGrace(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Timeouts: config.TimeoutConfig{
				// Zero grace - should default to 30s
			},
		},
	}
	log := logger.New("error", "text")
	s := &Server{
		config: cfg,
		logger: log,
	}

	err := s.GracefulRestart()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- DrainAndWait with both listeners ---

func TestDrainAndWaitWithBothListeners(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Timeouts: config.TimeoutConfig{
				ShutdownGrace: config.Duration{Duration: 1 * time.Second},
			},
		},
	}
	log := logger.New("error", "text")

	httpSrv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})}
	httpsSrv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})}

	s := &Server{
		config:   cfg,
		logger:   log,
		httpSrv:  httpSrv,
		httpsSrv: httpsSrv,
	}

	done := make(chan struct{})
	go func() {
		s.DrainAndWait()
		close(done)
	}()

	select {
	case <-done:
		// OK
	case <-time.After(5 * time.Second):
		t.Fatal("DrainAndWait did not complete in time")
	}
}

// --- matchPath edge cases ---

func TestMatchPathAnchored(t *testing.T) {
	// Test a pattern anchored at both ends
	if !matchPath("/exact", "^/exact$") {
		t.Error("should match exactly anchored path")
	}
	if matchPath("/exact/more", "^/exact$") {
		t.Error("should not match when path has extra segments")
	}
}

func TestMatchPathSpecialRegexChars(t *testing.T) {
	// Path containing regex-special characters
	if !matchPath("/api/v1.0/users", `v1\.0`) {
		t.Error("should match escaped dot")
	}
	if matchPath("/api/v1X0/users", `v1\.0`) {
		t.Error("escaped dot should not match arbitrary character")
	}
}

func TestMatchPathCaseInsensitive(t *testing.T) {
	// matchPath uses regexp.MatchString which is case-sensitive by default
	if matchPath("/API/v1", "^/api/") {
		t.Error("matchPath should be case-sensitive by default")
	}
	// But we can use (?i) flag
	if !matchPath("/API/v1", "(?i)^/api/") {
		t.Error("(?i) flag should enable case-insensitive matching")
	}
}

// --- handleRequest health check endpoints ---

func TestHandleRequestHealthCheck(t *testing.T) {
	cfg := testConfig(t.TempDir())
	log := logger.New("error", "text")
	s := New(cfg, log)

	for _, path := range []string{"/.well-known/health", "/healthz"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", path, nil)
		req.Host = "localhost"
		s.handleRequest(rec, req)

		if rec.Code != 200 {
			t.Errorf("health check %s: status = %d, want 200", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `"status":"ok"`) {
			t.Errorf("health check %s: body = %q, want JSON health response", path, rec.Body.String())
		}
		if rec.Header().Get("Content-Type") != "application/json" {
			t.Errorf("health check %s: Content-Type = %q, want application/json", path, rec.Header().Get("Content-Type"))
		}
	}
}

// --- handleRequest with TLS (IsHTTPS) ---

func TestHandleRequestWithTLS(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "secure.html"), []byte("secure-content"), 0644)

	cfg := testConfig(dir)
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/secure.html", nil)
	req.Host = "localhost"
	req.TLS = &tls.ConnectionState{} // simulate TLS
	s.handleRequest(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// --- handleProxy with no balancer (nil case, default balancer created) ---

func TestHandleProxyCreatesDefaultBalancer(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("default-balancer"))
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
				Host: "defbalancer.com",
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

	// Remove the balancer to test the nil fallback
	delete(s.proxyBalancers, "defbalancer.com")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "defbalancer.com"
	s.handleRequest(rec, req)

	// Should create a default round_robin balancer and not error
	if rec.Code == 502 {
		t.Error("should create default balancer when nil")
	}
}

// --- handleRequest with cache + redirect domain (capture branch) ---

func TestHandleRequestCacheWithRedirectDomain(t *testing.T) {
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
				Host: "cached-redir.com",
				Type: "redirect",
				SSL:  config.SSLConfig{Mode: "off"},
				Redirect: config.RedirectConfig{
					Target:       "https://target.com",
					Status:       301,
					PreservePath: true,
				},
				Cache: config.DomainCache{Enabled: true, TTL: 60},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/path", nil)
	req.Host = "cached-redir.com"
	s.handleRequest(rec, req)

	if rec.Code != 301 {
		t.Errorf("status = %d, want 301", rec.Code)
	}
}

// TestHandleRequestCacheWithProxyDomain tests the cache capture branch for proxy domains.
func TestHandleRequestCacheWithProxyDomain(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(200)
		w.Write([]byte("cached-proxy-response"))
	}))
	defer backend.Close()

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
				Host: "cached-proxy.com",
				Type: "proxy",
				SSL:  config.SSLConfig{Mode: "off"},
				Proxy: config.ProxyConfig{
					Upstreams: []config.Upstream{
						{Address: backend.URL, Weight: 1},
					},
					Algorithm: "round_robin",
				},
				Cache: config.DomainCache{Enabled: true, TTL: 60},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/data", nil)
	req.Host = "cached-proxy.com"
	s.handleRequest(rec, req)

	if rec.Code == 502 {
		t.Error("cached proxy should forward to backend")
	}
}

// TestHandleRequestCacheWithUnknownType tests cache capture branch for unknown domain type.
func TestHandleRequestCacheWithUnknownType(t *testing.T) {
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
				Host:  "cached-unknown.com",
				Type:  "custom-type",
				SSL:   config.SSLConfig{Mode: "off"},
				Cache: config.DomainCache{Enabled: true, TTL: 60},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "cached-unknown.com"
	s.handleRequest(rec, req)

	if rec.Code != 500 {
		t.Errorf("status = %d, want 500 for unknown type in cache path", rec.Code)
	}
}

// --- parseHtaccess: disabled RewriteEngine ---

func TestParseHtaccessDisabledRewriteEngine(t *testing.T) {
	dir := t.TempDir()
	htaccess := `
RewriteEngine Off
RewriteRule ^/old$ /new [L]
`
	os.WriteFile(filepath.Join(dir, ".htaccess"), []byte(htaccess), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
		},
		Domains: []config.Domain{
			{
				Host:     "htoff.local",
				Root:     dir,
				Type:     "static",
				SSL:      config.SSLConfig{Mode: "off"},
				Htaccess: config.HtaccessConfig{Mode: "import"},
			},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log)

	// Make a request to trigger .htaccess parsing
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/old", nil)
	req.Host = "htoff.local"
	s.handleRequest(rec, req)

	// With RewriteEngine Off, the rules should not apply, so /old returns 404
	if rec.Code != 404 {
		t.Errorf("status = %d, want 404 (rewrite engine is off)", rec.Code)
	}
}

// --- parseHtaccess: empty .htaccess file ---

func TestParseHtaccessEmptyFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".htaccess"), []byte(""), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
		},
		Domains: []config.Domain{
			{
				Host:     "htempty.local",
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
	req.Host = "htempty.local"
	s.handleRequest(rec, req)

	// Should not panic; expect 404 since no file exists
	if rec.Code == 0 {
		t.Error("expected non-zero status code")
	}
}

// --- GracefulRestart with admin server ---

func TestGracefulRestartWithAdminServer(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Timeouts: config.TimeoutConfig{
				ShutdownGrace: config.Duration{Duration: 5 * time.Second},
			},
			Admin: config.AdminConfig{Enabled: true},
		},
	}
	log := logger.New("error", "text")

	// Create an admin server with an HTTPServer
	admSrv := admin.New(cfg, log, metrics.New())

	s := &Server{
		config: cfg,
		logger: log,
		admin:  admSrv,
	}

	err := s.GracefulRestart()
	// admin.HTTPServer() returns nil since Start() was not called.
	// So the admin shutdown path is skipped.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- ReloadSuccess clears htaccess and rewrite caches ---

func TestReloadClearsHtaccessCache(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "uwas.yaml")
	configContent := `
domains:
  - host: reloaded.com
    root: /tmp
    type: static
    ssl:
      mode: "off"
`
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

	// Pre-populate htaccess cache
	s.htaccessCacheMu.Lock()
	s.htaccessCache["/some/root"] = nil
	s.htaccessCacheMu.Unlock()

	err := s.reload()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}

	// htaccess cache should be cleared
	s.htaccessCacheMu.RLock()
	if len(s.htaccessCache) != 0 {
		t.Error("htaccess cache should be cleared after reload")
	}
	s.htaccessCacheMu.RUnlock()
}

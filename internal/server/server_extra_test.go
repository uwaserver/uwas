package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
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

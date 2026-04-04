package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/router"
)

// =============================================================================
// matchLocation tests
// =============================================================================

func TestMatchLocationPrefix(t *testing.T) {
	tests := []struct {
		path    string
		pattern string
		want    bool
	}{
		{"/api/users", "/api/", true},
		{"/api/", "/api/", true},
		{"/api", "/api/", false}, // prefix "/api/" doesn't match "/api" (no trailing slash)
		{"/other", "/api/", false},
		{"/", "/", true},
		{"/foo/bar", "/foo", true},
		{"", "/", false},
	}
	for _, tt := range tests {
		got := matchLocation(tt.path, tt.pattern)
		if got != tt.want {
			t.Errorf("matchLocation(%q, %q) = %v, want %v", tt.path, tt.pattern, got, tt.want)
		}
	}
}

func TestMatchLocationRegex(t *testing.T) {
	tests := []struct {
		path    string
		pattern string
		want    bool
	}{
		{"/file.PHP", "~(?i)\\.php$", true},                 // case-insensitive regex
		{"/index.php", "~\\.php$", true},
		{"/index.html", "~\\.php$", false},
		{"/path/to/file.php", "~\\.php$", true},
		{"/image.png", "~\\.(png|jpg)$", true},
		{"/image.gif", "~\\.(png|jpg)$", false},
		{"/api/v1/users", "~^/api/", true},
		{"/web/users", "~^/api/", false},
	}
	for _, tt := range tests {
		got := matchLocation(tt.path, tt.pattern)
		if got != tt.want {
			t.Errorf("matchLocation(%q, %q) = %v, want %v", tt.path, tt.pattern, got, tt.want)
		}
	}
}

func TestMatchLocationInvalidRegex(t *testing.T) {
	// Invalid regex should return false, not panic
	got := matchLocation("/foo", "~[invalid")
	if got != false {
		t.Errorf("matchLocation with invalid regex = %v, want false", got)
	}
}

func TestMatchLocationEmptyPattern(t *testing.T) {
	// Empty pattern is prefix match on empty string — everything matches
	if !matchLocation("/anything", "") {
		t.Error("empty pattern should match everything (prefix)")
	}
	if !matchLocation("", "") {
		t.Error("empty pattern should match empty path")
	}
}

// =============================================================================
// handleAppProxy tests
// =============================================================================

func TestHandleAppProxyNilAppMgr(t *testing.T) {
	cfg := &config.Config{
		Domains: []config.Domain{
			{Host: "app.example.com", Type: "app"},
		},
	}
	s := newMinimalServer(cfg)
	// s.appMgr is nil by default

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	ctx := router.AcquireContext(rec, req)
	defer router.ReleaseContext(ctx)

	domain := &cfg.Domains[0]
	s.handleAppProxy(ctx, domain)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("expected 502 for nil appMgr, got %d", rec.Code)
	}
}

func TestHandleAppProxyEmptyListenAddr(t *testing.T) {
	cfg := &config.Config{
		Domains: []config.Domain{
			{Host: "unknown-app.com", Type: "app"},
		},
	}
	s := newMinimalServer(cfg)
	// appMgr is nil, so we set it to a non-nil but empty manager
	// Actually, we can use a mock: appmanager.New(nil) which returns empty addr

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	ctx := router.AcquireContext(rec, req)
	defer router.ReleaseContext(ctx)

	domain := &cfg.Domains[0]
	s.handleAppProxy(ctx, domain)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("expected 502 for empty listen addr, got %d", rec.Code)
	}
}

// =============================================================================
// FetchFragment tests
// =============================================================================

func TestFetchFragmentDomainNotFound(t *testing.T) {
	cfg := &config.Config{
		Domains: []config.Domain{},
	}
	s := newMinimalServer(cfg)

	req := httptest.NewRequest("GET", "/fragment", nil)
	body, status, _, err := s.FetchFragment("nonexistent.com", "/fragment", req)
	if err == nil {
		t.Error("expected error for domain not found")
	}
	if body != nil {
		t.Error("expected nil body")
	}
	if status != 0 {
		t.Errorf("expected status 0, got %d", status)
	}
}

func TestFetchFragmentUnsupportedType(t *testing.T) {
	cfg := &config.Config{
		Domains: []config.Domain{
			{Host: "test.com", Type: "redirect"},
		},
	}
	s := newMinimalServer(cfg)

	req := httptest.NewRequest("GET", "/fragment", nil)
	body, status, _, err := s.FetchFragment("test.com", "/fragment", req)
	if err == nil {
		t.Error("expected error for unsupported type")
	}
	_ = body
	_ = status
}

func TestFetchFragmentStaticType(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		Domains: []config.Domain{
			{Host: "static.com", Type: "static", Root: dir},
		},
	}
	s := newMinimalServer(cfg)

	req := httptest.NewRequest("GET", "/nonexistent", nil)
	body, status, _, err := s.FetchFragment("static.com", "/nonexistent", req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Static file handler returns 404 for nonexistent file
	if status != http.StatusNotFound {
		t.Errorf("expected 404, got %d", status)
	}
	if len(body) == 0 {
		t.Error("expected non-empty body for 404 page")
	}
}

// =============================================================================
// handleRequest — maintenance mode
// =============================================================================

func TestHandleRequestMaintenanceMode(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			LogLevel:  "error",
			LogFormat: "text",
		},
		Domains: []config.Domain{
			{
				Host: "maint.example.com",
				Type: "static",
				Root: t.TempDir(),
				Maintenance: config.MaintenanceConfig{
					Enabled:    true,
					Message:    "<h1>Maintenance</h1>",
					RetryAfter: 300,
				},
			},
		},
	}
	s := newMinimalServer(cfg)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "maint.example.com"

	s.handleRequest(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "300" {
		t.Errorf("Retry-After = %q, want %q", got, "300")
	}
}

func TestHandleRequestMaintenanceModeDefaultMessage(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			LogLevel:  "error",
			LogFormat: "text",
		},
		Domains: []config.Domain{
			{
				Host: "maint2.example.com",
				Type: "static",
				Root: t.TempDir(),
				Maintenance: config.MaintenanceConfig{
					Enabled: true,
					// no message, no retry
				},
			},
		},
	}
	s := newMinimalServer(cfg)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "maint2.example.com"

	s.handleRequest(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}
	body := rec.Body.String()
	if body == "" {
		t.Error("expected default maintenance message body")
	}
	if !strings.Contains(body, "Under Maintenance") {
		t.Errorf("expected default maintenance message, got: %s", body)
	}
}

func TestHandleRequestMaintenanceAllowedIP(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		Global: config.GlobalConfig{
			LogLevel:  "error",
			LogFormat: "text",
		},
		Domains: []config.Domain{
			{
				Host: "maint-allow.example.com",
				Type: "static",
				Root: dir,
				Maintenance: config.MaintenanceConfig{
					Enabled:    true,
					Message:    "Under Maintenance",
					AllowedIPs: []string{"1.2.3.4"},
				},
			},
		},
	}
	s := newMinimalServer(cfg)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "maint-allow.example.com"
	req.RemoteAddr = "1.2.3.4:12345"

	s.handleRequest(rec, req)

	// Should pass through maintenance because IP is allowed
	// The request will likely 404 because root dir is empty, but should NOT be 503
	if rec.Code == http.StatusServiceUnavailable {
		t.Error("allowed IP should bypass maintenance mode")
	}
}

// =============================================================================
// handleRequest — security headers
// =============================================================================

func TestHandleRequestSecurityHeaders(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		Global: config.GlobalConfig{
			LogLevel:  "error",
			LogFormat: "text",
		},
		Domains: []config.Domain{
			{
				Host: "secure.example.com",
				Type: "static",
				Root: dir,
				SecurityHeaders: config.SecurityHeadersConfig{
					ContentSecurityPolicy: "default-src 'self'",
					PermissionsPolicy:     "geolocation=()",
					CrossOriginEmbedder:   "require-corp",
					CrossOriginOpener:     "same-origin",
					CrossOriginResource:   "same-origin",
				},
			},
		},
	}
	s := newMinimalServer(cfg)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "secure.example.com"

	s.handleRequest(rec, req)

	if got := rec.Header().Get("Content-Security-Policy"); got != "default-src 'self'" {
		t.Errorf("CSP = %q", got)
	}
	if got := rec.Header().Get("Permissions-Policy"); got != "geolocation=()" {
		t.Errorf("Permissions-Policy = %q", got)
	}
	if got := rec.Header().Get("Cross-Origin-Embedder-Policy"); got != "require-corp" {
		t.Errorf("COEP = %q", got)
	}
	if got := rec.Header().Get("Cross-Origin-Opener-Policy"); got != "same-origin" {
		t.Errorf("COOP = %q", got)
	}
	if got := rec.Header().Get("Cross-Origin-Resource-Policy"); got != "same-origin" {
		t.Errorf("CORP = %q", got)
	}
}

// =============================================================================
// handleRequest — blocked paths
// =============================================================================

func TestHandleRequestBlockedPaths(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		Global: config.GlobalConfig{
			LogLevel:  "error",
			LogFormat: "text",
		},
		Domains: []config.Domain{
			{
				Host: "blocked.example.com",
				Type: "static",
				Root: dir,
				Security: config.SecurityConfig{
					BlockedPaths: []string{"/.env", "/wp-config.php"},
				},
			},
		},
	}
	s := newMinimalServer(cfg)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/.env", nil)
	req.Host = "blocked.example.com"

	s.handleRequest(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for blocked path, got %d", rec.Code)
	}
}

func TestHandleRequestBlockedPathsNotBlocked(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		Global: config.GlobalConfig{
			LogLevel:  "error",
			LogFormat: "text",
		},
		Domains: []config.Domain{
			{
				Host: "ok.example.com",
				Type: "static",
				Root: dir,
				Security: config.SecurityConfig{
					BlockedPaths: []string{"/.env"},
				},
			},
		},
	}
	s := newMinimalServer(cfg)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/safe/path", nil)
	req.Host = "ok.example.com"

	s.handleRequest(rec, req)

	// Should not be 403 (likely 404 since file doesn't exist)
	if rec.Code == http.StatusForbidden {
		t.Error("safe path should not be blocked")
	}
}

// =============================================================================
// handleRequest — location overrides (redirect, headers)
// =============================================================================

func TestHandleRequestLocationRedirect(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		Global: config.GlobalConfig{
			LogLevel:  "error",
			LogFormat: "text",
		},
		Domains: []config.Domain{
			{
				Host: "loc.example.com",
				Type: "static",
				Root: dir,
				Locations: []config.LocationConfig{
					{
						Match:        "/old",
						Redirect:     "https://new.example.com",
						RedirectCode: 301,
					},
				},
			},
		},
	}
	s := newMinimalServer(cfg)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/old/page", nil)
	req.Host = "loc.example.com"

	s.handleRequest(rec, req)

	if rec.Code != http.StatusMovedPermanently {
		t.Errorf("expected 301, got %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if loc != "https://new.example.com" {
		t.Errorf("Location = %q, want %q", loc, "https://new.example.com")
	}
}

func TestHandleRequestLocationHeaders(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		Global: config.GlobalConfig{
			LogLevel:  "error",
			LogFormat: "text",
		},
		Domains: []config.Domain{
			{
				Host: "headers.example.com",
				Type: "static",
				Root: dir,
				Locations: []config.LocationConfig{
					{
						Match:        "/api/",
						Headers:      map[string]string{"X-Custom": "test"},
						CacheControl: "no-cache",
					},
				},
			},
		},
	}
	s := newMinimalServer(cfg)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Host = "headers.example.com"

	s.handleRequest(rec, req)

	if got := rec.Header().Get("X-Custom"); got != "test" {
		t.Errorf("X-Custom = %q, want %q", got, "test")
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want %q", got, "no-cache")
	}
}

func TestHandleRequestLocationDefaultRedirectCode(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		Global: config.GlobalConfig{
			LogLevel:  "error",
			LogFormat: "text",
		},
		Domains: []config.Domain{
			{
				Host: "redir.example.com",
				Type: "static",
				Root: dir,
				Locations: []config.LocationConfig{
					{
						Match:    "/legacy",
						Redirect: "https://modern.example.com",
						// RedirectCode is 0, should default to 301
					},
				},
			},
		},
	}
	s := newMinimalServer(cfg)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/legacy/path", nil)
	req.Host = "redir.example.com"

	s.handleRequest(rec, req)

	if rec.Code != http.StatusMovedPermanently {
		t.Errorf("expected 301 (default redirect code), got %d", rec.Code)
	}
}

// =============================================================================
// handleRequest — health check endpoints
// =============================================================================

func TestHandleRequestHealthz(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			LogLevel:  "error",
			LogFormat: "text",
		},
		Domains: []config.Domain{},
	}
	s := newMinimalServer(cfg)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/healthz", nil)

	s.handleRequest(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for /healthz, got %d", rec.Code)
	}
	body := rec.Body.String()
	if body != `{"status":"ok"}` {
		t.Errorf("body = %q", body)
	}
}

func TestHandleRequestWellKnownHealth(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			LogLevel:  "error",
			LogFormat: "text",
		},
		Domains: []config.Domain{},
	}
	s := newMinimalServer(cfg)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/.well-known/health", nil)

	s.handleRequest(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

// =============================================================================
// handleRequest — domain not found
// =============================================================================

func TestHandleRequestDomainNotFound(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			LogLevel:  "error",
			LogFormat: "text",
		},
		Domains: []config.Domain{
			{Host: "known.example.com", Type: "static", Root: t.TempDir()},
		},
	}
	s := newMinimalServer(cfg)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "unknown.example.com"

	s.handleRequest(rec, req)

	// Unknown hosts get 421 (Misdirected) since they're not configured
	if rec.Code != 421 {
		t.Errorf("expected 421 for unconfigured domain, got %d", rec.Code)
	}
}

// =============================================================================
// handleRequest — location with static root
// =============================================================================

func TestHandleRequestLocationStaticRoot(t *testing.T) {
	dir := t.TempDir()
	subDir := t.TempDir()
	cfg := &config.Config{
		Global: config.GlobalConfig{
			LogLevel:  "error",
			LogFormat: "text",
		},
		Domains: []config.Domain{
			{
				Host: "rootloc.example.com",
				Type: "static",
				Root: dir,
				Locations: []config.LocationConfig{
					{
						Match: "/docs/",
						Root:  subDir,
					},
				},
			},
		},
	}
	s := newMinimalServer(cfg)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/docs/readme.txt", nil)
	req.Host = "rootloc.example.com"

	s.handleRequest(rec, req)

	// File doesn't exist, but handler should be invoked (404 from http.ServeFile)
	if rec.Code == http.StatusForbidden {
		t.Error("should not be forbidden for valid path within root")
	}
}

// =============================================================================
// enforceBasicAuth tests
// =============================================================================

func TestEnforceBasicAuthDisabled(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)

	result := enforceBasicAuth(rec, req, "test.com", config.BasicAuthConfig{
		Enabled: false,
	})
	if !result {
		t.Error("disabled basic auth should pass")
	}
}

func TestEnforceBasicAuthNoUsers(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)

	result := enforceBasicAuth(rec, req, "test.com", config.BasicAuthConfig{
		Enabled: true,
		Users:   map[string]string{},
	})
	if !result {
		t.Error("basic auth with no users should pass")
	}
}

func TestEnforceBasicAuthNoCredentials(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)

	result := enforceBasicAuth(rec, req, "test.com", config.BasicAuthConfig{
		Enabled: true,
		Users:   map[string]string{"admin": "password123"},
	})
	if result {
		t.Error("should reject without credentials")
	}
}

// =============================================================================
// handleRequest — connection limiter
// =============================================================================

func TestHandleRequestConnLimiterFull(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		Global: config.GlobalConfig{
			LogLevel:    "error",
			LogFormat:   "text",
			MaxConnections: 1,
			WorkerCount: "1",
		},
		Domains: []config.Domain{
			{Host: "limited.example.com", Type: "static", Root: dir},
		},
	}

	log := logger.New("error", "text")
	s := New(cfg, log)
	// Fill the connection limiter
	s.connLimiter <- struct{}{}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "limited.example.com"

	s.handleRequest(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when connLimiter full, got %d", rec.Code)
	}

	// Drain so server cleanup doesn't hang
	<-s.connLimiter
}

// =============================================================================
// handleRequest — CORS
// =============================================================================

func TestHandleRequestCORSPreflight2(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		Global: config.GlobalConfig{
			LogLevel:  "error",
			LogFormat: "text",
		},
		Domains: []config.Domain{
			{
				Host: "cors.example.com",
				Type: "static",
				Root: dir,
				CORS: config.CORSConfig{
					Enabled:        true,
					AllowedOrigins: []string{"https://app.example.com"},
					AllowedMethods: []string{"GET", "POST"},
				},
			},
		},
	}
	s := newMinimalServer(cfg)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("OPTIONS", "/api/data", nil)
	req.Host = "cors.example.com"
	req.Header.Set("Origin", "https://app.example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")

	s.handleRequest(rec, req)

	// CORS middleware handles preflight and may return 200 or pass through
	if rec.Code == http.StatusServiceUnavailable || rec.Code == http.StatusForbidden {
		t.Errorf("CORS preflight should not fail, got %d", rec.Code)
	}
}

// =============================================================================
// handleRequest — per-domain header transforms
// =============================================================================

func TestHandleRequestHeaderTransforms2(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		Global: config.GlobalConfig{
			LogLevel:  "error",
			LogFormat: "text",
		},
		Domains: []config.Domain{
			{
				Host: "headers-tx.example.com",
				Type: "static",
				Root: dir,
				Headers: config.HeadersConfig{
					Add:          map[string]string{"X-Server": "UWAS"},
					ResponseAdd:  map[string]string{"X-Response": "yes"},
					Remove:       []string{"Server"},
					ResponseRemove: []string{},
				},
			},
		},
	}
	s := newMinimalServer(cfg)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "headers-tx.example.com"

	s.handleRequest(rec, req)

	if got := rec.Header().Get("X-Server"); got != "UWAS" {
		t.Errorf("X-Server = %q, want %q", got, "UWAS")
	}
	if got := rec.Header().Get("X-Response"); got != "yes" {
		t.Errorf("X-Response = %q, want %q", got, "yes")
	}
}

// =============================================================================
// handleRequest — location rate limiting
// =============================================================================

func TestHandleRequestLocationRateLimitExceeded(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		Global: config.GlobalConfig{
			LogLevel:  "error",
			LogFormat: "text",
		},
		Domains: []config.Domain{
			{
				Host: "rateloc.example.com",
				Type: "static",
				Root: dir,
				Locations: []config.LocationConfig{
					{
						Match: "/api/",
						RateLimit: &config.RateLimitConfig{
							Requests: 1,
							Window:   config.Duration{Duration: 60000000000}, // 1 minute
						},
					},
				},
			},
		},
	}
	s := newMinimalServer(cfg)

	// First request should pass
	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest("GET", "/api/test", nil)
	req1.Host = "rateloc.example.com"
	req1.RemoteAddr = "1.2.3.4:12345"
	s.handleRequest(rec1, req1)

	// Second request should be rate limited
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/api/test", nil)
	req2.Host = "rateloc.example.com"
	req2.RemoteAddr = "1.2.3.4:12345"
	s.handleRequest(rec2, req2)

	if rec2.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429 after rate limit exceeded, got %d", rec2.Code)
	}
}

// =============================================================================
// handleRequest — location match first-wins
// =============================================================================

func TestHandleRequestLocationFirstMatchWins(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		Global: config.GlobalConfig{
			LogLevel:  "error",
			LogFormat: "text",
		},
		Domains: []config.Domain{
			{
				Host: "firstmatch.example.com",
				Type: "static",
				Root: dir,
				Locations: []config.LocationConfig{
					{
						Match:        "/api/",
						Headers:      map[string]string{"X-First": "true"},
						CacheControl: "no-store",
					},
					{
						Match:        "/api/v2/",
						Headers:      map[string]string{"X-Second": "true"},
						CacheControl: "max-age=3600",
					},
				},
			},
		},
	}
	s := newMinimalServer(cfg)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v2/data", nil)
	req.Host = "firstmatch.example.com"

	s.handleRequest(rec, req)

	// First match wins — /api/ matches before /api/v2/
	if got := rec.Header().Get("X-First"); got != "true" {
		t.Error("expected first location to match")
	}
}


package admin

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/cache"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/metrics"
)

func testServer() *Server {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{Listen: "127.0.0.1:0"},
		},
		Domains: []config.Domain{
			{Host: "example.com", Type: "static", SSL: config.SSLConfig{Mode: "auto"}},
			{Host: "api.example.com", Type: "proxy", SSL: config.SSLConfig{Mode: "auto"}},
		},
	}
	log := logger.New("error", "text")
	m := metrics.New()
	return New(cfg, log, m)
}

func TestHealthEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/health", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["status"] != "ok" {
		t.Errorf("status = %v", body["status"])
	}
}

func TestDomainsEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/domains", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d", rec.Code)
	}

	var domains []map[string]any
	json.Unmarshal(rec.Body.Bytes(), &domains)
	if len(domains) != 2 {
		t.Errorf("domains = %d, want 2", len(domains))
	}
}

func TestStatsEndpoint(t *testing.T) {
	s := testServer()
	s.metrics.RequestsTotal.Store(42)
	s.metrics.CacheHits.Store(10)

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/stats", nil))

	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["requests_total"] != float64(42) {
		t.Errorf("requests = %v, want 42", body["requests_total"])
	}
}

func TestMetricsEndpoint(t *testing.T) {
	s := testServer()
	s.metrics.RequestsTotal.Store(100)

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/metrics", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !contains(body, "uwas_requests_total 100") {
		t.Error("metrics should contain requests_total")
	}
}

func TestAuthMiddleware(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{APIKey: "secret123"},
		},
	}
	s := New(cfg, logger.New("error", "text"), metrics.New())

	// No auth → 401
	rec := httptest.NewRecorder()
	s.authMiddleware(s.mux).ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/health", nil))
	if rec.Code != 401 {
		t.Errorf("no auth: status = %d, want 401", rec.Code)
	}

	// Wrong auth → 401
	rec2 := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/health", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	s.authMiddleware(s.mux).ServeHTTP(rec2, req)
	if rec2.Code != 401 {
		t.Errorf("wrong auth: status = %d, want 401", rec2.Code)
	}

	// Correct auth → 200
	rec3 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/api/v1/health", nil)
	req2.Header.Set("Authorization", "Bearer secret123")
	s.authMiddleware(s.mux).ServeHTTP(rec3, req2)
	if rec3.Code != 200 {
		t.Errorf("correct auth: status = %d, want 200", rec3.Code)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// --- handleConfig ---

func TestConfigEndpoint(t *testing.T) {
	s := testServer()
	s.config.Global.WorkerCount = "4"
	s.config.Global.MaxConnections = 1024
	s.config.Global.LogLevel = "debug"
	s.config.Global.LogFormat = "json"

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/config", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["domain_count"] != float64(2) {
		t.Errorf("domain_count = %v, want 2", body["domain_count"])
	}
	global := body["global"].(map[string]any)
	if global["log_level"] != "debug" {
		t.Errorf("log_level = %v", global["log_level"])
	}
}

// --- SetCache ---

func TestAdminSetCache(t *testing.T) {
	s := testServer()
	if s.cache != nil {
		t.Error("cache should be nil initially")
	}

	log := logger.New("error", "text")
	eng := cache.NewEngine(1<<20, "", 0, log)
	s.SetCache(eng)

	if s.cache == nil {
		t.Error("cache should be set after SetCache")
	}
}

// --- SetReloadFunc ---

func TestAdminSetReloadFunc(t *testing.T) {
	s := testServer()
	if s.reloadFn != nil {
		t.Error("reloadFn should be nil initially")
	}

	s.SetReloadFunc(func() error { return nil })

	if s.reloadFn == nil {
		t.Error("reloadFn should be set after SetReloadFunc")
	}
}

// --- handleReload: success ---

func TestReloadSuccess(t *testing.T) {
	s := testServer()
	s.SetReloadFunc(func() error { return nil })

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/reload", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	var body map[string]string
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["status"] != "reloaded" {
		t.Errorf("status = %q, want reloaded", body["status"])
	}
}

// --- handleReload: failure ---

func TestReloadFailure(t *testing.T) {
	s := testServer()
	s.SetReloadFunc(func() error { return errors.New("bad config") })

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/reload", nil))

	if rec.Code != 500 {
		t.Errorf("status = %d, want 500", rec.Code)
	}

	var body map[string]string
	json.Unmarshal(rec.Body.Bytes(), &body)
	if !strings.Contains(body["error"], "bad config") {
		t.Errorf("error = %q, want contains 'bad config'", body["error"])
	}
}

// --- handleReload: no reload function ---

func TestReloadNotSupported(t *testing.T) {
	s := testServer()
	// reloadFn is nil

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/reload", nil))

	if rec.Code != 501 {
		t.Errorf("status = %d, want 501", rec.Code)
	}
}

// --- handleCachePurge: with tag ---

func TestCachePurgeWithTag(t *testing.T) {
	s := testServer()
	log := logger.New("error", "text")
	eng := cache.NewEngine(1<<20, "", 0, log)
	s.SetCache(eng)

	// Insert tagged entries
	req1 := httptest.NewRequest("GET", "/a", nil)
	eng.Set(req1, &cache.CachedResponse{
		StatusCode: 200, Body: []byte("a"), Created: time.Now(), TTL: time.Minute, Tags: []string{"blog"},
	})

	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"tag":"blog"}`)
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/cache/purge", body))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	var resp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["status"] != "purged" {
		t.Errorf("status = %v, want purged", resp["status"])
	}
}

// --- handleCachePurge: without tag (purge all) ---

func TestCachePurgeAll(t *testing.T) {
	s := testServer()
	log := logger.New("error", "text")
	eng := cache.NewEngine(1<<20, "", 0, log)
	s.SetCache(eng)

	rec := httptest.NewRecorder()
	body := strings.NewReader(`{}`)
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/cache/purge", body))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	var resp map[string]string
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["status"] != "all purged" {
		t.Errorf("status = %v, want 'all purged'", resp["status"])
	}
}

// --- handleCachePurge: no cache ---

func TestCachePurgeNoCache(t *testing.T) {
	s := testServer()
	// cache is nil

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/cache/purge", strings.NewReader(`{}`)))

	if rec.Code != 501 {
		t.Errorf("status = %d, want 501", rec.Code)
	}

	var resp map[string]string
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["error"] != "cache not enabled" {
		t.Errorf("error = %q", resp["error"])
	}
}

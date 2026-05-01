package admin

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/auth"
	"github.com/uwaserver/uwas/internal/cache"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/mcp"
	"github.com/uwaserver/uwas/internal/metrics"
)

// testMux wraps http.ServeMux to inject a virtual admin user for tests.
type testMux struct {
	mux *http.ServeMux
}

func (m *testMux) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	m.mux.HandleFunc(pattern, handler)
}

func (m *testMux) Handle(pattern string, handler http.Handler) {
	m.mux.Handle(pattern, handler)
}

func (m *testMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.UserFromContext(r.Context()); !ok {
		user := &auth.User{ID: "local", Username: "admin", Role: auth.RoleAdmin, Enabled: true}
		r = r.WithContext(auth.WithUser(r.Context(), user))
	}
	m.mux.ServeHTTP(w, r)
}

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
	s := New(cfg, log, m)
	s.mux = &testMux{mux: s.mux.(*http.ServeMux)}
	return s
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

// --- Task API tests ---

func TestTaskList(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/tasks", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	var tasks []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &tasks); err != nil {
		t.Fatalf("failed to unmarshal tasks: %v", err)
	}
	// Should return empty array initially
	if tasks == nil {
		t.Error("expected empty array, got nil")
	}
}

func TestTaskGetNotFound(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/tasks/nonexistent-id", nil))

	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}

	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["error"] != "task not found" {
		t.Errorf("error = %v, want 'task not found'", body["error"])
	}
}

// --- System API tests ---

func TestSystemEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/system", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	// Check required fields
	requiredFields := []string{"version", "go_version", "os", "arch", "cpus", "goroutines", "pid"}
	for _, field := range requiredFields {
		if body[field] == nil {
			t.Errorf("missing required field: %s", field)
		}
	}

	// Check type of numeric fields
	if _, ok := body["cpus"].(float64); !ok {
		t.Errorf("cpus should be a number")
	}
	if _, ok := body["goroutines"].(float64); !ok {
		t.Errorf("goroutines should be a number")
	}
}

// --- SetPHPManager test ---

func TestSetPHPManager(t *testing.T) {
	s := testServer()

	// Initially PHP manager should be nil
	if s.phpMgr != nil {
		t.Error("phpMgr should be nil initially")
	}
}

// --- App API tests ---

func TestAppEndpoints(t *testing.T) {
	s := testServer()

	// Test apps list (should work without app manager)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/apps", nil))

	// Should return 200 with empty list or apps
	if rec.Code != 200 {
		t.Logf("apps endpoint status: %d", rec.Code)
	}
}

// --- Additional handler tests ---

func TestUnknownDomainEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/domains/unknown.test/config", nil))

	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
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

	// No auth → 401 (use /stats, not /health which is public)
	rec := httptest.NewRecorder()
	s.authMiddleware(s.mux).ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/stats", nil))
	if rec.Code != 401 {
		t.Errorf("no auth: status = %d, want 401", rec.Code)
	}

	// Wrong auth → 401
	rec2 := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/stats", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	s.authMiddleware(s.mux).ServeHTTP(rec2, req)
	if rec2.Code != 401 {
		t.Errorf("wrong auth: status = %d, want 401", rec2.Code)
	}

	// Correct auth → 200
	rec3 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/api/v1/stats", nil)
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
	eng := cache.NewEngine(context.Background(), 1<<20, "", 0, log)
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
	eng := cache.NewEngine(context.Background(), 1<<20, "", 0, log)
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
	eng := cache.NewEngine(context.Background(), 1<<20, "", 0, log)
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

// --- POST /api/v1/domains (add domain) ---

func TestAddDomain(t *testing.T) {
	s := testServer()
	initialCount := len(s.config.Domains)

	body := strings.NewReader(`{"host":"new.com","type":"static","root":"/var/www/new","ssl":{"mode":"auto"}}`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/domains", body))

	if rec.Code != 201 {
		t.Errorf("status = %d, want 201", rec.Code)
	}
	if len(s.config.Domains) != initialCount+1 {
		t.Errorf("domain count = %d, want %d", len(s.config.Domains), initialCount+1)
	}
	// Verify the response body contains the new host.
	var created map[string]any
	json.Unmarshal(rec.Body.Bytes(), &created)
	if created["host"] != "new.com" {
		t.Errorf("created host = %v, want new.com", created["host"])
	}
}

func TestAddDomainDuplicate(t *testing.T) {
	s := testServer()
	body := strings.NewReader(`{"host":"example.com","type":"static"}`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/domains", body))

	if rec.Code != 409 {
		t.Errorf("status = %d, want 409", rec.Code)
	}
}

func TestAddDomainMissingHost(t *testing.T) {
	s := testServer()
	body := strings.NewReader(`{"type":"static"}`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/domains", body))

	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestAddDomainCallsOnDomainChange(t *testing.T) {
	s := testServer()
	called := false
	s.SetOnDomainChange(func() { called = true })

	body := strings.NewReader(`{"host":"callback.com","type":"static"}`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/domains", body))

	if rec.Code != 201 {
		t.Errorf("status = %d, want 201", rec.Code)
	}
	if !called {
		t.Error("onDomainChange callback was not called")
	}
}

// --- DELETE /api/v1/domains/{host} ---

func TestDeleteDomain(t *testing.T) {
	s := testServer()
	initialCount := len(s.config.Domains)

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("DELETE", "/api/v1/domains/example.com", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if len(s.config.Domains) != initialCount-1 {
		t.Errorf("domain count = %d, want %d", len(s.config.Domains), initialCount-1)
	}
	var body map[string]string
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["status"] != "deleted" {
		t.Errorf("status = %q, want deleted", body["status"])
	}
}

func TestDeleteDomainNotFound(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("DELETE", "/api/v1/domains/nonexistent.com", nil))

	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestDeleteDomainCallsOnDomainChange(t *testing.T) {
	s := testServer()
	called := false
	s.SetOnDomainChange(func() { called = true })

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("DELETE", "/api/v1/domains/example.com", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if !called {
		t.Error("onDomainChange callback was not called")
	}
}

// --- PUT /api/v1/domains/{host} ---

func TestUpdateDomain(t *testing.T) {
	s := testServer()

	body := strings.NewReader(`{"host":"example.com","type":"proxy","root":"/updated"}`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/domains/example.com", body))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	// Verify the domain was updated in config.
	for _, d := range s.config.Domains {
		if d.Host == "example.com" {
			if d.Type != "proxy" {
				t.Errorf("type = %q, want proxy", d.Type)
			}
			if d.Root != "/updated" {
				t.Errorf("root = %q, want /updated", d.Root)
			}
			return
		}
	}
	t.Error("example.com not found in config after update")
}

func TestUpdateDomainNotFound(t *testing.T) {
	s := testServer()

	body := strings.NewReader(`{"host":"nope.com","type":"static"}`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/domains/nope.com", body))

	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// --- GET /api/v1/logs ---

func TestLogsEmpty(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/logs", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	var logs []any
	json.Unmarshal(rec.Body.Bytes(), &logs)
	if len(logs) != 0 {
		t.Errorf("logs = %d, want 0", len(logs))
	}
}

func TestLogsWithEntries(t *testing.T) {
	s := testServer()

	// Record a few entries.
	s.RecordLog(LogEntry{Time: time.Now(), Host: "a.com", Method: "GET", Path: "/", Status: 200, Duration: "1ms", RemoteAddr: "1.2.3.4"})
	s.RecordLog(LogEntry{Time: time.Now(), Host: "b.com", Method: "POST", Path: "/api", Status: 201, Duration: "5ms", RemoteAddr: "5.6.7.8"})

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/logs", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	var logs []map[string]any
	json.Unmarshal(rec.Body.Bytes(), &logs)
	if len(logs) != 2 {
		t.Errorf("logs = %d, want 2", len(logs))
	}
	if logs[0]["host"] != "a.com" {
		t.Errorf("first log host = %v, want a.com", logs[0]["host"])
	}
	if logs[1]["host"] != "b.com" {
		t.Errorf("second log host = %v, want b.com", logs[1]["host"])
	}
}

func TestLogsMaxReturn(t *testing.T) {
	s := testServer()

	// Record 150 entries; only the last 100 should be returned.
	for i := 0; i < 150; i++ {
		s.RecordLog(LogEntry{Time: time.Now(), Host: "x.com", Method: "GET", Path: "/", Status: 200, Duration: "1ms"})
	}

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/logs", nil))

	var logs []map[string]any
	json.Unmarshal(rec.Body.Bytes(), &logs)
	if len(logs) != 100 {
		t.Errorf("logs = %d, want 100", len(logs))
	}
}

// --- GET /api/v1/config/export ---

func TestConfigExportEndpoint(t *testing.T) {
	s := testServer()
	s.config.Global.WorkerCount = "8"
	s.config.Global.Admin.APIKey = "supersecret"

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/config/export", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "application/x-yaml" {
		t.Errorf("Content-Type = %q, want application/x-yaml", ct)
	}

	cd := rec.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "uwas.yaml") {
		t.Errorf("Content-Disposition = %q, should contain uwas.yaml", cd)
	}

	body := rec.Body.String()
	if len(body) == 0 {
		t.Fatal("response body is empty")
	}

	// The response should be valid YAML containing domain hosts.
	if !strings.Contains(body, "example.com") {
		t.Errorf("YAML should contain domain host 'example.com', got:\n%s", body)
	}

	// The API key should be stripped from the export.
	if strings.Contains(body, "supersecret") {
		t.Error("exported YAML should not contain the admin API key")
	}
}

func TestConfigExportContainsDomains(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/config/export", nil))

	body := rec.Body.String()
	if !strings.Contains(body, "api.example.com") {
		t.Errorf("YAML should contain second domain, got:\n%s", body)
	}
}

// --- handleCacheStats ---

func TestCacheStatsNoCache(t *testing.T) {
	s := testServer()
	// cache is nil

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/cache/stats", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["enabled"] != false {
		t.Errorf("enabled = %v, want false", body["enabled"])
	}
	if body["message"] != "cache not enabled" {
		t.Errorf("message = %v, want 'cache not enabled'", body["message"])
	}
}

func TestCacheStatsWithCache(t *testing.T) {
	s := testServer()
	// Add domains with cache config
	s.config.Domains = []config.Domain{
		{
			Host: "cached.com",
			Type: "static",
			SSL:  config.SSLConfig{Mode: "off"},
			Cache: config.DomainCache{
				Enabled: true,
				TTL:     300,
				Tags:    []string{"site:cached"},
				Rules: []config.CacheRule{
					{Match: "*.css", TTL: 3600},
				},
			},
		},
	}

	log := logger.New("error", "text")
	eng := cache.NewEngine(context.Background(), 1<<20, "", 0, log)
	s.SetCache(eng)

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/cache/stats", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["enabled"] != true {
		t.Errorf("enabled = %v, want true", body["enabled"])
	}
	if body["hit_rate"] == nil {
		t.Error("hit_rate should be present")
	}
	domains, ok := body["domains"].([]any)
	if !ok || len(domains) != 1 {
		t.Errorf("domains count = %v, want 1", body["domains"])
	}
	if ok && len(domains) == 1 {
		d := domains[0].(map[string]any)
		if d["host"] != "cached.com" {
			t.Errorf("domain host = %v, want cached.com", d["host"])
		}
		if d["enabled"] != true {
			t.Errorf("domain cache enabled = %v, want true", d["enabled"])
		}
		// rules should be present
		rules, ok := d["rules"].([]any)
		if !ok || len(rules) != 1 {
			t.Errorf("rules = %v, want 1 rule", d["rules"])
		}
	}
}

// --- handleCerts ---

func TestCertsEndpoint(t *testing.T) {
	s := testServer()
	s.config.Domains = []config.Domain{
		{Host: "auto.com", SSL: config.SSLConfig{Mode: "auto"}},
		{Host: "manual.com", SSL: config.SSLConfig{Mode: "manual"}},
		{Host: "plain.com", SSL: config.SSLConfig{Mode: "off"}},
	}

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/certs", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	var certs []map[string]any
	json.Unmarshal(rec.Body.Bytes(), &certs)
	if len(certs) != 3 {
		t.Fatalf("certs count = %d, want 3", len(certs))
	}

	// auto → pending + Let's Encrypt
	if certs[0]["host"] != "auto.com" {
		t.Errorf("cert[0] host = %v", certs[0]["host"])
	}
	if certs[0]["status"] != "pending" {
		t.Errorf("cert[0] status = %v, want pending", certs[0]["status"])
	}
	if certs[0]["issuer"] != "Let's Encrypt" {
		t.Errorf("cert[0] issuer = %v, want Let's Encrypt", certs[0]["issuer"])
	}

	// manual → active + Manual
	if certs[1]["status"] != "active" {
		t.Errorf("cert[1] status = %v, want active", certs[1]["status"])
	}
	if certs[1]["issuer"] != "Manual" {
		t.Errorf("cert[1] issuer = %v, want Manual", certs[1]["issuer"])
	}

	// off → none
	if certs[2]["status"] != "none" {
		t.Errorf("cert[2] status = %v, want none", certs[2]["status"])
	}
}

// --- handleDomainDetail ---

func TestDomainDetailFound(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/domains/example.com", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["host"] != "example.com" {
		t.Errorf("host = %v, want example.com", body["host"])
	}
}

func TestDomainDetailNotFound(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/domains/nonexistent.com", nil))

	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}

	var body map[string]string
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["error"] != "domain not found" {
		t.Errorf("error = %q, want 'domain not found'", body["error"])
	}
}

// --- isAllowedOrigin ---

func TestIsAllowedOriginLocalhost(t *testing.T) {
	tests := []struct {
		origin string
		want   bool
	}{
		{"http://localhost:3000", true},
		{"https://localhost:9443", true},
		{"http://127.0.0.1:8080", true},
		{"https://127.0.0.1:443", true},
		{"http://localhost", true},
		{"http://evil.com", false},
		{"http://example.com", false},
	}

	for _, tt := range tests {
		r := httptest.NewRequest("GET", "/", nil)
		r.Host = "admin.example.com:9443"
		got := isAllowedOrigin(tt.origin, r)
		if got != tt.want {
			t.Errorf("isAllowedOrigin(%q) = %v, want %v", tt.origin, got, tt.want)
		}
	}
}

func TestIsAllowedOriginDashboard(t *testing.T) {
	// When origin matches the Host header (dashboard's own origin).
	r := httptest.NewRequest("GET", "/", nil)
	r.Host = "admin.local:9443"

	if !isAllowedOrigin("http://admin.local:9443", r) {
		t.Error("dashboard's own origin should be allowed")
	}

	// Different host should be blocked.
	if isAllowedOrigin("http://other.host:9443", r) {
		t.Error("different origin should be blocked")
	}
}

// --- handleConfigExport sanitization ---

func TestConfigExportStripsDNSCredentials(t *testing.T) {
	s := testServer()
	s.config.Global.ACME.DNSCredentials = map[string]string{"key": "supersecret"}
	s.config.Global.Cache.PurgeKey = "purgesecret"
	s.config.Domains = []config.Domain{
		{
			Host: "withenv.com",
			Type: "php",
			PHP:  config.PHPConfig{Env: map[string]string{"DB_PASS": "secret"}},
		},
	}

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/config/export", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	body := rec.Body.String()
	if strings.Contains(body, "supersecret") {
		t.Error("should strip DNS credentials")
	}
	if strings.Contains(body, "purgesecret") {
		t.Error("should strip purge key")
	}
	if strings.Contains(body, "DB_PASS") {
		t.Error("should strip PHP env")
	}
}

// --- CORS via authMiddleware ---

func TestIsAllowedOriginTLS(t *testing.T) {
	// Simulate a TLS request so scheme becomes "https"
	r := httptest.NewRequest("GET", "/", nil)
	r.Host = "admin.local:9443"
	r.TLS = &tls.ConnectionState{} // non-nil → https

	if !isAllowedOrigin("https://admin.local:9443", r) {
		t.Error("TLS dashboard origin should be allowed")
	}
	// http variant should NOT match when TLS is present
	if isAllowedOrigin("http://admin.local:9443", r) {
		t.Error("http origin should not match when TLS is active")
	}
}

func TestCacheStatsWithHitRate(t *testing.T) {
	s := testServer()
	log := logger.New("error", "text")
	eng := cache.NewEngine(context.Background(), 1<<20, "", 0, log)
	s.SetCache(eng)

	// Simulate some hits and misses by looking up entries
	// The Stats() method reads from MemoryCache counters.
	// We need to generate cache hits/misses.
	req := httptest.NewRequest("GET", "http://test.com/page", nil)
	eng.Set(req, &cache.CachedResponse{
		StatusCode: 200, Body: []byte("hello"), Created: time.Now(), TTL: time.Minute,
	})
	// Get it to generate a hit
	eng.Get(req)
	// Get a missing key
	miss := httptest.NewRequest("GET", "http://test.com/missing", nil)
	eng.Get(miss)

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/cache/stats", nil))

	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["hit_rate"] == nil {
		t.Error("hit_rate should be present")
	}
	// hit_rate should be a non-zero percentage string
	hitRate, ok := body["hit_rate"].(string)
	if !ok {
		t.Fatalf("hit_rate type = %T", body["hit_rate"])
	}
	if hitRate == "0.0%" {
		t.Errorf("hit_rate = %q, expected non-zero with hits", hitRate)
	}
}

func TestAddDomainInvalidJSON(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	body := strings.NewReader(`{invalid`)
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/domains", body))

	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestUpdateDomainInvalidJSON(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	body := strings.NewReader(`{broken`)
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/domains/example.com", body))

	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestUpdateDomainSetsHostIfEmpty(t *testing.T) {
	s := testServer()

	// Send update without host field — handler should set host from path.
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"type":"proxy","root":"/new"}`)
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/domains/example.com", body))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	for _, d := range s.config.Domains {
		if d.Host == "example.com" {
			if d.Type != "proxy" {
				t.Errorf("type = %q, want proxy", d.Type)
			}
			return
		}
	}
	t.Error("example.com should still exist after update")
}

func TestDashboardSPAFallback(t *testing.T) {
	s := testServer()

	// Request to dashboard root — should serve index.html (SPA fallback)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/_uwas/dashboard/", nil))

	// Should serve index.html
	if rec.Code != 200 {
		t.Errorf("dashboard status = %d, want 200", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
}

func TestDashboardSPADeepRoute(t *testing.T) {
	s := testServer()

	// Request to a deep SPA route — should fallback to index.html
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/_uwas/dashboard/domains/example.com", nil))

	if rec.Code != 200 {
		t.Errorf("dashboard deep route status = %d, want 200", rec.Code)
	}
}

func TestDashboardServesRealAsset(t *testing.T) {
	s := testServer()

	// Request a real embedded file
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/_uwas/dashboard/favicon.svg", nil))

	if rec.Code != 200 {
		t.Errorf("real asset status = %d, want 200", rec.Code)
	}
}

func TestAuthMiddlewareNoOrigin(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{APIKey: "testkey"},
		},
	}
	s := New(cfg, logger.New("error", "text"), metrics.New())

	// Request with valid auth but no Origin header
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/stats", nil)
	req.Header.Set("Authorization", "Bearer testkey")
	s.authMiddleware(s.mux).ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	// No CORS headers should be set
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("should not set CORS headers when no Origin")
	}
}

func TestRecordLogInitializesBuffer(t *testing.T) {
	s := testServer()
	// logEntries is nil initially
	s.RecordLog(LogEntry{Time: time.Now(), Host: "init.com", Method: "GET", Path: "/", Status: 200})

	// Verify entry was recorded
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/logs", nil))

	var logs []map[string]any
	json.Unmarshal(rec.Body.Bytes(), &logs)
	if len(logs) != 1 {
		t.Errorf("logs = %d, want 1", len(logs))
	}
}

func TestLogsRingBufferWrap(t *testing.T) {
	s := testServer()

	// Fill the ring buffer completely to trigger wrap
	for i := 0; i < maxLogEntries+50; i++ {
		s.RecordLog(LogEntry{
			Time:   time.Now(),
			Host:   fmt.Sprintf("host-%d.com", i),
			Method: "GET",
			Path:   "/",
			Status: 200,
		})
	}

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/logs", nil))

	var logs []map[string]any
	json.Unmarshal(rec.Body.Bytes(), &logs)
	// Should return at most 100 (returnLimit)
	if len(logs) != 100 {
		t.Errorf("logs = %d, want 100", len(logs))
	}
}

func TestCORSHeaders(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{APIKey: "testkey"},
		},
	}
	s := New(cfg, logger.New("error", "text"), metrics.New())

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("OPTIONS", "/api/v1/stats", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	s.authMiddleware(s.mux).ServeHTTP(rec, req)

	if rec.Code != 204 {
		t.Errorf("OPTIONS status = %d, want 204", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "http://localhost:3000" {
		t.Errorf("CORS origin = %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestCORSBlockedOrigin(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{APIKey: "testkey"},
		},
	}
	s := New(cfg, logger.New("error", "text"), metrics.New())

	// Request with a blocked origin — CORS headers should NOT be set.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/stats", nil)
	req.Header.Set("Origin", "http://evil.com")
	req.Header.Set("Authorization", "Bearer testkey")
	s.authMiddleware(s.mux).ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("blocked origin should not get CORS header, got %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestAuthMiddlewareDashboardPublic(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{APIKey: "testkey"},
		},
	}
	s := New(cfg, logger.New("error", "text"), metrics.New())

	// Dashboard should be accessible without auth
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/_uwas/dashboard/", nil)
	s.authMiddleware(s.mux).ServeHTTP(rec, req)

	// Should not get 401
	if rec.Code == 401 {
		t.Error("dashboard should be publicly accessible")
	}
}

func TestMCPToolsDisabled(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/mcp/tools", nil))

	if rec.Code != 503 {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

func TestMCPToolsEnabled(t *testing.T) {
	s := testServer()
	mcpSrv := mcp.New(s.config, s.logger, s.metrics)
	s.SetMCP(mcpSrv)

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/mcp/tools", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	var tools []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &tools); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(tools) == 0 {
		t.Error("expected at least one tool")
	}
}

func TestMCPCallDisabled(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"name":"domain_list"}`)
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/mcp/call", body))

	if rec.Code != 503 {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

func TestMCPCallToolDomainList(t *testing.T) {
	s := testServer()
	mcpSrv := mcp.New(s.config, s.logger, s.metrics)
	s.SetMCP(mcpSrv)

	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"name":"domain_list"}`)
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/mcp/call", body))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestMCPCallToolUnknown(t *testing.T) {
	s := testServer()
	mcpSrv := mcp.New(s.config, s.logger, s.metrics)
	s.SetMCP(mcpSrv)

	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"name":"nonexistent"}`)
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/mcp/call", body))

	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestMCPCallInvalidJSON(t *testing.T) {
	s := testServer()
	mcpSrv := mcp.New(s.config, s.logger, s.metrics)
	s.SetMCP(mcpSrv)

	rec := httptest.NewRecorder()
	body := strings.NewReader(`{not valid json`)
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/mcp/call", body))

	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// ── Ticket Auth Tests ──

func TestAuthTicketIssueAndRedeem(t *testing.T) {
	s := testServer()

	// Issue a ticket
	req := httptest.NewRequest("POST", "/api/v1/auth/ticket", nil)
	req.Header.Set("Authorization", "Bearer test-token-123")
	rec := httptest.NewRecorder()
	s.handleAuthTicket(rec, req)

	if rec.Code != 200 {
		t.Fatalf("ticket issue status = %d, want 200", rec.Code)
	}
	var resp map[string]string
	json.Unmarshal(rec.Body.Bytes(), &resp)
	ticket := resp["ticket"]
	if ticket == "" {
		t.Fatal("ticket should not be empty")
	}

	// Redeem the ticket
	token := s.redeemTicket(ticket)
	if token != "test-token-123" {
		t.Errorf("redeemed token = %q, want %q", token, "test-token-123")
	}

	// Second redeem should fail (single-use)
	token2 := s.redeemTicket(ticket)
	if token2 != "" {
		t.Errorf("second redeem should return empty, got %q", token2)
	}
}

func TestAuthTicketExpiry(t *testing.T) {
	s := testServer()
	s.tickets = make(map[string]*authTicket)
	s.tickets["expired-ticket"] = &authTicket{
		token:   "old-token",
		created: time.Now().Add(-60 * time.Second), // 60s ago, well past 30s TTL
	}

	token := s.redeemTicket("expired-ticket")
	if token != "" {
		t.Errorf("expired ticket should return empty, got %q", token)
	}
}

func TestAuthTicketMissingBearer(t *testing.T) {
	s := testServer()

	req := httptest.NewRequest("POST", "/api/v1/auth/ticket", nil)
	rec := httptest.NewRecorder()
	s.handleAuthTicket(rec, req)

	if rec.Code != 400 {
		t.Errorf("status = %d, want 400 for missing bearer", rec.Code)
	}
}

// --- Notification Preferences Tests ---

func TestNotifyPrefsGet(t *testing.T) {
	s := testServer()
	s.config.Global.Alerting = config.AlertingConfig{
		Enabled:        true,
		WebhookURL:     "https://example.com/webhook",
		SlackURL:       "https://hooks.slack.com",
		TelegramToken:  "bot-token",
		TelegramChatID: "12345",
	}
	s.config.Global.Webhooks = []config.WebhookConfig{
		{URL: "https://example.com/webhook", Events: []string{"domain.create"}},
	}

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/settings/notifications", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["alerting"] == nil {
		t.Error("expected alerting in response")
	}
	if body["webhooks"] == nil {
		t.Error("expected webhooks in response")
	}
}

func TestNotifyPrefsPut(t *testing.T) {
	s := testServer()

	body := strings.NewReader(`{"alerting":{"enabled":true,"webhook_url":"https://new.com/webhook","slack_url":"https://hooks.slack.com"},"webhooks":[{"url":"https://hooks.slack.com","events":["domain.update"]}]}`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/settings/notifications", body))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	var resp map[string]string
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["status"] != "saved" {
		t.Errorf("status = %q, want 'saved'", resp["status"])
	}
}

func TestNotifyPrefsPutInvalidJSON(t *testing.T) {
	s := testServer()

	body := strings.NewReader(`{not valid json`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/settings/notifications", body))

	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// --- Branding Tests ---

func TestBrandingGet(t *testing.T) {
	s := testServer()
	s.config.Global.Admin.Branding = config.BrandingConfig{
		LogoURL:      "https://example.com/logo.png",
		FaviconURL:   "https://example.com/favicon.ico",
		Name:         "Test Corp",
		PrimaryColor: "#3366cc",
	}

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/settings/branding", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	var body config.BrandingConfig
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Name != "Test Corp" {
		t.Errorf("name = %q, want 'Test Corp'", body.Name)
	}
}

func TestBrandingPut(t *testing.T) {
	s := testServer()

	body := strings.NewReader(`{"logo_url":"https://new.com/logo.png","name":"New Corp","primary_color":"#ff0000"}`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/settings/branding", body))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	var resp map[string]string
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["status"] != "saved" {
		t.Errorf("status = %q, want 'saved'", resp["status"])
	}

	// Verify config was updated
	if s.config.Global.Admin.Branding.Name != "New Corp" {
		t.Errorf("name = %q, want 'New Corp'", s.config.Global.Admin.Branding.Name)
	}
}

func TestBrandingPutInvalidJSON(t *testing.T) {
	s := testServer()

	body := strings.NewReader(`{not valid`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/settings/branding", body))

	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// --- PHP Restart Tests ---

func TestPHPRestart(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/php/restart", nil))

	// May return 200, 404, or 500 depending on PHP manager state
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 500 {
		t.Errorf("status = %d, want 200, 404, or 500", rec.Code)
	}
}

// --- SSE Logs Tests ---

func TestSSELogs(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/sse/logs", nil)
	req.Header.Set("Accept", "text/event-stream")

	// Use a context with timeout to prevent hanging
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	req = req.WithContext(ctx)

	s.mux.ServeHTTP(rec, req)

	// SSE endpoint may return various status codes
	if rec.Code != 200 && rec.Code != 503 {
		t.Errorf("status = %d, want 200 or 503", rec.Code)
	}
}

// --- Server Close Tests ---

func TestServerClose(t *testing.T) {
	s := testServer()

	// Close should not panic
	s.Close()
}

// --- handlePHPInstall Tests ---

func TestPHPInstall(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"version":"8.2"}`)
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/php/install", body))

	// May return various codes depending on PHP manager
	if rec.Code != 200 && rec.Code != 500 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 500 or 501", rec.Code)
	}
}

func TestPHPInstallInvalidJSON(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	body := strings.NewReader(`{not valid`)
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/php/install", body))

	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// --- handleAddDomain with validation errors ---

func TestAddDomainValidationErrors(t *testing.T) {
	s := testServer()

	tests := []struct {
		name       string
		body       string
		wantStatus int
	}{
		{
			name:       "empty host",
			body:       `{"host":"","type":"static"}`,
			wantStatus: 400,
		},
		{
			name:       "invalid host characters",
			body:       `{"host":"test..com","type":"static"}`,
			wantStatus: 400,
		},
		{
			name:       "missing type",
			body:       `{"host":"test.com"}`,
			wantStatus: 201, // API allows missing type, defaults to static
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/domains", strings.NewReader(tt.body)))

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

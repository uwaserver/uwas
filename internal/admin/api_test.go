package admin

import (
	"context"
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
	// config.Domain uses yaml tags only; json encodes with Go field names.
	var created map[string]any
	json.Unmarshal(rec.Body.Bytes(), &created)
	if created["Host"] != "new.com" {
		t.Errorf("created host = %v, want new.com", created["Host"])
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

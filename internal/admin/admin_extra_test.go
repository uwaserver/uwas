package admin

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/alerting"
	"github.com/uwaserver/uwas/internal/analytics"
	"github.com/uwaserver/uwas/internal/cache"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/metrics"
	"github.com/uwaserver/uwas/internal/monitor"
)

// TestHandleSSEStats tests the Server-Sent Events stats endpoint.
func TestHandleSSEStats(t *testing.T) {
	s := testServer()
	s.metrics.RequestsTotal.Store(100)

	// Create a server and make a real HTTP request so we get flushing support
	srv := httptest.NewServer(s.mux)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/v1/sse/stats", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SSE request: %v", err)
	}
	defer resp.Body.Close()

	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", resp.Header.Get("Content-Type"))
	}

	// Read the first SSE event
	scanner := bufio.NewScanner(resp.Body)
	var gotData bool
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			gotData = true
			data := line[6:]
			var stats map[string]any
			if err := json.Unmarshal([]byte(data), &stats); err != nil {
				t.Fatalf("invalid JSON in SSE event: %v", err)
			}
			if stats["requests_total"] != float64(100) {
				t.Errorf("requests_total = %v, want 100", stats["requests_total"])
			}
			break
		}
	}
	if !gotData {
		t.Error("should receive at least one SSE data event")
	}
}

// TestHandleDomainDetailWithFullDomain tests domain detail with more fields.
func TestHandleDomainDetailWithFullDomain(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{Listen: "127.0.0.1:0"},
		},
		Domains: []config.Domain{
			{
				Host: "detailed.com",
				Type: "php",
				Root: "/var/www/detailed",
				SSL:  config.SSLConfig{Mode: "auto"},
				PHP:  config.PHPConfig{FPMAddress: "127.0.0.1:9000"},
			},
		},
	}
	log := logger.New("error", "text")
	m := metrics.New()
	s := New(cfg, log, m)

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/domains/detailed.com", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["host"] != "detailed.com" {
		t.Errorf("host = %v, want detailed.com", body["host"])
	}
	if body["root"] != "/var/www/detailed" {
		t.Errorf("root = %v, want /var/www/detailed", body["root"])
	}
}

// TestHandleCacheStatsWithMultipleDomains tests cache stats with domains having cache rules.
func TestHandleCacheStatsWithMultipleDomains(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{Listen: "127.0.0.1:0"},
		},
		Domains: []config.Domain{
			{
				Host: "site1.com", Type: "static",
				Cache: config.DomainCache{
					Enabled: true, TTL: 120, Tags: []string{"site1"},
					Rules: []config.CacheRule{
						{Match: "*.css", TTL: 3600},
						{Match: "*.js", TTL: 1800, Bypass: true},
					},
				},
			},
			{
				Host: "site2.com", Type: "proxy",
				Cache: config.DomainCache{Enabled: false},
			},
		},
	}
	log := logger.New("error", "text")
	m := metrics.New()
	s := New(cfg, log, m)

	eng := cache.NewEngine(context.Background(), 1<<20, "", 0, log)
	s.SetCache(eng)

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/cache/stats", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	domains, ok := body["domains"].([]any)
	if !ok || len(domains) != 2 {
		t.Errorf("domains count = %v, want 2", body["domains"])
	}
	if ok && len(domains) >= 1 {
		d := domains[0].(map[string]any)
		rules, ok := d["rules"].([]any)
		if !ok || len(rules) != 2 {
			t.Errorf("rules count for site1 = %v, want 2", d["rules"])
		}
	}
}

// TestAuthMiddlewareOptionsWithBlockedOrigin tests that OPTIONS with blocked origin returns 204 without CORS headers.
func TestAuthMiddlewareOptionsWithBlockedOrigin(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{APIKey: "testkey"},
		},
	}
	s := New(cfg, logger.New("error", "text"), metrics.New())

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("OPTIONS", "/api/v1/stats", nil)
	req.Header.Set("Origin", "http://evil.com")
	s.authMiddleware(s.mux).ServeHTTP(rec, req)

	if rec.Code != 204 {
		t.Errorf("OPTIONS status = %d, want 204", rec.Code)
	}
	// CORS headers should NOT be set for blocked origin
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("should not set CORS for blocked origin")
	}
}

// TestUpdateDomainCallsOnDomainChange verifies the callback is triggered.
func TestUpdateDomainCallsOnDomainChange(t *testing.T) {
	s := testServer()
	called := false
	s.SetOnDomainChange(func() { called = true })

	body := strings.NewReader(`{"type":"proxy"}`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/domains/example.com", body))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if !called {
		t.Error("onDomainChange should be called on update")
	}
}

// TestNotifyDomainChangeNilCallback verifies no panic when callback is nil.
func TestNotifyDomainChangeNilCallback(t *testing.T) {
	s := testServer()
	// onDomainChange is nil
	s.notifyDomainChange() // should not panic
}

// --- handleAlerts ---

func TestHandleAlertsNoAlerter(t *testing.T) {
	s := testServer()
	// alerter is nil

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/alerts", nil))

	if rec.Code != 501 {
		t.Errorf("status = %d, want 501", rec.Code)
	}
	var body map[string]string
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["error"] != "alerting not enabled" {
		t.Errorf("error = %q, want 'alerting not enabled'", body["error"])
	}
}

func TestHandleAlertsWithAlerter(t *testing.T) {
	s := testServer()
	log := logger.New("error", "text")
	a := alerting.New(true, "", log)
	s.SetAlerter(a)

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/alerts", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	// Should return empty array
	body := rec.Body.String()
	if !strings.Contains(body, "[]") {
		t.Errorf("body = %q, want empty array", body)
	}
}

func TestHandleAlertsWithNilAlerts(t *testing.T) {
	s := testServer()
	log := logger.New("error", "text")
	a := alerting.New(false, "", log) // disabled alerter returns nil from Alerts()
	s.SetAlerter(a)

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/alerts", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	// Should return empty array (not null)
	body := rec.Body.String()
	if !strings.Contains(body, "[]") {
		t.Errorf("body = %q, want empty array (not null)", body)
	}
}

// --- handleMonitor ---

func TestHandleMonitorNoMonitor(t *testing.T) {
	s := testServer()
	// monitor is nil

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/monitor", nil))

	if rec.Code != 501 {
		t.Errorf("status = %d, want 501", rec.Code)
	}
	var body map[string]string
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["error"] != "monitor not enabled" {
		t.Errorf("error = %q, want 'monitor not enabled'", body["error"])
	}
}

func TestHandleMonitorWithMonitor(t *testing.T) {
	s := testServer()
	log := logger.New("error", "text")
	m := monitor.New(nil, log) // empty domains
	s.SetMonitor(m)

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/monitor", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// --- SetAlerter / SetMonitor ---

func TestSetAlerter(t *testing.T) {
	s := testServer()
	if s.alerter != nil {
		t.Error("alerter should be nil initially")
	}

	log := logger.New("error", "text")
	a := alerting.New(true, "", log)
	s.SetAlerter(a)

	if s.alerter == nil {
		t.Error("alerter should be set after SetAlerter")
	}
}

func TestSetMonitor(t *testing.T) {
	s := testServer()
	if s.monitor != nil {
		t.Error("monitor should be nil initially")
	}

	log := logger.New("error", "text")
	m := monitor.New(nil, log)
	s.SetMonitor(m)

	if s.monitor == nil {
		t.Error("monitor should be set after SetMonitor")
	}
}

// --- SetConfigPath ---

func TestSetConfigPath(t *testing.T) {
	s := testServer()
	if s.configPath != "" {
		t.Error("configPath should be empty initially")
	}

	s.SetConfigPath("/etc/uwas/uwas.yaml")

	if s.configPath != "/etc/uwas/uwas.yaml" {
		t.Errorf("configPath = %q, want /etc/uwas/uwas.yaml", s.configPath)
	}
}

// --- HTTPServer ---

func TestHTTPServer(t *testing.T) {
	s := testServer()
	// Before Start(), httpSrv is nil
	if s.HTTPServer() != nil {
		t.Error("HTTPServer should be nil before Start()")
	}
}

// --- SetAnalytics ---

func TestSetAnalytics(t *testing.T) {
	s := testServer()
	if s.analytics != nil {
		t.Error("analytics should be nil initially")
	}

	a := analytics.New()
	s.SetAnalytics(a)

	if s.analytics == nil {
		t.Error("analytics should be set after SetAnalytics")
	}
	if s.Analytics() == nil {
		t.Error("Analytics() should return the collector")
	}
}

func TestAnalyticsEndpointsRegistered(t *testing.T) {
	s := testServer()
	a := analytics.New()
	s.SetAnalytics(a)

	// Record some data
	a.RecordFull("example.com", "/", "1.2.3.4:80", "", "", 200, 100)

	// Test all analytics endpoint
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/analytics", nil))
	if rec.Code != 200 {
		t.Errorf("analytics all: status = %d, want 200", rec.Code)
	}

	// Test per-host analytics endpoint
	rec2 := httptest.NewRecorder()
	s.mux.ServeHTTP(rec2, httptest.NewRequest("GET", "/api/v1/analytics/example.com", nil))
	if rec2.Code != 200 {
		t.Errorf("analytics host: status = %d, want 200", rec2.Code)
	}
}

// --- handleConfigRawPut edge cases ---

func TestConfigRawPutWithNoReloadFunc(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	os.WriteFile(cfgPath, []byte("global:\n  log_level: info\n"), 0644)

	s := testServer()
	s.SetConfigPath(cfgPath)
	// reloadFn is nil

	newContent := "global:\n  log_level: debug\n"
	jsonBody, _ := json.Marshal(map[string]string{"content": newContent})
	body := strings.NewReader(string(jsonBody))
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/config/raw", body))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	var resp map[string]string
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["status"] != "saved" {
		t.Errorf("status = %q, want saved", resp["status"])
	}

	// Verify the file was written even without reload
	data, _ := os.ReadFile(cfgPath)
	if string(data) != newContent {
		t.Errorf("file content = %q, want %q", string(data), newContent)
	}
}

// --- handleDomainRawPut edge cases ---

func TestDomainRawPutNoConfigPath(t *testing.T) {
	s := testServer()
	// configPath is empty

	jsonBody, _ := json.Marshal(map[string]string{"content": "host: example.com\ntype: static\n"})
	body := strings.NewReader(string(jsonBody))
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/config/domains/example.com/raw", body))

	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestDomainRawPutReloadError(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	os.WriteFile(cfgPath, []byte("global: {}"), 0644)

	s := testServer()
	s.SetConfigPath(cfgPath)
	s.SetReloadFunc(func() error { return errors.New("domain reload boom") })

	domainYAML := "host: example.com\ntype: static\n"
	jsonBody, _ := json.Marshal(map[string]string{"content": domainYAML})
	body := strings.NewReader(string(jsonBody))
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/config/domains/example.com/raw", body))

	if rec.Code != 500 {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	var resp map[string]string
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if !strings.Contains(resp["error"], "reload failed") {
		t.Errorf("error = %q, want contains 'reload failed'", resp["error"])
	}
}

func TestDomainRawPutWithNoReloadFunc(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	os.WriteFile(cfgPath, []byte("global: {}"), 0644)

	s := testServer()
	s.SetConfigPath(cfgPath)
	// reloadFn is nil

	domainYAML := "host: newsite.com\ntype: static\n"
	jsonBody, _ := json.Marshal(map[string]string{"content": domainYAML})
	body := strings.NewReader(string(jsonBody))
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/config/domains/newsite.com/raw", body))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// --- Auth middleware: no API key configured ---

func TestAuthMiddlewareNoAPIKeyConfigured(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{APIKey: ""},
		},
		Domains: []config.Domain{
			{Host: "example.com", Type: "static"},
		},
	}
	s := New(cfg, logger.New("error", "text"), metrics.New())

	// All endpoints should be accessible when no API key is configured
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/stats", nil)
	s.authMiddleware(s.mux).ServeHTTP(rec, req)

	if rec.Code == 401 {
		t.Error("should not require auth when no API key is configured")
	}
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// --- Auth middleware: TLS request with dashboard origin ---

func TestAuthMiddlewareCORSTLSOrigin(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{APIKey: "testkey"},
		},
	}
	s := New(cfg, logger.New("error", "text"), metrics.New())

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("OPTIONS", "/api/v1/stats", nil)
	req.Header.Set("Origin", "https://127.0.0.1:9443")
	s.authMiddleware(s.mux).ServeHTTP(rec, req)

	if rec.Code != 204 {
		t.Errorf("OPTIONS status = %d, want 204", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "https://127.0.0.1:9443" {
		t.Error("should set CORS headers for 127.0.0.1 origin")
	}
}

// --- handleUpdateDomain: empty host in body preserves URL host ---

func TestUpdateDomainEmptyHostInBody(t *testing.T) {
	s := testServer()

	// Omit host from body; it should be filled from URL path
	body := strings.NewReader(`{"type":"proxy","root":"/var/www/proxy"}`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/domains/example.com", body))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	// Verify host was preserved
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

// --- domainFilePath: invalid host variations ---

func TestDomainFilePathInvalidHosts(t *testing.T) {
	s := testServer()
	s.SetConfigPath("/etc/uwas/uwas.yaml")

	// These should be rejected by domainFilePath
	invalidHosts := []string{"..", ".", "foo/bar", "foo\\bar"}
	for _, host := range invalidHosts {
		_, err := s.domainFilePath(host)
		if err == nil {
			t.Errorf("domainFilePath(%q) should return error", host)
		}
	}
}

// --- handleConfigExport ---

func TestHandleConfigExportSuccess(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/config/export", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if rec.Header().Get("Content-Type") != "application/x-yaml" {
		t.Errorf("Content-Type = %q, want application/x-yaml", rec.Header().Get("Content-Type"))
	}
	if rec.Header().Get("Content-Disposition") != "attachment; filename=uwas.yaml" {
		t.Errorf("Content-Disposition = %q", rec.Header().Get("Content-Disposition"))
	}
}

// --- handleCacheStats: cache not enabled ---

func TestHandleCacheStatsNoCache(t *testing.T) {
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
}

// --- handleCerts ---

func TestHandleCerts(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{Listen: "127.0.0.1:0"},
		},
		Domains: []config.Domain{
			{Host: "auto.com", SSL: config.SSLConfig{Mode: "auto"}},
			{Host: "manual.com", SSL: config.SSLConfig{Mode: "manual"}},
			{Host: "off.com", SSL: config.SSLConfig{Mode: "off"}},
		},
	}
	log := logger.New("error", "text")
	s := New(cfg, log, metrics.New())

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
	// Verify statuses
	for _, c := range certs {
		host := c["host"].(string)
		status := c["status"].(string)
		switch host {
		case "auto.com":
			if status != "pending" {
				t.Errorf("auto.com status = %q, want pending", status)
			}
		case "manual.com":
			if status != "active" {
				t.Errorf("manual.com status = %q, want active", status)
			}
		case "off.com":
			if status != "none" {
				t.Errorf("off.com status = %q, want none", status)
			}
		}
	}
}

// --- RecordLog ---

func TestRecordLogRingBuffer(t *testing.T) {
	s := testServer()

	// Fill the ring buffer past capacity
	for i := 0; i < maxLogEntries+10; i++ {
		s.RecordLog(LogEntry{
			Host:   "test.com",
			Method: "GET",
			Path:   "/",
			Status: 200,
		})
	}

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/logs", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	var logs []LogEntry
	json.Unmarshal(rec.Body.Bytes(), &logs)
	if len(logs) != 100 { // returnLimit = 100
		t.Errorf("logs count = %d, want 100 (return limit)", len(logs))
	}
}

// --- handleLogs: empty ---

func TestHandleLogsEmpty(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/logs", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "[]") {
		t.Errorf("body = %q, want empty array", body)
	}
}

// --- Auth middleware: CORS with valid origin ---

func TestAuthMiddlewareCORSLocalhost(t *testing.T) {
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
		t.Error("should set CORS headers for localhost origin")
	}
}

// --- isAllowedOrigin ---

func TestIsAllowedOriginVariants(t *testing.T) {
	tests := []struct {
		origin string
		host   string
		want   bool
	}{
		{"http://localhost:3000", "admin.example.com", true},
		{"https://localhost:443", "admin.example.com", true},
		{"http://127.0.0.1:8080", "admin.example.com", true},
		{"https://127.0.0.1", "admin.example.com", true},
		{"http://evil.com", "admin.example.com", false},
		{"http://admin.example.com", "admin.example.com", true}, // same origin
	}

	for _, tt := range tests {
		req := httptest.NewRequest("GET", "/", nil)
		req.Host = tt.host
		got := isAllowedOrigin(tt.origin, req)
		if got != tt.want {
			t.Errorf("isAllowedOrigin(%q, host=%q) = %v, want %v", tt.origin, tt.host, got, tt.want)
		}
	}
}

// --- handleDomainDetail: not found ---

func TestHandleDomainDetailNotFound(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/domains/nonexistent.com", nil))

	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// --- handleConfigRawPut: body too large ---

func TestConfigRawPutBodyTooLarge(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	os.WriteFile(cfgPath, []byte("global:\n  log_level: info\n"), 0644)

	s := testServer()
	s.SetConfigPath(cfgPath)

	// Create a body larger than 1MB
	bigBody := strings.NewReader(strings.Repeat("a", 2*1024*1024))
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/config/raw", bigBody))

	if rec.Code != 400 {
		t.Errorf("status = %d, want 400 for oversized body", rec.Code)
	}
}

// --- handleDomainRawPut: body too large ---

func TestDomainRawPutBodyTooLarge(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	os.WriteFile(cfgPath, []byte("global: {}"), 0644)

	s := testServer()
	s.SetConfigPath(cfgPath)

	bigBody := strings.NewReader(strings.Repeat("a", 2*1024*1024))
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/config/domains/example.com/raw", bigBody))

	if rec.Code != 400 {
		t.Errorf("status = %d, want 400 for oversized body", rec.Code)
	}
}

// --- handleConfigRawPut: write to read-only dir ---

func TestConfigRawPutReadOnlyDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("read-only dir test not reliable on Windows")
	}
	if os.Getuid() == 0 {
		t.Skip("root can write to read-only dirs")
	}

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	os.WriteFile(cfgPath, []byte("global:\n  log_level: info\n"), 0644)
	// Make dir read-only so CreateTemp fails
	os.Chmod(dir, 0555)
	defer os.Chmod(dir, 0755)

	s := testServer()
	s.SetConfigPath(cfgPath)

	body := strings.NewReader("global:\n  log_level: debug\n")
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/config/raw", body))

	if rec.Code != 500 {
		t.Errorf("status = %d, want 500 for read-only dir", rec.Code)
	}
}

// --- handleDomainRawGet: read error (not IsNotExist) ---

func TestDomainRawGetReadError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file permission tests not reliable on Windows")
	}

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	os.WriteFile(cfgPath, []byte("global: {}"), 0644)

	// Create a domains.d directory with a file that exists but can't be read
	domainsDir := filepath.Join(dir, "domains.d")
	os.MkdirAll(domainsDir, 0755)
	domainFile := filepath.Join(domainsDir, "perm.com.yaml")
	os.WriteFile(domainFile, []byte("test"), 0644)
	// Make it unreadable
	os.Chmod(domainFile, 0000)

	s := testServer()
	s.SetConfigPath(cfgPath)

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/config/domains/perm.com/raw", nil))

	// Should get an error (500 for permission denied)
	if rec.Code == 200 {
		t.Error("should not return 200 for unreadable file")
	}

	// Restore permissions for cleanup
	os.Chmod(domainFile, 0644)
}

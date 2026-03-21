package admin

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

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

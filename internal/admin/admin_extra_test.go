package admin

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/cache"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/metrics"
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
	if body["Host"] != "detailed.com" {
		t.Errorf("host = %v, want detailed.com", body["Host"])
	}
	if body["Root"] != "/var/www/detailed" {
		t.Errorf("root = %v, want /var/www/detailed", body["Root"])
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

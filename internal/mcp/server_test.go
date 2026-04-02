package mcp

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/cache"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/metrics"
)

func testMCPServer() *Server {
	cfg := &config.Config{
		Global: config.GlobalConfig{WorkerCount: "auto", MaxConnections: 65536, LogLevel: "info"},
		Domains: []config.Domain{
			{Host: "example.com", Type: "static", SSL: config.SSLConfig{Mode: "auto"}},
		},
	}
	return New(cfg, logger.New("error", "text"), metrics.New())
}

func TestListTools(t *testing.T) {
	s := testMCPServer()
	tools := s.ListTools()

	if len(tools) < 4 {
		t.Errorf("tools = %d, want >= 4", len(tools))
	}

	names := make(map[string]bool)
	for _, tool := range tools {
		names[tool.Name] = true
	}

	required := []string{"domain_list", "stats", "config_show", "cache_purge"}
	for _, name := range required {
		if !names[name] {
			t.Errorf("missing tool: %s", name)
		}
	}
}

func TestCallToolDomainList(t *testing.T) {
	s := testMCPServer()
	result, err := s.CallTool("domain_list", nil)
	if err != nil {
		t.Fatal(err)
	}

	data, _ := json.Marshal(result)
	var domains []map[string]string
	json.Unmarshal(data, &domains)

	if len(domains) != 1 {
		t.Errorf("domains = %d, want 1", len(domains))
	}
	if domains[0]["host"] != "example.com" {
		t.Errorf("host = %q", domains[0]["host"])
	}
}

func TestCallToolStats(t *testing.T) {
	s := testMCPServer()
	s.metrics.RequestsTotal.Store(99)

	result, err := s.CallTool("stats", nil)
	if err != nil {
		t.Fatal(err)
	}

	data := result.(map[string]any)
	if data["requests_total"] != int64(99) {
		t.Errorf("requests = %v", data["requests_total"])
	}
}

func TestCallToolUnknown(t *testing.T) {
	s := testMCPServer()
	_, err := s.CallTool("nonexistent", nil)
	if err == nil {
		t.Error("should error for unknown tool")
	}
}

func TestCallToolConfigShow(t *testing.T) {
	s := testMCPServer()
	result, err := s.CallTool("config_show", nil)
	if err != nil {
		t.Fatal(err)
	}

	data := result.(map[string]any)
	if data["domain_count"] != 1 {
		t.Errorf("domain_count = %v", data["domain_count"])
	}
}

func TestSetCache(t *testing.T) {
	s := testMCPServer()
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

func TestCachePurgeWithEngine(t *testing.T) {
	s := testMCPServer()

	log := logger.New("error", "text")
	eng := cache.NewEngine(context.Background(), 1<<20, "", 0, log)
	s.SetCache(eng)

	// Insert entries with tags via the engine's memory cache
	req1 := httptest.NewRequest("GET", "/tagged1", nil)
	eng.Set(req1, &cache.CachedResponse{
		StatusCode: 200, Body: []byte("t1"), Created: time.Now(), TTL: time.Minute, Tags: []string{"blog"},
	})
	req2 := httptest.NewRequest("GET", "/tagged2", nil)
	eng.Set(req2, &cache.CachedResponse{
		StatusCode: 200, Body: []byte("t2"), Created: time.Now(), TTL: time.Minute, Tags: []string{"shop"},
	})

	// Purge by tag
	result, err := s.CallTool("cache_purge", json.RawMessage(`{"tag":"blog"}`))
	if err != nil {
		t.Fatal(err)
	}
	data := result.(map[string]any)
	if data["status"] != "purged" {
		t.Errorf("status = %v, want purged", data["status"])
	}

	// Purge all
	result2, err := s.CallTool("cache_purge", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	data2 := result2.(map[string]string)
	if data2["status"] != "all purged" {
		t.Errorf("status = %v, want 'all purged'", data2["status"])
	}
}

// --- cache_purge with nil cache ---

func TestCachePurgeWithoutEngine(t *testing.T) {
	s := testMCPServer()
	// cache is nil by default

	// Purge by tag should indicate cache not enabled
	result, err := s.CallTool("cache_purge", json.RawMessage(`{"tag":"blog"}`))
	if err != nil {
		t.Fatal(err)
	}
	data := result.(map[string]string)
	if data["status"] != "cache not enabled" {
		t.Errorf("status = %q, want 'cache not enabled'", data["status"])
	}

	// Purge all with nil cache
	result2, err := s.CallTool("cache_purge", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	data2 := result2.(map[string]string)
	if data2["status"] != "cache not enabled" {
		t.Errorf("status = %q, want 'cache not enabled'", data2["status"])
	}
}

// --- domain_get tool ---

func TestCallToolDomainGet(t *testing.T) {
	cfg := &config.Config{
		Domains: []config.Domain{
			{Host: "example.com", Type: "static", Root: "/var/www", SSL: config.SSLConfig{Mode: "auto"}},
			{Host: "api.example.com", Type: "proxy", SSL: config.SSLConfig{Mode: "manual"}},
		},
	}
	s := New(cfg, logger.New("error", "text"), metrics.New())

	// Get existing domain
	result, err := s.CallTool("domain_get", json.RawMessage(`{"host":"example.com"}`))
	if err != nil {
		t.Fatal(err)
	}

	domain := result.(config.Domain)
	if domain.Host != "example.com" {
		t.Errorf("host = %q, want 'example.com'", domain.Host)
	}
	if domain.Type != "static" {
		t.Errorf("type = %q, want 'static'", domain.Type)
	}
}

func TestCallToolDomainGetNotFound(t *testing.T) {
	s := testMCPServer()

	_, err := s.CallTool("domain_get", json.RawMessage(`{"host":"nonexistent.com"}`))
	if err == nil {
		t.Error("expected error for non-existent domain")
	}
	if !strings.Contains(err.Error(), "domain not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCallToolDomainGetInvalidInput(t *testing.T) {
	s := testMCPServer()

	_, err := s.CallTool("domain_get", json.RawMessage(`{"invalid":123}`))
	if err == nil {
		t.Error("expected error for invalid input")
	}
}

// --- domain_types tool ---

func TestCallToolDomainTypes(t *testing.T) {
	cfg := &config.Config{
		Domains: []config.Domain{
			{Host: "a.com", Type: "static"},
			{Host: "b.com", Type: "static"},
			{Host: "c.com", Type: "php"},
			{Host: "d.com", Type: "proxy"},
		},
	}
	s := New(cfg, logger.New("error", "text"), metrics.New())

	result, err := s.CallTool("domain_types", nil)
	if err != nil {
		t.Fatal(err)
	}

	data := result.(map[string]int)
	if data["static"] != 2 {
		t.Errorf("static = %d, want 2", data["static"])
	}
	if data["php"] != 1 {
		t.Errorf("php = %d, want 1", data["php"])
	}
	if data["proxy"] != 1 {
		t.Errorf("proxy = %d, want 1", data["proxy"])
	}
	if data["total"] != 4 {
		t.Errorf("total = %d, want 4", data["total"])
	}
}

func TestCallToolDomainTypesEmpty(t *testing.T) {
	cfg := &config.Config{
		Domains: []config.Domain{},
	}
	s := New(cfg, logger.New("error", "text"), metrics.New())

	result, err := s.CallTool("domain_types", nil)
	if err != nil {
		t.Fatal(err)
	}

	data := result.(map[string]int)
	if data["total"] != 0 {
		t.Errorf("total = %d, want 0", data["total"])
	}
}

// --- ssl_status tool ---

func TestCallToolSSLStatus(t *testing.T) {
	cfg := &config.Config{
		Domains: []config.Domain{
			{Host: "a.com", Type: "static", SSL: config.SSLConfig{Mode: "auto"}},
			{Host: "b.com", Type: "php", SSL: config.SSLConfig{Mode: "off"}},
			{Host: "c.com", Type: "proxy", SSL: config.SSLConfig{Mode: "manual"}},
		},
	}
	s := New(cfg, logger.New("error", "text"), metrics.New())

	result, err := s.CallTool("ssl_status", nil)
	if err != nil {
		t.Fatal(err)
	}

	data, _ := json.Marshal(result)
	var statuses []map[string]string
	json.Unmarshal(data, &statuses)

	if len(statuses) != 3 {
		t.Fatalf("expected 3 statuses, got %d", len(statuses))
	}

	modes := make(map[string]string)
	for _, s := range statuses {
		modes[s["host"]] = s["ssl_mode"]
	}

	if modes["a.com"] != "auto" {
		t.Errorf("a.com ssl_mode = %q, want 'auto'", modes["a.com"])
	}
	if modes["b.com"] != "off" {
		t.Errorf("b.com ssl_mode = %q, want 'off'", modes["b.com"])
	}
	if modes["c.com"] != "manual" {
		t.Errorf("c.com ssl_mode = %q, want 'manual'", modes["c.com"])
	}
}

// --- security_overview tool ---

func TestCallToolSecurityOverview(t *testing.T) {
	cfg := &config.Config{
		Domains: []config.Domain{
			{
				Host: "secure.com",
				Type: "static",
				Security: config.SecurityConfig{
					WAF: config.WAFConfig{Enabled: true},
					RateLimit: config.RateLimitConfig{Requests: 100},
					IPWhitelist: []string{"192.168.1.1"},
					IPBlacklist: []string{"10.0.0.1"},
				},
			},
			{
				Host: "open.com",
				Type: "php",
				Security: config.SecurityConfig{
					WAF: config.WAFConfig{Enabled: false},
				},
			},
		},
	}
	s := New(cfg, logger.New("error", "text"), metrics.New())

	result, err := s.CallTool("security_overview", nil)
	if err != nil {
		t.Fatal(err)
	}

	data, _ := json.Marshal(result)
	var securities []map[string]any
	json.Unmarshal(data, &securities)

	if len(securities) != 2 {
		t.Fatalf("expected 2 securities, got %d", len(securities))
	}

	// Check first domain (secure.com)
	if securities[0]["host"] != "secure.com" {
		t.Errorf("host = %v", securities[0]["host"])
	}
	if securities[0]["waf"] != true {
		t.Errorf("waf = %v, want true", securities[0]["waf"])
	}
	if securities[0]["rate_limit_rps"].(float64) != 100 {
		t.Errorf("rate_limit = %v, want 100", securities[0]["rate_limit_rps"])
	}
	if securities[0]["ip_whitelist_count"].(float64) != 1 {
		t.Errorf("whitelist = %v, want 1", securities[0]["ip_whitelist_count"])
	}
	if securities[0]["ip_blacklist_count"].(float64) != 1 {
		t.Errorf("blacklist = %v, want 1", securities[0]["ip_blacklist_count"])
	}
}

// --- cache_stats tool ---

func TestCallToolCacheStats(t *testing.T) {
	cfg := &config.Config{}
	m := metrics.New()
	m.CacheHits.Store(80)
	m.CacheMisses.Store(20)

	s := New(cfg, logger.New("error", "text"), m)

	result, err := s.CallTool("cache_stats", nil)
	if err != nil {
		t.Fatal(err)
	}

	data := result.(map[string]any)
	if data["hits"] != int64(80) {
		t.Errorf("hits = %v, want 80", data["hits"])
	}
	if data["misses"] != int64(20) {
		t.Errorf("misses = %v, want 20", data["misses"])
	}
	if data["total"] != int64(100) {
		t.Errorf("total = %v, want 100", data["total"])
	}
	// Hit rate should be 80%
	if data["hit_rate"] != "80.0%" {
		t.Errorf("hit_rate = %v, want '80.0%%'", data["hit_rate"])
	}
}

func TestCallToolCacheStatsEmpty(t *testing.T) {
	cfg := &config.Config{}
	m := metrics.New()

	s := New(cfg, logger.New("error", "text"), m)

	result, err := s.CallTool("cache_stats", nil)
	if err != nil {
		t.Fatal(err)
	}

	data := result.(map[string]any)
	if data["hits"] != int64(0) {
		t.Errorf("hits = %v, want 0", data["hits"])
	}
	// Hit rate should be 0% when no requests
	if data["hit_rate"] != "0.0%" {
		t.Errorf("hit_rate = %v, want '0.0%%'", data["hit_rate"])
	}
}

// --- error_summary tool ---

func TestCallToolErrorSummary(t *testing.T) {
	cfg := &config.Config{}
	m := metrics.New()
	m.RequestsTotal.Store(1000)
	m.BytesSent.Store(1024)
	m.ActiveConns.Store(5)

	s := New(cfg, logger.New("error", "text"), m)

	result, err := s.CallTool("error_summary", nil)
	if err != nil {
		t.Fatal(err)
	}

	data := result.(map[string]any)
	if data["requests_total"] != int64(1000) {
		t.Errorf("requests_total = %v, want 1000", data["requests_total"])
	}
	if data["bytes_sent"] != int64(1024) {
		t.Errorf("bytes_sent = %v, want 1024", data["bytes_sent"])
	}
	if data["active_conns"] != int64(5) {
		t.Errorf("active_conns = %v, want 5", data["active_conns"])
	}
}

// --- config_summary tool ---

func TestCallToolConfigSummary(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			HTTPListen:     ":80",
			HTTPSListen:    ":443",
			HTTP3Enabled:   true,
			MaxConnections: 10000,
			LogLevel:       "info",
			Admin:          config.AdminConfig{Enabled: true},
			Cache:          config.CacheConfig{Enabled: true},
			Backup:         config.BackupConfig{Enabled: false},
		},
		Domains: []config.Domain{
			{Host: "example.com", Type: "static"},
			{Host: "api.example.com", Type: "proxy"},
		},
	}

	s := New(cfg, logger.New("error", "text"), metrics.New())

	result, err := s.CallTool("config_summary", nil)
	if err != nil {
		t.Fatal(err)
	}

	data := result.(map[string]any)
	if data["http_listen"] != ":80" {
		t.Errorf("http_listen = %v", data["http_listen"])
	}
	if data["https_listen"] != ":443" {
		t.Errorf("https_listen = %v", data["https_listen"])
	}
	if data["http3"] != true {
		t.Errorf("http3 = %v, want true", data["http3"])
	}
	if data["admin_enabled"] != true {
		t.Errorf("admin_enabled = %v, want true", data["admin_enabled"])
	}
	if data["cache_enabled"] != true {
		t.Errorf("cache_enabled = %v, want true", data["cache_enabled"])
	}
	if data["backup_enabled"] != false {
		t.Errorf("backup_enabled = %v, want false", data["backup_enabled"])
	}
	if data["max_connections"] != 10000 {
		t.Errorf("max_connections = %v, want 10000", data["max_connections"])
	}
	if data["domain_count"] != 2 {
		t.Errorf("domain_count = %v, want 2", data["domain_count"])
	}
	if data["log_level"] != "info" {
		t.Errorf("log_level = %v", data["log_level"])
	}
}

// --- performance tool ---

func TestCallToolPerformance(t *testing.T) {
	cfg := &config.Config{}
	m := metrics.New()
	m.RequestsTotal.Store(500)
	m.ActiveConns.Store(10)
	m.BytesSent.Store(2048)
	m.CacheHits.Store(400)
	m.CacheMisses.Store(100)
	m.SlowRequests.Store(5)
	m.StaticRequests.Store(200)
	m.PHPRequests.Store(150)
	m.ProxyRequests.Store(150)

	s := New(cfg, logger.New("error", "text"), m)

	result, err := s.CallTool("performance", nil)
	if err != nil {
		t.Fatal(err)
	}

	data := result.(map[string]any)
	if data["requests_total"] != int64(500) {
		t.Errorf("requests_total = %v, want 500", data["requests_total"])
	}
	if data["active_conns"] != int64(10) {
		t.Errorf("active_conns = %v, want 10", data["active_conns"])
	}
	if data["bytes_sent"] != int64(2048) {
		t.Errorf("bytes_sent = %v, want 2048", data["bytes_sent"])
	}
	if data["cache_hits"] != int64(400) {
		t.Errorf("cache_hits = %v, want 400", data["cache_hits"])
	}
	if data["slow_requests"] != int64(5) {
		t.Errorf("slow_requests = %v, want 5", data["slow_requests"])
	}
	if data["static_requests"] != int64(200) {
		t.Errorf("static_requests = %v, want 200", data["static_requests"])
	}
	if data["php_requests"] != int64(150) {
		t.Errorf("php_requests = %v, want 150", data["php_requests"])
	}
	if data["proxy_requests"] != int64(150) {
		t.Errorf("proxy_requests = %v, want 150", data["proxy_requests"])
	}
}

// --- CallTool domain_list with multiple domains ---

func TestCallToolDomainListMultiple(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{WorkerCount: "auto", MaxConnections: 65536, LogLevel: "info"},
		Domains: []config.Domain{
			{Host: "a.com", Type: "static", SSL: config.SSLConfig{Mode: "auto"}},
			{Host: "b.com", Type: "php", SSL: config.SSLConfig{Mode: "off"}},
			{Host: "c.com", Type: "proxy", SSL: config.SSLConfig{Mode: "manual"}},
		},
	}
	s := New(cfg, logger.New("error", "text"), metrics.New())

	result, err := s.CallTool("domain_list", nil)
	if err != nil {
		t.Fatal(err)
	}

	data, _ := json.Marshal(result)
	var domains []map[string]string
	json.Unmarshal(data, &domains)

	if len(domains) != 3 {
		t.Errorf("domains = %d, want 3", len(domains))
	}
}

package mcp

import (
	"encoding/json"
	"net/http/httptest"
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
	eng := cache.NewEngine(1<<20, "", 0, log)
	s.SetCache(eng)

	if s.cache == nil {
		t.Error("cache should be set after SetCache")
	}
}

func TestCachePurgeWithEngine(t *testing.T) {
	s := testMCPServer()

	log := logger.New("error", "text")
	eng := cache.NewEngine(1<<20, "", 0, log)
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

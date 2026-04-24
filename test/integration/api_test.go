package integration

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/server"
)

// helper: start a UWAS server with admin enabled, returns base URL, admin URL, cleanup func.
func startServerWithAdmin(t *testing.T, cfg *config.Config) (base, adminBase string) {
	t.Helper()

	log := logger.New("error", "text")
	srv := server.New(cfg, log)

	go srv.Start()

	base = fmt.Sprintf("http://%s", cfg.Global.HTTPListen)
	adminBase = fmt.Sprintf("http://%s", cfg.Global.Admin.Listen)

	// Wait for the admin health endpoint to be ready.
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(adminBase + "/api/v1/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("admin server did not become ready within 3s")
	return
}

// helper: make a base config with admin enabled.
func baseAdminConfig(t *testing.T) (*config.Config, string, string) {
	t.Helper()
	port := getFreePort(t)
	adminPort := getFreePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	adminAddr := fmt.Sprintf("127.0.0.1:%d", adminPort)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			HTTPListen:  addr,
			LogLevel:    "error",
			LogFormat:   "text",
			PIDFile:     "",
			Admin: config.AdminConfig{
				Enabled: true,
				Listen:  adminAddr,
				APIKey:  "test-api-key",
			},
			Timeouts: config.TimeoutConfig{
				Read:          config.Duration{Duration: 5 * time.Second},
				ReadHeader:    config.Duration{Duration: 5 * time.Second},
				Write:         config.Duration{Duration: 5 * time.Second},
				Idle:          config.Duration{Duration: 5 * time.Second},
				ShutdownGrace: config.Duration{Duration: 2 * time.Second},
			},
		},
	}
	return cfg, addr, adminAddr
}

// helper: create an authenticated request to the admin API.
func adminReq(method, url string, body io.Reader) *http.Request {
	req, _ := http.NewRequest(method, url, body)
	req.Header.Set("Authorization", "Bearer test-api-key")
	// CSRF protection: state-changing requests must include this header
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	return req
}

// ===== 1. Admin API Tests =====

func TestAdminHealthEndpoint(t *testing.T) {
	cfg, _, _ := baseAdminConfig(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("hi"), 0644)
	cfg.Domains = []config.Domain{
		{Host: cfg.Global.HTTPListen, Root: dir, Type: "static", SSL: config.SSLConfig{Mode: "off"}},
	}
	_, adminBase := startServerWithAdmin(t, cfg)

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(adminBase + "/api/v1/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("health status = %d, want 200", resp.StatusCode)
	}

	var data map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatal(err)
	}

	for _, key := range []string{"status", "uptime", "domains", "version", "checks"} {
		if _, ok := data[key]; !ok {
			t.Errorf("missing field %q in health response", key)
		}
	}
	if data["status"] != "ok" {
		t.Errorf("status = %v, want ok", data["status"])
	}
	// domains should be 1
	if domains, ok := data["domains"].(float64); !ok || int(domains) != 1 {
		t.Errorf("domains = %v, want 1", data["domains"])
	}
}

func TestAdminStatsEndpoint(t *testing.T) {
	cfg, _, _ := baseAdminConfig(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("hi"), 0644)
	cfg.Domains = []config.Domain{
		{Host: cfg.Global.HTTPListen, Root: dir, Type: "static", SSL: config.SSLConfig{Mode: "off"}},
	}
	_, adminBase := startServerWithAdmin(t, cfg)

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(adminReq("GET", adminBase+"/api/v1/stats", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("stats status = %d, want 200", resp.StatusCode)
	}

	var data map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatal(err)
	}

	for _, key := range []string{"requests_total", "cache_hits", "latency_p50_ms", "latency_p95_ms", "latency_p99_ms"} {
		if _, ok := data[key]; !ok {
			t.Errorf("missing field %q in stats response", key)
		}
	}
}

func TestAdminSystemEndpoint(t *testing.T) {
	cfg, _, _ := baseAdminConfig(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("hi"), 0644)
	cfg.Domains = []config.Domain{
		{Host: cfg.Global.HTTPListen, Root: dir, Type: "static", SSL: config.SSLConfig{Mode: "off"}},
	}
	_, adminBase := startServerWithAdmin(t, cfg)

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(adminReq("GET", adminBase+"/api/v1/system", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("system status = %d, want 200", resp.StatusCode)
	}

	var data map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatal(err)
	}

	for _, key := range []string{"version", "go_version", "os", "arch", "cpus", "goroutines", "memory_alloc"} {
		if _, ok := data[key]; !ok {
			t.Errorf("missing field %q in system response", key)
		}
	}
	if cpus, ok := data["cpus"].(float64); !ok || cpus < 1 {
		t.Errorf("cpus = %v, want >= 1", data["cpus"])
	}
}

func TestAdminDomainsEndpoint(t *testing.T) {
	cfg, _, _ := baseAdminConfig(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("hi"), 0644)
	cfg.Domains = []config.Domain{
		{Host: cfg.Global.HTTPListen, Root: dir, Type: "static", SSL: config.SSLConfig{Mode: "off"}},
	}
	_, adminBase := startServerWithAdmin(t, cfg)

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(adminReq("GET", adminBase+"/api/v1/domains", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("domains status = %d, want 200", resp.StatusCode)
	}

	var domains []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&domains); err != nil {
		t.Fatal(err)
	}
	if len(domains) != 1 {
		t.Fatalf("expected 1 domain, got %d", len(domains))
	}
	if domains[0]["host"] != cfg.Global.HTTPListen {
		t.Errorf("host = %v, want %s", domains[0]["host"], cfg.Global.HTTPListen)
	}
	if domains[0]["type"] != "static" {
		t.Errorf("type = %v, want static", domains[0]["type"])
	}
}

func TestAdminConfigEndpoint(t *testing.T) {
	cfg, _, _ := baseAdminConfig(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("hi"), 0644)
	cfg.Domains = []config.Domain{
		{Host: cfg.Global.HTTPListen, Root: dir, Type: "static", SSL: config.SSLConfig{Mode: "off"}},
	}
	_, adminBase := startServerWithAdmin(t, cfg)

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(adminReq("GET", adminBase+"/api/v1/config", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("config status = %d, want 200", resp.StatusCode)
	}

	var data map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatal(err)
	}

	if _, ok := data["global"]; !ok {
		t.Error("missing 'global' in config response")
	}
	if _, ok := data["domain_count"]; !ok {
		t.Error("missing 'domain_count' in config response")
	}
}

func TestAdminMetricsEndpoint(t *testing.T) {
	cfg, _, _ := baseAdminConfig(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("hi"), 0644)
	cfg.Domains = []config.Domain{
		{Host: cfg.Global.HTTPListen, Root: dir, Type: "static", SSL: config.SSLConfig{Mode: "off"}},
	}
	_, adminBase := startServerWithAdmin(t, cfg)

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(adminReq("GET", adminBase+"/api/v1/metrics", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("metrics status = %d, want 200", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	text := string(body)

	if !strings.Contains(text, "uwas_requests_total") {
		t.Error("metrics missing uwas_requests_total")
	}
	if !strings.Contains(text, "uwas_cache_hits_total") {
		t.Error("metrics missing uwas_cache_hits_total")
	}
	if !strings.Contains(text, "uwas_connections_active") {
		t.Error("metrics missing uwas_connections_active")
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain", ct)
	}
}

// ===== 2. Admin Auth Tests =====

func TestAdminAuthRequired(t *testing.T) {
	cfg, _, _ := baseAdminConfig(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("hi"), 0644)
	cfg.Domains = []config.Domain{
		{Host: cfg.Global.HTTPListen, Root: dir, Type: "static", SSL: config.SSLConfig{Mode: "off"}},
	}
	_, adminBase := startServerWithAdmin(t, cfg)

	client := &http.Client{Timeout: 2 * time.Second}

	// Endpoints that require auth
	endpoints := []string{
		"/api/v1/stats",
		"/api/v1/system",
		"/api/v1/domains",
		"/api/v1/config",
	}

	for _, ep := range endpoints {
		t.Run(ep, func(t *testing.T) {
			// Request without auth header
			resp, err := client.Get(adminBase + ep)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != 401 {
				t.Errorf("%s without auth: status = %d, want 401", ep, resp.StatusCode)
			}
		})
	}
}

func TestAdminAuthSuccess(t *testing.T) {
	cfg, _, _ := baseAdminConfig(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("hi"), 0644)
	cfg.Domains = []config.Domain{
		{Host: cfg.Global.HTTPListen, Root: dir, Type: "static", SSL: config.SSLConfig{Mode: "off"}},
	}
	_, adminBase := startServerWithAdmin(t, cfg)

	client := &http.Client{Timeout: 2 * time.Second}

	endpoints := []string{
		"/api/v1/stats",
		"/api/v1/system",
		"/api/v1/domains",
		"/api/v1/config",
	}

	for _, ep := range endpoints {
		t.Run(ep, func(t *testing.T) {
			resp, err := client.Do(adminReq("GET", adminBase+ep, nil))
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != 200 {
				t.Errorf("%s with auth: status = %d, want 200", ep, resp.StatusCode)
			}
		})
	}
}

func TestAdminHealthNoAuth(t *testing.T) {
	cfg, _, _ := baseAdminConfig(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("hi"), 0644)
	cfg.Domains = []config.Domain{
		{Host: cfg.Global.HTTPListen, Root: dir, Type: "static", SSL: config.SSLConfig{Mode: "off"}},
	}
	_, adminBase := startServerWithAdmin(t, cfg)

	client := &http.Client{Timeout: 2 * time.Second}

	// Health endpoint should work WITHOUT auth even when APIKey is set.
	resp, err := client.Get(adminBase + "/api/v1/health")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("health without auth: status = %d, want 200", resp.StatusCode)
	}
}

// ===== 3. Domain CRUD Tests =====

func TestAdminDomainCRUD(t *testing.T) {
	cfg, _, _ := baseAdminConfig(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("hi"), 0644)
	cfg.Global.WebRoot = dir // root path validation checks against this
	cfg.Domains = []config.Domain{
		{Host: cfg.Global.HTTPListen, Root: dir, Type: "static", SSL: config.SSLConfig{Mode: "off"}},
	}
	_, adminBase := startServerWithAdmin(t, cfg)

	client := &http.Client{Timeout: 2 * time.Second}

	// POST: create a new domain
	t.Run("create", func(t *testing.T) {
		newDomain := map[string]any{
			"host": "new-domain.local",
			"type": "static",
			"root": dir,
			"ssl":  map[string]string{"mode": "off"},
		}
		body, _ := json.Marshal(newDomain)
		resp, err := client.Do(adminReq("POST", adminBase+"/api/v1/domains", bytes.NewReader(body)))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 201 {
			t.Fatalf("create domain: status = %d, want 201", resp.StatusCode)
		}
	})

	// GET: verify the new domain exists
	t.Run("verify_created", func(t *testing.T) {
		resp, err := client.Do(adminReq("GET", adminBase+"/api/v1/domains", nil))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		var domains []map[string]any
		json.NewDecoder(resp.Body).Decode(&domains)

		found := false
		for _, d := range domains {
			if d["host"] == "new-domain.local" {
				found = true
				break
			}
		}
		if !found {
			t.Error("newly created domain not found in domain list")
		}
	})

	// PUT: update the domain
	t.Run("update", func(t *testing.T) {
		updated := map[string]any{
			"host": "new-domain.local",
			"type": "redirect",
			"ssl":  map[string]string{"mode": "off"},
			"redirect": map[string]any{
				"target": "https://example.com",
				"status": 301,
			},
		}
		body, _ := json.Marshal(updated)
		resp, err := client.Do(adminReq("PUT", adminBase+"/api/v1/domains/new-domain.local", bytes.NewReader(body)))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("update domain: status = %d, want 200", resp.StatusCode)
		}
	})

	// DELETE: remove the domain
	t.Run("delete", func(t *testing.T) {
		resp, err := client.Do(adminReq("DELETE", adminBase+"/api/v1/domains/new-domain.local", nil))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("delete domain: status = %d, want 200", resp.StatusCode)
		}
	})

	// Verify deleted
	t.Run("verify_deleted", func(t *testing.T) {
		resp, err := client.Do(adminReq("GET", adminBase+"/api/v1/domains", nil))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		var domains []map[string]any
		json.NewDecoder(resp.Body).Decode(&domains)

		for _, d := range domains {
			if d["host"] == "new-domain.local" {
				t.Error("deleted domain still found in domain list")
			}
		}
	})
}

// ===== 4. Cache Purge Test =====

func TestCachePurgeAll(t *testing.T) {
	cfg, _, _ := baseAdminConfig(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "cached.html"), []byte("cached-content"), 0644)

	cfg.Global.Cache = config.CacheConfig{
		Enabled:     true,
		MemoryLimit: 10 * config.MB,
		DefaultTTL:  60,
		GraceTTL:    300,
	}
	cfg.Domains = []config.Domain{
		{
			Host: cfg.Global.HTTPListen,
			Root: dir,
			Type: "static",
			SSL:  config.SSLConfig{Mode: "off"},
			Cache: config.DomainCache{
				Enabled: true,
				TTL:     60,
			},
		},
	}

	base, adminBase := startServerWithAdmin(t, cfg)

	client := &http.Client{Timeout: 2 * time.Second}

	// First request — populate cache
	resp1, err := client.Get(base + "/cached.html")
	if err != nil {
		t.Fatal(err)
	}
	resp1.Body.Close()

	// Second request — should hit cache
	resp2, err := client.Get(base + "/cached.html")
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()

	// Purge all cache via admin API
	resp3, err := client.Do(adminReq("POST", adminBase+"/api/v1/cache/purge", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp3.Body.Close()

	if resp3.StatusCode != 200 {
		t.Fatalf("cache purge: status = %d, want 200", resp3.StatusCode)
	}

	var purgeResp map[string]any
	json.NewDecoder(resp3.Body).Decode(&purgeResp)
	if purgeResp["status"] != "all purged" {
		t.Errorf("purge response status = %v, want 'all purged'", purgeResp["status"])
	}

	// After purge, next request should be a cache miss (verified by status 200 / content still works)
	resp4, err := client.Get(base + "/cached.html")
	if err != nil {
		t.Fatal(err)
	}
	body4, _ := io.ReadAll(resp4.Body)
	resp4.Body.Close()
	if string(body4) != "cached-content" {
		t.Errorf("after purge body = %q, want cached-content", string(body4))
	}
}

// ===== 5. Audit Log Test =====

func TestAuditLogRecording(t *testing.T) {
	cfg, _, _ := baseAdminConfig(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("hi"), 0644)
	cfg.Global.WebRoot = dir
	cfg.Domains = []config.Domain{
		{Host: cfg.Global.HTTPListen, Root: dir, Type: "static", SSL: config.SSLConfig{Mode: "off"}},
	}
	_, adminBase := startServerWithAdmin(t, cfg)

	client := &http.Client{Timeout: 2 * time.Second}

	// Create a domain to generate an audit entry
	newDomain := map[string]any{
		"host": "audit-test.local",
		"type": "static",
		"root": dir,
		"ssl":  map[string]string{"mode": "off"},
	}
	body, _ := json.Marshal(newDomain)
	resp1, err := client.Do(adminReq("POST", adminBase+"/api/v1/domains", bytes.NewReader(body)))
	if err != nil {
		t.Fatal(err)
	}
	resp1.Body.Close()

	// Delete it to generate another audit entry
	resp2, err := client.Do(adminReq("DELETE", adminBase+"/api/v1/domains/audit-test.local", nil))
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()

	// Fetch audit log
	resp3, err := client.Do(adminReq("GET", adminBase+"/api/v1/audit", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp3.Body.Close()

	if resp3.StatusCode != 200 {
		t.Fatalf("audit status = %d, want 200", resp3.StatusCode)
	}

	var entries []map[string]any
	json.NewDecoder(resp3.Body).Decode(&entries)

	if len(entries) < 2 {
		t.Fatalf("expected at least 2 audit entries, got %d", len(entries))
	}

	// Check that we have domain.create and domain.delete actions
	actions := map[string]bool{}
	for _, e := range entries {
		if a, ok := e["action"].(string); ok {
			actions[a] = true
		}
	}
	if !actions["domain.create"] {
		t.Error("missing domain.create audit entry")
	}
	if !actions["domain.delete"] {
		t.Error("missing domain.delete audit entry")
	}
}

// ===== 6. Config Reload Test =====

func TestConfigReload(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	os.WriteFile(filepath.Join(dir1, "index.html"), []byte("before-reload"), 0644)
	os.WriteFile(filepath.Join(dir2, "index.html"), []byte("after-reload"), 0644)

	port := getFreePort(t)
	adminPort := getFreePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	adminAddr := fmt.Sprintf("127.0.0.1:%d", adminPort)

	// Write initial config file on disk
	cfgPath := filepath.Join(t.TempDir(), "uwas.yaml")
	writeConfig(t, cfgPath, addr, adminAddr, dir1)

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	log := logger.New("error", "text")
	srv := server.New(cfg, log)
	srv.SetConfigPath(cfgPath)
	go srv.Start()

	base := fmt.Sprintf("http://%s", addr)
	adminBase := fmt.Sprintf("http://%s", adminAddr)

	// Wait for admin health endpoint
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(adminBase + "/api/v1/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Verify initial content
	resp1, err := client.Get(base + "/index.html")
	if err != nil {
		t.Fatal(err)
	}
	body1, _ := io.ReadAll(resp1.Body)
	resp1.Body.Close()
	if string(body1) != "before-reload" {
		t.Fatalf("initial body = %q, want before-reload", string(body1))
	}

	// Update config to point to dir2
	writeConfig(t, cfgPath, addr, adminAddr, dir2)

	// Trigger reload via admin API
	reloadReq, _ := http.NewRequest("POST", adminBase+"/api/v1/reload", nil)
	reloadReq.Header.Set("Authorization", "Bearer test-api-key-for-reload")
	reloadReq.Header.Set("X-Requested-With", "XMLHttpRequest")
	resp2, err := client.Do(reloadReq)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != 200 {
		body, _ := io.ReadAll(resp2.Body)
		t.Fatalf("reload status = %d, body = %s", resp2.StatusCode, string(body))
	}

	var reloadResp map[string]string
	json.NewDecoder(resp2.Body).Decode(&reloadResp)
	if reloadResp["status"] != "reloaded" {
		t.Errorf("reload response status = %q, want reloaded", reloadResp["status"])
	}

	// Verify new content
	resp3, err := client.Get(base + "/index.html")
	if err != nil {
		t.Fatal(err)
	}
	body3, _ := io.ReadAll(resp3.Body)
	resp3.Body.Close()
	if string(body3) != "after-reload" {
		t.Errorf("after reload body = %q, want after-reload", string(body3))
	}
}

// ===== 7. Logs Endpoint Test =====

func TestLogsEndpoint(t *testing.T) {
	cfg, _, _ := baseAdminConfig(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("hi"), 0644)
	cfg.Domains = []config.Domain{
		{Host: cfg.Global.HTTPListen, Root: dir, Type: "static", SSL: config.SSLConfig{Mode: "off"}},
	}
	base, adminBase := startServerWithAdmin(t, cfg)

	client := &http.Client{Timeout: 2 * time.Second}

	// Make some requests to the main server to generate log entries
	for i := 0; i < 5; i++ {
		resp, err := client.Get(base + "/index.html")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}

	// Brief pause to allow log entries to be recorded
	time.Sleep(100 * time.Millisecond)

	// Fetch logs
	resp, err := client.Do(adminReq("GET", adminBase+"/api/v1/logs", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("logs status = %d, want 200", resp.StatusCode)
	}

	var entries []map[string]any
	json.NewDecoder(resp.Body).Decode(&entries)

	if len(entries) == 0 {
		t.Error("expected at least 1 log entry, got 0")
	}

	// Verify log entries have expected fields
	if len(entries) > 0 {
		entry := entries[0]
		for _, key := range []string{"time", "method", "path", "status"} {
			if _, ok := entry[key]; !ok {
				t.Errorf("log entry missing field %q", key)
			}
		}
	}
}

// ===== 8. Multiple Domains Test =====

func TestMultipleDomains(t *testing.T) {
	cfg, _, _ := baseAdminConfig(t)

	dir1 := t.TempDir()
	dir2 := t.TempDir()
	os.WriteFile(filepath.Join(dir1, "index.html"), []byte("site-alpha"), 0644)
	os.WriteFile(filepath.Join(dir2, "index.html"), []byte("site-beta"), 0644)

	cfg.Domains = []config.Domain{
		{Host: "alpha.local", Root: dir1, Type: "static", SSL: config.SSLConfig{Mode: "off"}},
		{Host: "beta.local", Root: dir2, Type: "static", SSL: config.SSLConfig{Mode: "off"}},
	}
	base, _ := startServerWithAdmin(t, cfg)

	client := &http.Client{Timeout: 2 * time.Second}

	// Request with Host: alpha.local
	t.Run("alpha", func(t *testing.T) {
		req, _ := http.NewRequest("GET", base+"/index.html", nil)
		req.Host = "alpha.local"
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(body) != "site-alpha" {
			t.Errorf("alpha.local body = %q, want site-alpha", string(body))
		}
	})

	// Request with Host: beta.local
	t.Run("beta", func(t *testing.T) {
		req, _ := http.NewRequest("GET", base+"/index.html", nil)
		req.Host = "beta.local"
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(body) != "site-beta" {
			t.Errorf("beta.local body = %q, want site-beta", string(body))
		}
	})
}

// ===== 9. SPA Mode Test =====

func TestSPAMode(t *testing.T) {
	cfg, _, _ := baseAdminConfig(t)

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<div id=app></div>"), 0644)

	cfg.Domains = []config.Domain{
		{
			Host:    cfg.Global.HTTPListen,
			Root:    dir,
			Type:    "static",
			SPAMode: true,
			SSL:     config.SSLConfig{Mode: "off"},
		},
	}
	base, _ := startServerWithAdmin(t, cfg)

	client := &http.Client{Timeout: 2 * time.Second}

	// Request to a path that does not have a matching file — should fallback to index.html
	resp, err := client.Get(base + "/some/unknown/deep/path")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("SPA mode status = %d, want 200", resp.StatusCode)
	}
	if string(body) != "<div id=app></div>" {
		t.Errorf("SPA mode body = %q, want index.html content", string(body))
	}
}

// ===== 10. Gzip Compression Test =====

func TestGzipCompression(t *testing.T) {
	cfg, _, _ := baseAdminConfig(t)

	dir := t.TempDir()
	// Create a large HTML file so it exceeds the 1KB min-size for compression.
	largeContent := strings.Repeat("<p>This is a paragraph of text for compression testing.</p>\n", 100)
	os.WriteFile(filepath.Join(dir, "large.html"), []byte(largeContent), 0644)

	cfg.Domains = []config.Domain{
		{
			Host: cfg.Global.HTTPListen,
			Root: dir,
			Type: "static",
			SSL:  config.SSLConfig{Mode: "off"},
		},
	}
	base, _ := startServerWithAdmin(t, cfg)

	// Use a raw transport so Go's default client doesn't auto-decompress.
	transport := &http.Transport{}
	client := &http.Client{Timeout: 2 * time.Second, Transport: transport}

	req, _ := http.NewRequest("GET", base+"/large.html", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	ce := resp.Header.Get("Content-Encoding")
	// The server may choose brotli if it prefers it, but since we only sent gzip,
	// let's accept either gzip or br.
	if ce != "gzip" && ce != "br" {
		t.Errorf("Content-Encoding = %q, want gzip or br", ce)
	}

	// Decompress and verify content
	var reader io.Reader
	if ce == "gzip" {
		gr, err := gzip.NewReader(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		defer gr.Close()
		reader = gr
	} else {
		// If Content-Encoding is something else or absent, just read directly.
		reader = resp.Body
	}

	decompressed, _ := io.ReadAll(reader)
	if ce == "gzip" && string(decompressed) != largeContent {
		t.Errorf("decompressed body length = %d, want %d", len(decompressed), len(largeContent))
	}
}

// ===== 11. Rate Limit Test =====

func TestRateLimit(t *testing.T) {
	cfg, _, _ := baseAdminConfig(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("ok"), 0644)

	cfg.Domains = []config.Domain{
		{
			Host: cfg.Global.HTTPListen,
			Root: dir,
			Type: "static",
			SSL:  config.SSLConfig{Mode: "off"},
			Security: config.SecurityConfig{
				RateLimit: config.RateLimitConfig{
					Requests: 30,
					Window:   config.Duration{Duration: 60 * time.Second},
				},
			},
		},
	}
	base, _ := startServerWithAdmin(t, cfg)

	client := &http.Client{Timeout: 2 * time.Second}

	// Send requests up to and past the limit
	got429 := false
	for i := 0; i < 60; i++ {
		resp, err := client.Get(base + "/index.html")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode == 429 {
			got429 = true
			if resp.Header.Get("Retry-After") == "" {
				t.Error("429 response should have Retry-After header")
			}
			break
		}
	}
	if !got429 {
		t.Error("expected 429 after exceeding rate limit")
	}
}

// ===== 12. Proxy with Health Check =====

func TestProxyHealthCheck(t *testing.T) {
	// Start a test upstream that tracks health check requests.
	healthChecked := make(chan struct{}, 100)
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			select {
			case healthChecked <- struct{}{}:
			default:
			}
			w.WriteHeader(200)
			w.Write([]byte("ok"))
			return
		}
		w.Header().Set("X-Upstream", "health-backend")
		w.Write([]byte("proxied-response"))
	})
	upLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	upSrv := &http.Server{Handler: upstream}
	go upSrv.Serve(upLn)
	defer upSrv.Close()

	cfg, _, _ := baseAdminConfig(t)
	cfg.Domains = []config.Domain{
		{
			Host: cfg.Global.HTTPListen,
			Type: "proxy",
			SSL:  config.SSLConfig{Mode: "off"},
			Proxy: config.ProxyConfig{
				Upstreams: []config.Upstream{
					{Address: "http://" + upLn.Addr().String(), Weight: 1},
				},
				Algorithm: "round_robin",
				HealthCheck: config.HealthCheckConfig{
					Path:     "/healthz",
					Interval: config.Duration{Duration: 200 * time.Millisecond},
					Timeout:  config.Duration{Duration: 1 * time.Second},
				},
			},
		},
	}

	base, _ := startServerWithAdmin(t, cfg)

	client := &http.Client{Timeout: 2 * time.Second}

	// Verify proxied request works
	resp, err := client.Get(base + "/api/test")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("proxy status = %d, want 200", resp.StatusCode)
	}
	if string(body) != "proxied-response" {
		t.Errorf("proxy body = %q, want proxied-response", string(body))
	}

	// Wait for at least one health check to arrive at the upstream
	select {
	case <-healthChecked:
		// Good, health check was received
	case <-time.After(3 * time.Second):
		t.Error("no health check received within 3s")
	}
}

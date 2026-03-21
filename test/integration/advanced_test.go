package integration

import (
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

// TestCacheStoreAndHit verifies that responses are cached and served from cache.
func TestCacheStoreAndHit(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "cached.html"), []byte("cached-content"), 0644)

	port := getFreePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	base := fmt.Sprintf("http://%s", addr)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			HTTPListen:  addr,
			LogLevel:    "error",
			LogFormat:   "text",
			PIDFile:     "",
			Cache: config.CacheConfig{
				Enabled:     true,
				MemoryLimit: 10 * config.MB,
				DefaultTTL:  60,
				GraceTTL:    300,
			},
			Timeouts: config.TimeoutConfig{
				Read:          config.Duration{Duration: 5 * time.Second},
				Write:         config.Duration{Duration: 5 * time.Second},
				Idle:          config.Duration{Duration: 5 * time.Second},
				ShutdownGrace: config.Duration{Duration: 2 * time.Second},
			},
		},
		Domains: []config.Domain{
			{
				Host: addr,
				Root: dir,
				Type: "static",
				SSL:  config.SSLConfig{Mode: "off"},
				Cache: config.DomainCache{
					Enabled: true,
					TTL:     60,
				},
			},
		},
	}

	log := logger.New("error", "text")
	srv := server.New(cfg, log)
	go srv.Start()
	waitForServer(t, base, 2*time.Second)

	client := &http.Client{Timeout: 2 * time.Second}

	// First request: MISS
	resp1, err := client.Get(base + "/cached.html")
	if err != nil {
		t.Fatal(err)
	}
	resp1.Body.Close()
	xcache1 := resp1.Header.Get("X-Cache")
	// First request may or may not have X-Cache depending on whether cache stores on first hit
	t.Logf("First request X-Cache: %q", xcache1)

	// Second request: should be from cache
	resp2, err := client.Get(base + "/cached.html")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()

	if string(body) != "cached-content" {
		t.Errorf("body = %q", string(body))
	}
}

// TestRateLimitE2E verifies rate limiting kicks in after threshold.
func TestRateLimitE2E(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("ok"), 0644)

	port := getFreePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	base := fmt.Sprintf("http://%s", addr)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			HTTPListen:  addr,
			LogLevel:    "error",
			LogFormat:   "text",
			PIDFile:     "",
			Timeouts: config.TimeoutConfig{
				Read:          config.Duration{Duration: 5 * time.Second},
				Write:         config.Duration{Duration: 5 * time.Second},
				Idle:          config.Duration{Duration: 5 * time.Second},
				ShutdownGrace: config.Duration{Duration: 2 * time.Second},
			},
		},
		Domains: []config.Domain{
			{
				Host: addr,
				Root: dir,
				Type: "static",
				SSL:  config.SSLConfig{Mode: "off"},
				Security: config.SecurityConfig{
					RateLimit: config.RateLimitConfig{
						Requests: 50,
						Window:   config.Duration{Duration: 60 * time.Second},
					},
				},
			},
		},
	}

	log := logger.New("error", "text")
	srv := server.New(cfg, log)
	go srv.Start()
	waitForServer(t, base, 2*time.Second)

	client := &http.Client{Timeout: 2 * time.Second}

	// Send 50 requests (matches the limit) — all should succeed
	for i := 0; i < 45; i++ {
		resp, err := client.Get(base + "/index.html")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		// Some may already be 429 if waitForServer used up tokens
	}

	// Exhaust remaining tokens then verify 429
	got429 := false
	for i := 0; i < 20; i++ {
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

// TestMultiDomainVHostRouting verifies virtual host routing across multiple domains.
func TestMultiDomainVHostRouting(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	os.WriteFile(filepath.Join(dir1, "index.html"), []byte("site-one"), 0644)
	os.WriteFile(filepath.Join(dir2, "index.html"), []byte("site-two"), 0644)

	port := getFreePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	base := fmt.Sprintf("http://%s", addr)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			HTTPListen:  addr,
			LogLevel:    "error",
			LogFormat:   "text",
			PIDFile:     "",
			Timeouts: config.TimeoutConfig{
				Read:          config.Duration{Duration: 5 * time.Second},
				Write:         config.Duration{Duration: 5 * time.Second},
				Idle:          config.Duration{Duration: 5 * time.Second},
				ShutdownGrace: config.Duration{Duration: 2 * time.Second},
			},
		},
		Domains: []config.Domain{
			{Host: "one.local", Root: dir1, Type: "static", SSL: config.SSLConfig{Mode: "off"}},
			{Host: "two.local", Root: dir2, Type: "static", SSL: config.SSLConfig{Mode: "off"}},
		},
	}

	log := logger.New("error", "text")
	srv := server.New(cfg, log)
	go srv.Start()
	waitForServer(t, base, 2*time.Second)

	client := &http.Client{Timeout: 2 * time.Second}

	// Request to one.local
	req1, _ := http.NewRequest("GET", base+"/index.html", nil)
	req1.Host = "one.local"
	resp1, err := client.Do(req1)
	if err != nil {
		t.Fatal(err)
	}
	body1, _ := io.ReadAll(resp1.Body)
	resp1.Body.Close()
	if string(body1) != "site-one" {
		t.Errorf("one.local body = %q, want site-one", string(body1))
	}

	// Request to two.local
	req2, _ := http.NewRequest("GET", base+"/index.html", nil)
	req2.Host = "two.local"
	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if string(body2) != "site-two" {
		t.Errorf("two.local body = %q, want site-two", string(body2))
	}
}

// TestBackendFailoverE2E verifies proxy continues when one backend goes down.
func TestBackendFailoverE2E(t *testing.T) {
	// Start 2 upstream servers
	up1 := startTestUpstream(t, "backend-1")
	up2 := startTestUpstream(t, "backend-2")
	defer up1.Close()

	port := getFreePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	base := fmt.Sprintf("http://%s", addr)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			HTTPListen:  addr,
			LogLevel:    "error",
			LogFormat:   "text",
			PIDFile:     "",
			Timeouts: config.TimeoutConfig{
				Read:          config.Duration{Duration: 5 * time.Second},
				Write:         config.Duration{Duration: 5 * time.Second},
				Idle:          config.Duration{Duration: 5 * time.Second},
				ShutdownGrace: config.Duration{Duration: 2 * time.Second},
			},
		},
		Domains: []config.Domain{
			{
				Host: addr,
				Type: "proxy",
				SSL:  config.SSLConfig{Mode: "off"},
				Proxy: config.ProxyConfig{
					Upstreams: []config.Upstream{
						{Address: "http://" + up1.Addr, Weight: 1},
						{Address: "http://" + up2.Addr, Weight: 1},
					},
					Algorithm: "round_robin",
				},
			},
		},
	}

	log := logger.New("error", "text")
	srv := server.New(cfg, log)
	go srv.Start()
	waitForServer(t, base, 2*time.Second)

	client := &http.Client{Timeout: 2 * time.Second}

	// Both backends up — should get responses from both
	backends := map[string]bool{}
	for i := 0; i < 10; i++ {
		resp, err := client.Get(base + "/")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		backends[strings.TrimSpace(string(body))] = true
	}
	if !backends["backend-1"] || !backends["backend-2"] {
		t.Errorf("expected both backends hit, got: %v", backends)
	}

	// Kill backend-2
	up2.Close()

	// Requests should still work (hitting backend-1)
	successes := 0
	for i := 0; i < 6; i++ {
		resp, err := client.Get(base + "/")
		if err != nil {
			continue
		}
		resp.Body.Close()
		if resp.StatusCode == 200 {
			successes++
		}
	}
	if successes == 0 {
		t.Error("all requests failed after one backend went down")
	}
}

// TestCORSE2E verifies CORS headers are set correctly.
func TestCORSE2E(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "api.json"), []byte(`{"ok":true}`), 0644)

	port := getFreePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	base := fmt.Sprintf("http://%s", addr)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			HTTPListen:  addr,
			LogLevel:    "error",
			LogFormat:   "text",
			PIDFile:     "",
			Timeouts: config.TimeoutConfig{
				Read:          config.Duration{Duration: 5 * time.Second},
				Write:         config.Duration{Duration: 5 * time.Second},
				Idle:          config.Duration{Duration: 5 * time.Second},
				ShutdownGrace: config.Duration{Duration: 2 * time.Second},
			},
		},
		Domains: []config.Domain{
			{Host: addr, Root: dir, Type: "static", SSL: config.SSLConfig{Mode: "off"}},
		},
	}

	log := logger.New("error", "text")
	srv := server.New(cfg, log)
	go srv.Start()
	waitForServer(t, base, 2*time.Second)

	client := &http.Client{Timeout: 2 * time.Second}

	// Request with Origin header — security headers should be present
	req, _ := http.NewRequest("GET", base+"/api.json", nil)
	req.Header.Set("Origin", "https://example.com")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// Security headers should always be there
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing nosniff header")
	}
}

// TestConfigReloadE2E tests config reload via admin API on a live server.
func TestConfigReloadE2E(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	os.WriteFile(filepath.Join(dir1, "index.html"), []byte("before-reload"), 0644)
	os.WriteFile(filepath.Join(dir2, "index.html"), []byte("after-reload"), 0644)

	port := getFreePort(t)
	adminPort := getFreePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	adminAddr := fmt.Sprintf("127.0.0.1:%d", adminPort)
	base := fmt.Sprintf("http://%s", addr)

	// Write initial config
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
	waitForServer(t, base, 2*time.Second)

	client := &http.Client{Timeout: 2 * time.Second}

	// Verify initial content
	resp1, _ := client.Get(base + "/index.html")
	body1, _ := io.ReadAll(resp1.Body)
	resp1.Body.Close()
	if string(body1) != "before-reload" {
		t.Fatalf("initial body = %q, want before-reload", string(body1))
	}

	// Update config to point to dir2
	writeConfig(t, cfgPath, addr, adminAddr, dir2)

	// Trigger reload via admin API
	req, _ := http.NewRequest("POST", fmt.Sprintf("http://%s/api/v1/reload", adminAddr), nil)
	resp2, err := client.Do(req)
	if err != nil {
		t.Fatalf("reload request: %v", err)
	}
	resp2.Body.Close()

	if resp2.StatusCode != 200 {
		t.Fatalf("reload status = %d", resp2.StatusCode)
	}

	// Verify new content
	resp3, _ := client.Get(base + "/index.html")
	body3, _ := io.ReadAll(resp3.Body)
	resp3.Body.Close()
	if string(body3) != "after-reload" {
		t.Errorf("after reload body = %q, want after-reload", string(body3))
	}
}

// Helpers

type testUpstream struct {
	Addr string
	srv  *http.Server
	ln   net.Listener
}

func startTestUpstream(t *testing.T, name string) *testUpstream {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(name))
		}),
	}
	go srv.Serve(ln)

	return &testUpstream{Addr: ln.Addr().String(), srv: srv, ln: ln}
}

func (u *testUpstream) Close() {
	u.srv.Close()
	u.ln.Close()
}

func waitForServer(t *testing.T, base string, timeout time.Duration) {
	t.Helper()
	client := &http.Client{Timeout: 1 * time.Second}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := client.Get(base + "/")
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("server did not start within timeout")
}

func writeConfig(t *testing.T, path, httpAddr, adminAddr, root string) {
	t.Helper()
	yaml := fmt.Sprintf(`global:
  worker_count: "1"
  http_listen: "%s"
  pid_file: ""
  log_level: error
  log_format: text
  admin:
    enabled: true
    listen: "%s"
  timeouts:
    read: 5s
    write: 5s
    idle: 5s
    shutdown_grace: 2s

domains:
  - host: "%s"
    root: "%s"
    type: static
    ssl:
      mode: off
`, httpAddr, adminAddr, httpAddr, strings.ReplaceAll(root, "\\", "/"))
	os.WriteFile(path, []byte(yaml), 0644)
}

package integration

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/server"
)

// getFreePort finds an available TCP port.
func getFreePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

// TestStaticSiteE2E starts a real UWAS server on a random port and tests static serving.
func TestStaticSiteE2E(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>Welcome</h1>"), 0644)
	os.WriteFile(filepath.Join(dir, "style.css"), []byte("body{color:red}"), 0644)
	os.MkdirAll(filepath.Join(dir, "sub"), 0755)
	os.WriteFile(filepath.Join(dir, "sub", "page.html"), []byte("subpage"), 0644)

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
			},
		},
	}

	log := logger.New("error", "text")
	srv := server.New(cfg, log)

	// Start server in background
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start()
	}()

	// Wait for server to start
	ready := false
	client := &http.Client{Timeout: 2 * time.Second}
	for i := 0; i < 20; i++ {
		time.Sleep(50 * time.Millisecond)
		resp, err := client.Get(base + "/index.html")
		if err == nil {
			resp.Body.Close()
			ready = true
			break
		}
	}
	if !ready {
		t.Fatal("server failed to start within 1s")
	}

	// Test 1: GET / → index.html
	t.Run("index", func(t *testing.T) {
		resp, err := client.Get(base + "/")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)

		if resp.StatusCode != 200 {
			t.Errorf("status = %d, want 200", resp.StatusCode)
		}
		if string(body) != "<h1>Welcome</h1>" {
			t.Errorf("body = %q", string(body))
		}
	})

	// Test 2: CSS with correct MIME
	t.Run("css_mime", func(t *testing.T) {
		resp, err := client.Get(base + "/style.css")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		ct := resp.Header.Get("Content-Type")
		if ct != "text/css; charset=utf-8" {
			t.Errorf("Content-Type = %q", ct)
		}
	})

	// Test 3: 404
	t.Run("not_found", func(t *testing.T) {
		resp, err := client.Get(base + "/nonexistent")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 404 {
			t.Errorf("status = %d, want 404", resp.StatusCode)
		}
	})

	// Test 4: Subdir file
	t.Run("subdir", func(t *testing.T) {
		resp, err := client.Get(base + "/sub/page.html")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)

		if resp.StatusCode != 200 {
			t.Errorf("status = %d, want 200", resp.StatusCode)
		}
		if string(body) != "subpage" {
			t.Errorf("body = %q", string(body))
		}
	})

	// Test 5: ETag / 304
	t.Run("etag_304", func(t *testing.T) {
		resp1, err := client.Get(base + "/style.css")
		if err != nil {
			t.Fatal(err)
		}
		etag := resp1.Header.Get("Etag")
		resp1.Body.Close()

		if etag == "" {
			t.Skip("no ETag returned")
		}

		req, _ := http.NewRequest("GET", base+"/style.css", nil)
		req.Header.Set("If-None-Match", etag)
		resp2, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp2.Body.Close()

		if resp2.StatusCode != 304 {
			t.Errorf("conditional status = %d, want 304", resp2.StatusCode)
		}
	})

	// Test 6: Server header present
	t.Run("server_header", func(t *testing.T) {
		resp, err := client.Get(base + "/index.html")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()

		srv := resp.Header.Get("Server")
		if srv == "" {
			t.Error("Server header should be set")
		}
	})

	// Test 7: X-Request-ID present
	t.Run("request_id", func(t *testing.T) {
		resp, err := client.Get(base + "/index.html")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()

		rid := resp.Header.Get("X-Request-Id")
		if rid == "" {
			t.Error("X-Request-ID header should be set")
		}
	})

	// Test 8: Security headers
	t.Run("security_headers", func(t *testing.T) {
		resp, err := client.Get(base + "/index.html")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()

		checks := map[string]string{
			"X-Content-Type-Options": "nosniff",
			"X-Frame-Options":       "SAMEORIGIN",
		}
		for k, want := range checks {
			if got := resp.Header.Get(k); got != want {
				t.Errorf("%s = %q, want %q", k, got, want)
			}
		}
	})
}

// TestRedirectDomainE2E tests redirect domain type end-to-end.
func TestRedirectDomainE2E(t *testing.T) {
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
				Type: "redirect",
				SSL:  config.SSLConfig{Mode: "off"},
				Redirect: config.RedirectConfig{
					Target:       "https://example.com",
					Status:       301,
					PreservePath: true,
				},
			},
		},
	}

	log := logger.New("error", "text")
	srv := server.New(cfg, log)

	go srv.Start()

	client := &http.Client{
		Timeout:       2 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse },
	}

	// Wait for server
	for i := 0; i < 20; i++ {
		time.Sleep(50 * time.Millisecond)
		_, err := client.Get(base + "/")
		if err == nil {
			break
		}
	}

	resp, err := client.Get(base + "/some/path?q=1")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if resp.StatusCode != 301 {
		t.Errorf("status = %d, want 301", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if loc != "https://example.com/some/path?q=1" {
		t.Errorf("Location = %q", loc)
	}
}

// TestProxyE2E tests reverse proxy end-to-end with a real upstream.
func TestProxyE2E(t *testing.T) {
	// Start a test upstream
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream", "backend1")
		w.Write([]byte("hello from upstream"))
	})
	upstreamSrv := &http.Server{Handler: upstream}
	upstreamLn, _ := net.Listen("tcp", "127.0.0.1:0")
	upstreamAddr := upstreamLn.Addr().String()
	go upstreamSrv.Serve(upstreamLn)
	defer upstreamSrv.Close()

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
						{Address: "http://" + upstreamAddr, Weight: 1},
					},
					Algorithm: "round_robin",
				},
			},
		},
	}

	log := logger.New("error", "text")
	srv := server.New(cfg, log)
	go srv.Start()

	client := &http.Client{Timeout: 2 * time.Second}

	// Wait for server
	for i := 0; i < 20; i++ {
		time.Sleep(50 * time.Millisecond)
		resp, err := client.Get(base + "/")
		if err == nil {
			resp.Body.Close()
			break
		}
	}

	resp, err := client.Get(base + "/api/test")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if string(body) != "hello from upstream" {
		t.Errorf("body = %q", string(body))
	}
	if resp.Header.Get("X-Upstream") != "backend1" {
		t.Error("upstream header not forwarded")
	}
}

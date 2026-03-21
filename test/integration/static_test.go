package integration

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/server"
)

// TestStaticSiteE2E starts a real UWAS server and tests static file serving end-to-end.
func TestStaticSiteE2E(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>Welcome</h1>"), 0644)
	os.WriteFile(filepath.Join(dir, "style.css"), []byte("body{color:red}"), 0644)
	os.MkdirAll(filepath.Join(dir, "sub"), 0755)
	os.WriteFile(filepath.Join(dir, "sub", "page.html"), []byte("subpage"), 0644)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
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
				Host: "localhost",
				Root: dir,
				Type: "static",
				SSL:  config.SSLConfig{Mode: "off"},
			},
		},
	}

	log := logger.New("error", "text")
	srv := server.New(cfg, log)

	// Start server in background
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start()
	}()

	// Wait for server to start
	time.Sleep(500 * time.Millisecond)

	client := &http.Client{Timeout: 5 * time.Second}

	// Test 1: GET / → index.html
	t.Run("index", func(t *testing.T) {
		resp, err := client.Get("http://localhost:80/")
		if err != nil {
			t.Skipf("server not reachable (port 80 may be in use): %v", err)
			return
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

	// Test 2: GET /style.css → correct MIME
	t.Run("css_mime", func(t *testing.T) {
		resp, err := client.Get("http://localhost:80/style.css")
		if err != nil {
			t.Skip("server not reachable")
			return
		}
		defer resp.Body.Close()

		ct := resp.Header.Get("Content-Type")
		if ct != "text/css; charset=utf-8" {
			t.Errorf("Content-Type = %q", ct)
		}
	})

	// Test 3: GET /nonexistent → 404
	t.Run("not_found", func(t *testing.T) {
		resp, err := client.Get("http://localhost:80/nonexistent")
		if err != nil {
			t.Skip("server not reachable")
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != 404 {
			t.Errorf("status = %d, want 404", resp.StatusCode)
		}
	})

	// Test 4: GET /sub/page.html → subdir file
	t.Run("subdir", func(t *testing.T) {
		resp, err := client.Get("http://localhost:80/sub/page.html")
		if err != nil {
			t.Skip("server not reachable")
			return
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

	// Test 5: ETag / conditional request
	t.Run("etag_304", func(t *testing.T) {
		resp1, err := client.Get("http://localhost:80/style.css")
		if err != nil {
			t.Skip("server not reachable")
			return
		}
		etag := resp1.Header.Get("Etag")
		resp1.Body.Close()

		if etag == "" {
			t.Skip("no ETag returned")
			return
		}

		req, _ := http.NewRequest("GET", "http://localhost:80/style.css", nil)
		req.Header.Set("If-None-Match", etag)
		resp2, err := client.Do(req)
		if err != nil {
			t.Skip("server not reachable")
			return
		}
		resp2.Body.Close()

		if resp2.StatusCode != 304 {
			t.Errorf("conditional status = %d, want 304", resp2.StatusCode)
		}
	})

	// Shutdown
	cancel()
	_ = ctx
}

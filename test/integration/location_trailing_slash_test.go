package integration

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/server"
)

// F62300: a regex location anchored with "$" ("~\.txt$") decided on the path
// as sent, and the canonical form keeps a trailing slash, while the static
// handler opens files through filepath.Clean. "/private/a.txt/" therefore
// skipped the location's basic_auth and still answered with the file.
func TestLocationRegexCannotBeSkippedWithTrailingSlash(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "private"), 0755)
	os.WriteFile(filepath.Join(dir, "private", "a.txt"), []byte("secret"), 0644)
	os.WriteFile(filepath.Join(dir, "open.html"), []byte("<p>open</p>"), 0644)
	os.MkdirAll(filepath.Join(dir, "docs"), 0755)
	os.WriteFile(filepath.Join(dir, "docs", "index.html"), []byte("<p>docs</p>"), 0644)

	addr := fmt.Sprintf("127.0.0.1:%d", getFreePort(t))
	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1", HTTPListen: addr, LogLevel: "error", LogFormat: "text",
			Timeouts: config.TimeoutConfig{
				Read:          config.Duration{Duration: 5 * time.Second},
				Write:         config.Duration{Duration: 5 * time.Second},
				Idle:          config.Duration{Duration: 5 * time.Second},
				ShutdownGrace: config.Duration{Duration: 2 * time.Second},
			},
		},
		Domains: []config.Domain{{
			Host: addr, Root: dir, Type: "static", SSL: config.SSLConfig{Mode: "off"},
			Locations: []config.LocationConfig{{
				Match:     `~\.txt$`,
				BasicAuth: &config.BasicAuthConfig{Enabled: true, Users: map[string]string{"u": "pw"}},
			}},
		}},
	}
	srv := server.New(cfg, logger.New("error", "text"))
	go srv.Start()
	client := &http.Client{Timeout: 2 * time.Second}
	for i := 0; i < 40; i++ {
		time.Sleep(50 * time.Millisecond)
		if resp, err := client.Get("http://" + addr + "/open.html"); err == nil {
			resp.Body.Close()
			break
		}
	}

	status := func(path string) int {
		resp, err := client.Get("http://" + addr + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}

	// Controls: the protected file asks for credentials, an unprotected file
	// does not.
	if got := status("/private/a.txt"); got != http.StatusUnauthorized {
		t.Fatalf("control: GET /private/a.txt = %d, want 401", got)
	}
	if got := status("/open.html"); got != http.StatusOK {
		t.Fatalf("control: GET /open.html = %d, want 200", got)
	}
	// A trailing slash still names a directory and serves its index.
	if got := status("/docs/"); got != http.StatusOK {
		t.Fatalf("control: GET /docs/ = %d, want 200", got)
	}
	for _, p := range []string{"/private/a.txt/", "/private/a.txt//", "/private//a.txt/", "/private/./a.txt/", "/x/../private/a.txt/"} {
		if got := status(p); got == http.StatusOK {
			t.Errorf("GET %s = 200: served the protected file without credentials", p)
		}
	}
}

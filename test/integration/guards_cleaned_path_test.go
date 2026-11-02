package integration

import (
	"bufio"
	"fmt"
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

// F63300/F63301/F63302: the always-on wp-config copy guard, the WAF file_probe
// family and the hotlink guard decided on the path as sent, while the static
// handler opens files through the cleaned path. "/sub/wp-config.old/." names
// the same file as "/sub/wp-config.old" but ends in "." and met no rule.
func TestGuardsDecideOnCleanedPath(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"sub/wp-config.old", "dump.sql", "img/a.jpg", "open.html"} {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, f)), 0755)
		os.WriteFile(filepath.Join(dir, f), []byte("DATA"), 0644)
	}
	addr := fmt.Sprintf("127.0.0.1:%d", getFreePort(t))
	cfg := &config.Config{
		Global: config.GlobalConfig{WorkerCount: "1", HTTPListen: addr, LogLevel: "error", LogFormat: "text",
			Timeouts: config.TimeoutConfig{Read: config.Duration{Duration: 5 * time.Second}, Write: config.Duration{Duration: 5 * time.Second}, Idle: config.Duration{Duration: 5 * time.Second}, ShutdownGrace: config.Duration{Duration: 2 * time.Second}}},
		Domains: []config.Domain{{Host: addr, Root: dir, Type: "static", SSL: config.SSLConfig{Mode: "off"},
			Security: config.SecurityConfig{
				WAF:               config.WAFConfig{Enabled: true},
				HotlinkProtection: config.HotlinkConfig{Enabled: true},
			}}},
	}
	srv := server.New(cfg, logger.New("error", "text"))
	go srv.Start()
	for i := 0; i < 40; i++ {
		time.Sleep(50 * time.Millisecond)
		if c, err := net.Dial("tcp", addr); err == nil {
			c.Close()
			break
		}
	}
	get := func(target string) int {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(3 * time.Second))
		fmt.Fprintf(c, "GET %s HTTP/1.1\r\nHost: %s\r\nReferer: http://evil.example/\r\nConnection: close\r\n\r\n", target, addr)
		resp, err := http.ReadResponse(bufio.NewReader(c), nil)
		if err != nil {
			return -1
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	// Control: an unguarded file is served.
	if got := get("/open.html"); got != http.StatusOK {
		t.Fatalf("control: /open.html = %d, want 200", got)
	}
	for _, base := range []string{"/sub/wp-config.old", "/dump.sql", "/img/a.jpg"} {
		if got := get(base); got != http.StatusForbidden {
			t.Fatalf("control: GET %s = %d, want 403", base, got)
		}
		for _, suffix := range []string{"/.", "//.", "/x/.."} {
			if got := get(base + suffix); got == http.StatusOK {
				t.Errorf("GET %s%s = 200: the guard that blocks %s was skipped", base, suffix, base)
			}
		}
	}
}

package server

import (
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// F68400: global.mcp.enabled was read only in New, so Save & Reload of the
// Settings toggle left a disabled MCP answering /api/v1/mcp/* and an enabled
// one answering 503 until restart.
func TestReloadAppliesMCPEnabled(t *testing.T) {
	const key = "mcp-toggle-admin-key-0123456789"
	const tmpl = "global:\n  worker_count: \"1\"\n  log_level: error\n  log_format: text\n  web_root: %s\n" +
		"  admin:\n    enabled: true\n    listen: %q\n    api_key: %q\n  mcp:\n    enabled: %v\n" +
		"domains:\n  - host: a.test\n    type: static\n    root: %s\n    ssl: {mode: \"off\"}\n"
	dir := t.TempDir()
	data, root := filepath.Join(dir, "data"), filepath.Join(dir, "www")
	for _, d := range []string{data, root} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	hold, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Close()
	path := filepath.Join(dir, "uwas.yaml")
	write := func(on bool) {
		t.Helper()
		y := fmt.Sprintf(tmpl, data, hold.Addr().String(), key, on, root)
		if err := os.WriteFile(path, []byte(y), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(false)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	s := New(cfg, logger.New("error", "text"))
	t.Cleanup(func() { s.cancel() })
	s.SetConfigPath(path)
	if err := s.admin.Start(); err == nil {
		t.Fatal("admin.Start bound a held address")
	}
	h := s.admin.HTTPServer().Handler
	call := func() int {
		req := httptest.NewRequest("POST", "/api/v1/mcp/call", strings.NewReader(`{"name":"domain_list","input":{}}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
		req.Host = "127.0.0.1"
		req.RemoteAddr = "127.0.0.1:40000"
		req.Header.Set("Authorization", "Bearer "+key)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if c := call(); c != 503 {
		t.Fatalf("MCP disabled at startup: %d, want 503", c)
	}
	for _, on := range []bool{true, false, true} {
		write(on)
		if err := s.reload(); err != nil {
			t.Fatalf("reload: %v", err)
		}
		want := 503
		if on {
			want = 200
		}
		if c := call(); c != want {
			t.Errorf("after reload with mcp.enabled=%v: %d, want %d", on, c, want)
		}
	}
}

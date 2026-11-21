package server

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// F66400: reload refreshed global.admin.api_key only on the auth manager the
// server built at startup. With users enabled at runtime the admin API builds
// its own manager and the server's stays nil, so a key rotated in the file and
// reloaded left the rotated-out key authenticating until restart.

const lazyAuthYAML = `global:
  worker_count: "1"
  log_level: error
  log_format: text
  web_root: %s
  admin:
    enabled: true
    listen: %q
    api_key: %q
  users:
    enabled: false
domains:
  - host: a.test
    type: static
    root: %s
    ssl: {mode: "off"}
`

func TestReloadRefreshesLazyAuthManagerKey(t *testing.T) {
	const oldKey, newKey = "first-admin-key-0123456789", "second-admin-key-9876543210"
	dir := t.TempDir()
	data, root := filepath.Join(dir, "data"), filepath.Join(dir, "www")
	for _, d := range []string{data, root} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Hold the admin address: Start builds the handler, then fails to bind.
	hold, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Close()
	path := filepath.Join(dir, "uwas.yaml")
	y := fmt.Sprintf(lazyAuthYAML, data, hold.Addr().String(), oldKey, root)
	if err := os.WriteFile(path, []byte(y), 0o600); err != nil {
		t.Fatal(err)
	}
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

	do := func(method, target, key, body string) int {
		var req *http.Request
		if body != "" {
			req = httptest.NewRequest(method, target, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Requested-With", "XMLHttpRequest")
		} else {
			req = httptest.NewRequest(method, target, nil)
		}
		req.Host = "127.0.0.1"
		req.RemoteAddr = "127.0.0.1:40000"
		req.Header.Set("Authorization", "Bearer "+key)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	if c := do("PUT", "/api/v1/settings", oldKey, `{"global.users.enabled": true}`); c != 200 {
		t.Fatalf("enabling users via settings: %d", c)
	}
	if s.authMgr != nil {
		t.Fatal("precondition: the server's own manager should be nil")
	}

	rotate := func(from, to string) {
		t.Helper()
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(strings.ReplaceAll(string(b), from, to)), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := s.reload(); err != nil {
			t.Fatalf("reload: %v", err)
		}
	}

	rotate(oldKey, newKey)
	if c := do("GET", "/api/v1/domains", oldKey, ""); c != http.StatusUnauthorized {
		t.Errorf("rotated-out key after reload: %d, want 401", c)
	}
	if c := do("GET", "/api/v1/domains", newKey, ""); c != 200 {
		t.Errorf("new key after reload: %d, want 200", c)
	}

	rotate(newKey, oldKey)
	if c := do("GET", "/api/v1/domains", oldKey, ""); c != 200 {
		t.Errorf("restored key after reload: %d, want 200", c)
	}
	if c := do("GET", "/api/v1/domains", newKey, ""); c != http.StatusUnauthorized {
		t.Errorf("previous key after rotating back: %d, want 401", c)
	}
}

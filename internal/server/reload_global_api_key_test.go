package server

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// F2141: the multi-user auth manager captured global.admin.api_key (and the
// users.* switches) at startup; a reload never refreshed them, so a rotated
// admin key kept the OLD key valid and rejected the new one until restart.

func authReloadConfig(t *testing.T, dir, apiKey string, allowLegacy bool) string {
	t.Helper()
	root := filepath.Join(dir, "site")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	y := fmt.Sprintf(`global:
  worker_count: "1"
  log_level: error
  log_format: text
  web_root: %s
  admin:
    enabled: true
    listen: "127.0.0.1:19877"
    api_key: %q
  users:
    enabled: true
    allow_legacy_plaintext_api_key: %v
domains:
  - host: auth.test
    type: static
    root: %s
    ssl: {mode: "off"}
`, filepath.Join(dir, "data"), apiKey, allowLegacy, root)
	p := filepath.Join(dir, "uwas.yaml")
	if err := os.WriteFile(p, []byte(y), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func newAuthReloadServer(t *testing.T, dir, key string) (*Server, string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := authReloadConfig(t, dir, key, false)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	s := New(cfg, logger.New("error", "text"))
	t.Cleanup(func() { s.cancel() })
	s.SetConfigPath(path)
	if s.authMgr == nil {
		t.Fatal("multi-user auth manager not created")
	}
	return s, path
}

func TestReloadRefreshesGlobalAPIKey(t *testing.T) {
	dir := t.TempDir()
	const oldKey, newKey = "old-admin-key-0123456789", "new-admin-key-9876543210"
	s, _ := newAuthReloadServer(t, dir, oldKey)

	// Controls: the configured key works, an unknown one does not.
	if _, err := s.authMgr.AuthenticateAPIKey(oldKey); err != nil {
		t.Fatalf("current key rejected: %v", err)
	}
	if _, err := s.authMgr.AuthenticateAPIKey("some-other-key"); err == nil {
		t.Fatal("unknown key accepted")
	}

	authReloadConfig(t, dir, newKey, false)
	if err := s.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if _, err := s.authMgr.AuthenticateAPIKey(oldKey); err == nil {
		t.Error("rotated-out key still authenticates after reload")
	}
	if _, err := s.authMgr.AuthenticateAPIKey(newKey); err != nil {
		t.Errorf("new key rejected after reload: %v", err)
	}

	// Rotating back works too, and an empty key disables the global key.
	authReloadConfig(t, dir, oldKey, false)
	if err := s.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if _, err := s.authMgr.AuthenticateAPIKey(oldKey); err != nil {
		t.Errorf("restored key rejected: %v", err)
	}
	if _, err := s.authMgr.AuthenticateAPIKey(newKey); err == nil {
		t.Error("previous key still authenticates after rotating back")
	}
}

// Requests racing a reload must be safe under -race.
func TestGlobalAPIKeySwapRace(t *testing.T) {
	s, _ := newAuthReloadServer(t, t.TempDir(), "old-admin-key-0123456789")
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				_, _ = s.authMgr.AuthenticateAPIKey("old-admin-key-0123456789")
			}
		}
	}()
	for i := 0; i < 50; i++ {
		s.authMgr.SetGlobalAPIKey("k-a-0123456789")
		s.authMgr.SetGlobalAPIKey("old-admin-key-0123456789")
	}
	close(stop)
	<-done
}

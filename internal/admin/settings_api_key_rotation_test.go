package admin

// F2170: PUT /api/v1/settings of global.admin.api_key persisted the new key but
// the live auth.Manager (users.enabled) kept authenticating the old one until
// restart.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/uwaserver/uwas/internal/auth"
	"github.com/uwaserver/uwas/internal/config"
)

func apiKeyRotationServer(t *testing.T, usersEnabled bool) (*Server, *auth.Manager) {
	t.Helper()
	dir := t.TempDir()
	webRoot := filepath.Join(dir, "www")
	if err := os.MkdirAll(webRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Global: config.GlobalConfig{WebRoot: webRoot, LogLevel: "info", LogFormat: "text"}}
	cfg.Global.Admin.APIKey = "old-global-key-0001"
	cfg.Global.Users.Enabled = usersEnabled
	s := testServerFromConfig(t, cfg)
	s.configPath = filepath.Join(dir, "uwas.yaml")
	mgr, err := auth.NewManager(filepath.Join(dir, "auth"), "old-global-key-0001")
	if err != nil {
		t.Fatal(err)
	}
	s.SetAuthManager(mgr)
	return s, mgr
}

func putSettingsRot(s *Server, body string) int {
	r := withAdminContext(httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(body)))
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, r)
	return rec.Code
}

func TestSettingsPutRotatesLiveGlobalAPIKey(t *testing.T) {
	s, mgr := apiKeyRotationServer(t, true)

	// Control: before any PUT the old key works and the new one does not.
	if _, err := mgr.AuthenticateAPIKey("old-global-key-0001"); err != nil {
		t.Fatalf("control: old key rejected: %v", err)
	}
	if _, err := mgr.AuthenticateAPIKey("new-global-key-0002"); err == nil {
		t.Fatal("control: new key accepted before the PUT")
	}

	if code := putSettingsRot(s, `{"global.admin.api_key":"new-global-key-0002"}`); code != 200 {
		t.Fatalf("PUT status=%d", code)
	}
	if _, err := mgr.AuthenticateAPIKey("old-global-key-0001"); err == nil {
		t.Error("old global key still authenticates after rotation")
	}
	if _, err := mgr.AuthenticateAPIKey("new-global-key-0002"); err != nil {
		t.Errorf("new global key rejected after rotation: %v", err)
	}

	// A masked value echoed back by the panel must not touch the key.
	if code := putSettingsRot(s, `{"global.admin.api_key":"****0002","global.log_level":"warn"}`); code != 200 {
		t.Fatalf("masked PUT status=%d", code)
	}
	if _, err := mgr.AuthenticateAPIKey("new-global-key-0002"); err != nil {
		t.Errorf("masked echo broke the key: %v", err)
	}

	// Rotating back is honoured too (repeated call).
	if code := putSettingsRot(s, `{"global.admin.api_key":"old-global-key-0001"}`); code != 200 {
		t.Fatalf("rotate-back PUT status=%d", code)
	}
	if _, err := mgr.AuthenticateAPIKey("old-global-key-0001"); err != nil {
		t.Errorf("rotated-back key rejected: %v", err)
	}
	if _, err := mgr.AuthenticateAPIKey("new-global-key-0002"); err == nil {
		t.Error("intermediate key still authenticates")
	}
}

func TestSettingsPutAPIKeyUsersDisabledLeavesManagerAlone(t *testing.T) {
	// With multi-user auth off the lazy hook returns early; the PUT must not
	// panic and the manager (if any) is untouched.
	s, mgr := apiKeyRotationServer(t, false)
	if code := putSettingsRot(s, `{"global.admin.api_key":"new-global-key-0002"}`); code != 200 {
		t.Fatalf("PUT status=%d", code)
	}
	if _, err := mgr.AuthenticateAPIKey("old-global-key-0001"); err != nil {
		t.Errorf("users disabled: manager key changed: %v", err)
	}
}

// Rotation races with logins/API-key checks: run under -race. The barrier makes
// every authenticating goroutine overlap the PUT.
func TestSettingsPutAPIKeyRotationRace(t *testing.T) {
	s, mgr := apiKeyRotationServer(t, true)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 50; j++ {
				_, _ = mgr.AuthenticateAPIKey("old-global-key-0001")
				_, _ = mgr.AuthenticateAPIKey("new-global-key-0002")
			}
		}()
	}
	close(start)
	if code := putSettingsRot(s, `{"global.admin.api_key":"new-global-key-0002","global.users.session_ttl":2}`); code != 200 {
		t.Fatalf("PUT status=%d", code)
	}
	wg.Wait()
	if _, err := mgr.AuthenticateAPIKey("new-global-key-0002"); err != nil {
		t.Errorf("new key rejected: %v", err)
	}
}

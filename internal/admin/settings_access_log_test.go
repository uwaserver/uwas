package admin

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// The settings API is an explicit allow-list in both directions: a key absent
// from the GET map never reaches the panel, and a key absent from the PUT
// switch is accepted with a 200 and silently dropped. Adding the toggle to the
// dashboard is worth nothing unless both ends carry it, so these test the
// round trip rather than the struct.

func settingsTestServer(t *testing.T) *Server {
	t.Helper()

	s := testServer()
	cfgPath := filepath.Join(t.TempDir(), "uwas.yaml")
	if err := os.WriteFile(cfgPath, []byte("global: {}"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	s.SetConfigPath(cfgPath)
	return s
}

func settingsBody(t *testing.T, s *Server) map[string]any {
	t.Helper()

	rec := httptest.NewRecorder()
	s.handleSettingsGet(rec, withAdminContext(httptest.NewRequest("GET", "/api/v1/settings", nil)))
	if rec.Code != 200 {
		t.Fatalf("settings GET status = %d, want 200", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	return body
}

func putSettings(t *testing.T, s *Server, payload string) {
	t.Helper()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/settings", strings.NewReader(payload))
	req.RemoteAddr = "10.0.0.1:1234"
	s.handleSettingsPut(rec, withAdminContext(req))
	if rec.Code != 200 {
		t.Fatalf("settings PUT status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
}

// An absent access_log block means on, and the panel has to render that as a
// toggle already switched on rather than as a missing value.
func TestSettingsGetExposesAccessLogEnabled(t *testing.T) {
	s := settingsTestServer(t)

	got, ok := settingsBody(t, s)["global.access_log.enabled"]
	if !ok {
		t.Fatal("global.access_log.enabled is missing from the settings payload — the panel field would render empty")
	}
	if got != true {
		t.Errorf("global.access_log.enabled = %v, want true for an absent block", got)
	}
}

func TestSettingsPutTogglesAccessLog(t *testing.T) {
	s := settingsTestServer(t)

	putSettings(t, s, `{"global.access_log.enabled":false}`)
	if s.config.Global.AccessLog.RequestLogEnabled() {
		t.Error("the request log is still on after saving false")
	}
	if got := settingsBody(t, s)["global.access_log.enabled"]; got != false {
		t.Errorf("settings GET returned %v after saving false", got)
	}

	putSettings(t, s, `{"global.access_log.enabled":true}`)
	if !s.config.Global.AccessLog.RequestLogEnabled() {
		t.Error("the request log did not come back on after saving true")
	}
	if got := settingsBody(t, s)["global.access_log.enabled"]; got != true {
		t.Errorf("settings GET returned %v after saving true", got)
	}
}

// Saving false has to write an explicit pointer, not leave the field nil:
// nil is how "never configured" is spelled, and it reads back as on.
func TestSettingsPutWritesExplicitFalse(t *testing.T) {
	s := settingsTestServer(t)

	putSettings(t, s, `{"global.access_log.enabled":false}`)
	if s.config.Global.AccessLog.Enabled == nil {
		t.Fatal("Enabled is still nil after saving false — the value would read back as on")
	}
	if *s.config.Global.AccessLog.Enabled {
		t.Error("Enabled is true after saving false")
	}
}

// Saving an unrelated key must not disturb a toggle the operator already set.
func TestSettingsPutLeavesAccessLogAlone(t *testing.T) {
	s := settingsTestServer(t)
	s.config.Global.AccessLog.Enabled = config.BoolPtr(false)

	putSettings(t, s, `{"global.log_level":"warn"}`)
	if s.config.Global.AccessLog.RequestLogEnabled() {
		t.Error("saving log_level turned the request log back on")
	}
}

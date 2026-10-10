package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/uwaserver/uwas/internal/apps"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/metrics"
)

// F1751 (regression): a webhook deploy that was already running when its app was deleted
// records its result afterwards, recreating the deploy history ForgetApp just
// removed; an app re-created under the same name inherits that entry.
func TestDeployInFlightResultNotRecordedForDeletedApp(t *testing.T) {
	dir := t.TempDir()
	store := apps.NewStore(dir)
	mk := func(name string) {
		if err := store.Save(&apps.App{Name: name, Runtime: apps.RuntimeCustom, WorkDir: filepath.Join(dir, "apps", name)}); err != nil {
			t.Fatal(err)
		}
	}
	mgr := apps.NewManager(store, logger.New("error", "text"))
	cfg := &config.Config{Global: config.GlobalConfig{Admin: config.AdminConfig{Listen: "127.0.0.1:0"}}}
	s := New(cfg, logger.New("error", "text"), metrics.New())
	s.mux = &testMux{mux: s.mux.(*http.ServeMux)}
	s.appsMgr = mgr
	history := func(n string) int {
		rec := httptest.NewRecorder()
		s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/apps/"+n+"/deploy-history", nil))
		var out struct{ Items []json.RawMessage }
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return len(out.Items)
	}

	// control: an app that still exists keeps what its deploy records.
	mk("live")
	deployHandler.RunWebhookDeploy("live", "refs/heads/main") // app has no work_dir deploy config -> records a failure
	if history("live") != 1 {
		t.Fatalf("invalid proof: control history=%d, want 1", history("live"))
	}

	mk("gone")
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("DELETE", "/api/v1/apps/gone", nil))
	if rec.Code != 200 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	// the deploy that was in flight finishes now
	deployHandler.RunWebhookDeploy("gone", "refs/heads/main")
	mk("gone") // same name re-created
	_, statErr := os.Stat(filepath.Join(dir, ".deploy-history", "gone.json"))
	t.Logf("EXPECTED: no history file for the deleted app, re-created app history empty")
	t.Logf("ACTUAL:   historyFile=%v recreatedHistory=%d", statErr == nil, history("gone"))
	if statErr == nil || history("gone") != 0 {
		t.Fatal("a deploy finishing after its app was deleted recorded history for it")
	}
}

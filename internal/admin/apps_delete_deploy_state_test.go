package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	deployadmin "github.com/uwaserver/uwas/internal/admin/deploy"
	"github.com/uwaserver/uwas/internal/apps"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/metrics"
)

// F1180 (regression): deleting an app leaves its persisted deploy history, deploy key dir
// and the deploy handler's in-memory history; an app re-created with the same
// name inherits the old history and the old private key stays on disk.
func TestAppDeleteClearsDeployHistoryAndKey(t *testing.T) {
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

	for _, n := range []string{"gone", "keep"} {
		mk(n)
		if err := persistAppDeployHistory(dir, n, []appDeployHistoryEntry{{Source: "manual", OK: true, CommitSHA: "old-" + n}}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := deployadmin.GenerateAppDeployKey(dir, n); err != nil {
			t.Fatal(err)
		}
	}
	history := func(n string) int {
		rec := httptest.NewRecorder()
		s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/apps/"+n+"/deploy-history", nil))
		var out struct{ Items []json.RawMessage }
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return len(out.Items)
	}
	if history("gone") != 1 || history("keep") != 1 { // also warms the in-memory cache
		t.Fatal("setup: history not visible")
	}

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("DELETE", "/api/v1/apps/gone", nil))
	if rec.Code != 200 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	mk("gone") // same name re-created

	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }
	histFile := filepath.Join(dir, ".deploy-history", "gone.json")
	keyDir := filepath.Join(dir, "deploy-keys", "gone")
	t.Logf("EXPECTED: history file gone, key dir gone, re-created app history empty; other app untouched")
	t.Logf("ACTUAL: historyFile=%v keyDir=%v recreatedHistory=%d keepHistory=%d keepKey=%v",
		exists(histFile), exists(keyDir), history("gone"), history("keep"), exists(filepath.Join(dir, "deploy-keys", "keep")))
	if history("keep") != 1 || !exists(filepath.Join(dir, "deploy-keys", "keep")) {
		t.Fatal("control failed: unrelated app was affected")
	}
	if exists(histFile) || exists(keyDir) || history("gone") != 0 {
		t.Fatal("deleted app left deploy state behind")
	}

	rec = httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("DELETE", "/api/v1/apps/ghost", nil))
	if rec.Code != 404 || history("keep") != 1 || !exists(filepath.Join(dir, "deploy-keys", "keep")) {
		t.Fatalf("deleting a missing app: code=%d, unrelated state must stay", rec.Code)
	}
}

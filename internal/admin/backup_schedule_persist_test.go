package admin

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/uwaserver/uwas/internal/config"
)

// F2022: PUT /api/v1/backups/schedule must be written to the config file; it
// used to apply in memory only and revert on restart.

func scheduleServer(t *testing.T) (*Server, string) {
	t.Helper()
	s := testServerFromConfig(t, &config.Config{Global: config.GlobalConfig{Admin: config.AdminConfig{Listen: "127.0.0.1:0"}}})
	s.SetBackupManager(testBackupManager(t))
	p := filepath.Join(t.TempDir(), "uwas.yaml")
	s.SetConfigPath(p)
	return s, p
}

func schedulePut(s *Server, body string) int {
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/backups/schedule", strings.NewReader(body)))
	return rec.Code
}

func persistedBackup(t *testing.T, p string) config.BackupConfig {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		return config.BackupConfig{}
	}
	var c config.Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	return c.Global.Backup
}

func TestBackupSchedulePutPersisted(t *testing.T) {
	s, p := scheduleServer(t)
	// A previously configured cron must not outrank the new interval at startup.
	s.config.Global.Backup.Cron = "0 2 * * *"

	if c := schedulePut(s, `{"interval":"1d","keep":4}`); c != http.StatusOK {
		t.Fatalf("put = %d", c)
	}
	bc := persistedBackup(t, p)
	d, err := time.ParseDuration(bc.Schedule) // startup parses it with ParseDuration
	if err != nil || d != 24*time.Hour {
		t.Fatalf("persisted schedule %q not a 24h duration (err=%v)", bc.Schedule, err)
	}
	if bc.Cron != "" || bc.Keep != 4 {
		t.Fatalf("persisted cron=%q keep=%d, want cron cleared and keep 4", bc.Cron, bc.Keep)
	}

	// Rejected requests change nothing on disk.
	for _, body := range []string{`{"interval":"10s"}`, `{"interval":"nonsense"}`, `{}`} {
		if c := schedulePut(s, body); c != http.StatusBadRequest {
			t.Fatalf("put %s = %d, want 400", body, c)
		}
	}
	if got := persistedBackup(t, p); got.Schedule != bc.Schedule || got.Keep != 4 {
		t.Fatalf("rejected request altered the persisted schedule: %+v", got)
	}

	if c := schedulePut(s, `{"enabled":false}`); c != http.StatusOK {
		t.Fatalf("disable = %d", c)
	}
	if got := persistedBackup(t, p); got.Schedule != "" || got.Cron != "" || got.Keep != 4 {
		t.Fatalf("disable not persisted as empty schedule (keep preserved): %+v", got)
	}
}

func TestBackupSchedulePersistFailureIsReported(t *testing.T) {
	s, p := scheduleServer(t)
	blockConfigPath(t, p)
	if c := schedulePut(s, `{"interval":"6h"}`); c != http.StatusInternalServerError {
		t.Fatalf("put with unwritable config = %d, want 500", c)
	}
}

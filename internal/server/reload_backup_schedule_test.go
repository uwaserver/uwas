package server

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// F2921: backup.schedule / backup.cron were applied only by Start(); editing
// them in the YAML and reloading changed nothing until restart.

func backupScheduleConfig(t *testing.T, dir, backupLines string) string {
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
  backup:
    enabled: true
    provider: local
    keep: 3
    local:
      path: %s
%sdomains:
  - host: bk.test
    type: static
    root: %s
    ssl:
      mode: "off"
`, dir, filepath.Join(dir, "backups"), backupLines, root)
	p := filepath.Join(dir, "uwas.yaml")
	if err := os.WriteFile(p, []byte(y), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReloadAppliesBackupSchedule(t *testing.T) {
	dir := t.TempDir()
	path := backupScheduleConfig(t, dir, "    schedule: 1h\n")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	s := New(cfg, logger.New("error", "text"))
	t.Cleanup(func() { s.cancel(); s.backupMgr.Stop() })
	s.SetConfigPath(path)
	s.backupMgr.ScheduleBackup(time.Hour) // what Start() does for the startup schedule

	reloadWith := func(lines string) (time.Duration, bool) {
		backupScheduleConfig(t, dir, lines)
		if err := s.reload(); err != nil {
			t.Fatalf("reload: %v", err)
		}
		return s.backupMgr.ScheduleStatus()
	}

	if iv, on := reloadWith("    schedule: 1h\n"); iv != time.Hour || !on {
		t.Fatalf("control: unchanged schedule altered: %v active=%v", iv, on)
	}
	if iv, on := reloadWith("    schedule: 30m\n"); iv != 30*time.Minute || !on {
		t.Errorf("changed interval not applied: %v active=%v, want 30m active", iv, on)
	}
	if _, on := reloadWith("    cron: \"0 3 * * *\"\n"); !on {
		t.Error("cron added by the reload did not start the scheduler")
	}
	if iv, on := reloadWith("    schedule: 2h\n"); iv != 2*time.Hour || !on {
		t.Errorf("cron replaced by an interval: %v active=%v, want 2h active", iv, on)
	}
	if _, on := reloadWith(""); on {
		t.Error("schedule removed from the config but the scheduler kept running")
	}
}

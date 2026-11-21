package server

// F64600: a Docker DB container whose dump fails (or a docker daemon that cannot be
// listed) left a backup without that container's databases, which retention
// counted as complete and used to prune the older backup that still held them.
// Fake docker on PATH; no real docker, no network.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/uwaserver/uwas/internal/backup"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

const f64600FakeDocker = `#!/bin/sh
read mode < "$FAKE_DOCKER_STATE"
case "$1" in
ps)
  if [ "$mode" = psfail ]; then echo "Cannot connect to the Docker daemon" >&2; exit 1; fi
  echo '{"ID":"abc","Names":"uwas-db-shop","Image":"mariadb:11","Status":"Up","Ports":"","State":"running"}'
  exit 0;;
exec)
  if [ "$mode" = execfail ]; then echo "mysqldump: Got error: 2013: Lost connection" >&2; exit 2; fi
  echo "-- MariaDB dump of shop"
  exit 0;;
esac
exit 1
`

type f64600Env struct {
	s       *Server
	state   string
	binDir  string
	backups string
}

func newF64600Env(t *testing.T, withDocker bool) *f64600Env {
	t.Helper()
	dir := t.TempDir()
	e := &f64600Env{binDir: filepath.Join(dir, "bin"), state: filepath.Join(dir, "state"), backups: filepath.Join(dir, "backups")}
	if err := os.MkdirAll(e.binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if withDocker {
		if err := os.WriteFile(filepath.Join(e.binDir, "docker"), []byte(f64600FakeDocker), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", e.binDir) // no mysqldump/mariadb-dump: native dump is the files-only case
	t.Setenv("FAKE_DOCKER_STATE", e.state)
	e.setMode(t, "ok")

	cfgPath := filepath.Join(dir, "uwas.yaml")
	if err := os.WriteFile(cfgPath, []byte("global:\n  log_level: error\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Global: config.GlobalConfig{Backup: config.BackupConfig{
		Enabled: true, Provider: "local", Keep: 1, Local: config.BackupLocalConfig{Path: e.backups},
	}}}
	e.s = New(cfg, logger.New("error", "text"))
	if e.s.backupMgr == nil {
		t.Fatal("backup manager not created")
	}
	e.s.SetConfigPath(cfgPath)
	t.Cleanup(func() { backup.SetDockerDumpFunc(nil) })
	return e
}

func (e *f64600Env) setMode(t *testing.T, mode string) {
	t.Helper()
	if err := os.WriteFile(e.state, []byte(mode+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (e *f64600Env) names(t *testing.T) map[string]bool {
	t.Helper()
	items, err := backup.NewLocalProvider(e.backups).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, it := range items {
		out[it.Name] = true
	}
	return out
}

// scenario: a complete backup (with the docker dump), then a second backup in
// mode; returns whether the first backup survived retention.
func f64600Scenario(t *testing.T, withDocker bool, mode string) (firstKept bool, count int) {
	e := newF64600Env(t, withDocker)
	first, err := e.s.backupMgr.CreateBackup("local")
	if err != nil {
		t.Fatalf("first backup: %v", err)
	}
	e.setMode(t, mode)
	if _, err := e.s.backupMgr.CreateBackup("local"); err != nil {
		t.Fatalf("second backup: %v", err)
	}
	n := e.names(t)
	return n[first.Name], len(n)
}

func TestBackupDockerDumpFailureKeepsOlderBackup(t *testing.T) {
	for _, mode := range []string{"execfail", "psfail"} {
		if kept, n := f64600Scenario(t, true, mode); !kept || n != 2 {
			t.Errorf("%s: older backup holding the docker dump kept=%v (count %d), want kept", mode, kept, n)
		}
	}
	// Controls: every dump succeeded, or no docker binary at all (files-only
	// host): retention applies.
	if kept, n := f64600Scenario(t, true, "ok"); kept || n != 1 {
		t.Errorf("all dumps ok: kept=%v count=%d, want pruned to 1", kept, n)
	}
	if kept, n := f64600Scenario(t, false, "ok"); kept || n != 1 {
		t.Errorf("no docker binary: kept=%v count=%d, want pruned to 1", kept, n)
	}
}

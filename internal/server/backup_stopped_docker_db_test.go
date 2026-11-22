package server

import (
	"os"
	"path/filepath"
	"testing"
)

// F68401: a stopped docker DB container was skipped without an error, so the
// backup taken meanwhile counted as complete and retention pruned the older
// backup still holding its databases (the displacement F64600 forbids).
func TestBackupStoppedDockerDBKeepsOlderBackup(t *testing.T) {
	const fake = `#!/bin/sh
read mode < "$FAKE_DOCKER_STATE"
case "$1" in
ps)
  state=running; [ "$mode" = stopped ] && state=exited
  echo '{"ID":"a","Names":"uwas-db-shop","Image":"mariadb:11","Status":"x","Ports":"","State":"'$state'"}'
  echo '{"ID":"b","Names":"uwas-db-pg","Image":"postgres:17","Status":"x","Ports":"","State":"exited"}'
  exit 0;;
exec) echo "-- dump"; exit 0;;
esac
exit 1
`
	run := func(second string) int {
		e := newF64600Env(t, true)
		if err := os.WriteFile(filepath.Join(e.binDir, "docker"), []byte(fake), 0o755); err != nil {
			t.Fatal(err)
		}
		for _, mode := range []string{"ok", second} {
			e.setMode(t, mode)
			if _, err := e.s.backupMgr.CreateBackup("local"); err != nil {
				t.Fatalf("backup (%s): %v", mode, err)
			}
		}
		return len(e.names(t))
	}
	if n := run("stopped"); n != 2 {
		t.Errorf("MariaDB container stopped at the second backup: %d backups left, want 2 (older one kept)", n)
	}
	// Control: everything dumpable dumped (the stopped Postgres container was
	// never in a backup, so it does not hold retention back).
	if n := run("ok"); n != 1 {
		t.Errorf("all dumpable containers running: %d backups left, want 1", n)
	}
}

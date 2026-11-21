package backup

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// F64600: a Docker DB container that could not be dumped left a backup
// without its databases, which retention counted as complete and used to
// prune the older backup that still held them. The dumps that did succeed are
// still archived.
func TestPruneSkippedWhenDockerDumpFails(t *testing.T) {
	origDump, origDocker := dumpAllDatabasesFunc, dockerDumpFn
	t.Cleanup(func() { dumpAllDatabasesFunc, dockerDumpFn = origDump, origDocker })
	dumpAllDatabasesFunc = func() ([]byte, error) { return nil, exec.ErrNotFound }

	m, mp := testManager(t)
	m.SetKeepCount(1)
	cfg := filepath.Join(t.TempDir(), "uwas.yaml")
	if err := os.WriteFile(cfg, []byte("global:\n  log_level: info\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.SetPaths(cfg, "")
	count := func() int {
		items, err := mp.List(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return len(items)
	}

	SetDockerDumpFunc(func() (map[string][]byte, error) {
		return map[string][]byte{"shop": []byte("-- shop"), "blog": []byte("-- blog")}, nil
	})
	if _, err := m.CreateBackup("mem"); err != nil {
		t.Fatal(err)
	}

	SetDockerDumpFunc(func() (map[string][]byte, error) {
		return map[string][]byte{"shop": []byte("-- shop")}, errors.New("uwas-db-blog: exit status 2")
	})
	partial, err := m.CreateBackup("mem")
	if err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 2 {
		t.Fatalf("backups after a failed docker dump = %d, want 2 (older one holding the dump kept)", n)
	}
	var dbs []string
	for _, name := range tarEntryNames(t, mp.files[partial.Name]) {
		if strings.HasPrefix(name, "databases/") {
			dbs = append(dbs, name)
		}
	}
	if len(dbs) != 1 || dbs[0] != "databases/docker-shop.sql" {
		t.Fatalf("partial backup databases = %v, want the dump that succeeded", dbs)
	}

	// Control: every container dumped -> retention applies again.
	SetDockerDumpFunc(func() (map[string][]byte, error) {
		return map[string][]byte{"shop": []byte("-- shop"), "blog": []byte("-- blog")}, nil
	})
	if _, err := m.CreateBackup("mem"); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 1 {
		t.Fatalf("backups after a complete one = %d, want 1", n)
	}
}

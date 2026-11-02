package backup

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// F63000: a native dump that fails while a client exists (server down, bad
// credentials) produced a DB-less backup that retention counted as complete,
// deleting the older one that still held the databases. A host with no client
// at all is the normal files-only case and keeps pruning.
func TestPruneSkippedWhenDatabaseDumpFails(t *testing.T) {
	origDump, origDocker := dumpAllDatabasesFunc, dockerDumpFn
	defer func() { dumpAllDatabasesFunc, dockerDumpFn = origDump, origDocker }()
	dockerDumpFn = nil

	m, mp := testManager(t)
	m.SetKeepCount(1)
	cfg := filepath.Join(t.TempDir(), "uwas.yaml")
	if err := os.WriteFile(cfg, []byte("global:\n  log_level: info\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.SetPaths(cfg, "")

	count := func() (n int, names map[string]bool) {
		names = map[string]bool{}
		items, _ := mp.List(context.Background())
		for _, it := range items {
			names[it.Name] = true
		}
		return len(items), names
	}

	dumpAllDatabasesFunc = func() ([]byte, error) { return []byte("-- dump\n"), nil }
	first, err := m.CreateBackup("mem")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)

	dumpAllDatabasesFunc = func() ([]byte, error) { return nil, errors.New("exit status 2") }
	if _, err := m.CreateBackup("mem"); err != nil {
		t.Fatal(err)
	}
	if _, names := count(); !names[first.Name] {
		t.Fatalf("backup %s holding the databases was pruned for a DB-less one; have %v", first.Name, names)
	}

	// Control: no client installed is the files-only host; retention applies.
	time.Sleep(5 * time.Millisecond)
	dumpAllDatabasesFunc = func() ([]byte, error) { return nil, exec.ErrNotFound }
	if _, err := m.CreateBackup("mem"); err != nil {
		t.Fatal(err)
	}
	if n, _ := count(); n != 1 {
		t.Fatalf("without a dump client retention keeps %d, want 1", n)
	}
}

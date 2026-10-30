package backup

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestPruneSkippedWhenNewBackupMissesDomainRoot: domain roots are archived
// best-effort, so an unreadable directory yields a "successful" backup that
// lacks site content. Retention used to count it as a full backup and delete
// the older complete one; with keep=1 the only restorable copy was lost.
func TestPruneSkippedWhenNewBackupMissesDomainRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission-based failure injection does not apply to root")
	}
	m, mp := testManager(t)
	m.SetKeepCount(1)

	tmp := t.TempDir()
	cfg := filepath.Join(tmp, "uwas.yaml")
	if err := os.WriteFile(cfg, []byte("global:\n  log_level: info\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(tmp, "site")
	if err := os.MkdirAll(filepath.Join(root, "locked"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.SetPaths(cfg, "")
	m.SetDomainPaths("", "", []string{root})

	first, err := m.CreateBackup("mem")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)

	if err := os.Chmod(filepath.Join(root, "locked"), 0); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(filepath.Join(root, "locked"), 0o755) }()
	if _, err := os.ReadDir(filepath.Join(root, "locked")); err == nil {
		t.Skip("directory permissions are not enforced here")
	}
	if _, err := m.CreateBackup("mem"); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	items, _ := mp.List(nil)
	for _, it := range items {
		names[it.Name] = true
	}
	if !names[first.Name] {
		t.Fatalf("complete backup %s was pruned in favour of an incomplete one; have %v", first.Name, names)
	}

	// Control: once the root is readable again the next backup is complete and
	// retention resumes.
	if err := os.Chmod(filepath.Join(root, "locked"), 0o755); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := m.CreateBackup("mem"); err != nil {
		t.Fatal(err)
	}
	items, _ = mp.List(nil)
	if len(items) != 1 {
		t.Fatalf("after a complete backup retention keeps %d, want 1", len(items))
	}
}

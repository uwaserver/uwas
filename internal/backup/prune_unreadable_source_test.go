package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// F64400/F64401: a full backup that could not read domains.d, a domain root or
// the certs dir was treated as complete (or the unreadable path as absent), so
// retention deleted the older backup that still held that content.
func TestBackupUnreadableSourceKeepsOlderBackup(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses permission bits")
	}
	noDBClient(t)
	dir := t.TempDir()
	cfg := filepath.Join(dir, "uwas.yaml")
	if err := os.WriteFile(cfg, []byte("global: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sealed := filepath.Join(dir, "sealed")
	for _, d := range []string{"certs", "site", "domains.d"} {
		if err := os.MkdirAll(filepath.Join(sealed, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	locked := filepath.Join(sealed, "domains.d", "b.example.yaml")
	if err := os.WriteFile(locked, []byte("host: b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sealed, 0o755); _ = os.Chmod(locked, 0o644) })

	newMgr := func(name string) (*BackupManager, *LocalProvider) {
		m, _ := testManager(t)
		st := NewLocalProvider(filepath.Join(dir, name))
		m.providers["mem"] = st
		m.SetKeepCount(1)
		m.SetPaths(cfg, "")
		return m, st
	}
	keeps := func(t *testing.T, m *BackupManager, st *LocalProvider, breakIt, fixIt func()) {
		t.Helper()
		first, err := m.CreateBackup("mem")
		if err != nil {
			t.Fatal(err)
		}
		breakIt()
		_, err = m.CreateBackup("mem")
		fixIt()
		if err != nil {
			t.Fatalf("best-effort source failure returned %v", err)
		}
		items, _ := st.List(context.Background())
		for _, it := range items {
			if it.Name == first.Name {
				return
			}
		}
		t.Fatalf("complete backup %s pruned for an incomplete one", first.Name)
	}

	t.Run("domains.d entry unreadable", func(t *testing.T) {
		m, st := newMgr("s1")
		m.SetDomainPaths("", filepath.Join(sealed, "domains.d"), nil)
		keeps(t, m, st, func() { _ = os.Chmod(locked, 0) }, func() { _ = os.Chmod(locked, 0o644) })
	})
	t.Run("domain root unreadable", func(t *testing.T) {
		m, st := newMgr("s2")
		m.SetDomainPaths(dir, "", []string{filepath.Join(sealed, "site")})
		keeps(t, m, st, func() { _ = os.Chmod(sealed, 0) }, func() { _ = os.Chmod(sealed, 0o755) })
	})
	t.Run("certs dir unreadable fails", func(t *testing.T) {
		m, st := newMgr("s3")
		m.SetPaths(cfg, filepath.Join(sealed, "certs"))
		_ = os.Chmod(sealed, 0)
		_, err := m.CreateBackup("mem")
		_ = os.Chmod(sealed, 0o755)
		if err == nil {
			t.Fatal("unreadable certs dir: backup reported success without the certificates")
		}
		if items, _ := st.List(context.Background()); len(items) != 0 {
			t.Fatalf("uploaded %d archives without certs", len(items))
		}
	})
	t.Run("absent paths still prune", func(t *testing.T) {
		m, st := newMgr("s4")
		m.SetPaths(cfg, filepath.Join(dir, "nocerts"))
		m.SetDomainPaths(dir, filepath.Join(dir, "nodomains"), []string{filepath.Join(dir, "noroot")})
		for i := 0; i < 2; i++ {
			if _, err := m.CreateBackup("mem"); err != nil {
				t.Fatal(err)
			}
		}
		if items, _ := st.List(context.Background()); len(items) != 1 {
			t.Fatalf("retention kept %d, want 1", len(items))
		}
	})
}

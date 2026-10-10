//go:build unix

package cli

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestCreateBackupArchiveIsOwnerOnly(t *testing.T) {
	old := syscall.Umask(0o022)
	defer syscall.Umask(old)
	dir := t.TempDir()
	cfg := filepath.Join(dir, "uwas.yaml")
	os.WriteFile(cfg, []byte("api_key: secret\n"), 0o600)
	certs := filepath.Join(dir, "certs")
	os.MkdirAll(certs, 0o700)
	os.WriteFile(filepath.Join(certs, "a.key"), []byte("KEY"), 0o600)

	// fresh output, pre-existing 0644 output (re-run), and empty certs dir
	fresh := filepath.Join(dir, "fresh.tar.gz")
	prev := filepath.Join(dir, "prev.tar.gz")
	os.WriteFile(prev, []byte("old"), 0o644)
	for _, out := range []string{fresh, prev} {
		if err := createBackup(out, cfg, certs); err != nil {
			t.Fatal(err)
		}
		if m := mustMode(t, out); m != 0o600 {
			t.Fatalf("%s mode %#o, want 0600", out, m)
		}
	}
	// round trip still restores
	cd, kd := filepath.Join(dir, "rc"), filepath.Join(dir, "rk")
	if err := restoreBackup(fresh, cd, kd); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(kd, "a.key")); string(b) != "KEY" {
		t.Fatalf("restore lost key: %q", b)
	}

}

func mustMode(t *testing.T, p string) os.FileMode {
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return st.Mode().Perm()
}

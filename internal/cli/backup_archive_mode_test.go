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

// A destination that is not a regular file (a dry run to /dev/null, a pipe)
// cannot be chmod'ed by an unprivileged user; the F1150 chmod must not turn
// that into a failed backup (F1780).
func TestCreateBackupToDeviceDestination(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root may chmod /dev/null")
	}
	if st, err := os.Stat("/dev/null"); err != nil || st.Mode()&os.ModeCharDevice == 0 {
		t.Skip("no /dev/null")
	}
	dir := t.TempDir()
	cfg := filepath.Join(dir, "uwas.yaml")
	os.WriteFile(cfg, []byte("api_key: secret\n"), 0o600)
	certs := filepath.Join(dir, "certs")
	os.MkdirAll(certs, 0o700)
	if err := createBackup("/dev/null", cfg, certs); err != nil {
		t.Fatalf("createBackup(/dev/null): %v", err)
	}
}

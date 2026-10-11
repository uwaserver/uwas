package siteuser

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sshd_config must be replaced atomically: a truncating in-place write that
// is interrupted leaves a config sshd cannot start with (F2050).
func TestSSHDConfigReplacedAtomically(t *testing.T) {
	hooks := saveHooks()
	defer restoreHooks(hooks)

	dir := t.TempDir()
	path := filepath.Join(dir, "sshd_config")
	old := "Port 22\nSubsystem sftp internal-sftp\n"
	if err := os.WriteFile(path, []byte(old), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0640); err != nil {
		t.Fatal(err)
	}
	sshdConfigPath = path
	execCommandFn = fakeExecCommand

	snapshot, err := os.Open(path) // descriptor taken before the update
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()

	if err := ensureSFTPConfig("uwas-x--com", "/var/www/x.com", "/public_html"); err != nil {
		t.Fatalf("ensureSFTPConfig: %v", err)
	}

	now, _ := os.ReadFile(path)
	if !strings.Contains(string(now), "Match User uwas-x--com") {
		t.Fatalf("new block missing at path:\n%s", now)
	}
	if seen, _ := io.ReadAll(snapshot); string(seen) != old {
		t.Errorf("pre-update descriptor saw %q, want the old whole file %q", seen, old)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0640 {
		t.Errorf("mode = %v, want 0640 preserved", info.Mode().Perm())
	}
	if _, err := os.Stat(path + ".uwas-new"); !os.IsNotExist(err) {
		t.Errorf("temp file left behind: %v", err)
	}
}

func TestSSHDConfigWriteFailureKeepsOriginal(t *testing.T) {
	hooks := saveHooks()
	defer restoreHooks(hooks)

	dir := t.TempDir()
	path := filepath.Join(dir, "sshd_config")
	old := "Port 22\n"
	if err := os.WriteFile(path, []byte(old), 0644); err != nil {
		t.Fatal(err)
	}
	sshdConfigPath = path
	execCommandFn = fakeExecCommand

	// A stale temp file from an earlier crash must not break the next update.
	if err := os.WriteFile(path+".uwas-new", []byte("junk"), 0644); err != nil {
		t.Fatal(err)
	}

	osWriteFileFn = func(name string, data []byte, perm os.FileMode) error {
		// Simulate a disk-full failure after a partial write.
		_ = os.WriteFile(name, data[:len(data)/2], perm)
		return errors.New("no space left on device")
	}
	if err := ensureSFTPConfig("uwas-x--com", "/var/www/x.com", "/public_html"); err == nil {
		t.Fatal("expected the write error to be returned")
	}
	if got, _ := os.ReadFile(path); string(got) != old {
		t.Errorf("sshd_config = %q after a failed update, want it untouched %q", got, old)
	}
	if _, err := os.Stat(path + ".uwas-new"); !os.IsNotExist(err) {
		t.Errorf("partial temp file left behind: %v", err)
	}
}

func TestSSHDConfigRenameFailureCleansTemp(t *testing.T) {
	hooks := saveHooks()
	defer restoreHooks(hooks)

	dir := t.TempDir()
	path := filepath.Join(dir, "sshd_config")
	if err := os.WriteFile(path, []byte("Port 22\n"), 0644); err != nil {
		t.Fatal(err)
	}
	sshdConfigPath = path
	execCommandFn = fakeExecCommand
	// Make the destination un-replaceable: the config path becomes a non-empty
	// directory between the read and the publish.
	osReadFileFn = func(name string) ([]byte, error) {
		data, err := os.ReadFile(name)
		if err == nil {
			_ = os.Remove(name)
			_ = os.MkdirAll(filepath.Join(name, "keep"), 0755)
		}
		return data, err
	}
	if err := ensureSFTPConfig("uwas-x--com", "/var/www/x.com", "/public_html"); err == nil {
		t.Fatal("expected the rename error to be returned")
	}
	if _, err := os.Stat(path + ".uwas-new"); !os.IsNotExist(err) {
		t.Errorf("temp file left behind after a failed rename: %v", err)
	}
}

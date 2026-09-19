package siteuser

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestEnsureSFTPConfigWriteErrorSurfaced verifies that ensureSFTPConfig
// returns an error when osWriteFileFn fails, rather than silently returning.
// BUG: ensureSFTPConfig is void and both osWriteFileFn call sites swallow
// errors when sshdConfigWriteErr == nil (always in production).
func TestEnsureSFTPConfigWriteErrorSurfaced(t *testing.T) {
	hooks := saveHooks()
	defer restoreHooks(hooks)

	tmpDir := t.TempDir()
	sshdConfigPath = filepath.Join(tmpDir, "sshd_config")

	// Write a valid initial config so the function has content to update.
	osWriteFileFn(filepath.Join(tmpDir, "sshd_config"), []byte("# empty"), 0644)

	// Make the parent dir read-only so any write fails.
	os.Chmod(tmpDir, 0555)
	defer os.Chmod(tmpDir, 0755)

	// Override write to fail deterministically.
	writeErr := errors.New("disk full: write /etc/ssh/sshd_config: permission denied")
	osWriteFileFn = func(path string, data []byte, perm os.FileMode) error {
		return writeErr
	}
	// Ensure sshdConfigWriteErr is nil (production state).
	sshdConfigWriteErr = nil

	// Call ensureSFTPConfig — after fix, it returns the write error.
	err := ensureSFTPConfig("testuser", "/nonexistent", "/home/testuser")
	if err == nil {
		t.Error("ensureSFTPConfig returned nil error — should have returned the write error")
	} else {
		t.Logf("FIXED: ensureSFTPConfig returned error: %v", err)
	}
}

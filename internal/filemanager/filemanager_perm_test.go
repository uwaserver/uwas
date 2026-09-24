package filemanager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteFilePermission verifies that WriteFile creates files with owner-only
// permissions (0600), not world-readable (0644). Backup archives and TLS certs
// stored by the web root are sensitive — other local users on a shared host must
// not be able to read them.
func TestWriteFilePermission(t *testing.T) {
	baseDir := t.TempDir()

	// Write a file containing sensitive content.
	err := WriteFile(baseDir, "secret.txt", []byte("tls-key-MII..."))
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	path := filepath.Join(baseDir, "secret.txt")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	// Permission bits mask: 0777 → only owner read/write allowed.
	got := info.Mode().Perm()
	want := os.FileMode(0600)
	if got != want {
		t.Errorf("WriteFile permission = %#o, want %#o; "+
			"mode=%v — file is world-readable and must be owner-only",
			got, want, info.Mode())
	}
}

// TestSaveUploadPermission verifies that SaveUpload creates files with owner-only
// permissions (0600), not world-readable (0644).
func TestSaveUploadPermission(t *testing.T) {
	baseDir := t.TempDir()
	uploaded := strings.NewReader("backup-archive-tar-gz")
	fullPath := filepath.Join(baseDir, "subdir", "backup.tar.gz")

	n, err := SaveUpload(baseDir, "subdir/backup.tar.gz", uploaded)
	if err != nil {
		t.Fatalf("SaveUpload: %v", err)
	}
	if n == 0 {
		t.Fatalf("SaveUpload wrote 0 bytes")
	}

	info, err := os.Stat(fullPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	got := info.Mode().Perm()
	want := os.FileMode(0600)
	if got != want {
		t.Errorf("SaveUpload permission = %#o, want %#o; "+
			"mode=%v — file is world-readable and must be owner-only",
			got, want, info.Mode())
	}
}

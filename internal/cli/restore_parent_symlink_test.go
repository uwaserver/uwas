package cli

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func writeRestoreTestArchive(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "backup.tar.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create archive: %v", err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatalf("write header: %v", err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatalf("write body: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close archive: %v", err)
	}
	return path
}

func TestRestoreRejectsParentSymlinkEscape(t *testing.T) {
	configDir, certsDir, outside := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(configDir, "domains.d")); err != nil {
		t.Fatalf("create parent symlink: %v", err)
	}

	err := restoreBackup(writeRestoreTestArchive(t, "config/domains.d/escaped.yaml", "escaped"), configDir, certsDir)
	if err == nil {
		t.Fatal("restoreBackup() returned nil for escaping parent symlink")
	}
	if _, statErr := os.Stat(filepath.Join(outside, "escaped.yaml")); !os.IsNotExist(statErr) {
		t.Fatalf("outside file was created: %v", statErr)
	}
}

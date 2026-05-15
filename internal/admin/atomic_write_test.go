package admin

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestAtomicWriteFile_NewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cert.pem")
	if err := atomicWriteFile(path, []byte("hello"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("data = %q, want %q", data, "hello")
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(path)
		if info.Mode().Perm() != 0600 {
			t.Errorf("perm = %v, want 0600", info.Mode().Perm())
		}
	}
}

func TestAtomicWriteFile_OverwriteExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := atomicWriteFile(path, []byte("new"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "new" {
		t.Errorf("data = %q, want %q", data, "new")
	}
}

func TestAtomicWriteFile_NoStaleTempLeft(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.pem")
	if err := atomicWriteFile(path, []byte("x"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Directory must contain only the target file.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("expected 1 file, got %d: %v", len(entries), names)
	}
}

package filemanager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListDir(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hello"), 0644)
	os.MkdirAll(filepath.Join(dir, "subdir"), 0755)

	entries, err := List(dir, ".")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name] = true
	}
	if !names["file.txt"] || !names["subdir"] {
		t.Errorf("expected file.txt and subdir, got %v", names)
	}
}

func TestListSubdir(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "a", "b"), 0755)
	os.WriteFile(filepath.Join(dir, "a", "b", "deep.txt"), []byte("x"), 0644)

	entries, err := List(dir, "a/b")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "deep.txt" {
		t.Fatalf("expected deep.txt, got %v", entries)
	}
}

func TestReadWriteFile(t *testing.T) {
	dir := t.TempDir()

	err := WriteFile(dir, "test.txt", []byte("hello world"))
	if err != nil {
		t.Fatal(err)
	}

	data, err := ReadFile(dir, "test.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello world" {
		t.Errorf("content = %q, want %q", string(data), "hello world")
	}
}

func TestDelete(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "del.txt"), []byte("x"), 0644)

	err := Delete(dir, "del.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "del.txt")); !os.IsNotExist(err) {
		t.Error("file should be deleted")
	}
}

func TestDeletePreventBaseDir(t *testing.T) {
	dir := t.TempDir()
	err := Delete(dir, ".")
	if err == nil {
		t.Error("should not allow deleting base dir")
	}
}

func TestCreateDir(t *testing.T) {
	dir := t.TempDir()
	err := CreateDir(dir, "new/nested/dir")
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "new", "nested", "dir"))
	if err != nil || !info.IsDir() {
		t.Error("directory should exist")
	}
}

func TestSaveUpload(t *testing.T) {
	dir := t.TempDir()
	n, err := SaveUpload(dir, "uploaded.txt", strings.NewReader("upload content"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 14 {
		t.Errorf("bytes = %d, want 14", n)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "uploaded.txt"))
	if string(data) != "upload content" {
		t.Errorf("content = %q", string(data))
	}
}

func TestDiskUsage(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("12345"), 0644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("67890"), 0644)

	usage, err := DiskUsage(dir)
	if err != nil {
		t.Fatal(err)
	}
	if usage != 10 {
		t.Errorf("usage = %d, want 10", usage)
	}
}

func TestSafePathTraversal(t *testing.T) {
	dir := t.TempDir()
	if safePath(dir, "../../../etc/passwd") != "" {
		t.Error("should reject traversal")
	}
	if safePath(dir, "valid/path.txt") == "" {
		t.Error("should accept valid relative path")
	}
}

func TestSafePathTraversalDotDot(t *testing.T) {
	dir := t.TempDir()
	if safePath(dir, "../../..") != "" {
		t.Error("should reject pure traversal")
	}
	if safePath(dir, "a/../../../etc") != "" {
		t.Error("should reject sneaky traversal")
	}
}

func TestReadFileTooLarge(t *testing.T) {
	dir := t.TempDir()
	// Create a file > 5MB
	f, _ := os.Create(filepath.Join(dir, "big.bin"))
	f.Truncate(6 << 20)
	f.Close()

	_, err := ReadFile(dir, "big.bin")
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Errorf("expected too large error, got %v", err)
	}
}

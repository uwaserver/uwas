package filemanager

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestQuotaTracker(t *testing.T) {
	q := NewQuotaTracker(10)

	if got := q.Usage("site"); got != 0 {
		t.Fatalf("initial usage = %d, want 0", got)
	}
	if err := q.Check("site", 8); err != nil {
		t.Fatalf("unexpected quota error: %v", err)
	}
	q.Add("site", 8)
	if got := q.Usage("site"); got != 8 {
		t.Fatalf("usage = %d, want 8", got)
	}
	if err := q.Check("site", 3); err == nil {
		t.Fatal("expected quota exceeded error")
	}
	q.Reset("site")
	if got := q.Usage("site"); got != 0 {
		t.Fatalf("usage after reset = %d, want 0", got)
	}
}

func TestQuotaTrackerUnlimited(t *testing.T) {
	q := NewQuotaTracker(0)
	if err := q.Check("site", 1<<40); err != nil {
		t.Fatalf("unlimited quota should allow upload: %v", err)
	}
}

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

// --- Invalid path tests (safePath returns "") for every function ---

func TestListInvalidPath(t *testing.T) {
	dir := t.TempDir()
	_, err := List(dir, "../../etc")
	if err == nil || !strings.Contains(err.Error(), "invalid path") {
		t.Errorf("expected invalid path error, got %v", err)
	}
}

func TestListNonexistentDir(t *testing.T) {
	dir := t.TempDir()
	_, err := List(dir, "nonexistent")
	if err == nil {
		t.Error("expected error listing nonexistent directory")
	}
}

func TestReadFileInvalidPath(t *testing.T) {
	dir := t.TempDir()
	_, err := ReadFile(dir, "../../../etc/passwd")
	if err == nil || !strings.Contains(err.Error(), "invalid path") {
		t.Errorf("expected invalid path error, got %v", err)
	}
}

func TestReadFileNonexistent(t *testing.T) {
	dir := t.TempDir()
	_, err := ReadFile(dir, "nosuchfile.txt")
	if err == nil {
		t.Error("expected error reading nonexistent file")
	}
}

func TestReadFileIsDirectory(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "subdir"), 0755)
	_, err := ReadFile(dir, "subdir")
	if err == nil || !strings.Contains(err.Error(), "cannot read directory") {
		t.Errorf("expected 'cannot read directory' error, got %v", err)
	}
}

func TestWriteFileInvalidPath(t *testing.T) {
	dir := t.TempDir()
	err := WriteFile(dir, "../../../tmp/evil.txt", []byte("bad"))
	if err == nil || !strings.Contains(err.Error(), "invalid path") {
		t.Errorf("expected invalid path error, got %v", err)
	}
}

func TestDeleteInvalidPath(t *testing.T) {
	dir := t.TempDir()
	err := Delete(dir, "../../escape")
	if err == nil || !strings.Contains(err.Error(), "invalid path") {
		t.Errorf("expected invalid path error, got %v", err)
	}
}

func TestCreateDirInvalidPath(t *testing.T) {
	dir := t.TempDir()
	err := CreateDir(dir, "../../../escape")
	if err == nil || !strings.Contains(err.Error(), "invalid path") {
		t.Errorf("expected invalid path error, got %v", err)
	}
}

func TestSaveUploadInvalidPath(t *testing.T) {
	dir := t.TempDir()
	_, err := SaveUpload(dir, "../../escape.txt", strings.NewReader("x"))
	if err == nil || !strings.Contains(err.Error(), "invalid path") {
		t.Errorf("expected invalid path error, got %v", err)
	}
}

func TestSaveUploadCreateError(t *testing.T) {
	dir := t.TempDir()
	// Try to save to a path where a directory exists with the same name
	os.MkdirAll(filepath.Join(dir, "blocker"), 0755)
	_, err := SaveUpload(dir, "blocker", strings.NewReader("x"))
	if err == nil {
		t.Error("expected error creating file where directory exists")
	}
}

func TestDiskUsageSubdirs(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sub"), 0755)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("aaa"), 0644)
	os.WriteFile(filepath.Join(dir, "sub", "b.txt"), []byte("bbbbb"), 0644)

	usage, err := DiskUsage(dir)
	if err != nil {
		t.Fatal(err)
	}
	if usage != 8 {
		t.Errorf("usage = %d, want 8", usage)
	}
}

func TestSafePathAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	// On Windows, filepath.IsAbs requires a drive letter prefix.
	// On Unix, /etc/passwd is absolute.
	// Use a path that's absolute on the current OS.
	absPath := filepath.VolumeName(dir) + string(filepath.Separator) + "etc" + string(filepath.Separator) + "passwd"
	if safePath(dir, absPath) != "" {
		t.Error("should reject absolute path")
	}
}

func TestSafePathCleanTraversal(t *testing.T) {
	dir := t.TempDir()
	// After Clean, "foo/../../.." becomes ".." which starts with ".."
	if safePath(dir, "foo/../../..") != "" {
		t.Error("should reject path that cleans to traversal")
	}
}

func TestSafePathValidNested(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "a", "b"), 0755)
	result := safePath(dir, "a/b")
	if result == "" {
		t.Error("should accept valid nested path")
	}
}

func TestDeleteSubdirectory(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sub", "nested"), 0755)
	os.WriteFile(filepath.Join(dir, "sub", "nested", "file.txt"), []byte("x"), 0644)

	err := Delete(dir, "sub")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sub")); !os.IsNotExist(err) {
		t.Error("subdirectory should be deleted")
	}
}

func TestWriteFileCreatesSubdirs(t *testing.T) {
	dir := t.TempDir()
	err := WriteFile(dir, "new/sub/file.txt", []byte("content"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "new", "sub", "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "content" {
		t.Errorf("content = %q", string(data))
	}
}

func TestSaveUploadCreatesSubdirs(t *testing.T) {
	dir := t.TempDir()
	n, err := SaveUpload(dir, "deep/nested/upload.txt", strings.NewReader("data"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Errorf("bytes = %d, want 4", n)
	}
}

func TestListEntryProperties(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello"), 0644)
	os.MkdirAll(filepath.Join(dir, "mydir"), 0755)

	entries, err := List(dir, ".")
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range entries {
		if e.Name == "hello.txt" {
			if e.IsDir {
				t.Error("hello.txt should not be a dir")
			}
			if e.Size != 5 {
				t.Errorf("hello.txt size = %d, want 5", e.Size)
			}
			if e.Path == "" {
				t.Error("path should not be empty")
			}
			if e.Mode == "" {
				t.Error("mode should not be empty")
			}
		}
		if e.Name == "mydir" {
			if !e.IsDir {
				t.Error("mydir should be a dir")
			}
		}
	}
}

func TestDiskUsageEmptyDir(t *testing.T) {
	dir := t.TempDir()
	usage, err := DiskUsage(dir)
	if err != nil {
		t.Fatal(err)
	}
	if usage != 0 {
		t.Errorf("empty dir usage = %d, want 0", usage)
	}
}

func TestSafePathSymlinkOutsideBase(t *testing.T) {
	dir := t.TempDir()
	outsideDir := t.TempDir()

	// Override evalSymlinks to simulate a symlink pointing outside the base
	origEval := evalSymlinks
	evalSymlinks = func(path string) (string, error) {
		absBase, _ := filepath.Abs(dir)
		absPath, _ := filepath.Abs(path)
		if absPath == filepath.Join(absBase, "link") {
			// Simulate symlink resolving to outside directory
			return filepath.Join(outsideDir, "escaped"), nil
		}
		return origEval(path)
	}
	defer func() { evalSymlinks = origEval }()

	// Create the "link" entry so the path exists for Abs resolution
	os.WriteFile(filepath.Join(dir, "link"), []byte("x"), 0644)

	result := safePath(dir, "link")
	if result != "" {
		t.Error("should reject path where symlink resolves outside base")
	}
}

func TestSafePathPrefixCheck(t *testing.T) {
	// Test the !strings.HasPrefix(absFull, absBase) defense-in-depth check.
	// Override absFunc so that the full path does not appear to be under the base.
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "ok.txt"), []byte("x"), 0644)

	origAbs := absFunc
	absCallCount := 0
	absFunc = func(path string) (string, error) {
		absCallCount++
		real, err := origAbs(path)
		if err != nil {
			return real, err
		}
		// On the second call (absFull), return a path that is NOT under absBase
		if absCallCount%2 == 0 {
			return filepath.Join(t.TempDir(), "outside"), nil
		}
		return real, nil
	}
	defer func() { absFunc = origAbs }()

	result := safePath(dir, "ok.txt")
	if result != "" {
		t.Error("should reject path when absolute resolution puts it outside base")
	}
}

func TestDiskUsageWalkError(t *testing.T) {
	// filepath.Walk calls the WalkFunc with a non-nil error for nonexistent paths.
	// DiskUsage skips those errors (returns nil from the callback).
	usage, err := DiskUsage(filepath.Join(t.TempDir(), "nonexistent_subdir"))
	if err != nil {
		t.Fatalf("DiskUsage should not return error (errors are skipped): %v", err)
	}
	if usage != 0 {
		t.Errorf("usage = %d, want 0 for nonexistent path", usage)
	}
}

func TestListEntryInfoError(t *testing.T) {
	// Test the entry.Info() error branch in List by hooking entryInfo.
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "good.txt"), []byte("g"), 0644)
	os.WriteFile(filepath.Join(dir, "bad.txt"), []byte("b"), 0644)

	origInfo := entryInfo
	entryInfo = func(e os.DirEntry) (os.FileInfo, error) {
		if e.Name() == "bad.txt" {
			return nil, fmt.Errorf("simulated info error")
		}
		return e.Info()
	}
	defer func() { entryInfo = origInfo }()

	entries, err := List(dir, ".")
	if err != nil {
		t.Fatal(err)
	}
	// Only "good.txt" should be included; "bad.txt" should be skipped
	if len(entries) != 1 {
		t.Errorf("expected 1 entry (bad.txt skipped), got %d", len(entries))
	}
	if len(entries) > 0 && entries[0].Name != "good.txt" {
		t.Errorf("expected good.txt, got %q", entries[0].Name)
	}
}

func TestPathHelpersAbsError(t *testing.T) {
	origAbs := absFunc
	defer func() { absFunc = origAbs }()

	absFunc = func(path string) (string, error) {
		return "", errors.New("abs failed")
	}

	if isWithinBase("base", "target") {
		t.Fatal("isWithinBase should fail when base abs fails")
	}
	if isWithinBaseResolved("base", "target") {
		t.Fatal("isWithinBaseResolved should fail when resolvePath fails")
	}
	if got := safePath("base", "target"); got != "" {
		t.Fatalf("safePath = %q, want empty", got)
	}
}

func TestResolvePathNonNotExistError(t *testing.T) {
	origEval := evalSymlinks
	defer func() { evalSymlinks = origEval }()

	evalSymlinks = func(path string) (string, error) {
		return "", errors.New("permission denied")
	}

	if _, err := resolvePath(t.TempDir()); err == nil {
		t.Fatal("expected resolvePath error")
	}
}

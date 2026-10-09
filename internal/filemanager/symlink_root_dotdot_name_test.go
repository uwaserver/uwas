package filemanager

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDiskUsageSymlinkedRoot pins F76: WalkDir does not follow a symlinked
// root, so a web root reached through a symlink reported only the link size.
func TestDiskUsageSymlinkedRoot(t *testing.T) {
	tmp := t.TempDir()
	real := filepath.Join(tmp, "real")
	if err := os.MkdirAll(filepath.Join(real, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(real, "a.bin"), make([]byte, 4000), 0o644)
	os.WriteFile(filepath.Join(real, "sub", "b.bin"), make([]byte, 1000), 0o644)
	link := filepath.Join(tmp, "webroot")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	for _, dir := range []string{real, link} {
		got, err := DiskUsage(dir)
		if err != nil || got != 5000 {
			t.Errorf("DiskUsage(%s) = %d, %v; want 5000, nil", dir, got, err)
		}
	}
}

// TestSafePathAllowsDotDotPrefixedNames pins F77: in-root names that merely
// start with ".." (e.g. "..data") were rejected as traversal.
func TestSafePathAllowsDotDotPrefixedNames(t *testing.T) {
	base := filepath.Join(t.TempDir(), "root")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"..data", "...", "a/..b", "..cache/sub"} {
		if err := WriteFile(base, p, []byte("x")); err != nil {
			t.Errorf("WriteFile(%q) = %v; want nil", p, err)
		}
	}
	for _, p := range []string{"..", "../escape", "a/../../escape"} {
		if err := WriteFile(base, p, []byte("x")); err == nil {
			t.Errorf("WriteFile(%q) accepted traversal", p)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(base), "escape")); err == nil {
		t.Fatal("traversal wrote outside the web root")
	}
}

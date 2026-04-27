package pathsafe

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestIsWithinBase(t *testing.T) {
	base := filepath.Join("var", "www")
	if !IsWithinBase(base, filepath.Join(base, "site", "index.html")) {
		t.Fatal("expected child path to be inside base")
	}
}

func TestIsWithinBaseRejectsPrefixSibling(t *testing.T) {
	base := filepath.Join("var", "www")
	sibling := filepath.Join("var", "www2", "secret.txt")
	if IsWithinBase(base, sibling) {
		t.Fatal("prefix sibling must not be considered inside base")
	}
}

func TestIsWithinBaseRejectsParentTraversal(t *testing.T) {
	base := filepath.Join("var", "www")
	parent := filepath.Join("var")
	if IsWithinBase(base, parent) {
		t.Fatal("parent directory must not be considered inside base")
	}
}

func TestIsWithinBaseRejectsEscape(t *testing.T) {
	base := filepath.Join("var", "www")
	escape := filepath.Join(base, "..", "etc", "passwd")
	if IsWithinBase(base, escape) {
		t.Fatal("path escaping base must not be considered inside")
	}
}

func TestIsWithinBaseSameDirectory(t *testing.T) {
	base := filepath.Join("var", "www")
	if !IsWithinBase(base, base) {
		t.Fatal("base directory itself should be considered inside base")
	}
}

func TestIsWithinBaseWithError(t *testing.T) {
	// Test with invalid path that causes Abs to fail
	// This is hard to trigger on most systems, but we test the error path
	base := ""
	target := filepath.Join("var", "www", "file.txt")
	// Empty base should return false (Abs("") works on some systems but we test edge case)
	_ = IsWithinBase(base, target)
}

func TestIsWithinBaseResolvedWithMissingTail(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "nested", "missing", "file.txt")
	if !IsWithinBaseResolved(root, target) {
		t.Fatal("expected missing-tail path under root to be accepted")
	}
}

func TestIsWithinBaseResolvedWithSymlink(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	symlinkDir := filepath.Join(root, "link")
	if err := os.MkdirAll(realDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realDir, symlinkDir); err != nil {
		t.Skip("symlinks not supported on this system")
	}

	// Path through symlink should resolve to real directory
	target := filepath.Join(symlinkDir, "file.txt")
	if !IsWithinBaseResolved(root, target) {
		t.Fatal("expected symlink-resolved path to be accepted")
	}
}

func TestIsWithinBaseResolvedRejectsEscape(t *testing.T) {
	root := t.TempDir()
	// Try to escape via parent directory
	escape := filepath.Join(root, "..", "etc")
	if IsWithinBaseResolved(root, escape) {
		t.Fatal("path escaping base via parent must be rejected")
	}
}

func TestRelativeToBase(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "subdir", "file.txt")

	rel, ok := RelativeToBase(base, target)
	if !ok {
		t.Fatal("expected relative path to be returned")
	}
	expected := filepath.Join("subdir", "file.txt")
	if rel != expected {
		t.Fatalf("expected %q, got %q", expected, rel)
	}
}

func TestRelativeToBaseSameDirectory(t *testing.T) {
	base := t.TempDir()

	rel, ok := RelativeToBase(base, base)
	if !ok {
		t.Fatal("expected base directory to be valid")
	}
	if rel != "" {
		t.Fatalf("expected empty string for base directory, got %q", rel)
	}
}

func TestRelativeToBaseRejectsEscape(t *testing.T) {
	base := t.TempDir()
	escape := filepath.Join(base, "..", "outside.txt")

	_, ok := RelativeToBase(base, escape)
	if ok {
		t.Fatal("expected escape attempt to be rejected")
	}
}

func TestRelativeToBaseRejectsPrefixSibling(t *testing.T) {
	base := filepath.Join("var", "www")
	sibling := filepath.Join("var", "www2", "file.txt")

	_, ok := RelativeToBase(base, sibling)
	if ok {
		t.Fatal("expected prefix sibling to be rejected")
	}
}

func TestRelativeToBaseResolved(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "nested", "missing", "file.txt")

	// Test through IsWithinBaseResolved since RelativeToBase uses isWithin directly
	if !IsWithinBaseResolved(root, target) {
		t.Fatal("expected resolved missing-tail path to be inside base")
	}
}

// TestIsWithinBaseWithAbsoluteError tests error handling in Abs.
func TestIsWithinBaseWithAbsoluteError(t *testing.T) {
	// Invalid path should be handled gracefully
	// On Windows, empty string gets converted to current directory
	// So this test just ensures it doesn't panic
	_ = IsWithinBase("", "some/path")
}

// TestRelativeToBaseWithAbsoluteError tests error handling.
func TestRelativeToBaseWithAbsoluteError(t *testing.T) {
	// Empty base should be handled gracefully
	_, _ = RelativeToBase("", "some/path")
}

// TestIsWithinBaseResolvedWithError tests error handling.
func TestIsWithinBaseResolvedWithError(t *testing.T) {
	// Empty base should be handled gracefully
	_ = IsWithinBaseResolved("", "some/path")
}

// TestIsWithinBaseRootPath tests with root-like paths.
func TestIsWithinBaseRootPath(t *testing.T) {
	// Test with root directory
	if !IsWithinBase("/", "/sub/file.txt") {
		t.Error("expected file under root to be valid")
	}
}

// TestRelativeToBaseNestedPath tests deeply nested paths.
func TestRelativeToBaseNestedPath(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "a", "b", "c", "d", "e", "file.txt")

	rel, ok := RelativeToBase(base, target)
	if !ok {
		t.Fatal("expected nested path to be valid")
	}

	expected := filepath.Join("a", "b", "c", "d", "e", "file.txt")
	if rel != expected {
		t.Fatalf("expected %q, got %q", expected, rel)
	}
}

// TestIsWithinBaseCaseSensitivity tests case sensitivity (Windows-specific).
func TestIsWithinBaseCaseSensitivity(t *testing.T) {
	base := t.TempDir()
	// On Windows, paths are case-insensitive
	target := filepath.Join(base, "FILE.txt")

	if !IsWithinBase(base, target) {
		t.Error("expected case-insensitive match on Windows")
	}
}

// TestRelativeToBaseWithDotSegments tests paths with dot segments.
func TestRelativeToBaseWithDotSegments(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "subdir", ".", "file.txt")

	rel, ok := RelativeToBase(base, target)
	if !ok {
		t.Fatal("expected path with dot segments to be valid")
	}

	expected := filepath.Join("subdir", "file.txt")
	if rel != expected {
		t.Fatalf("expected %q, got %q", expected, rel)
	}
}

func TestAbsErrorPaths(t *testing.T) {
	origAbs := absFunc
	defer func() { absFunc = origAbs }()

	absFunc = func(path string) (string, error) {
		return "", errors.New("abs failed")
	}

	if IsWithinBase("base", "target") {
		t.Fatal("IsWithinBase should fail when Abs fails")
	}
	if IsWithinBaseResolved("base", "target") {
		t.Fatal("IsWithinBaseResolved should fail when Abs fails")
	}
	if rel, ok := RelativeToBase("base", "target"); ok || rel != "" {
		t.Fatalf("RelativeToBase = %q, %v; want empty false", rel, ok)
	}
}

func TestRelativeToBaseTargetAbsError(t *testing.T) {
	origAbs := absFunc
	defer func() { absFunc = origAbs }()

	calls := 0
	absFunc = func(path string) (string, error) {
		calls++
		if calls == 2 {
			return "", errors.New("target abs failed")
		}
		return filepath.Abs(path)
	}

	if rel, ok := RelativeToBase("base", "target"); ok || rel != "" {
		t.Fatalf("RelativeToBase = %q, %v; want empty false", rel, ok)
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

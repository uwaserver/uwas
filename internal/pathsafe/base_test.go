package pathsafe

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNewBaseSuccess(t *testing.T) {
	root := t.TempDir()
	b, err := NewBase(root)
	if err != nil {
		t.Fatalf("NewBase: %v", err)
	}
	if b.Raw() != root {
		t.Errorf("Raw() = %q, want %q", b.Raw(), root)
	}
	if b.Resolved() == "" {
		t.Error("Resolved() should be non-empty")
	}
	// Resolved should match resolvePath(root).
	want, _ := resolvePath(root)
	if b.Resolved() != want {
		t.Errorf("Resolved() = %q, want %q", b.Resolved(), want)
	}
}

func TestNewBaseError(t *testing.T) {
	origAbs := absFunc
	defer func() { absFunc = origAbs }()
	absFunc = func(string) (string, error) { return "", errors.New("abs failed") }

	if _, err := NewBase("anything"); err == nil {
		t.Fatal("NewBase should error when path resolution fails")
	}
}

func TestBaseContains(t *testing.T) {
	root := t.TempDir()
	b, err := NewBase(root)
	if err != nil {
		t.Fatal(err)
	}

	// Inside the base (missing tail is allowed).
	if !b.Contains(filepath.Join(root, "sub", "file.txt")) {
		t.Error("expected child path to be contained")
	}
	// The base itself.
	if !b.Contains(root) {
		t.Error("expected base itself to be contained")
	}
	// Escaping the base.
	if b.Contains(filepath.Join(root, "..", "outside.txt")) {
		t.Error("escape path must not be contained")
	}
}

func TestBaseContainsResolveError(t *testing.T) {
	root := t.TempDir()
	b, err := NewBase(root) // resolved with the real evalSymlinks
	if err != nil {
		t.Fatal(err)
	}

	origEval := evalSymlinks
	defer func() { evalSymlinks = origEval }()
	// Make target resolution fail (non-NotExist error).
	evalSymlinks = func(string) (string, error) { return "", errors.New("permission denied") }

	if b.Contains(filepath.Join(root, "file.txt")) {
		t.Error("Contains must return false when target resolution fails")
	}
}

func TestCachedBaseAndInvalidate(t *testing.T) {
	root := t.TempDir()
	InvalidateBase(root) // ensure clean slate

	b1, err := CachedBase(root)
	if err != nil {
		t.Fatalf("CachedBase: %v", err)
	}
	b2, err := CachedBase(root)
	if err != nil {
		t.Fatalf("CachedBase (cached): %v", err)
	}
	if b1 != b2 {
		t.Error("CachedBase should return the same cached instance")
	}

	InvalidateBase(root)
	b3, err := CachedBase(root)
	if err != nil {
		t.Fatalf("CachedBase after invalidate: %v", err)
	}
	if b3 == b1 {
		t.Error("CachedBase should re-resolve a fresh instance after InvalidateBase")
	}

	// InvalidateBase on an unknown key must be a safe no-op.
	InvalidateBase(filepath.Join(root, "does-not-exist"))
}

func TestCachedBaseError(t *testing.T) {
	origAbs := absFunc
	defer func() { absFunc = origAbs }()
	absFunc = func(string) (string, error) { return "", errors.New("abs failed") }

	if _, err := CachedBase("uncacheable-" + t.Name()); err == nil {
		t.Fatal("CachedBase should propagate a resolution error")
	}
}

// TestIsWithinBaseTargetAbsError covers the target-side Abs error branch of
// IsWithinBase (the base-side branch is covered elsewhere).
func TestIsWithinBaseTargetAbsError(t *testing.T) {
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
	if IsWithinBase("base", "target") {
		t.Fatal("IsWithinBase must fail when target Abs fails")
	}
}

// TestIsWithinRelError covers the filepath.Rel error branch of isWithin by
// calling it directly with an absolute base and a relative target (Rel can't
// relate the two).
func TestIsWithinRelError(t *testing.T) {
	if isWithin(string(filepath.Separator)+"abs"+string(filepath.Separator)+"base", filepath.Join("relative", "target")) {
		t.Fatal("isWithin must return false when filepath.Rel errors")
	}
}

// TestResolvePathRootNotExist covers the loop-to-root branch of resolvePath:
// when no ancestor (up to the filesystem root) resolves, it returns the error.
func TestResolvePathRootNotExist(t *testing.T) {
	origEval := evalSymlinks
	defer func() { evalSymlinks = origEval }()
	evalSymlinks = func(string) (string, error) { return "", os.ErrNotExist }

	if _, err := resolvePath(filepath.Join(t.TempDir(), "x", "y")); err == nil {
		t.Fatal("expected error when no ancestor resolves")
	}
}

// TestIsWithinBaseResolvedTargetError covers the target-side resolvePath error
// branch of IsWithinBaseResolved (base resolves, target fails).
func TestIsWithinBaseResolvedTargetError(t *testing.T) {
	root := t.TempDir()
	bad := filepath.Join(root, "bad")

	origEval := evalSymlinks
	defer func() { evalSymlinks = origEval }()
	evalSymlinks = func(path string) (string, error) {
		if path == bad || filepath.Base(path) == "bad" {
			return "", errors.New("permission denied")
		}
		return os.Getwd() // any benign success for the base resolution
	}
	if IsWithinBaseResolved(root, bad) {
		t.Fatal("IsWithinBaseResolved must fail when target resolution fails")
	}
}

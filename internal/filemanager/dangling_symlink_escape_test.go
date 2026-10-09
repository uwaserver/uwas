package filemanager

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// buildDanglingLinkLayout mirrors internal/pathsafe's dangling-symlink
// regression fixture:
//
//	base/a  -> base          (symlink to the web root itself)
//	base/dl -> ../x          (dangling relative link: escapes to <tmp>/x)
//	base/ad -> base/d        (symlink to a real in-base directory)
//	base/d/dl -> x           (dangling relative link: stays inside)
//
// It returns tmp and base.
func buildDanglingLinkLayout(t *testing.T) (tmp, base string) {
	t.Helper()
	tmp = t.TempDir()
	base = filepath.Join(tmp, "base")
	if err := os.MkdirAll(filepath.Join(base, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, l := range [][2]string{
		{base, filepath.Join(base, "a")},
		{filepath.Join(base, "d"), filepath.Join(base, "ad")},
		{"../x", filepath.Join(base, "dl")},
		{"x", filepath.Join(base, "d", "dl")},
	} {
		if err := os.Symlink(l[0], l[1]); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}
	}
	return tmp, base
}

// TestSafePathRejectsDanglingLinkViaSymlinkedDir pins that safePath resolves a
// relative dangling-symlink target from the link's REAL directory. Joining
// lexically instead classifies base/a/dl as base/x, which is inside the base,
// and lets a write land at <tmp>/x — outside the web root entirely.
func TestSafePathRejectsDanglingLinkViaSymlinkedDir(t *testing.T) {
	tmp, base := buildDanglingLinkLayout(t)

	escape := filepath.Join(base, "a", "dl")
	if err := WriteFile(base, "a/dl", []byte("pwned")); err == nil {
		t.Errorf("WriteFile(%q) accepted a path the kernel resolves to %s, outside the web root %s",
			escape, filepath.Join(tmp, "x"), base)
	}
	if _, err := os.Stat(filepath.Join(tmp, "x")); err == nil {
		t.Errorf("write escaped the web root and created %s", filepath.Join(tmp, "x"))
	}
}

// TestSafePathAllowsInBaseDanglingLinkViaSymlinkedDir is the control: the same
// dangling-link mechanism, but genuinely inside the base, must stay writable.
func TestSafePathAllowsInBaseDanglingLinkViaSymlinkedDir(t *testing.T) {
	_, base := buildDanglingLinkLayout(t)

	inside := filepath.Join(base, "ad", "dl")
	if err := WriteFile(base, "ad/dl", []byte("ok")); err != nil {
		t.Fatalf("WriteFile(%q) resolves inside %s and must stay writable, got %v", inside, base, err)
	}
	got, err := os.ReadFile(filepath.Join(base, "d", "x"))
	if err != nil || string(got) != "ok" {
		t.Fatalf("control write landed wrong: %q, %v", string(got), err)
	}
}

// TestResolvePathDanglingLinkCycleTerminates pins the hop bound: a dangling
// link whose target cleans back onto itself ("m/../loop") must fail closed
// instead of spinning resolvePath forever.
func TestResolvePathDanglingLinkCycleTerminates(t *testing.T) {
	base := t.TempDir()
	if err := os.Symlink("m/../loop", filepath.Join(base, "loop")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	done := make(chan string, 1)
	go func() { done <- safePath(base, "loop") }()
	select {
	case got := <-done:
		if got != "" {
			t.Fatalf("ambiguous dangling link must fail closed, got %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("safePath did not return for a dangling symlink cycle")
	}
}

// TestHasInnerDotDot covers the guard that rejects a ".." appearing after a
// named element, which the kernel resolves against that element's real
// location and a lexical Join cannot model.
func TestHasInnerDotDot(t *testing.T) {
	for _, tc := range []struct {
		link string
		want bool
	}{
		{"../x", false},     // leading "..": unambiguous once parent is known
		{"../../x", false},  // still leading-only
		{"x", false},        // plain
		{"m/../loop", true}, // ".." after a named element
		{"a/b/../c", true},  // deeper
		{"a/./b", false},    // "." is not a name
		{"a/..", true},      // trailing ".." after a name
	} {
		if got := hasInnerDotDot(tc.link); got != tc.want {
			t.Errorf("hasInnerDotDot(%q) = %v, want %v", tc.link, got, tc.want)
		}
	}
}

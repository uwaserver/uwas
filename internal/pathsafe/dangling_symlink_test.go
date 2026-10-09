package pathsafe

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A dangling relative symlink reached through an in-base directory symlink
// must be resolved from its real directory: base/a -> base, base/dl -> ../x
// means base/a/dl really points at <parent>/x, outside base.
func TestIsWithinBaseResolvedDanglingLinkViaSymlinkedDir(t *testing.T) {
	tmp := t.TempDir()
	base := filepath.Join(tmp, "base")
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
			t.Fatal(err)
		}
	}
	b, err := NewBase(base)
	if err != nil {
		t.Fatal(err)
	}
	escape := filepath.Join(base, "a", "dl")
	if IsWithinBaseResolved(base, escape) || b.Contains(escape) {
		t.Fatalf("%s resolves to %s and must not be inside base", escape, filepath.Join(tmp, "x"))
	}
	inside := filepath.Join(base, "ad", "dl")
	if !IsWithinBaseResolved(base, inside) || !b.Contains(inside) {
		t.Fatalf("%s resolves to %s and must be inside base", inside, filepath.Join(base, "d", "x"))
	}
}

// A dangling link like "m/../loop" (m missing) must not make resolvePath
// loop forever: filepath.Join cleans it back to the link itself.
func TestIsWithinBaseResolvedDanglingLinkCycleTerminates(t *testing.T) {
	base := t.TempDir()
	if err := os.Symlink("m/../loop", filepath.Join(base, "loop")); err != nil {
		t.Fatal(err)
	}
	done := make(chan bool, 1)
	go func() { done <- IsWithinBaseResolved(base, filepath.Join(base, "loop")) }()
	select {
	case got := <-done:
		if got {
			t.Fatal("ambiguous dangling link must fail closed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("IsWithinBaseResolved did not return for a dangling symlink cycle")
	}
}

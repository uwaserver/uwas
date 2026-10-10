package filemanager

import (
	"os"
	"path/filepath"
	"testing"
)

// F1601: unlinking a symlink that points outside the web root must work and
// must never touch the target; the parent directory still has to be inside.
func TestDeleteSymlinkPointingOutside(t *testing.T) {
	base := t.TempDir()
	outside := t.TempDir()
	victim := filepath.Join(outside, "victim.txt")
	outDir := filepath.Join(outside, "dir")
	for _, d := range []string{outDir, filepath.Join(base, "sub")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(victim, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "f"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := func(target, name string) {
		if err := os.Symlink(target, filepath.Join(base, name)); err != nil {
			t.Skip("symlink unavailable")
		}
	}
	link(victim, "outfile")
	link(outDir, "outdir")
	link(filepath.Join(outside, "missing"), "dangling")
	link(victim, "sub/nested")
	link(outside, "escape") // parent-escape vector
	gone := func(name string) bool {
		_, err := os.Lstat(filepath.Join(base, name))
		return err != nil
	}
	for _, name := range []string{"outfile", "outdir", "dangling", "sub/nested"} {
		if err := Delete(base, name); err != nil {
			t.Fatalf("Delete(%s): %v", name, err)
		}
		if !gone(name) {
			t.Fatalf("%s still present", name)
		}
	}
	for _, p := range []string{victim, filepath.Join(outDir, "f")} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("outside target touched: %v", err)
		}
	}
	// repeated call: entry gone, RemoveAll semantics (nil), no panic
	if err := Delete(base, "outfile"); err != nil {
		t.Fatalf("repeat Delete: %v", err)
	}
	// a path THROUGH the escaping link is still rejected and removes nothing
	if err := Delete(base, "escape/victim.txt"); err == nil {
		t.Fatal("path through escaping symlink was accepted")
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("victim removed via escape link: %v", err)
	}
	// traversal and base itself stay rejected
	for _, p := range []string{"../x", ".", "", "sub/../../x"} {
		if err := Delete(base, p); err == nil {
			t.Fatalf("Delete(%q) accepted", p)
		}
	}
	// a symlinked base is not removable via "."
	lb := filepath.Join(t.TempDir(), "lb")
	if err := os.Symlink(base, lb); err != nil {
		t.Skip("symlink unavailable")
	}
	if err := Delete(lb, "."); err == nil {
		t.Fatal("symlinked base removable via .")
	}
	if _, err := os.Lstat(lb); err != nil {
		t.Fatalf("base link removed: %v", err)
	}
	t.Log("FIX VERIFIED")
}

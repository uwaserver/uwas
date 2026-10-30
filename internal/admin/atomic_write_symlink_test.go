package admin

import (
	"os"
	"path/filepath"
	"testing"
)

// F56300: a config file that is a symlink (config management, a shared
// volume) must stay a symlink after a panel save; the rename used to replace
// the link with a regular file and leave the real target stale.
func TestAtomicWriteFileFollowsSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "managed", "uwas.yaml")
	if err := os.MkdirAll(filepath.Dir(real), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(real, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "uwas.yaml")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("symlinks unavailable:", err)
	}

	if err := atomicWriteFile(link, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink replaced by a regular file")
	}
	got, _ := os.ReadFile(real)
	if string(got) != "new" {
		t.Fatalf("symlink target = %q, want new", got)
	}
}

// Control: a plain file and a missing file are written as before.
func TestAtomicWriteFilePlainAndMissing(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.yaml", "b.yaml"} {
		p := filepath.Join(dir, name)
		if name == "a.yaml" {
			os.WriteFile(p, []byte("old"), 0600)
		}
		if err := atomicWriteFile(p, []byte("new"), 0600); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(p); string(got) != "new" {
			t.Fatalf("%s = %q", name, got)
		}
	}
}

// A dangling link is followed too: the file appears at the link's target.
func TestAtomicWriteFileDanglingSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "target.yaml")
	link := filepath.Join(dir, "uwas.yaml")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if err := atomicWriteFile(link, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Lstat(link); fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("dangling symlink replaced")
	}
	if got, _ := os.ReadFile(real); string(got) != "new" {
		t.Fatalf("target = %q", got)
	}
}

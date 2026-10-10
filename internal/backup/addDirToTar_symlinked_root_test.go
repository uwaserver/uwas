package backup

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func tarNames(t *testing.T, root, prefix string) []string {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := addDirToTar(tw, root, prefix); err != nil {
		t.Fatalf("addDirToTar(%s): %v", root, err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	var names []string
	tr := tar.NewReader(&buf)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return names
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
	}
}

// F1600: a symlinked archive root (web root / certs / domains.d on another
// volume) must archive its target tree; links inside the tree stay skipped.
func TestAddDirToTarSymlinkedRoot(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	outside := filepath.Join(base, "outside")
	for _, d := range []string{filepath.Join(real, "sub"), outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(real, "sub", "index.html"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("s"), 0o644); err != nil {
		t.Fatal(err)
	}
	// a link inside the tree must still be skipped
	if err := os.Symlink(outside, filepath.Join(real, "escape")); err != nil {
		t.Skip("symlink unavailable")
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("symlink unavailable")
	}
	chain := filepath.Join(base, "chain") // link -> link -> real
	if err := os.Symlink(link, chain); err != nil {
		t.Skip("symlink unavailable")
	}

	want := []string{"sites/x/", "sites/x/sub/", "sites/x/sub/index.html"}
	for name, root := range map[string]string{"real": real, "link": link, "chain": chain} {
		got := tarNames(t, root, "sites/x")
		if len(got) != len(want) {
			t.Fatalf("%s: got %v want %v", name, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s: got %v want %v", name, got, want)
			}
		}
	}

	// dangling root: no panic, no entries, caller's Stat guard handles it
	dang := filepath.Join(base, "dangling")
	if err := os.Symlink(filepath.Join(base, "nope"), dang); err != nil {
		t.Skip("symlink unavailable")
	}
	if got := tarNames(t, dang, "sites/x"); len(got) != 0 {
		t.Fatalf("dangling root archived %v", got)
	}
	t.Log("FIX VERIFIED")
}

package backup

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestVanishedFileDoesNotAbortRootWalk: a file deleted between the directory
// listing and its open (cache, session and log files of a live site) made
// addRootFileToTar return ENOENT, which aborted the whole walk: every later
// file of the root was dropped and, with F59001, retention was skipped.
func TestVanishedFileDoesNotAbortRootWalk(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	if err := addRootFileToTar(tw, root, "gone.txt", "sites/x/gone.txt", nil); err != nil {
		t.Fatalf("EXPECTED: vanished file skipped; ACTUAL: %v", err)
	}

	// Control: a present file is still archived.
	if err := os.WriteFile(filepath.Join(dir, "here.txt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := addRootFileToTar(tw, root, "here.txt", "sites/x/here.txt", nil); err != nil {
		t.Fatalf("control: %v", err)
	}
	_ = tw.Close()
	tr := tar.NewReader(&buf)
	h, err := tr.Next()
	if err != nil || h.Name != "sites/x/here.txt" {
		t.Fatalf("control entry = %v, %v", h, err)
	}
}

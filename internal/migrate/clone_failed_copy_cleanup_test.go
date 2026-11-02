package migrate

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// F61301: a file copy that failed half way left the partial tree in the
// target root, including the source's wp-config.php with its production
// database credentials. The retry was then refused because the target was no
// longer empty, so the only way out was to delete it by hand.
func TestFailedFileCopyLeavesNoPartialTarget(t *testing.T) {
	origFiles, origChown := runCloneFiles, runCloneChown
	defer func() { runCloneFiles, runCloneChown = origFiles, origChown }()
	runCloneChown = func(string) {}
	runCloneFiles = func(src, dst string, _ *strings.Builder) error {
		if err := os.WriteFile(filepath.Join(dst, "wp-config.php"), []byte("prod-secret"), 0o644); err != nil {
			return err
		}
		return errors.New("rsync: disk full")
	}

	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "staging")
	res := Clone(CloneRequest{SourceDomain: "a.test", TargetDomain: "b.test", SourceRoot: src, TargetRoot: dst})
	if res.Status != "error" {
		t.Fatalf("status = %q, want error", res.Status)
	}
	if entries, err := os.ReadDir(dst); err == nil && len(entries) > 0 {
		t.Fatalf("partial target left behind: %d entries (wp-config copied: %v)", len(entries), entries[0].Name())
	}
}

// Control: a target that already held an empty directory is left in place
// (only its content is cleaned), and a successful clone keeps its files.
func TestSuccessfulCloneKeepsCopiedFiles(t *testing.T) {
	origFiles, origChown := runCloneFiles, runCloneChown
	defer func() { runCloneFiles, runCloneChown = origFiles, origChown }()
	runCloneChown = func(string) {}
	runCloneFiles = func(src, dst string, _ *strings.Builder) error {
		return os.WriteFile(filepath.Join(dst, "index.php"), []byte("x"), 0o644)
	}
	src := t.TempDir()
	dst := t.TempDir() // exists and is empty
	res := Clone(CloneRequest{SourceDomain: "a.test", TargetDomain: "b.test", SourceRoot: src, TargetRoot: dst})
	if res.Status != "done" {
		t.Fatalf("status = %q: %s", res.Status, res.Error)
	}
	if _, err := os.Stat(filepath.Join(dst, "index.php")); err != nil {
		t.Fatal(err)
	}
}

//go:build unix

package wordpress

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeTreeModesReportsUnreachableRoot(t *testing.T) {
	if err := normalizeTreeModes(filepath.Join(t.TempDir(), "missing"), 0o755, 0o644, nil); err == nil {
		t.Fatal("a missing web root must be reported, not treated as done")
	}
	var log strings.Builder
	out, _ := FixPermissions(filepath.Join(t.TempDir(), "missing"))
	log.WriteString(out)
	if !strings.Contains(log.String(), "chmod dirs 755 and files 644 failed") {
		t.Errorf("FixPermissions must say the mode step failed: %s", log.String())
	}
}

func TestNormalizeTreeModesLeavesSymlinksAndTheirTargetsAlone(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "docroot")
	outsideDir := filepath.Join(base, "outside")
	os.MkdirAll(root, 0o700)
	os.MkdirAll(outsideDir, 0o700)
	os.WriteFile(filepath.Join(outsideDir, "f"), []byte("x"), 0o600)
	os.Symlink(outsideDir, filepath.Join(root, "linkdir"))
	os.Symlink(filepath.Join(outsideDir, "f"), filepath.Join(root, "linkfile"))

	if err := normalizeTreeModes(root, 0o755, 0o644, nil); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(outsideDir); fi.Mode().Perm() != 0o700 {
		t.Errorf("directory behind a symlink changed to %o", fi.Mode().Perm())
	}
	if fi, _ := os.Stat(filepath.Join(outsideDir, "f")); fi.Mode().Perm() != 0o600 {
		t.Errorf("file behind a symlink changed to %o", fi.Mode().Perm())
	}
	if fi, _ := os.Stat(root); fi.Mode().Perm() != 0o755 {
		t.Errorf("docroot mode = %o, want 755", fi.Mode().Perm())
	}
}

func TestNormalizeTreeModesLeavesSymlinkedRootAlone(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	os.MkdirAll(real, 0o700)
	os.WriteFile(filepath.Join(real, "f"), []byte("x"), 0o600)
	link := filepath.Join(base, "link")
	os.Symlink(real, link)
	if err := normalizeTreeModes(link, 0o755, 0o644, nil); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(filepath.Join(real, "f")); fi.Mode().Perm() != 0o600 {
		t.Errorf("a web root that is itself a symlink was followed: %o", fi.Mode().Perm())
	}
}

func TestNormalizeTreeModesKeepsSetgidOnDirectoriesAndSkipsNamedFiles(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	os.MkdirAll(sub, 0o700)
	if err := os.Chmod(sub, 0o700|os.ModeSetgid); err != nil {
		t.Skipf("cannot set setgid here: %v", err)
	}
	if fi, _ := os.Stat(sub); fi.Mode()&os.ModeSetgid == 0 {
		t.Skip("filesystem ignores setgid")
	}
	os.WriteFile(filepath.Join(sub, "wp-config.php"), []byte("c"), 0o600)
	os.WriteFile(filepath.Join(sub, "a.php"), []byte("a"), 0o600)
	if err := normalizeTreeModes(root, 0o755, 0o644, func(n string) bool { return n == "wp-config.php" }); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(sub)
	if fi.Mode().Perm() != 0o755 || fi.Mode()&os.ModeSetgid == 0 {
		t.Errorf("dir mode = %v, want 755 with setgid kept", fi.Mode())
	}
	if fi, _ := os.Stat(filepath.Join(sub, "wp-config.php")); fi.Mode().Perm() != 0o600 {
		t.Errorf("skipped file changed to %o", fi.Mode().Perm())
	}
	if fi, _ := os.Stat(filepath.Join(sub, "a.php")); fi.Mode().Perm() != 0o644 {
		t.Errorf("file mode = %o, want 644", fi.Mode().Perm())
	}
}

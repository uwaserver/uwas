//go:build unix

package wordpress

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// F46001: FixPermissions and the installer ran `find <root> -type f -exec chmod
// 644 {} ;` (and the directory twin) as root over a tree the domain user owns.
// find decides an entry is a regular file, then spawns chmod on its path; chmod
// follows symlinks, so a file the tenant swapped for a symlink in that window
// had the host file it pointed at chmodded (a 0640 /etc/shadow becomes 0644).
// The swap is made deterministic by a chmod wrapper in PATH that performs it
// right before the real chmod runs, which is the moment the tenant wins.
func permissionSwapSetup(t *testing.T) (root, outsideFile, outsideDir string) {
	t.Helper()
	realChmod, err := exec.LookPath("chmod")
	if err != nil {
		t.Skip("chmod not available")
	}
	if _, err := exec.LookPath("find"); err != nil {
		t.Skip("find not available")
	}
	base := t.TempDir()
	root = filepath.Join(base, "docroot")
	outsideFile = filepath.Join(base, "host-file")
	outsideDir = filepath.Join(base, "host-dir")
	for _, d := range []string{root, filepath.Join(root, "vdir"), outsideDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(root, "victim.txt"), []byte("site file"), 0o644)
	os.WriteFile(outsideFile, []byte("host secret"), 0o600)
	os.Chmod(outsideDir, 0o700)

	bin := t.TempDir()
	script := "#!/bin/sh\nfor a in \"$@\"; do last=\"$a\"; done\ncase \"$last\" in\n" +
		"  */victim.txt) rm -f \"$last\"; ln -s '" + outsideFile + "' \"$last\";;\n" +
		"  */vdir) rm -rf \"$last\"; ln -s '" + outsideDir + "' \"$last\";;\nesac\nexec " + realChmod + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "chmod"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return root, outsideFile, outsideDir
}

func checkPermissionSwap(t *testing.T, outsideFile, outsideDir string) {
	t.Helper()
	fi, _ := os.Stat(outsideFile)
	t.Logf("EXPECTED: host file stays 0600\nACTUAL: %o", fi.Mode().Perm())
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("PROBLEM CONFIRMED: a file swapped for a symlink had its target chmodded to %o", fi.Mode().Perm())
	}
	di, _ := os.Stat(outsideDir)
	t.Logf("EXPECTED: host dir stays 0700\nACTUAL: %o", di.Mode().Perm())
	if di.Mode().Perm() != 0o700 {
		t.Errorf("PROBLEM CONFIRMED: a directory swapped for a symlink had its target chmodded to %o", di.Mode().Perm())
	}
}

func TestFixPermissionsDoesNotChmodThroughSwappedEntry(t *testing.T) {
	root, outsideFile, outsideDir := permissionSwapSetup(t)
	FixPermissions(root)
	checkPermissionSwap(t, outsideFile, outsideDir)
}

func TestSetWordPressPermissionsDoesNotChmodThroughSwappedEntry(t *testing.T) {
	root, outsideFile, outsideDir := permissionSwapSetup(t)
	var log strings.Builder
	setWordPressPermissions(root, &log)
	checkPermissionSwap(t, outsideFile, outsideDir)
}

// Control: ordinary files and directories still get 644 / 755.
func TestPermissionStepsStillNormalizeModes(t *testing.T) {
	root := filepath.Join(t.TempDir(), "docroot")
	os.MkdirAll(filepath.Join(root, "sub", "deep"), 0o700)
	os.WriteFile(filepath.Join(root, "sub", "a.txt"), []byte("a"), 0o600)
	os.WriteFile(filepath.Join(root, "sub", "deep", "b.php"), []byte("b"), 0o666)
	os.WriteFile(filepath.Join(root, "wp-config.php"), []byte("c"), 0o600)
	var log strings.Builder
	setWordPressPermissions(root, &log)
	want := map[string]os.FileMode{
		"sub": 0o755, "sub/deep": 0o755, "sub/a.txt": 0o644, "sub/deep/b.php": 0o644,
		"wp-config.php": 0o600, // the installer leaves the DB credentials file alone
	}
	for rel, mode := range want {
		fi, err := os.Stat(filepath.Join(root, rel))
		if err != nil {
			t.Errorf("%s: %v", rel, err)
			continue
		}
		if fi.Mode().Perm() != mode {
			t.Errorf("%s mode = %o, want %o", rel, fi.Mode().Perm(), mode)
		}
	}
	if fi, _ := os.Stat(root); fi.Mode().Perm() != 0o755 {
		t.Errorf("docroot mode = %o, want 755", fi.Mode().Perm())
	}
}

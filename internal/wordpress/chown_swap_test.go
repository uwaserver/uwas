//go:build unix

package wordpress

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// F46002: FixPermissions and the installer create wp-content/upgrade, uploads
// and .tmp and hand each to the web account with `chown www-data:www-data
// <dir>`, after a point-in-time pathHasSymlink check. The tenant owns the tree,
// so a directory (or its parent, wp-content) swapped for a symlink between the
// check and chown made root chown the host directory it pointed at: chown
// follows the leaf symlink and every intermediate one. The swap is made
// deterministic through the osMkdirAllFn seam, which runs in exactly that
// window; chown is observed with a wrapper that swaps the account for a group
// this unprivileged user may assign (as the neighbouring symlink tests do).
func chownSwapSetup(t *testing.T) (gid int, wrapperDir string) {
	t.Helper()
	realChown, err := exec.LookPath("chown")
	if err != nil {
		t.Skip("chown not available")
	}
	groups, _ := os.Getgroups()
	gid = -1
	for _, g := range groups {
		if g != os.Getgid() {
			gid = g
			break
		}
	}
	if gid < 0 {
		t.Skip("need a supplementary group to observe chown")
	}
	wrapperDir = t.TempDir()
	script := "#!/bin/sh\nfor a in \"$@\"; do\n  [ \"$a\" = \"www-data:www-data\" ] && a=\":" + fmt.Sprint(gid) + "\"\n  set -- \"$@\" \"$a\"\ndone\nshift $(( $# / 2 ))\nexec " + realChown + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(wrapperDir, "chown"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", wrapperDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return gid, wrapperDir
}

func gidOf(p string) int {
	fi, err := os.Stat(p)
	if err != nil {
		return -1
	}
	return int(fi.Sys().(*syscall.Stat_t).Gid)
}

type chownSwapCase struct {
	name string
	// swap runs right after the directory was created, as the tenant would.
	swap func(root, dir, outside string)
}

func chownSwapCases() []chownSwapCase {
	return []chownSwapCase{
		{"leaf directory becomes a symlink", func(root, dir, outside string) {
			os.Remove(dir)
			os.Symlink(outside, dir)
		}},
		{"parent wp-content becomes a symlink", func(root, dir, outside string) {
			os.Rename(filepath.Join(root, "wp-content"), filepath.Join(root, "wp-content.moved"))
			os.Symlink(filepath.Dir(outside), filepath.Join(root, "wp-content"))
		}},
	}
}

func runChownSwap(t *testing.T, run func(root string)) {
	gid, _ := chownSwapSetup(t)
	for _, c := range chownSwapCases() {
		t.Run(c.name, func(t *testing.T) {
			base := t.TempDir()
			root := filepath.Join(base, "docroot")
			hostParent := filepath.Join(base, "host")
			outside := filepath.Join(hostParent, "uploads")
			victimDir := outside
			os.MkdirAll(filepath.Join(root, "wp-content"), 0o755)
			os.MkdirAll(outside, 0o755)
			before := gidOf(victimDir)
			if before == gid {
				t.Skip("host dir already has the observable group")
			}

			snap := saveHooks()
			defer restoreHooks(snap)
			swapped := false
			dirToSwap := filepath.Join(root, "wp-content", "uploads")
			osMkdirAllFn = func(p string, m os.FileMode) error {
				err := os.MkdirAll(p, m)
				if p == dirToSwap && !swapped {
					swapped = true
					c.swap(root, p, outside)
				}
				return err
			}
			run(root)

			if !swapped {
				t.Fatal("test setup: the swap point was never reached")
			}
			got := gidOf(victimDir)
			t.Logf("EXPECTED: host directory keeps gid %d\nACTUAL: gid %d", before, got)
			if got != before {
				t.Errorf("PROBLEM CONFIRMED: root chowned a host directory (gid %d -> %d) through a swapped symlink", before, got)
			}
		})
	}
}

func TestFixPermissionsDoesNotChownThroughSwappedDir(t *testing.T) {
	runChownSwap(t, func(root string) { FixPermissions(root) })
}

func TestSetWordPressPermissionsDoesNotChownThroughSwappedDir(t *testing.T) {
	runChownSwap(t, func(root string) {
		var log strings.Builder
		setWordPressPermissions(root, &log)
	})
}

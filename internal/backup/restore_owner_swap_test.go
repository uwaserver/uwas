package backup

// F45302: a root UWAS hands every directory a restore created to the tenant who
// owns the tree (F43001). The tenant owns the parent directory, so between
// MkdirAll and the chown it can swap a just-created component for a symlink to
// a host directory; a path-based chown then resolves the rest of the path
// through that link and gives a host directory to the tenant (an archive entry
// named like the victim does the rest: /etc, /root/.ssh, ...). The hand-over
// must stay inside the restore tree. The swap is gated deterministically
// through the osMkdirAllFn seam, as restore_component_swap_test.go does.

import (
	"archive/tar"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

func TestRestoreOwnerHandOverNeverLeavesTheTree(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("needs an unprivileged test user: a root-owned tree is never handed over")
	}
	webRoot, cfgDir, hostDir := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(webRoot, "site.test"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The directory the attacker aims the chown at: it already exists on the
	// "host" and has the name of the directory the archive makes the restore create.
	victim := filepath.Join(hostDir, "uploads")
	if err := os.MkdirAll(victim, 0o700); err != nil {
		t.Fatal(err)
	}

	oldEuid, oldL, oldMk := geteuidFn, lchownFn, osMkdirAllFn
	t.Cleanup(func() { geteuidFn, lchownFn, osMkdirAllFn = oldEuid, oldL, oldMk })
	geteuidFn = func() int { return 0 }

	var escaped []string
	webReal, _ := filepath.EvalSymlinks(webRoot)
	lchownFn = func(root *os.Root, rel string, uid, gid int) error {
		// Where would this chown really land? Everything above the final
		// component is resolved by the kernel, symlinks included.
		path := filepath.Join(root.Name(), rel)
		parent, err := filepath.EvalSymlinks(filepath.Dir(path))
		if err == nil && !strings.HasPrefix(parent+string(filepath.Separator), webReal+string(filepath.Separator)) {
			// The root-relative call refuses this; record it only if it succeeds.
			if cerr := root.Lchown(rel, uid, gid); cerr == nil {
				escaped = append(escaped, filepath.Join(parent, filepath.Base(path)))
			}
			return nil
		}
		return root.Lchown(rel, uid, gid)
	}
	swapped := false
	osMkdirAllFn = func(path string, perm os.FileMode) error {
		err := oldMk(path, perm)
		// webRoot/site.test/work/uploads now exists. The tenant replaces "work"
		// with a symlink to the host directory.
		if err == nil && !swapped && strings.HasSuffix(filepath.ToSlash(path), "/work/uploads") {
			swapped = true
			work := filepath.Dir(path)
			if os.Remove(path) == nil && os.Remove(work) == nil {
				_ = os.Symlink(hostDir, work)
			}
		}
		return err
	}

	m := New(config.BackupConfig{}, logger.New("error", "text"))
	mp := newMemoryProvider("mem")
	m.providers["mem"] = mp
	mp.files["site.tar.gz"] = buildArchive(t, []tarEntry{
		{name: "sites/site.test/work/uploads/f", data: []byte("x"), typeflag: tar.TypeReg},
	})
	m.SetPaths(cfgDir, "")
	m.SetDomainPaths(webRoot, "", nil)
	_ = m.RestoreBackup("site.tar.gz", "mem") // the escaped entry is skipped or fails; the chown is the question

	if !swapped {
		t.Fatal("test setup: the component swap never fired")
	}
	t.Logf("EXPECTED: no chown outside %s\nACTUAL: %v", webReal, escaped)
	if len(escaped) > 0 {
		t.Fatalf("PROBLEM CONFIRMED: root chowned %v, a path the tenant redirected out of its tree", escaped)
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("control: victim directory vanished: %v", err)
	}
}

// Control: with no swap the created directories are still handed over.
func TestRestoreOwnerHandOverStillHappensWithoutSwap(t *testing.T) {
	_, _, _, dirs := restoreSiteArchive(t, true)
	if len(*dirs) != 3 {
		t.Errorf("control: %d directory hand-overs, want 3: %+v", len(*dirs), *dirs)
	}
}

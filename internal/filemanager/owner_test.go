//go:build unix

package filemanager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type ownCall struct {
	name     string
	uid, gid int
}

// simulateRoot makes the package believe UWAS runs as root and records the
// ownership changes it would make. owner is the uid/gid reported for the web
// root; webGID is the web account's group.
func simulateRoot(t *testing.T, ownerUID, ownerGID, webGID int) (files *[]ownCall, dirs *[]ownCall, modes *[]os.FileMode) {
	t.Helper()
	files, dirs, modes = &[]ownCall{}, &[]ownCall{}, &[]os.FileMode{}
	oldEuid, oldOwner, oldWeb, oldChown, oldChmod, oldLchown := geteuidFn, fileOwnerFn, webGIDFn, chownFn, chmodFn, lchownFn
	geteuidFn = func() int { return 0 }
	fileOwnerFn = func(os.FileInfo) (int, int, bool) { return ownerUID, ownerGID, true }
	webGIDFn = func() (int, bool) { return webGID, true }
	chownFn = func(f *os.File, uid, gid int) error {
		*files = append(*files, ownCall{filepath.Base(f.Name()), uid, gid})
		return nil
	}
	chmodFn = func(f *os.File, m os.FileMode) error { *modes = append(*modes, m); return f.Chmod(m) }
	lchownFn = func(_ *os.Root, name string, uid, gid int) error {
		*dirs = append(*dirs, ownCall{name, uid, gid})
		return nil
	}
	t.Cleanup(func() {
		geteuidFn, fileOwnerFn, webGIDFn, chownFn, chmodFn, lchownFn = oldEuid, oldOwner, oldWeb, oldChown, oldChmod, oldLchown
	})
	return files, dirs, modes
}

// F44001: with a root UWAS, files and directories created through the panel
// were root-owned (files 0600): the PHP worker (web account since F42700) could
// not read a PHP file the operator uploaded, and a tenant's SFTP user could not
// write into a directory the panel created in their tree.
func TestPanelCreatedContentTakesTheTenantOwner(t *testing.T) {
	files, dirs, modes := simulateRoot(t, 1001, 33, 33)
	base := t.TempDir()

	if err := WriteFile(base, "wp-content/themes/x/index.php", []byte("<?php")); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveUpload(base, "uploads/2026/app.php", strings.NewReader("<?php")); err != nil {
		t.Fatal(err)
	}
	if err := CreateDir(base, "cache/pages"); err != nil {
		t.Fatal(err)
	}

	wantFiles := map[string]bool{"index.php": false, "app.php": false}
	for _, c := range *files {
		if c.uid != 1001 || c.gid != 33 {
			t.Errorf("file %s handed to %d:%d, want 1001:33", c.name, c.uid, c.gid)
		}
		wantFiles[c.name] = true
	}
	for n, got := range wantFiles {
		if !got {
			t.Errorf("new file %s was not handed to the tenant owner (calls: %+v)", n, *files)
		}
	}
	wantDirs := []string{"wp-content", "wp-content/themes", "wp-content/themes/x", "uploads", "uploads/2026", "cache", "cache/pages"}
	got := map[string]bool{}
	for _, c := range *dirs {
		if c.uid != 1001 || c.gid != 33 {
			t.Errorf("dir %s handed to %d:%d, want 1001:33", c.name, c.uid, c.gid)
		}
		got[c.name] = true
	}
	for _, d := range wantDirs {
		if !got[d] {
			t.Errorf("created directory %s was not handed to the tenant owner (calls: %+v)", d, *dirs)
		}
	}
	for _, m := range *modes {
		if m != 0o640 {
			t.Errorf("new file mode %#o, want 0640 (group-readable for the web account)", m)
		}
	}
	info, err := os.Stat(filepath.Join(base, "uploads/2026/app.php"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Errorf("app.php is %#o, want 0640", info.Mode().Perm())
	}
}

// A root-owned web root (what a panel-created domain has) keeps root as owner
// and gets the web account's group, so PHP can read what the panel wrote.
func TestRootOwnedWebRootGetsTheWebGroup(t *testing.T) {
	files, _, _ := simulateRoot(t, 0, 0, 33)
	base := t.TempDir()
	if err := WriteFile(base, "index.php", []byte("<?php")); err != nil {
		t.Fatal(err)
	}
	if len(*files) != 1 || (*files)[0].uid != 0 || (*files)[0].gid != 33 {
		t.Fatalf("chown calls = %+v, want one call to 0:33", *files)
	}
}

// Controls: an existing file keeps its owner and mode; a non-root UWAS changes
// nothing (its files are already its own and stay 0600).
func TestExistingFilesAndNonRootUWASAreUntouched(t *testing.T) {
	files, dirs, modes := simulateRoot(t, 1001, 33, 33)
	base := t.TempDir()
	path := filepath.Join(base, "keep.php")
	if err := os.WriteFile(path, []byte("old"), 0o664); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(base, "keep.php", []byte("new")); err != nil {
		t.Fatal(err)
	}
	if len(*files) != 0 || len(*dirs) != 0 || len(*modes) != 0 {
		t.Errorf("overwriting an existing file changed ownership/mode: %+v %+v %+v", *files, *dirs, *modes)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o664 {
		t.Errorf("existing file mode = %#o, want 0664", info.Mode().Perm())
	}

	geteuidFn = func() int { return 1000 }
	if err := WriteFile(base, "fresh/new.php", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if len(*files) != 0 || len(*dirs) != 0 {
		t.Errorf("a non-root UWAS handed ownership away: %+v %+v", *files, *dirs)
	}
	if info, _ := os.Stat(filepath.Join(base, "fresh/new.php")); info.Mode().Perm() != 0o600 {
		t.Errorf("non-root new file mode = %#o, want 0600", info.Mode().Perm())
	}
}

// A root-owned web root and no resolvable web account: nothing to hand over,
// the write still succeeds.
func TestNoWebAccountLeavesContentAlone(t *testing.T) {
	files, _, _ := simulateRoot(t, 0, 0, 0)
	webGIDFn = func() (int, bool) { return 0, false }
	base := t.TempDir()
	if err := WriteFile(base, "index.php", []byte("<?php")); err != nil {
		t.Fatal(err)
	}
	if len(*files) != 0 {
		t.Errorf("chown calls = %+v, want none without a web account", *files)
	}
}

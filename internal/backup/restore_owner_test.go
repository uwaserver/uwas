package backup

// F43001: a root UWAS restoring a backup wrote every site file through a temp
// file and rename, so each restored file (and every directory it had to
// create) became root's. Since PHP workers dropped root (F42700) the site's PHP
// account cannot write them, and the SFTP user cannot replace them. The
// restore must keep the owner of the file it replaces, and give new files and
// directories the owner of the tree they land in. UWAS's own config and certs
// stay root's.
//
// The tests run unprivileged: the "am I root" check and the chown calls go
// through seams that record what a root UWAS would do.

import (
	"archive/tar"
	"os"
	"path/filepath"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

type chownCall struct {
	path     string
	uid, gid int
}

func recordOwnerCalls(t *testing.T, asRoot bool) (files, dirs *[]chownCall) {
	t.Helper()
	files, dirs = new([]chownCall), new([]chownCall)
	oldEuid, oldL, oldF := geteuidFn, lchownFn, fchownFn
	t.Cleanup(func() { geteuidFn, lchownFn, fchownFn = oldEuid, oldL, oldF })
	if asRoot {
		geteuidFn = func() int { return 0 }
	}
	lchownFn = func(root *os.Root, rel string, uid, gid int) error {
		*dirs = append(*dirs, chownCall{filepath.Join(root.Name(), rel), uid, gid})
		return root.Lchown(rel, uid, gid)
	}
	fchownFn = func(f *os.File, uid, gid int) error {
		*files = append(*files, chownCall{f.Name(), uid, gid})
		return f.Chown(uid, gid)
	}
	return files, dirs
}

func restoreSiteArchive(t *testing.T, asRoot bool) (webRoot, cfgDir string, files, dirs *[]chownCall) {
	t.Helper()
	if os.Getuid() == 0 {
		t.Skip("needs an unprivileged test user: a root-owned tree is never handed over")
	}
	files, dirs = recordOwnerCalls(t, asRoot)
	webRoot, cfgDir = t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(webRoot, "site.test"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(webRoot, "site.test", "index.php"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := New(config.BackupConfig{}, logger.New("error", "text"))
	mp := newMemoryProvider("mem")
	m.providers["mem"] = mp
	mp.files["site.tar.gz"] = buildArchive(t, []tarEntry{
		{name: "sites/site.test/index.php", data: []byte("new"), typeflag: tar.TypeReg},
		{name: "sites/site.test/wp-content/uploads/a.jpg", data: []byte("img"), typeflag: tar.TypeReg},
		{name: "sites/site.test/empty/", typeflag: tar.TypeDir},
		{name: "config/uwas.yaml", data: []byte("global: {}\n"), typeflag: tar.TypeReg},
	})
	m.SetPaths(cfgDir, "")
	m.SetDomainPaths(webRoot, "", nil)
	if err := m.RestoreBackup("site.tar.gz", "mem"); err != nil {
		t.Fatalf("RestoreBackup: %v", err)
	}
	return webRoot, cfgDir, files, dirs
}

func TestRestoreKeepsTenantOwnershipUnderRoot(t *testing.T) {
	webRoot, cfgDir, files, dirs := restoreSiteArchive(t, true)
	uid, gid := os.Getuid(), os.Getgid()

	for _, c := range *files {
		if c.uid != uid || c.gid != gid {
			t.Errorf("file %s handed to %d:%d, want %d:%d", c.path, c.uid, c.gid, uid, gid)
		}
	}
	if len(*files) != 2 {
		t.Errorf("EXPECTED: both site files handed to the tenant owner\nACTUAL: %d fchown calls %+v\nPROBLEM CONFIRMED", len(*files), *files)
	}

	var gotDirs []string
	for _, c := range *dirs {
		if c.uid != uid || c.gid != gid {
			t.Errorf("dir %s handed to %d:%d, want %d:%d", c.path, c.uid, c.gid, uid, gid)
		}
		gotDirs = append(gotDirs, c.path)
	}
	want := []string{
		filepath.Join(webRoot, "site.test", "wp-content"),
		filepath.Join(webRoot, "site.test", "wp-content", "uploads"),
		filepath.Join(webRoot, "site.test", "empty"),
	}
	if len(gotDirs) != len(want) {
		t.Fatalf("EXPECTED: only the created directories are handed over, top-down: %v\nACTUAL: %v\nPROBLEM CONFIRMED", want, gotDirs)
	}
	for i := range want {
		if gotDirs[i] != want[i] {
			t.Errorf("dir[%d] = %s, want %s", i, gotDirs[i], want[i])
		}
	}

	// UWAS's own config is not tenant space: nothing was handed over for it.
	for _, c := range append(append([]chownCall{}, *files...), *dirs...) {
		if filepath.Dir(c.path) == cfgDir || filepath.Base(c.path) == "uwas.yaml" {
			t.Errorf("config path %s must stay root's, got a chown", c.path)
		}
	}
	if b, err := os.ReadFile(filepath.Join(cfgDir, "uwas.yaml")); err != nil || string(b) != "global: {}\n" {
		t.Errorf("config not restored: %q %v", b, err)
	}
	if b, _ := os.ReadFile(filepath.Join(webRoot, "site.test", "index.php")); string(b) != "new" {
		t.Errorf("index.php = %q, want new", b)
	}
}

// Control: an unprivileged UWAS never chowns anything (it could not), so the
// restore behaves exactly as before.
func TestRestoreDoesNotChownWhenNotRoot(t *testing.T) {
	_, _, files, dirs := restoreSiteArchive(t, false)
	if len(*files)+len(*dirs) != 0 {
		t.Errorf("unprivileged restore chowned: files=%+v dirs=%+v", *files, *dirs)
	}
}

package filemanager

import (
	"os"
	"os/user"
	"path/filepath"
	"strconv"
)

// Seams for the ownership hand-over below; tests replace them.
var (
	geteuidFn   = os.Geteuid
	fileOwnerFn = fileOwner
	webGIDFn    = webAccountGID
	chownFn     = func(f *os.File, uid, gid int) error { return f.Chown(uid, gid) }
	chmodFn     = func(f *os.File, m os.FileMode) error { return f.Chmod(m) }
	lchownFn    = func(r *os.Root, name string, uid, gid int) error { return r.Lchown(name, uid, gid) }
)

// newContentOwner reports the owner new files and directories created through
// the panel must have, and whether the hand-over applies at all.
//
// Only a root UWAS creates root-owned content, and since PHP workers run as the
// web account (F42700) a root-owned 0600 file or a root-owned directory inside
// a tenant's tree is neither readable nor writable by the site's PHP or SFTP
// user (F44001) — the same class F43001 closed for restores. The new content
// takes the web root's owner; a root-owned web root (the default for panel
// created domains) keeps root as owner and uses the web account's group so PHP
// can read what the panel wrote.
func newContentOwner(root *os.Root) (uid, gid int, ok bool) {
	if geteuidFn() != 0 {
		return 0, 0, false
	}
	info, err := root.Stat(".")
	if err != nil {
		return 0, 0, false
	}
	uid, gid, known := fileOwnerFn(info)
	if !known {
		return 0, 0, false
	}
	if gid == 0 {
		g, found := webGIDFn()
		if !found {
			return 0, 0, false
		}
		gid = g
	}
	return uid, gid, true
}

// webAccountGID resolves the group of the conventional web account (the rule
// the php-fpm pool uses), falling back to "nobody".
func webAccountGID() (int, bool) {
	name := "nobody"
	for _, n := range []string{"www-data", "nginx", "apache", "http"} {
		if _, err := user.Lookup(n); err == nil {
			name = n
			break
		}
	}
	u, err := user.Lookup(name)
	if err != nil {
		return 0, false
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil || gid == 0 {
		return 0, false
	}
	return gid, true
}

// missingDirs lists, shallowest first, the directories of rel (relative to
// root) that do not exist yet.
func missingDirs(root *os.Root, rel string) []string {
	var missing []string
	for p := filepath.Clean(rel); p != "." && p != string(filepath.Separator); p = filepath.Dir(p) {
		if _, err := root.Lstat(p); err == nil {
			break
		}
		missing = append([]string{p}, missing...)
	}
	return missing
}

// mkdirAllOwned is root.MkdirAll that hands every directory it created to the
// content owner. Lchown never follows a symlink swapped in meanwhile.
func mkdirAllOwned(root *os.Root, rel string, perm os.FileMode) error {
	var created []string
	uid, gid, hand := newContentOwner(root)
	if hand {
		created = missingDirs(root, rel)
	}
	if err := root.MkdirAll(rel, perm); err != nil {
		return err
	}
	for _, d := range created {
		_ = lchownFn(root, d, uid, gid)
	}
	return nil
}

// ownNewFile hands a file the panel just created to the content owner and makes
// it readable by the owning group (the web account reads site files through the
// group). Existing files keep the owner and mode they have.
func ownNewFile(root *os.Root, f *os.File) {
	uid, gid, hand := newContentOwner(root)
	if !hand {
		return
	}
	_ = chownFn(f, uid, gid)
	_ = chmodFn(f, 0o640)
}

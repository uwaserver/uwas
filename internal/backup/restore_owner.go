package backup

import (
	"os"
	"path/filepath"
)

// Seams for the ownership hand-over below; tests replace them.
var (
	geteuidFn = os.Geteuid
	// lchownFn changes the owner of rel inside root. It is root-relative so the
	// kernel resolves rel under the directory descriptor root holds and refuses
	// a symlink that leads out of it; a path-based chown would follow a
	// component the tenant swapped for a link (F45302).
	lchownFn = func(root *os.Root, rel string, uid, gid int) error { return root.Lchown(rel, uid, gid) }
	fchownFn = func(f *os.File, uid, gid int) error { return f.Chown(uid, gid) }
)

// tenantOwner reports the owner that restored site content must keep, and
// whether the hand-over applies at all: only a root UWAS creates files that end
// up owned by root, and only a non-root owner is worth handing anything to.
func tenantOwner(info os.FileInfo) (uid, gid int, ok bool) {
	if geteuidFn() != 0 {
		return 0, 0, false
	}
	uid, gid, known := fileOwner(info)
	if !known || uid == 0 {
		return 0, 0, false
	}
	return uid, gid, true
}

// mkdirAllOwned is mk(dir, perm) that, for a root UWAS, gives every directory it
// created the owner of the deepest directory that already existed. Without it a
// restore leaves root-owned directories inside a tenant's tree, which neither
// the site's PHP account nor its SFTP user can write to (F43001).
func mkdirAllOwned(mk func(string, os.FileMode) error, dir string, perm os.FileMode) error {
	var created []string
	var uid, gid int
	var inherit bool
	var existing string
	if geteuidFn() == 0 {
		for p := dir; ; {
			info, err := os.Lstat(p)
			if err == nil {
				if info.IsDir() {
					uid, gid, inherit = tenantOwner(info)
					existing = p
				}
				break
			}
			created = append(created, p)
			parent := filepath.Dir(p)
			if parent == p {
				break
			}
			p = parent
		}
	}
	// Anchor the hand-over to the deepest directory that already existed,
	// before anything is created. The tenant owns that tree and can replace a
	// directory this call just made with a symlink to a host directory; the
	// descriptor opened here keeps every later chown inside it (F45302).
	var anchor *os.Root
	if inherit {
		r, err := os.OpenRoot(existing)
		if err != nil {
			inherit = false
		} else {
			anchor = r
			defer anchor.Close()
		}
	}
	if err := mk(dir, perm); err != nil {
		return err
	}
	if inherit {
		// Top-down. A component swapped for a link that leaves the tree is
		// refused by the root-relative call and stays root's.
		for i := len(created) - 1; i >= 0; i-- {
			rel, err := filepath.Rel(existing, created[i])
			if err != nil {
				continue
			}
			_ = lchownFn(anchor, rel, uid, gid)
		}
	}
	return nil
}

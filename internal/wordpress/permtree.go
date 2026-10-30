package wordpress

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// normalizeTreeModes sets dirMode on every directory and fileMode on every
// regular file under webRoot, the way `find -type d/-type f -exec chmod` did,
// but safe against a tenant who owns the tree while UWAS (often root) runs it
// (F46001). find classifies an entry and then spawns chmod on its path; chmod
// follows symlinks, so a file swapped for a symlink in between had the host
// file it pointed at chmodded. Here every change goes through an os.Root, which
// refuses a path that leaves the tree, so a swapped entry fails instead of
// reaching outside. Symlinks are never changed or entered (find's default), a
// symlink as webRoot itself is left alone, and a directory keeps its setuid and
// setgid bits like numeric chmod does. skipFile, when set, receives a file's
// base name and returns true to leave that file as it is.
func normalizeTreeModes(webRoot string, dirMode, fileMode fs.FileMode, skipFile func(name string) bool) error {
	if li, err := os.Lstat(webRoot); err != nil {
		return err
	} else if li.Mode()&fs.ModeSymlink != 0 || !li.IsDir() {
		return nil
	}
	root, err := os.OpenRoot(webRoot)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()

	var failed int
	var first error
	note := func(rel string, err error) {
		failed++
		if first == nil {
			first = fmt.Errorf("%s: %w", rel, err)
		}
	}
	_ = filepath.WalkDir(webRoot, func(path string, d fs.DirEntry, err error) error {
		rel, rerr := filepath.Rel(webRoot, path)
		if rerr != nil {
			rel = path
		}
		if err != nil {
			note(rel, err)
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		case d.IsDir():
			mode := dirMode
			if info, err := root.Lstat(rel); err == nil {
				mode |= info.Mode() & (fs.ModeSetuid | fs.ModeSetgid)
			}
			if err := root.Chmod(rel, mode); err != nil {
				note(rel, err)
			}
		case d.Type().IsRegular():
			if skipFile != nil && skipFile(d.Name()) {
				return nil
			}
			if err := root.Chmod(rel, fileMode); err != nil {
				note(rel, err)
			}
		}
		return nil
	})
	if failed > 0 {
		return fmt.Errorf("%d path(s) could not be changed, first: %w", failed, first)
	}
	return nil
}

// chownDirToWebAccount builds the command that hands rel, a directory under
// webRoot, to the web account, and a func to call once it has run (F46002).
// FixPermissions and the installer used `chown www-data:www-data <path>` after a
// point-in-time symlink check, but the tenant owns the tree: a directory, or a
// parent such as wp-content, swapped for a symlink in between made root chown
// the host directory it pointed at. On Linux the directory is opened through an
// os.Root (a path leaving the tree is refused) and chown is given the open
// descriptor as /proc/self/fd/3, so what is chowned is what was opened. A
// directory that does not exist yet, or a platform without /proc, falls back to
// `chown -h` on the path.
func chownDirToWebAccount(webRoot, rel string) (*exec.Cmd, func(), error) {
	path := filepath.Join(webRoot, rel)
	nothing := func() {}
	if runtime.GOOS != "linux" {
		return execCommandFn("chown", "-h", "www-data:www-data", path), nothing, nil
	}
	f, err := openDirNoFollow(webRoot, rel)
	if err != nil {
		if isNotExist(err) {
			return execCommandFn("chown", "-h", "www-data:www-data", path), nothing, nil
		}
		return nil, nothing, err
	}
	cmd := execCommandFn("chown", "www-data:www-data", "/proc/self/fd/3")
	cmd.ExtraFiles = []*os.File{f}
	return cmd, func() { _ = f.Close() }, nil
}

//go:build unix

package wordpress

import (
	"errors"
	"os"
	"syscall"
)

// openDirNoFollow opens rel, a directory under webRoot, through an os.Root: a
// path that leaves the tree (a tenant's symlink in rel or any parent) is
// refused, and O_NOFOLLOW|O_DIRECTORY refuse a leaf symlink or a file. The
// returned file is the directory itself, whatever the path becomes afterwards.
func openDirNoFollow(webRoot, rel string) (*os.File, error) {
	root, err := os.OpenRoot(webRoot)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	f, err := root.OpenFile(rel, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	return f, nil
}

func isNotExist(err error) bool { return errors.Is(err, os.ErrNotExist) }

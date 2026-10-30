package migrate

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// maxWPConfigSize bounds readWPConfigNoFollow; a wp-config.php is a few KiB.
const maxWPConfigSize = 16 << 20

// readWPConfigNoFollow reads a wp-config.php that lives in a tenant-writable
// tree. UWAS runs these migration steps as root and rsync -a copies a planted
// symlink as a symlink, so a plain os.ReadFile would follow it to another
// tenant's config or any host file (F45300). A final-component symlink is
// refused, the open never blocks on a FIFO, only regular files are read and the
// size is bounded.
func readWPConfigNoFollow(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|noFollowFlag|nonBlockFlag, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: not a regular file", filepath.Base(path))
	}
	if fi.Size() > maxWPConfigSize {
		return nil, fmt.Errorf("%s: file too large", filepath.Base(path))
	}
	data, err := io.ReadAll(io.LimitReader(f, maxWPConfigSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxWPConfigSize {
		return nil, fmt.Errorf("%s: file too large", filepath.Base(path))
	}
	return data, nil
}

// writeWPConfigNoFollow replaces the contents of an existing wp-config.php with
// perm, refusing a final-component symlink. The mode is applied through the open
// descriptor, so it can never land on a link target the way os.Chmod would.
func writeWPConfigNoFollow(path string, data []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|noFollowFlag|nonBlockFlag, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s: not a regular file", filepath.Base(path))
	}
	if err := f.Chmod(perm); err != nil {
		return err
	}
	if err := f.Truncate(0); err != nil {
		return err
	}
	_, err = f.WriteAt(data, 0)
	return err
}

// isSymlink reports whether path itself is a symbolic link.
func isSymlink(path string) bool {
	fi, err := os.Lstat(path)
	return err == nil && fi.Mode()&os.ModeSymlink != 0
}

package backup

import (
	"archive/tar"
	"errors"
	"io/fs"
	"os"
	"strings"
	"syscall"

	"github.com/uwaserver/uwas/internal/logger"
)

func isErrno(err error, target syscall.Errno) bool {
	var e syscall.Errno
	return errors.As(err, &e) && e == target
}

// addRootFileToTar archives rel, a path inside root, for a web root the tenant
// can write to while UWAS (often root) runs the backup. The walk decided the
// entry is a regular file from a directory listing, and by the time it is
// opened the tenant may have swapped it (F46000): a symlink made root copy a
// host file into the archive, a FIFO hung the open for good, and a directory
// replaced by a symlink redirected every file below it. Opening through the
// os.Root refuses any path that leaves the tree, O_NOFOLLOW refuses a leaf
// symlink, O_NONBLOCK returns at once for a FIFO, and the opened handle must
// still be a regular file. A swapped entry is skipped like a symlink is, not
// treated as a failed backup.
func addRootFileToTar(tw *tar.Writer, root *os.Root, rel, archiveName string, log *logger.Logger) error {
	f, err := root.OpenFile(rel, os.O_RDONLY|noFollowFlag|nonBlockFlag, 0)
	if err != nil {
		if isSymlinkLoop(err) || strings.Contains(err.Error(), "path escapes") {
			if log != nil {
				log.Warn("backup: skipped an entry replaced by a symlink while it was being archived", "path", rel)
			}
			return nil
		}
		if errors.Is(err, fs.ErrNotExist) {
			// Deleted after the walk listed it (cache, session, log files of a
			// live site): not part of the backup, and not a reason to abort
			// the walk and drop every later file of the root.
			return nil
		}
		return err
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		if log != nil {
			log.Warn("backup: skipped an entry that stopped being a regular file while it was being archived", "path", rel)
		}
		return nil
	}
	return writeTarFile(tw, f, info, archiveName)
}

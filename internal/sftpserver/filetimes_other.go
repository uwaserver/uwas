//go:build !unix

package sftpserver

import (
	"os"
	"time"
)

// setFileTimes falls back to the path on platforms without futimes; see
// noFollowFlag for why the symlink window does not apply there.
func setFileTimes(f *os.File, atime, mtime time.Time) error {
	return os.Chtimes(f.Name(), atime, mtime)
}

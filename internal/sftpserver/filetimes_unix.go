//go:build unix

package sftpserver

import (
	"os"
	"syscall"
	"time"
)

// setFileTimes sets atime/mtime on the open file itself, so SETSTAT cannot be
// redirected by a symlink planted after the path was validated.
func setFileTimes(f *os.File, atime, mtime time.Time) error {
	return syscall.Futimes(int(f.Fd()), []syscall.Timeval{
		syscall.NsecToTimeval(atime.UnixNano()),
		syscall.NsecToTimeval(mtime.UnixNano()),
	})
}

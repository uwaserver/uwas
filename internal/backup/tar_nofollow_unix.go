//go:build unix

package backup

import "syscall"

// noFollowFlag makes open(2) refuse a final-component symlink; nonBlockFlag
// keeps it from blocking on a FIFO a tenant swapped in.
const (
	noFollowFlag = syscall.O_NOFOLLOW
	nonBlockFlag = syscall.O_NONBLOCK
)

// isSymlinkLoop reports whether err is the kernel refusing a symlink.
func isSymlinkLoop(err error) bool { return isErrno(err, syscall.ELOOP) }

//go:build unix

package migrate

import "syscall"

// noFollowFlag is OR-ed into open(2) flags so the kernel refuses a
// final-component symlink planted in a tenant's tree.
const noFollowFlag = syscall.O_NOFOLLOW

// nonBlockFlag keeps open(2) from blocking on a planted FIFO.
const nonBlockFlag = syscall.O_NONBLOCK

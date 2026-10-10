//go:build unix

package server

import "syscall"

// noFollowFlag makes open(2) refuse a final-component symlink planted in a
// tenant's document root.
const noFollowFlag = syscall.O_NOFOLLOW

// nonBlockFlag keeps open(2) from blocking on a FIFO planted in a docroot.
const nonBlockFlag = syscall.O_NONBLOCK

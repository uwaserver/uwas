//go:build unix

package middleware

import "syscall"

// noFollowFlag makes open(2) refuse a final-component symlink.
const noFollowFlag = syscall.O_NOFOLLOW

// nonBlockFlag keeps open(2) from blocking on a planted FIFO.
const nonBlockFlag = syscall.O_NONBLOCK

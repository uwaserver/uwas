//go:build unix

package filemanager

import "syscall"

// nonBlockFlag keeps open(2) from blocking on a FIFO planted in a web root.
const nonBlockFlag = syscall.O_NONBLOCK

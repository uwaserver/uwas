//go:build unix

package sftpserver

import "syscall"

// noFollowFlag is OR-ed into open(2) flags so the kernel refuses to traverse
// a final-component symlink. This closes a narrow TOCTOU window where an
// SFTP user (who can write inside their own chroot) replaces a regular file
// with a symlink pointing outside the chroot between safePath()'s resolved
// containment check and the open syscall.
const noFollowFlag = syscall.O_NOFOLLOW

// nonBlockFlag keeps open(2) on a FIFO from waiting for its other end: a
// tenant-made FIFO otherwise parked the session, and the OS thread under it,
// until a writer appeared (F67401). It has no effect on regular files and
// directories.
const nonBlockFlag = syscall.O_NONBLOCK

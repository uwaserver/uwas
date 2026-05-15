//go:build unix

package sftpserver

import "syscall"

// noFollowFlag is OR-ed into open(2) flags so the kernel refuses to traverse
// a final-component symlink. This closes a narrow TOCTOU window where an
// SFTP user (who can write inside their own chroot) replaces a regular file
// with a symlink pointing outside the chroot between safePath()'s resolved
// containment check and the open syscall.
const noFollowFlag = syscall.O_NOFOLLOW

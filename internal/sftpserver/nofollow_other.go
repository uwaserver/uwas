//go:build !unix

package sftpserver

// noFollowFlag is a no-op on platforms (Windows, Plan 9) that do not expose
// O_NOFOLLOW. The TOCTOU window the flag would close requires both a
// chroot-style jail and POSIX symlinks; the Windows file-system semantics
// make the equivalent attack uninteresting.
const noFollowFlag = 0

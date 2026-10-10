//go:build unix

package wordpress

import "syscall"

// noFollowFlag is OR-ed into open(2) flags so the kernel refuses to write
// through a final-component symlink planted in a tenant's document root.
const noFollowFlag = syscall.O_NOFOLLOW

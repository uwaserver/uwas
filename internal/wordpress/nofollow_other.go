//go:build !unix

package wordpress

// noFollowFlag is a no-op on platforms without O_NOFOLLOW.
const noFollowFlag = 0

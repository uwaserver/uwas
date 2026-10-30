//go:build !unix

package middleware

// noFollowFlag is a no-op on platforms without O_NOFOLLOW.
const noFollowFlag = 0

// nonBlockFlag is a no-op on platforms without O_NONBLOCK.
const nonBlockFlag = 0

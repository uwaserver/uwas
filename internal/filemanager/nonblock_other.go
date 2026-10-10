//go:build !unix

package filemanager

// nonBlockFlag is a no-op on platforms without O_NONBLOCK.
const nonBlockFlag = 0

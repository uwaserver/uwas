//go:build !unix

package backup

const (
	noFollowFlag = 0
	nonBlockFlag = 0
)

func isSymlinkLoop(error) bool { return false }

//go:build unix

package backup

import (
	"os"
	"syscall"
)

// fileOwner reports the uid/gid that own the file described by info.
func fileOwner(info os.FileInfo) (uid, gid int, ok bool) {
	st, isStat := info.Sys().(*syscall.Stat_t)
	if !isStat {
		return 0, 0, false
	}
	return int(st.Uid), int(st.Gid), true
}

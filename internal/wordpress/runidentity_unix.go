//go:build unix

package wordpress

import (
	"os"
	"os/exec"
	"syscall"
)

// pathOwner reports the uid/gid that own path.
func pathOwner(path string) (uid, gid int, ok bool) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, 0, false
	}
	st, isStat := info.Sys().(*syscall.Stat_t)
	if !isStat {
		return 0, 0, false
	}
	return int(st.Uid), int(st.Gid), true
}

// applyRunIdentity makes cmd start as id. The switch drops the supplementary
// groups; an unprivileged UWAS cannot call setgroups, so they are left alone
// there (wpRunIdentity only yields an identity for a root UWAS in production).
func applyRunIdentity(cmd *exec.Cmd, id *runIdentity) {
	if id == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Credential = &syscall.Credential{
		Uid:         uint32(id.uid),
		Gid:         uint32(id.gid),
		NoSetGroups: os.Geteuid() != 0,
	}
}

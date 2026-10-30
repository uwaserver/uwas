//go:build unix

package phpmanager

import (
	"os"
	"os/exec"
	"syscall"
)

// applyWorkerIdentity makes cmd start as id. The master drops its supplementary
// groups with the switch; an unprivileged UWAS cannot call setgroups, so the
// groups are left alone there (workerIdentityFor only yields an identity for a
// root UWAS in production).
func applyWorkerIdentity(cmd *exec.Cmd, id *workerIdentity) {
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
	// UWAS's working directory (often /root) may not be searchable by the
	// web account.
	if cmd.Dir == "" {
		cmd.Dir = "/"
	}
}

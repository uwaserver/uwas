//go:build !windows

package cronjob

import (
	"os/exec"
	"syscall"
)

// setProcessGroup starts cmd in its own process group so the whole group can be
// signalled at once on timeout.
func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killProcessGroup SIGKILLs cmd's entire process group. A negative PID targets
// the group, so forked children (e.g. the command under `sh -c`) die too.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		// Fall back to killing just the process if the group kill fails.
		_ = cmd.Process.Kill()
	}
}

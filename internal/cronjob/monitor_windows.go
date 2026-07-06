//go:build windows

package cronjob

import "os/exec"

// setProcessGroup is a no-op on Windows; process-group semantics differ and the
// per-process Kill below is sufficient for the timeout path.
func setProcessGroup(cmd *exec.Cmd) {}

// killProcessGroup kills the command process on Windows.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

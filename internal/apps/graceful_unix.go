//go:build !windows

package apps

import (
	"os/exec"
	"syscall"
	"time"
)

func configureProcessGroup(cmd *exec.Cmd) {
	if cmd != nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
}

// gracefulKill sends SIGTERM, waits up to gracefulStopTimeout for the
// process tree to exit, then falls back to SIGKILL. Native apps are
// launched through a shell, so killing only the shell can leave npm/node
// children behind holding the old port after Restart.
func gracefulKill(cmd *exec.Cmd, _ string) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pid := cmd.Process.Pid

	termErr := syscall.Kill(-pid, syscall.SIGTERM)
	if termErr != nil {
		termErr = cmd.Process.Signal(syscall.SIGTERM)
	}

	deadline := time.Now().Add(gracefulStopTimeout)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(-pid, syscall.Signal(0)); err != nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	killErr := syscall.Kill(-pid, syscall.SIGKILL)
	if killErr != nil {
		killErr = cmd.Process.Kill()
	}
	if termErr != nil && killErr != nil {
		return killErr
	}

	for i := 0; i < 10; i++ {
		if err := syscall.Kill(-pid, syscall.Signal(0)); err != nil {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil
}

// gracefulStopTimeout is how long we wait for SIGTERM to take effect
// before sending SIGKILL. 3 seconds matches docker's default and the
// common-case node/python shutdown windows.
const gracefulStopTimeout = 3 * time.Second

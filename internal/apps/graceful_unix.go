//go:build !windows

package apps

import (
	"os/exec"
	"syscall"
	"time"
)

// gracefulKill sends SIGTERM, waits up to gracefulStopTimeout for the
// process to exit on its own, then falls back to SIGKILL. node /
// python / ruby / go apps that install shutdown handlers (express
// .close(), Django graceful, etc.) get a chance to flush in-flight
// requests, close DB connections, and persist state before being
// hard-killed.
//
// Returns nil even when SIGKILL was needed — the goal is "the process
// is dead", not "the process cooperated". Surfaces the SIGTERM send
// error only when both the syscall AND the fallback Kill fail.
func gracefulKill(cmd *exec.Cmd, _ string) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}

	// SIGTERM first.
	termErr := cmd.Process.Signal(syscall.SIGTERM)

	// Poll exit state via Signal(0) — sends no actual signal but
	// returns an error if the process is already gone. Cheaper than
	// reaping cmd.Wait() (which the monitor goroutine owns).
	deadline := time.Now().Add(gracefulStopTimeout)
	for time.Now().Before(deadline) {
		if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
			// Process is gone — graceful stop succeeded.
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Process is still alive after grace period — escalate to KILL.
	killErr := cmd.Process.Kill()
	if termErr != nil && killErr != nil {
		return killErr
	}
	return nil
}

// gracefulStopTimeout is how long we wait for SIGTERM to take effect
// before sending SIGKILL. 3 seconds matches docker's default and the
// common-case node/python shutdown windows.
const gracefulStopTimeout = 3 * time.Second

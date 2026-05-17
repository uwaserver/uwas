//go:build windows

package apps

import (
	"fmt"
	"os/exec"
	"strconv"
)

func configureProcessGroup(_ *exec.Cmd) {}

// gracefulKill on Windows. The OS has no SIGTERM equivalent for
// non-console GUI/service processes — sending CTRL_BREAK_EVENT only
// works for child processes spawned with a job object configured for
// console signal propagation, which we don't do.
//
// startNative invokes runtimes via `cmd /C <command>` so the actual
// long-lived process (node, python, etc.) is a GRANDCHILD of cmd.exe.
// `cmd.Process.Kill()` terminates only cmd.exe — the runtime child
// keeps running, holds its port, and the supervisor's "stopped" state
// silently diverges from reality. `Stop` returns clean, the next
// `Start` then fails with EADDRINUSE because the orphan is still
// bound.
//
// Fix: shell out to `taskkill /T /F /PID <pid>`. /T kills the entire
// process tree (cmd.exe AND its node.exe child), /F is force-kill.
// taskkill is part of every supported Windows install since at least
// XP; failure to find it is a configuration issue, not a corner case.
func gracefulKill(cmd *exec.Cmd, _ string) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pid := strconv.Itoa(cmd.Process.Pid)
	// `taskkill /T /F /PID <pid>` — terminate the whole tree, force.
	// We ignore the exit code because the process might have already
	// exited between the check and the call, which is fine (idempotent
	// stop). Always issue the kill as a safety net.
	out, err := exec.Command("taskkill", "/T", "/F", "/PID", pid).CombinedOutput()
	if err != nil {
		// Fall back to Process.Kill so an EXE without taskkill (locked
		// down sandbox?) still gets at least the parent killed.
		if killErr := cmd.Process.Kill(); killErr != nil {
			return fmt.Errorf("taskkill failed: %v (output: %s); fallback Kill also failed: %v",
				err, string(out), killErr)
		}
	}
	return nil
}

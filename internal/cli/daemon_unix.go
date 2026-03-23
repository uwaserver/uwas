//go:build !windows

package cli

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// daemonize re-executes the current process as a detached background daemon.
// The parent prints the child PID and exits immediately.
func daemonize(args []string) error {
	// Filter out -d flag to prevent infinite recursion
	var filteredArgs []string
	for _, a := range args {
		if a != "-d" {
			filteredArgs = append(filteredArgs, a)
		}
	}

	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}

	cmd := exec.Command(executable, filteredArgs...)
	cmd.Env = append(os.Environ(), "UWAS_DAEMON=1")
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true, // create new session (detach from terminal)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}

	fmt.Printf("UWAS daemon started (PID %d)\n", cmd.Process.Pid)
	os.Exit(0)
	return nil // unreachable
}

//go:build !windows

package cli

import (
	"os"
	"syscall"
)

func isProcessAlive(proc *os.Process) bool {
	return proc.Signal(syscall.Signal(0)) == nil
}

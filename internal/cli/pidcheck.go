package cli

import (
	"os"
	"strconv"
	"strings"
)

// readAlivePID reads a PID file and checks if the process is still running.
// Returns the PID and true if the process is alive.
func readAlivePID(pidFile string) (int, bool) {
	if pidFile == "" {
		return 0, false
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, false
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return 0, false
	}

	// On Unix, FindProcess always succeeds — send signal 0 to check liveness.
	// On Windows, FindProcess fails if the process doesn't exist.
	if !isProcessAlive(proc) {
		return 0, false
	}

	return pid, true
}

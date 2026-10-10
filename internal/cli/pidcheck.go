package cli

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// Hooks for testing.
var (
	isProcessAliveFn = isProcessAlive
	checkUWASPIDFn   = checkUWASPID
)

// procCommIsUWAS reports whether pid is a running uwas process, judged by
// /proc/<pid>/comm (the kernel truncates it to 15 bytes, so a renamed binary
// like "uwas-linux-amd64" still starts with "uwas").
func procCommIsUWAS(pid int) bool {
	comm, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm")
	return err == nil && strings.HasPrefix(strings.TrimSpace(string(comm)), "uwas")
}

// checkUWASPID returns an error unless pid, read from a PID file, still names
// a uwas process other than this CLI. A PID file left behind by a crashed
// server can name a process that later reused the PID; signalling it would
// hit an unrelated process. On Linux the check is /proc/<pid>/comm and a
// missing /proc refuses; other platforms have no procfs, so only the
// self-PID guard applies there.
func checkUWASPID(pid int) error {
	if pid == os.Getpid() {
		return fmt.Errorf("PID %d is this process", pid)
	}
	if runtime.GOOS != "linux" {
		return nil
	}
	if !procCommIsUWAS(pid) {
		return fmt.Errorf("process %d is not a uwas process (stale PID file?)", pid)
	}
	return nil
}

// readAlivePID reads a PID file and checks if the process is still running.
// Returns the PID and true if the process is alive.
func readAlivePID(pidFile string) (int, bool) {
	if pidFile == "" {
		return 0, false
	}
	data, err := osReadFileFn(pidFile)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	// A stale PID reused by another process (or by this one, e.g. PID 1 in
	// a restarted container) is not a running UWAS.
	if checkUWASPIDFn(pid) != nil {
		return 0, false
	}

	proc, err := osFindProcessFn(pid)
	if err != nil {
		return 0, false
	}

	// On Unix, FindProcess always succeeds — send signal 0 to check liveness.
	// On Windows, FindProcess fails if the process doesn't exist.
	if !isProcessAliveFn(proc) {
		return 0, false
	}

	return pid, true
}

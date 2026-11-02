//go:build linux

package cli

import (
	"syscall"
	"testing"
	"time"
)

// startNamed's process must stay alive until the test ends. A copy of
// /bin/sleep renamed to something else exits at once on hosts where sleep is a
// multi-call binary (uutils coreutils: "unknown program"), so the stale-PID
// tests below saw a dead process and refused it as "not a uwas process" (F61000).
func TestStartNamedProcessStaysAlive(t *testing.T) {
	pid, done := startNamed(t, "uwas-test")
	time.Sleep(300 * time.Millisecond)
	select {
	case <-done:
		t.Fatalf("EXPECTED: named helper process %d still running; ACTUAL: it exited", pid)
	default:
	}
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("kill(%d, 0) = %v, want the process alive", pid, err)
	}
}

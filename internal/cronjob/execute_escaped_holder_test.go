//go:build linux

package cronjob

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// A descendant that escapes the job's process group (setsid) keeps the output
// pipes open; Execute must not wait for it past pipeWaitDelay (F1690).
func execWithin(t *testing.T, m *Monitor, command string, within time.Duration) (ExecutionRecord, bool) {
	t.Helper()
	done := make(chan ExecutionRecord, 1)
	go func() { done <- m.Execute("", "* * * * *", command) }()
	select {
	case rec := <-done:
		return rec, true
	case <-time.After(within):
		return ExecutionRecord{}, false
	}
}

func TestExecuteNotHeldByEscapedDescendant(t *testing.T) {
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("no setsid")
	}
	m := NewMonitor(t.TempDir())
	m.SetTimeout(5 * time.Second)

	// escaped holder (setsid -f: the job exits at once), job itself exits 0: returns promptly and counts as success
	rec, ok := execWithin(t, m, "setsid -f sleep 7.31", 4*time.Second)
	if !ok {
		t.Fatal("Execute still blocked by a descendant outside the process group")
	}
	if !rec.Success {
		t.Errorf("job exited 0 but recorded failure: %+v", rec)
	}

	// the overlap guard was released: the same job is not skipped as "already running"
	rec, ok = execWithin(t, m, "setsid -f sleep 7.31", 4*time.Second)
	if !ok || strings.Contains(rec.Error, "already running") {
		t.Errorf("overlap guard stuck: ok=%v rec=%+v", ok, rec)
	}

	// in-group hang is still killed at the timeout and reported as such
	m.SetTimeout(300 * time.Millisecond)
	rec, ok = execWithin(t, m, "sleep 20", 4*time.Second)
	if !ok || rec.Success || !strings.Contains(rec.Error, "timed out") {
		t.Errorf("hung job: ok=%v rec=%+v", ok, rec)
	}

}

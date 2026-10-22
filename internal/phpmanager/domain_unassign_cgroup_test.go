package phpmanager

import (
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/rlimit"
)

// cgroupFakes swaps the rlimit seams for recorders and returns a snapshot func
// plus a channel that receives every domain passed to rlimitRemove.
func cgroupFakes(t *testing.T) (snapshot func() []string, removedCh chan string) {
	t.Helper()
	var mu sync.Mutex
	var removed []string
	removedCh = make(chan string, 16)
	origA, origP, origR := rlimitApply, rlimitAssignPID, rlimitRemove
	rlimitApply = func(d string, _ rlimit.Limits) (string, error) { return "/sys/fs/cgroup/uwas/" + d, nil }
	rlimitAssignPID = func(string, int) error { return nil }
	rlimitRemove = func(d string) error {
		mu.Lock()
		removed = append(removed, d)
		mu.Unlock()
		removedCh <- d
		return nil
	}
	t.Cleanup(func() { rlimitApply, rlimitAssignPID, rlimitRemove = origA, origP, origR })
	return func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), removed...) }, removedCh
}

func cgroupMgr(cmd string) *Manager {
	m := New(testLogger())
	m.installations = []PHPInstall{{Version: "8.4.27", Binary: "/usr/bin/php-cgi8.4", SAPI: "cgi-fcgi"}}
	m.execCommand = func(string, ...string) *exec.Cmd { return exec.Command("sh", "-c", cmd) }
	return m
}

// F23701: unassigning a domain whose worker is already dead (crash backoff)
// used to leave its cgroup directory behind: the monitor had passed its
// still-assigned check, and UnassignDomain had no process to wait for.
func TestUnassignDuringCrashBackoffRemovesCgroup(t *testing.T) {
	snapshot, _ := cgroupFakes(t)

	m := cgroupMgr("exit 1")
	atUnassign := make(chan []string, 1)
	// The crash callback runs on the monitor goroutine after the worker died and
	// before the backoff sleep: a deterministic "worker dead, still assigned".
	m.SetOnCrash(func(d string) {
		m.UnassignDomain(d)
		m.UnassignDomain(d) // idempotent: no second removal
		atUnassign <- snapshot()
	})
	if _, err := m.AssignDomain("leak.test", "8.4"); err != nil {
		t.Fatal(err)
	}
	if err := m.StartDomain("leak.test"); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-atUnassign:
		if len(got) != 1 || got[0] != "leak.test" {
			t.Fatalf("cgroup removals after unassign = %v, want [leak.test]", got)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("crash callback never fired")
	}
}

// A live worker's cgroup is still busy at unassign time; the monitor removes it
// once the worker has exited, not UnassignDomain.
func TestUnassignLiveWorkerLeavesCgroupToMonitor(t *testing.T) {
	snapshot, removedCh := cgroupFakes(t)

	m := cgroupMgr("sleep 60")
	if _, err := m.AssignDomain("live.test", "8.4"); err != nil {
		t.Fatal(err)
	}
	if err := m.StartDomain("live.test"); err != nil {
		t.Fatal(err)
	}
	m.UnassignDomain("live.test")
	if got := snapshot(); len(got) != 0 {
		t.Fatalf("UnassignDomain removed %v while the worker was alive; the monitor must do it after exit", got)
	}
	select {
	case d := <-removedCh:
		if d != "live.test" {
			t.Fatalf("monitor removed %q", d)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("monitor never removed the cgroup after the worker exited")
	}
}

// A domain that never started has no cgroup to remove.
func TestUnassignNeverStartedDomainRemovesNothing(t *testing.T) {
	snapshot, _ := cgroupFakes(t)
	m := cgroupMgr("sleep 60")
	if _, err := m.AssignDomain("idle.test", "8.4"); err != nil {
		t.Fatal(err)
	}
	m.UnassignDomain("idle.test")
	if got := snapshot(); len(got) != 0 {
		t.Fatalf("removed %v for a domain that never started", got)
	}
}

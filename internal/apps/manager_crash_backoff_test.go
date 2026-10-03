package apps

// Regression test for the double-spawn bug: when an app crashes,
// monitorNative clears p.cmd and schedules the auto-restart after a 2s+
// backoff while holding ITS OWN stopCh snapshot. If the operator clicks
// Start inside that window, Start installs a fresh stopCh and spawns a new
// process. The stale monitor's backoff then fires; because its channel was
// never closed it used to proceed with a second spawn, overwriting p.cmd —
// orphaning the operator's process (still running, still holding the port,
// invisible to Stop/Instances) and tracking a spawn that crash-loops on the
// busy port. The generation check in startNative/startDocker aborts any
// spawn whose caller-supplied stopCh is no longer the process's current one.

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"
)

func killTestPID(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Signal(os.Kill)
}

func testProcessAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

func TestStartDuringCrashBackoffSupersedesStaleAutoRestart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process semantics required")
	}

	app := &App{
		Name:        "backoff-supersede",
		Runtime:     RuntimeCustom,
		Command:     "sleep 58", // unique duration: no other test uses it
		AutoRestart: true,
		WorkDir:     filepath.Join(t.TempDir(), "workdir"),
	}
	m := NewManager(NewStore(t.TempDir()), nil)
	if err := m.Register(app); err != nil {
		t.Fatalf("Register: %v", err)
	}
	t.Cleanup(func() {
		m.StopAll()
		// Defense in depth: no stale spawn may outlive the test.
		_ = exec.Command("pkill", "-f", "sleep 58").Run()
	})

	if err := m.Start(app.Name); err != nil {
		t.Fatalf("initial Start: %v", err)
	}
	inst := m.Get(app.Name)
	if inst == nil || !inst.Running || inst.PID == 0 {
		t.Fatalf("setup: app not running after Start: %+v", inst)
	}

	// Simulate a crash; the monitor enters its 2s backoff.
	if err := killTestPID(inst.PID); err != nil {
		t.Fatalf("kill crashed process: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		inst = m.Get(app.Name)
		if inst != nil && !inst.Running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("monitor never observed the crash")
		}
		time.Sleep(25 * time.Millisecond)
	}

	// Operator clicks Start INSIDE the backoff window.
	if err := m.Start(app.Name); err != nil {
		t.Fatalf("Start during backoff: %v", err)
	}
	inst2 := m.Get(app.Name)
	if inst2 == nil || !inst2.Running || inst2.PID == 0 {
		t.Fatalf("Start during backoff left the app stopped: %+v", inst2)
	}
	pid2 := inst2.PID
	if !testProcessAlive(pid2) {
		t.Fatalf("operator's process (PID %d) died unexpectedly", pid2)
	}

	// Wait past the stale backoff (2s from the crash) plus margin.
	time.Sleep(3 * time.Second)

	inst3 := m.Get(app.Name)
	if inst3 == nil {
		t.Fatal("app unregistered after settle")
	}
	if !testProcessAlive(pid2) {
		t.Fatalf("operator's process (PID %d) was killed by the stale auto-restart", pid2)
	}
	if inst3.PID != pid2 {
		if inst3.PID != 0 {
			_ = killTestPID(inst3.PID)
		}
		t.Errorf("manager now tracks PID %d, but the operator's Start process (PID %d, still alive) was orphaned by the stale auto-restart — two processes exist for one app and Stop() can no longer stop the one holding the port", inst3.PID, pid2)
	}
}

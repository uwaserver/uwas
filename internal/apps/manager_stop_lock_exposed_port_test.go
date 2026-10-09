//go:build !windows

package apps

import (
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func stopLockStacks() string {
	buf := make([]byte, 1<<20)
	return string(buf[:runtime.Stack(buf, true)])
}

func stopLockWaitFor(t *testing.T, cond func(string) bool, what string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second) // failure bound only
	for time.Now().Before(deadline) {
		if cond(stopLockStacks()) {
			return
		}
		runtime.Gosched()
	}
	t.Fatalf("timed out waiting for %s", what)
}

func exposedPortManager(t *testing.T) *Manager {
	s := NewStore(t.TempDir())
	s.DataRoot = s.Dir
	return NewManager(s, nil)
}

func TestRegisterAvoidsOtherAppsExposedPorts(t *testing.T) {
	orig := isPortFreeFn
	isPortFreeFn = func(int) bool { return true }
	defer func() { isPortFreeFn = orig }()
	reg := func(m *Manager, a *App) {
		t.Helper()
		if err := m.Register(a); err != nil {
			t.Fatal(err)
		}
	}
	// repro: auto-assign and explicit request avoid another app's extra port
	m := exposedPortManager(t)
	m.nextPort = 3001
	reg(m, &App{Name: "owner", Runtime: RuntimeCustom, Command: "run", Port: 4000, Ports: []int{3001, 3002}})
	reg(m, &App{Name: "auto", Runtime: RuntimeCustom, Command: "run"})
	if got := m.procs["auto"].port; got == 3001 || got == 3002 || got == 4000 {
		t.Fatalf("auto got owned port %d", got)
	}
	reg(m, &App{Name: "req", Runtime: RuntimeCustom, Command: "run", Port: 3002})
	if got := m.procs["req"].port; got == 3001 || got == 3002 || got == 4000 {
		t.Fatalf("request kept owned port %d", got)
	}
	// persisted value matches in-memory resolution
	if saved, err := m.store.Get("req"); err != nil || saved.Port != m.procs["req"].port {
		t.Fatalf("persisted port mismatch: %+v %v", saved, err)
	}
	// edge: an app re-registering with its own extra port as primary is not a conflict with itself
	reg(m, &App{Name: "owner", Runtime: RuntimeCustom, Command: "run", Port: 3001, Ports: []int{3002}})
	if got := m.procs["owner"].port; got != 3001 {
		t.Fatalf("self extra port treated as conflict: %d", got)
	}
	// edge: free requested port is kept unchanged
	reg(m, &App{Name: "free", Runtime: RuntimeCustom, Command: "run", Port: 5555})
	if got := m.procs["free"].port; got != 5555 {
		t.Fatalf("free port changed: %d", got)
	}
	// edge: repeated registration is stable
	reg(m, &App{Name: "free", Runtime: RuntimeCustom, Command: "run", Port: 5555})
	if got := m.procs["free"].port; got != 5555 {
		t.Fatalf("repeat changed port: %d", got)
	}
	// edge: after the owner is unregistered its extra ports become assignable
	if err := m.Unregister("owner"); err != nil {
		t.Fatal(err)
	}
	reg(m, &App{Name: "late", Runtime: RuntimeCustom, Command: "run", Port: 3002})
	if got := m.procs["late"].port; got != 3002 {
		t.Fatalf("released port not reusable: %d", got)
	}
}

func TestStopReleasesManagerLockDuringGrace(t *testing.T) {
	origFree, origExec := isPortFreeFn, execCommandFn
	isPortFreeFn = func(int) bool { return true }
	execCommandFn = func(string, ...string) *exec.Cmd {
		return exec.Command("sh", "-c", "trap '' TERM; exec sleep 1000")
	}
	defer func() { isPortFreeFn, execCommandFn = origFree, origExec }()

	m := exposedPortManager(t)
	for i, n := range []string{"slow", "other"} {
		if err := m.Register(&App{Name: n, Runtime: RuntimeCustom, Command: "run", Port: 45101 + i, WorkDir: t.TempDir()}); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Start("slow"); err != nil {
		t.Fatal(err)
	}
	pid := m.Get("slow").PID

	stop1 := make(chan error, 1)
	go func() { stop1 <- m.Stop("slow") }()
	stopLockWaitFor(t, func(s string) bool { return strings.Contains(s, "apps.gracefulKill") }, "stop in grace window")

	// repro: readers for other apps are not blocked during the grace window
	r := make(chan struct{})
	go func() { _ = m.ListenAddrForPort("other", 0); _ = m.Instances(); close(r) }()
	stopLockWaitFor(t, func(s string) bool {
		select {
		case <-r:
			return true
		default:
			return false
		}
	}, "reader to return while stop is in grace window")
	if !strings.Contains(stopLockStacks(), "apps.gracefulKill") {
		t.Fatal("reader only returned after stop finished; not a valid check")
	}

	// edge: Start during the grace window is refused instead of spawning a second tree
	if err := m.Start("slow"); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("Start during stop: %v", err)
	}
	// edge: a concurrent second Stop is harmless
	stop2 := make(chan error, 1)
	go func() { stop2 <- m.Stop("slow") }()

	if err := <-stop1; err != nil {
		t.Fatal(err)
	}
	if err := <-stop2; err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(-pid, 0); err == nil {
		t.Fatal("process group still alive after Stop returned")
	}
	if inst := m.Get("slow"); inst.Running || m.ListenAddr("slow") != "" {
		t.Fatalf("still reported running after Stop: %+v", inst)
	}
	// edge: monitor does not auto-restart a stopped app
	stopLockWaitFor(t, func(s string) bool { return !strings.Contains(s, "apps.(*Manager).monitorNative") }, "monitor exit")
	if m.Get("slow").Running {
		t.Fatal("auto-restarted after Stop")
	}
	// edge: repeated Stop on an already-stopped app is a no-op
	if err := m.Stop("slow"); err != nil {
		t.Fatal(err)
	}
	// edge: Start works again after the stop completed, and Stop cleans it up
	if err := m.Start("slow"); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop("slow"); err != nil {
		t.Fatal(err)
	}
	if m.Get("slow").Running {
		t.Fatal("restart run not stopped")
	}
}

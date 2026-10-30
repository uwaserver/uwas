package apps

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// A superseded auto-restart (its stopCh no longer the process's current one) is
// abandoned by startNative, but the port file the LIVE run announced its port
// through must survive: WaitListening discovers a random-port app's port by
// reading that file, so deleting it makes a healthy app look like it never
// bound.
func TestStaleStartNativeLeavesLivePortFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process semantics required")
	}
	app := &App{
		Name:        "stale-portfile",
		Runtime:     RuntimeCustom,
		Command:     "sleep 57", // unique duration: no other test uses it
		AutoRestart: true,
		WorkDir:     filepath.Join(t.TempDir(), "workdir"),
	}
	m := testManager(t, NewStore(t.TempDir()), nil)
	if err := m.Register(app); err != nil {
		t.Fatalf("Register: %v", err)
	}
	t.Cleanup(func() {
		m.StopAll()
	})
	if err := m.Start(app.Name); err != nil {
		t.Fatalf("Start: %v", err)
	}

	m.mu.RLock()
	p := m.procs[app.Name]
	m.mu.RUnlock()
	portFile := runtimePortFile(p.workDir, p.name)
	if err := os.WriteFile(portFile, []byte("43111\n"), 0o644); err != nil {
		t.Fatalf("write port file: %v", err)
	}

	// A stale auto-restart: its stopCh is not the one Start installed.
	stale := make(chan struct{})
	if err := m.startNative(p, stale); err != nil {
		t.Fatalf("stale startNative: %v", err)
	}

	if _, err := os.Stat(portFile); err != nil {
		t.Fatalf("EXPECTED: live run's port file survives a superseded restart\nACTUAL: %v\nPROBLEM CONFIRMED", err)
	}
	// Control: the live process itself is untouched.
	if inst := m.Get(app.Name); inst == nil || !inst.Running {
		t.Fatalf("live process lost: %+v", inst)
	}
}

// Controls for the guard above: a legitimate (re)start still clears the previous
// run's port file, and a spawn racing Stop leaves the files alone and starts
// nothing.
func TestStartNativePortFileLifecycle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process semantics required")
	}
	app := &App{
		Name:        "portfile-lifecycle",
		Runtime:     RuntimeCustom,
		Command:     "sleep 56", // unique duration: no other test uses it
		AutoRestart: false,
		WorkDir:     filepath.Join(t.TempDir(), "workdir"),
	}
	m := testManager(t, NewStore(t.TempDir()), nil)
	if err := m.Register(app); err != nil {
		t.Fatalf("Register: %v", err)
	}
	t.Cleanup(m.StopAll)

	m.mu.RLock()
	p := m.procs[app.Name]
	m.mu.RUnlock()
	portFile := runtimePortFile(p.workDir, p.name)
	if err := os.MkdirAll(filepath.Dir(portFile), 0o755); err != nil {
		t.Fatal(err)
	}

	// Leftover from a previous run is cleared by a real start.
	if err := os.WriteFile(portFile, []byte("40001\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(app.Name); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := os.Stat(portFile); !os.IsNotExist(err) {
		t.Fatalf("a real start must clear the previous run's port file, stat err = %v", err)
	}

	// After Stop, a spawn still holding the old stopCh does nothing at all.
	if err := m.Stop(app.Name); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := os.WriteFile(portFile, []byte("40002\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.mu.RLock()
	oldCh := p.stopCh
	m.mu.RUnlock()
	if err := m.startNative(p, oldCh); err != nil {
		t.Fatalf("startNative after Stop: %v", err)
	}
	if _, err := os.Stat(portFile); err != nil {
		t.Fatalf("a spawn racing Stop removed the port file: %v", err)
	}
	if inst := m.Get(app.Name); inst == nil || inst.Running {
		t.Fatalf("a spawn racing Stop started a process: %+v", inst)
	}
}

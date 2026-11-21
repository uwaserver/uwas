package phpmanager

import (
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

// F65800: Status (polled by the dashboard) deleted StartFPM's in-flight
// reservation as if it were a dead process, so a second start spawned another
// worker for the same version and the first start's Store left it untracked.
func TestStatusKeepsInFlightStartReservation(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{{Version: "8.4.19", Binary: filepath.Join(t.TempDir(), "php-cgi"), SAPI: "cgi-fcgi"}}
	entered := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	var spawned []*exec.Cmd
	m.execCommand = func(string, ...string) *exec.Cmd {
		mu.Lock()
		c := exec.Command("sleep", "100")
		spawned = append(spawned, c)
		first := len(spawned) == 1
		mu.Unlock()
		if first {
			close(entered)
			<-release
		}
		return c
	}
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		for _, c := range spawned {
			if c.Process != nil {
				_ = c.Process.Kill()
			}
		}
	})

	done := make(chan error, 1)
	go func() { done <- m.StartFPM("8.4", "127.0.0.1:0") }()
	<-entered

	for _, st := range m.Status() {
		if st.Version == "8.4.19" && st.Running {
			t.Error("Status reported a still-starting worker as running")
		}
	}
	if _, ok := m.processes.Load("8.4.19"); !ok {
		t.Error("Status deleted the in-flight start reservation")
	}
	if err := m.StartFPM("8.4", "127.0.0.1:0"); err == nil {
		t.Error("a second StartFPM was allowed after Status ran during the first")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("first start: %v", err)
	}
	if err := m.StopFPM("8.4"); err != nil {
		t.Fatalf("stop after start: %v", err)
	}
	mu.Lock()
	n := len(spawned)
	mu.Unlock()
	if n != 1 {
		t.Errorf("spawned %d workers for one version, want 1", n)
	}
}

// Status still clears an entry whose process is gone.
func TestStatusClearsDeadEntry(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{{Version: "8.4.19", Binary: "/nonexistent/php-cgi", SAPI: "cgi-fcgi"}}
	c := exec.Command("true")
	if err := c.Run(); err != nil {
		t.Fatalf("true: %v", err)
	}
	m.processes.Store("8.4.19", &processInfo{cmd: c, listenAddr: "127.0.0.1:0"})
	_ = m.Status()
	if _, ok := m.processes.Load("8.4.19"); ok {
		t.Error("Status kept an entry whose process has exited")
	}
}

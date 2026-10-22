package phpmanager

import (
	"os/exec"
	"strings"
	"testing"
)

func procKeyMgr() *Manager {
	m := New(testLogger())
	m.installations = []PHPInstall{{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4", SAPI: "cgi-fcgi"}}
	m.execCommand = func(string, ...string) *exec.Cmd { return exec.Command("sleep", "60") }
	return m
}

func procKeyStopAll(m *Manager) {
	m.processes.Range(func(_, v any) bool {
		if p, ok := v.(*processInfo); ok && p.cmd != nil {
			_ = killProcessGroup(p.cmd)
		}
		return true
	})
}

func procKeyRunning(m *Manager) bool {
	for _, s := range m.Status() {
		if s.Version == "8.4.19" {
			return s.Running
		}
	}
	return false
}

// Regression F23400: the process table is keyed by the installation's full version no
// matter how the caller spelled it.
func TestFPMProcessKeyIsFullVersion(t *testing.T) {
	// Reproduction.
	m := procKeyMgr()
	defer procKeyStopAll(m)
	if err := m.StartFPM("8.4", "127.0.0.1:9000"); err != nil {
		t.Fatal(err)
	}
	if !procKeyRunning(m) {
		t.Fatal("Status must report the short-version start as running")
	}
	if err := m.StartFPM("8.4.19", "127.0.0.1:9001"); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("second start must be refused, got %v", err)
	}
	if err := m.StartFPM("8.4", "127.0.0.1:9002"); err == nil {
		t.Fatal("repeated short start must be refused")
	}

	// Edge: stop by short version stops the process started by full version.
	m2 := procKeyMgr()
	defer procKeyStopAll(m2)
	if err := m2.StartFPM("8.4.19", "127.0.0.1:9000"); err != nil {
		t.Fatal(err)
	}
	if err := m2.StopFPM("8.4"); err != nil {
		t.Fatalf("StopFPM(8.4): %v", err)
	}
	if _, ok := m2.processes.Load("8.4.19"); ok {
		t.Fatal("process entry must be gone after StopFPM(8.4)")
	}

	// Edge: restart by short version keeps a single entry and the listen addr.
	m3 := procKeyMgr()
	defer procKeyStopAll(m3)
	if err := m3.StartFPM("8.4", "127.0.0.1:9005"); err != nil {
		t.Fatal(err)
	}
	if err := m3.RestartFPM("8.4"); err != nil {
		t.Fatalf("RestartFPM(8.4): %v", err)
	}
	v, ok := m3.processes.Load("8.4.19")
	if !ok || v.(*processInfo).listenAddr != "127.0.0.1:9005" {
		t.Fatalf("restart must re-register 8.4.19 on the same address, got %+v ok=%v", v, ok)
	}
	if _, ok := m3.processes.Load("8.4"); ok {
		t.Fatal("no entry may be keyed by the short spelling")
	}

	// Edge: unknown version fails and leaves no reservation behind.
	m4 := procKeyMgr()
	if err := m4.StartFPM("9.9", "127.0.0.1:9000"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unknown version must be 'not found', got %v", err)
	}
	n := 0
	m4.processes.Range(func(_, _ any) bool { n++; return true })
	if n != 0 {
		t.Fatalf("unknown version left %d process entries", n)
	}
	t.Log("FIX VERIFIED")
}

package phpmanager

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The global worker must load the php.ini GetConfig/SetConfig edit. Without
// -c it read the binary's compiled default, so on a build that loaded no
// php.ini (UWAS created one) config saves never took effect (F65600).
func TestGlobalWorkerLoadsEditedPHPINI(t *testing.T) {
	for _, tc := range []struct {
		name, sapi string
		fpmSibling bool
	}{
		{"php-cgi", "cgi-fcgi", false},
		{"php-fpm sibling", "cgi-fcgi", true},
		{"php-fpm binary", "fpm-fcgi", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "php-cgi")
			if tc.sapi == "fpm-fcgi" {
				bin = filepath.Join(dir, "php-fpm")
			}
			if tc.fpmSibling {
				if err := os.WriteFile(filepath.Join(dir, "php-fpm"), []byte("#!/bin/sh\n"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			ini := filepath.Join(dir, "php.ini")
			if err := os.WriteFile(ini, []byte("[PHP]\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			m := New(testLogger())
			m.installations = []PHPInstall{{Version: "8.4.19", Binary: bin, SAPI: tc.sapi, ConfigFile: ini}}
			var mu sync.Mutex
			var argv []string
			m.execCommand = func(_ string, a ...string) *exec.Cmd {
				mu.Lock()
				argv = append([]string(nil), a...)
				mu.Unlock()
				return exec.Command("sleep", "100")
			}
			for _, step := range []func() error{
				func() error { return m.StartFPM("8.4", "127.0.0.1:0") },
				func() error { return m.RestartFPM("8.4") },
			} {
				if err := step(); err != nil {
					t.Fatal(err)
				}
				mu.Lock()
				got := strings.Join(argv, " ")
				mu.Unlock()
				if !strings.Contains(got, "-c "+ini) {
					t.Errorf("worker argv %q does not load %s", got, ini)
				}
			}
			if err := m.StopFPM("8.4"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// StopFPM used to delete StartFPM's in-flight reservation as a stale entry and
// report success; the worker then came up anyway and a second start spawned an
// untracked duplicate no stop could reach (F65601).
func TestStopFPMRefusesInFlightStart(t *testing.T) {
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

	if err := m.StopFPM("8.4"); err == nil {
		t.Error("StopFPM reported success while the worker was still starting")
	}
	if err := m.RestartFPM("8.4"); err == nil {
		t.Error("RestartFPM succeeded while the worker was still starting")
	}
	if err := m.StartFPM("8.4", "127.0.0.1:0"); err == nil {
		t.Error("a second StartFPM was allowed while the first was starting")
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

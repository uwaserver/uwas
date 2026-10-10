package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
)

// startSleepAs starts `sleep 300` from a copy named binName ("" keeps the
// real sleep binary) so the child's /proc/<pid>/comm is binName.
func startSleepAs(t *testing.T, binName string) int {
	t.Helper()
	sleepPath, err := exec.LookPath("sleep")
	if err != nil {
		t.Skipf("sleep not available: %v", err)
	}
	bin := sleepPath
	if binName != "" {
		data, err := os.ReadFile(sleepPath)
		if err != nil {
			t.Fatal(err)
		}
		bin = filepath.Join(t.TempDir(), binName)
		if err := os.WriteFile(bin, data, 0755); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(bin, "300")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	return cmd.Process.Pid
}

// installKillsFor runs installUWAS with /var/run/uwas.pid containing pid and
// returns the recorded `kill` invocations (no signal is actually sent).
func installKillsFor(t *testing.T, pid int) [][]string {
	t.Helper()
	var mu sync.Mutex
	var kills [][]string
	started := false
	installRootLinuxEnv(t, func(name string, arg ...string) *exec.Cmd {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case name == "kill":
			kills = append(kills, arg)
		case name == "systemctl" && len(arg) >= 1 && arg[0] == "start":
			started = true
		case name == "systemctl" && len(arg) >= 1 && arg[0] == "is-active":
			if started {
				return exec.Command("printf", "active")
			}
			return exec.Command("printf", "inactive")
		}
		return exec.Command("true")
	})
	installOsReadFile = func(name string) ([]byte, error) {
		if name == "/var/run/uwas.pid" {
			return []byte(strconv.Itoa(pid) + "\n"), nil
		}
		return []byte("binary-data"), nil
	}
	var err error
	captureStderr(t, func() {
		_ = captureStdout(t, func() { err = installUWAS([]string{"--no-config"}) })
	})
	if err != nil {
		t.Fatalf("install error: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	return kills
}

// TestInstallForceKillOnlyTargetsUWAS guards F845: a stale PID file whose PID
// was recycled by an unrelated process (or names the installer itself) must
// not get that process killed as root; a live uwas process still is.
func TestInstallForceKillOnlyTargetsUWAS(t *testing.T) {
	if _, err := os.Stat("/proc/self/comm"); err != nil {
		t.Skip("needs /proc")
	}
	if k := installKillsFor(t, startSleepAs(t, "")); len(k) != 0 {
		t.Errorf("unrelated process was signalled: %v", k)
	}
	if k := installKillsFor(t, os.Getpid()); len(k) != 0 {
		t.Errorf("installer's own pid was signalled: %v", k)
	}
	if k := installKillsFor(t, startSleepAs(t, "uwas")); len(k) != 2 {
		t.Errorf("live uwas process: kills = %v, want TERM and KILL", k)
	}
}

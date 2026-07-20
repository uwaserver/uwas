package cloudflare

import (
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRunner_StartRequiresToken(t *testing.T) {
	r := NewRunner(nil)
	if err := r.Start("tid", ""); err == nil {
		t.Fatal("expected error for empty token")
	}
}

func TestRunner_StartStop(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil && runtime.GOOS != "windows" {
		t.Skip("sleep not on PATH; needed to fake cloudflared")
	}

	// Stub execCommandFn to spawn a long-running sleep instead of cloudflared.
	orig := execCommandFn
	defer func() { execCommandFn = orig }()
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		// We ignore the real binary path and substitute sleep; this verifies
		// the lifecycle plumbing without needing cloudflared on PATH.
		if runtime.GOOS == "windows" {
			return exec.Command("cmd", "/c", "ping", "-n", "30", "127.0.0.1")
		}
		return exec.Command("sleep", "30")
	}

	r := &Runner{
		procs:  make(map[string]*runningProc),
		binary: "/fake/cloudflared", // any non-empty value bypasses LookPath
	}
	if err := r.Start("tid", "fake-token"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Give the process a moment to register.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if r.IsRunning("tid") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !r.IsRunning("tid") {
		t.Fatal("expected tunnel to be running")
	}

	st := r.StatusOf("tid")
	if !st.Running || st.PID == 0 {
		t.Fatalf("expected running status with PID, got %+v", st)
	}

	if err := r.Stop("tid"); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// After stop, the monitor should have set cmd=nil within ~1s.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !r.IsRunning("tid") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if r.IsRunning("tid") {
		t.Fatal("expected tunnel to be stopped")
	}
}

func TestRunner_StopUnknownIsNoop(t *testing.T) {
	r := NewRunner(nil)
	if err := r.Stop("does-not-exist"); err != nil {
		t.Fatalf("Stop on unknown should be a no-op, got %v", err)
	}
}

func TestRunner_DoubleStartFails(t *testing.T) {
	orig := execCommandFn
	defer func() { execCommandFn = orig }()
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		if runtime.GOOS == "windows" {
			return exec.Command("cmd", "/c", "ping", "-n", "30", "127.0.0.1")
		}
		return exec.Command("sleep", "30")
	}
	r := &Runner{
		procs:  make(map[string]*runningProc),
		binary: "/fake/cloudflared",
	}
	if err := r.Start("tid", "tok"); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	defer r.Stop("tid")

	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if r.IsRunning("tid") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if err := r.Start("tid", "tok"); err == nil {
		t.Fatal("expected double-start to fail")
	}
}

// TestRunner_StartAfterSpawnFailureRetries is the regression for the stuck
// placeholder bug: a failed spawn (missing binary, cmd.Start error) used to
// leave a non-stopped entry in r.procs, so every later Start was rejected with
// "already running or starting (pid 0)" until process restart.
func TestRunner_StartAfterSpawnFailureRetries(t *testing.T) {
	orig := execCommandFn
	defer func() { execCommandFn = orig }()
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		return exec.Command("/nonexistent/uwas-test-binary")
	}
	r := &Runner{
		procs:  make(map[string]*runningProc),
		binary: "/fake/cloudflared", // any non-empty value bypasses LookPath
	}
	if err := r.Start("tid", "tok"); err == nil {
		t.Fatal("expected first Start to fail (binary does not exist)")
	}

	// The failed placeholder must be gone so a retry is not rejected.
	if err := r.Start("tid", "tok"); err != nil && strings.Contains(err.Error(), "already running") {
		t.Fatalf("second Start rejected by stale placeholder: %v", err)
	}
	r.mu.Lock()
	_, tracked := r.procs["tid"]
	r.mu.Unlock()
	if tracked {
		t.Error("failed spawn left an entry in r.procs")
	}
}

func TestFormatUptime(t *testing.T) {
	cases := map[time.Duration]string{
		5 * time.Second:  "5s",
		90 * time.Second: "1m30s",
		2*time.Hour + 13*time.Minute + 5*time.Second: "2h13m",
	}
	for in, want := range cases {
		if got := formatUptime(in); got != want {
			t.Errorf("formatUptime(%v) = %q, want %q", in, got, want)
		}
	}
}

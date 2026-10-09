//go:build linux

package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A PID file holding "-N" must be rejected: kill(-N) signals the whole
// process group N. The helper leads its own group, so on regression only it
// can be reached.
func TestStopRestartRejectNonPositivePID(t *testing.T) {
	cmd := exec.Command("/bin/cat")
	if _, err := cmd.StdinPipe(); err != nil {
		t.Fatal(err)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-done
	})
	pid := cmd.Process.Pid
	if pg, err := syscall.Getpgid(pid); err != nil || pg != pid {
		t.Fatalf("helper pgid=%d err=%v, want %d", pg, err, pid)
	}

	pidFile := filepath.Join(t.TempDir(), "uwas.pid")
	if err := os.WriteFile(pidFile, []byte("-"+strconv.Itoa(pid)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := (&StopCommand{}).Run([]string{"--pid-file", pidFile}); err == nil || !strings.Contains(err.Error(), "invalid PID") {
		t.Errorf("stop: err=%v, want invalid PID", err)
	}
	if err := (&RestartCommand{}).Run([]string{"--pid-file", pidFile, "--api-url", "http://127.0.0.1:1"}); err == nil || !strings.Contains(err.Error(), "invalid PID") {
		t.Errorf("restart: err=%v, want invalid PID", err)
	}
	select {
	case err := <-done:
		done <- err
		t.Fatalf("helper process group was signalled: %v", err)
	case <-time.After(200 * time.Millisecond):
	}

	// pid 0 and -1 must also stop before the process lookup.
	old := osFindProcessFn
	t.Cleanup(func() { osFindProcessFn = old })
	osFindProcessFn = func(p int) (*os.Process, error) {
		t.Errorf("FindProcess called for pid %d", p)
		return nil, os.ErrInvalid
	}
	for _, v := range []string{"0", "-1"} {
		if err := os.WriteFile(pidFile, []byte(v), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := (&StopCommand{}).Run([]string{"--pid-file", pidFile}); err == nil || !strings.Contains(err.Error(), "invalid PID") {
			t.Errorf("stop %q: err=%v, want invalid PID", v, err)
		}
	}
}

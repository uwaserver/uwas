//go:build linux

package cli

import (
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// startNamed runs a copy of /bin/sleep under the given name, so its
// /proc/<pid>/comm is that name. It returns the PID and a channel closed
// when the process exits.
func startNamed(t *testing.T, name string) (int, <-chan struct{}) {
	t.Helper()
	src, err := os.Open("/bin/sleep")
	if err != nil {
		t.Skipf("no /bin/sleep: %v", err)
	}
	defer src.Close()
	bin := filepath.Join(t.TempDir(), name)
	dst, err := os.OpenFile(bin, os.O_CREATE|os.O_WRONLY, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		t.Fatal(err)
	}
	dst.Close()
	cmd := exec.Command(bin, "600")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-done })
	waitComm(t, cmd.Process.Pid, name)
	return cmd.Process.Pid, done
}

// waitComm waits until /proc/<pid>/comm shows name. Right after Start the
// child can still report the parent's comm until its exec completes, which
// made the uwas-named case flake under a loaded full-suite run.
func waitComm(t *testing.T, pid int, name string) {
	t.Helper()
	if len(name) > 15 { // TASK_COMM_LEN - 1
		name = name[:15]
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		comm, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm")
		if err == nil && strings.TrimSpace(string(comm)) == name {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("/proc/%d/comm = %q, want %q", pid, strings.TrimSpace(string(comm)), name)
		}
		time.Sleep(time.Millisecond)
	}
}

func writePID(t *testing.T, pid int) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "uwas.pid")
	if err := os.WriteFile(p, []byte(strconv.Itoa(pid)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// A PID file left by a crashed server can name a PID that an unrelated
// process (or this CLI) has since reused; stop and restart must refuse to
// signal it (F855), and serve's already-running check must not count it (F856).
func TestStopRestartRefuseStaleNonUWASPID(t *testing.T) {
	oldAlive := isProcessAliveFn
	isProcessAliveFn = func(*os.Process) bool { return false } // skip stop's wait loop
	t.Cleanup(func() { isProcessAliveFn = oldAlive })

	// Catch SIGTERM so a regression that signals this process fails the
	// test instead of killing the test binary.
	term := make(chan os.Signal, 1)
	signal.Notify(term, syscall.SIGTERM)
	t.Cleanup(func() { signal.Stop(term) })

	cmds := map[string]func(string) error{
		"stop": func(pf string) error { return (&StopCommand{}).Run([]string{"--pid-file", pf}) },
		"restart": func(pf string) error {
			return (&RestartCommand{}).Run([]string{"--pid-file", pf, "--api-url", "http://127.0.0.1:1"})
		},
	}
	for name, run := range cmds {
		t.Run(name, func(t *testing.T) {
			pid, _ := startNamed(t, "postgres")
			if err := run(writePID(t, pid)); err == nil || !strings.Contains(err.Error(), "refusing to signal") {
				t.Errorf("unrelated pid: err=%v, want refusing to signal", err)
			}
			if err := run(writePID(t, os.Getpid())); err == nil || !strings.Contains(err.Error(), "refusing to signal") {
				t.Errorf("own pid: err=%v, want refusing to signal", err)
			}
			select {
			case <-term:
				t.Fatal("test process was sent SIGTERM")
			default:
			}

			upid, udone := startNamed(t, "uwas-test")
			if err := run(writePID(t, upid)); err != nil {
				t.Fatalf("uwas pid: err=%v, want nil", err)
			}
			<-udone // SIGTERM reached the uwas process
		})
	}
}

func TestReadAlivePIDIgnoresReusedPID(t *testing.T) {
	other, _ := startNamed(t, "postgres")
	if _, ok := readAlivePID(writePID(t, other)); ok {
		t.Error("unrelated live pid reported as a running uwas")
	}
	if _, ok := readAlivePID(writePID(t, os.Getpid())); ok {
		t.Error("own pid reported as a running uwas")
	}
	upid, _ := startNamed(t, "uwas-test")
	if pid, ok := readAlivePID(writePID(t, upid)); !ok || pid != upid {
		t.Errorf("uwas pid: got %d,%v want %d,true", pid, ok, upid)
	}
}

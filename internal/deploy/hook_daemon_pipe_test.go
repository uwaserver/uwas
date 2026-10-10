//go:build linux

package deploy

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// A deploy command that leaves a daemon holding its output pipes must not hang
// the deploy for the daemon's lifetime (F1691), and must not lose its output or
// its failure status.
func TestRunCmdNotHeldByDaemon(t *testing.T) {
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("no setsid")
	}
	type result struct {
		out string
		err error
	}
	run := func(name string, args ...string) (result, bool) {
		ch := make(chan result, 1)
		go func() {
			out, err := runCmdImpl("", nil, name, args...)
			ch <- result{out, err}
		}()
		select {
		case r := <-ch:
			return r, true
		case <-time.After(5 * time.Second):
			return result{}, false
		}
	}

	r, ok := run("sh", "-c", "echo built; setsid -f sleep 7.35")
	if !ok {
		t.Fatal("success path blocked by a daemon holding the pipes")
	}
	if r.err != nil || !strings.Contains(r.out, "built") {
		t.Errorf("success with daemon: out=%q err=%v, want output kept and nil error", r.out, r.err)
	}

	r, ok = run("sh", "-c", "echo broke; setsid -f sleep 7.36; exit 3")
	if !ok {
		t.Fatal("failure path blocked by a daemon holding the pipes")
	}
	if r.err == nil || !strings.Contains(r.out, "broke") {
		t.Errorf("failure with daemon: out=%q err=%v, want output kept and the exit error", r.out, r.err)
	}

	// shell hooks share the same bounded runner
	ch := make(chan error, 1)
	go func() { _, err := runShellImpl("", nil, "setsid -f sleep 7.37"); ch <- err }()
	select {
	case err := <-ch:
		if err != nil {
			t.Errorf("runShellImpl: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runShellImpl blocked by a daemon holding the pipes")
	}
}

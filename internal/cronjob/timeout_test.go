package cronjob

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// crontab invocations run inside an admin HTTP handler and had no deadline.
// crontab can block — waiting on a lock, on a filesystem that is not
// answering, or on a host where installing a crontab needs a permission the
// process does not have — and a blocked call held the request open with
// nothing to interrupt it.
//
// It also hung the test suite: internal/admin took the full 10 minute package
// timeout on a machine where `crontab <file>` blocks, instead of 54 seconds.

func withCrontabTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	orig := crontabTimeout
	crontabTimeout = d
	t.Cleanup(func() { crontabTimeout = orig })
}

func TestRunCrontabKillsAHungCommand(t *testing.T) {
	withCrontabTimeout(t, 100*time.Millisecond)

	start := time.Now()
	_, err := runCrontab(exec.Command("sleep", "60"), false)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a command that outlived the deadline returned no error")
	}
	if !strings.Contains(err.Error(), "did not respond") {
		t.Errorf("error = %v, want it to name the timeout", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("waited %v — the deadline did not interrupt the command", elapsed)
	}
}

// The capturing path is used by crontab -l and must be bounded too.
func TestRunCrontabKillsAHungCapture(t *testing.T) {
	withCrontabTimeout(t, 100*time.Millisecond)

	start := time.Now()
	if _, err := runCrontab(exec.Command("sleep", "60"), true); err == nil {
		t.Fatal("a capturing command that outlived the deadline returned no error")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("waited %v", elapsed)
	}
}

// A command that finishes in time must behave exactly as before: output
// returned, exit status surfaced.
func TestRunCrontabPassesThroughAFastCommand(t *testing.T) {
	withCrontabTimeout(t, 10*time.Second)

	out, err := runCrontab(exec.Command("echo", "hello"), true)
	if err != nil {
		t.Fatalf("a fast command failed: %v", err)
	}
	if strings.TrimSpace(string(out)) != "hello" {
		t.Errorf("output = %q, want hello", out)
	}

	if _, err := runCrontab(exec.Command("false"), false); err == nil {
		t.Error("a non-zero exit was reported as success")
	}
}

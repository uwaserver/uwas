package services

import (
	"errors"
	"os/exec"
	"testing"
	"time"
)

// F57001: start/stop/restart ran systemctl with no deadline, so a wedged
// systemd pinned the admin request, a goroutine and a systemctl process.
func TestServiceActionsBoundedWhenSystemctlHangs(t *testing.T) {
	oldExec, oldTimeout := execCommandFn, actionTimeout
	t.Cleanup(func() { execCommandFn, actionTimeout = oldExec, oldTimeout })
	actionTimeout = 150 * time.Millisecond
	execCommandFn = func(string, ...string) *exec.Cmd { return exec.Command("sleep", "30") }

	for name, fn := range map[string]func(string) error{
		"start": StartService, "stop": StopService, "restart": RestartService,
	} {
		done := make(chan error, 1)
		go func() { done <- fn("cron") }()
		select {
		case err := <-done:
			if !errors.Is(err, errActionTimeout) {
				t.Errorf("%s: err = %v, want errActionTimeout", name, err)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("%s did not return with a hung systemctl", name)
		}
	}
}

// Control: a fast successful command and a failing one keep their results.
func TestServiceActionsKeepResults(t *testing.T) {
	oldExec := execCommandFn
	t.Cleanup(func() { execCommandFn = oldExec })
	execCommandFn = func(string, ...string) *exec.Cmd { return exec.Command("true") }
	if err := RestartService("cron"); err != nil {
		t.Errorf("restart = %v, want nil", err)
	}
	if err := StartService("cron"); err != nil {
		t.Errorf("start = %v, want nil", err)
	}
	execCommandFn = func(string, ...string) *exec.Cmd { return exec.Command("false") }
	var ee *exec.ExitError
	if err := StopService("cron"); !errors.As(err, &ee) {
		t.Errorf("stop err = %v, want ExitError", err)
	}
}

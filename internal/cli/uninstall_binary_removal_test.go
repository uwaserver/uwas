package cli

// F49603: `uwas uninstall` run through the installed binary (the normal case:
// /usr/bin/uwas -> /usr/local/bin/uwas) never removed /usr/local/bin/uwas. It
// printed "Binary will be removed on next reboot" but nothing was scheduled, so
// the binary stayed forever. Unlinking a running executable is fine on Linux
// (the inode lives until the process exits). Failed removals were also reported
// as "Removed ...".

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func runUninstall(t *testing.T, self string, removeErr func(string) error) (removed []string, out string, err error) {
	removed, _, out, err = runUninstallAll(t, self, removeErr)
	return
}

func runUninstallAll(t *testing.T, self string, removeErr func(string) error) (removed, removedAll []string, out string, err error) {
	t.Helper()
	origGOOS, origGetuid, origExec, origRemove, origRemoveAll, origCmd := installRuntimeGOOS, installOsGetuid, installOsExecutable, installOsRemove, installOsRemoveAll, installExecCommand
	t.Cleanup(func() {
		installRuntimeGOOS, installOsGetuid, installOsExecutable, installOsRemove, installOsRemoveAll, installExecCommand = origGOOS, origGetuid, origExec, origRemove, origRemoveAll, origCmd
	})
	installRuntimeGOOS = "linux"
	installOsGetuid = func() int { return 0 }
	installOsExecutable = func() (string, error) { return self, nil }
	installOsRemove = func(name string) error {
		removed = append(removed, name)
		if removeErr != nil {
			return removeErr(name)
		}
		return nil
	}
	installOsRemoveAll = func(name string) error {
		removedAll = append(removedAll, name)
		return nil
	}
	installExecCommand = func(string, ...string) *exec.Cmd { return exec.Command("true") }
	out = captureStdout(t, func() {
		feedStdin(t, "y\n", func() { err = (&UninstallCmd{}).Run(nil) })
	})
	return
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func TestUninstallRemovesTheRunningBinary(t *testing.T) {
	removed, out, err := runUninstall(t, "/usr/local/bin/uwas", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("EXPECTED: /usr/local/bin/uwas removed\nACTUAL: removed=%v", removed)
	if !contains(removed, "/usr/local/bin/uwas") {
		t.Fatalf("PROBLEM CONFIRMED: the running binary was never removed; output:\n%s", out)
	}
	if strings.Contains(out, "next reboot") {
		t.Errorf("output still promises a deferred removal nothing schedules:\n%s", out)
	}
}

// Control: when the CLI runs from another path the binary was already removed.
func TestUninstallRemovesInstalledBinaryFromElsewhere(t *testing.T) {
	removed, _, err := runUninstall(t, "/tmp/downloaded/uwas", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(removed, "/usr/local/bin/uwas") {
		t.Fatalf("control failed: %v", removed)
	}
}

func TestUninstallDoesNotClaimFailedRemovals(t *testing.T) {
	_, out, err := runUninstall(t, "/tmp/downloaded/uwas", func(name string) error {
		if name == "/usr/local/bin/uwas" {
			return errors.New("permission denied")
		}
		if name == "/etc/systemd/system/uwas.service" {
			return os.ErrNotExist
		}
		return nil
	})
	if err == nil {
		t.Fatalf("a binary that could not be removed must be an error; output:\n%s", out)
	}
	if strings.Contains(out, "Removed /usr/local/bin/uwas") {
		t.Errorf("claims it removed the binary it could not:\n%s", out)
	}
}

// The resilience drop-in written by scripts/install.sh must go with the unit.
func TestUninstallRemovesTheResilienceDropIn(t *testing.T) {
	_, removedAll, _, err := runUninstallAll(t, "/usr/local/bin/uwas", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(removedAll, "/etc/systemd/system/uwas.service.d") {
		t.Fatalf("drop-in directory left behind: %v", removedAll)
	}
}

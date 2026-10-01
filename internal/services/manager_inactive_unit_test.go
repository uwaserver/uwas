package services

import (
	"fmt"
	"os/exec"
	"testing"
)

// systemctlExitCode builds a systemctl stand-in that prints out and exits with
// the given code. Real `systemctl is-active` exits 0 when the unit is active,
// 3 when the unit is loaded but not running (inactive/failed/activating), and
// 4 when the unit is not known at all — so a non-zero exit is NOT by itself
// evidence that a service is absent.
func systemctlExitCode(out string, code int) func(string, ...string) *exec.Cmd {
	return func(_ string, args ...string) *exec.Cmd {
		if len(args) > 0 && args[0] == "is-active" {
			return exec.Command("sh", "-c", fmt.Sprintf("printf '%%s\\n' %q; exit %d", out, code))
		}
		return exec.Command("sh", "-c", "printf 'disabled\n'; exit 0")
	}
}

// A service that is installed but stopped must be reported as down, not
// dropped from the list. Omitting it made the dashboard render a stopped
// service as though it were not installed at all, so an operator could not
// tell a crash from a missing package.
func TestCheckServiceReportsInstalledButInactiveUnit(t *testing.T) {
	orig := execCommandFn
	defer func() { execCommandFn = orig }()
	execCommandFn = systemctlExitCode("inactive", 3)

	svc := checkService("redis-server", "Redis")
	if svc == nil {
		t.Fatal("checkService returned nil for an installed but inactive unit (systemctl is-active exits 3); " +
			"a stopped service must be reported as down, not omitted from ListServices")
	}
	if svc.Active != "inactive" {
		t.Errorf("Active = %q, want %q", svc.Active, "inactive")
	}
	if svc.Running {
		t.Error("Running = true, want false for an inactive unit")
	}
	if svc.Name != "redis-server" || svc.Display != "Redis" {
		t.Errorf("identity = %q/%q, want redis-server/Redis", svc.Name, svc.Display)
	}
}

// A crashed unit carries state "failed" and also exits 3. It must be reported
// with that state so the failure is distinguishable from a clean stop.
func TestCheckServiceReportsFailedUnit(t *testing.T) {
	orig := execCommandFn
	defer func() { execCommandFn = orig }()
	execCommandFn = systemctlExitCode("failed", 3)

	svc := checkService("memcached", "Memcached")
	if svc == nil {
		t.Fatal("checkService returned nil for a unit in state \"failed\" (is-active exits 3); " +
			"a crashed service must be reported, not omitted")
	}
	if svc.Active != "failed" {
		t.Errorf("Active = %q, want %q", svc.Active, "failed")
	}
	if svc.Running {
		t.Error("Running = true, want false for a failed unit")
	}
}

// Boundary: a unit that is genuinely not installed (exit 4) must still be
// omitted, so ListServices' alias fallback keeps probing the aliases.
func TestCheckServiceOmitsUnknownUnit(t *testing.T) {
	orig := execCommandFn
	defer func() { execCommandFn = orig }()
	execCommandFn = systemctlExitCode("inactive", 4)

	if svc := checkService("does-not-exist", "Ghost"); svc != nil {
		t.Errorf("unknown unit (exit 4) reported as %+v, want nil so aliases are still tried", svc)
	}
}

// Boundary: the enabled probe is independent of the active probe, and a
// stopped-but-enabled unit must still report Enabled=true.
func TestCheckServiceReportsEnabledForInactiveUnit(t *testing.T) {
	orig := execCommandFn
	defer func() { execCommandFn = orig }()
	execCommandFn = func(_ string, args ...string) *exec.Cmd {
		if len(args) > 0 && args[0] == "is-active" {
			return exec.Command("sh", "-c", "printf 'inactive\\n'; exit 3")
		}
		return exec.Command("sh", "-c", "printf 'enabled\\n'; exit 0")
	}

	svc := checkService("nginx", "Nginx")
	if svc == nil {
		t.Fatal("checkService returned nil for a stopped-but-enabled unit")
	}
	if !svc.Enabled {
		t.Error("Enabled = false, want true — the is-enabled probe is independent of is-active")
	}
	if svc.Running {
		t.Error("Running = true, want false")
	}
}

// Control: an active unit is reported exactly as before.
func TestCheckServiceReportsActiveUnitUnchanged(t *testing.T) {
	orig := execCommandFn
	defer func() { execCommandFn = orig }()
	execCommandFn = systemctlExitCode("active", 0)

	svc := checkService("nginx", "Nginx")
	if svc == nil {
		t.Fatal("checkService returned nil for an active unit")
	}
	if svc.Active != "active" || !svc.Running {
		t.Errorf("Active=%q Running=%v, want \"active\"/true", svc.Active, svc.Running)
	}
}

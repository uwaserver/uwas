package phpmanager

import (
	"os/exec"
	"testing"

	"github.com/uwaserver/uwas/internal/rlimit"
)

// domain.resources was dead configuration: internal/rlimit implemented cgroup
// v2 limits in full and no package ever imported it, so cpu_percent /
// memory_mb / pid_max were defaulted, merged, echoed back by the admin API
// and never enforced.

func limitsManager(t *testing.T) *Manager {
	t.Helper()

	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4", SAPI: "cgi-fcgi"},
	}
	m.execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("sleep", "100")
	}
	return m
}

func TestSetDomainLimitsRecordsAndReports(t *testing.T) {
	m := limitsManager(t)
	if _, err := m.AssignDomain("limits.test", "8.4"); err != nil {
		t.Fatalf("AssignDomain: %v", err)
	}

	want := rlimit.Limits{CPUPercent: 50, MemoryMB: 256, PIDMax: 100}
	if !m.SetDomainLimits("limits.test", want) {
		t.Fatal("SetDomainLimits returned false for an assigned domain")
	}

	got, ok := m.DomainLimits("limits.test")
	if !ok {
		t.Fatal("DomainLimits returned false for an assigned domain")
	}
	if got != want {
		t.Errorf("limits = %+v, want %+v", got, want)
	}
}

func TestSetDomainLimitsUnknownDomain(t *testing.T) {
	m := limitsManager(t)
	if m.SetDomainLimits("yok.test", rlimit.Limits{MemoryMB: 64}) {
		t.Error("returned true for an unassigned domain")
	}
	if _, ok := m.DomainLimits("yok.test"); ok {
		t.Error("limits reported for an unassigned domain")
	}
}

// StartDomain must ask rlimit to build the cgroup with the recorded limits,
// and must move the worker it just started into it.
func TestStartDomainAppliesRecordedLimits(t *testing.T) {
	defer rlimitTestHooks()()

	var (
		appliedDomain string
		appliedLimits rlimit.Limits
		assignedPath  string
		assignedPID   int
	)
	rlimitApply = func(domain string, l rlimit.Limits) (string, error) {
		appliedDomain, appliedLimits = domain, l
		return "/sys/fs/cgroup/uwas/" + domain, nil
	}
	rlimitAssignPID = func(path string, pid int) error {
		assignedPath, assignedPID = path, pid
		return nil
	}

	m := limitsManager(t)
	if _, err := m.AssignDomain("limits.test", "8.4"); err != nil {
		t.Fatalf("AssignDomain: %v", err)
	}
	want := rlimit.Limits{CPUPercent: 25, MemoryMB: 512, PIDMax: 64}
	m.SetDomainLimits("limits.test", want)

	if err := m.StartDomain("limits.test"); err != nil {
		t.Fatalf("StartDomain: %v", err)
	}
	t.Cleanup(func() { _ = m.StopDomain("limits.test") })

	if appliedDomain != "limits.test" || appliedLimits != want {
		t.Errorf("Apply(%q, %+v), want (%q, %+v)", appliedDomain, appliedLimits, "limits.test", want)
	}
	if assignedPath != "/sys/fs/cgroup/uwas/limits.test" {
		t.Errorf("AssignPID yolu = %q", assignedPath)
	}
	if assignedPID == 0 {
		t.Error("AssignPID was not called — the worker runs outside the cgroup")
	}
}

// A host without cgroups must still serve PHP.
func TestStartDomainSurvivesLimitFailure(t *testing.T) {
	defer rlimitTestHooks()()
	rlimitApply = func(string, rlimit.Limits) (string, error) {
		return "", errCgroupUnavailable
	}

	m := limitsManager(t)
	if _, err := m.AssignDomain("limits.test", "8.4"); err != nil {
		t.Fatalf("AssignDomain: %v", err)
	}
	m.SetDomainLimits("limits.test", rlimit.Limits{MemoryMB: 128})

	if err := m.StartDomain("limits.test"); err != nil {
		t.Fatalf("a cgroup failure took the domain down: %v", err)
	}
	t.Cleanup(func() { _ = m.StopDomain("limits.test") })

	if m.RunningAddrForDomain("limits.test") == "" {
		t.Error("PHP did not start when the cgroup could not be applied")
	}
}

// No limits configured: nothing should be created.
func TestStartDomainWithoutLimitsCreatesNoCgroup(t *testing.T) {
	defer rlimitTestHooks()()

	called := false
	rlimitApply = func(domain string, l rlimit.Limits) (string, error) {
		called = true
		if l != (rlimit.Limits{}) {
			t.Errorf("want empty limits, got %+v", l)
		}
		return "", nil // rlimit.Apply returns "" for empty limits
	}
	rlimitAssignPID = func(string, int) error {
		t.Error("AssignPID was called with no cgroup")
		return nil
	}

	m := limitsManager(t)
	if _, err := m.AssignDomain("plain.test", "8.4"); err != nil {
		t.Fatalf("AssignDomain: %v", err)
	}
	if err := m.StartDomain("plain.test"); err != nil {
		t.Fatalf("StartDomain: %v", err)
	}
	t.Cleanup(func() { _ = m.StopDomain("plain.test") })

	if !called {
		t.Error("Apply was never called")
	}
}

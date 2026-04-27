package services

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"testing"
)

// fakeExecCommand returns an *exec.Cmd that re-invokes the test binary
// with TestHelperProcess as the entry point, passing the original command
// and args after a "--" sentinel so the helper can inspect them.
func fakeExecCommand(command string, args ...string) *exec.Cmd {
	cs := []string{"-test.run=TestHelperProcess", "--", command}
	cs = append(cs, args...)
	cmd := exec.Command(os.Args[0], cs...)
	cmd.Env = []string{"GO_WANT_HELPER_PROCESS=1"}
	return cmd
}

// fakeExecCommandFail is like fakeExecCommand but tells the helper to exit 1.
func fakeExecCommandFail(command string, args ...string) *exec.Cmd {
	cs := []string{"-test.run=TestHelperProcess", "--", command}
	cs = append(cs, args...)
	cmd := exec.Command(os.Args[0], cs...)
	cmd.Env = []string{"GO_WANT_HELPER_PROCESS=1", "GO_HELPER_FAIL=1"}
	return cmd
}

// fakeExecCommandInactive tells the helper to report "inactive" for is-active.
func fakeExecCommandInactive(command string, args ...string) *exec.Cmd {
	cs := []string{"-test.run=TestHelperProcess", "--", command}
	cs = append(cs, args...)
	cmd := exec.Command(os.Args[0], cs...)
	cmd.Env = []string{"GO_WANT_HELPER_PROCESS=1", "GO_HELPER_INACTIVE=1"}
	return cmd
}

// fakeExecCommandDisabled tells the helper to report "active" for is-active
// but "disabled" for is-enabled.
func fakeExecCommandDisabled(command string, args ...string) *exec.Cmd {
	cs := []string{"-test.run=TestHelperProcess", "--", command}
	cs = append(cs, args...)
	cmd := exec.Command(os.Args[0], cs...)
	cmd.Env = []string{"GO_WANT_HELPER_PROCESS=1", "GO_HELPER_DISABLED=1"}
	return cmd
}

// fakeExecCommandAlias tells the helper to fail for primary service names
// but succeed for aliases (simulates "primary not found, alias found").
func fakeExecCommandAlias(command string, args ...string) *exec.Cmd {
	cs := []string{"-test.run=TestHelperProcess", "--", command}
	cs = append(cs, args...)
	cmd := exec.Command(os.Args[0], cs...)
	cmd.Env = []string{"GO_WANT_HELPER_PROCESS=1", "GO_HELPER_ALIAS_ONLY=1"}
	return cmd
}

// TestHelperProcess is invoked by the fake exec commands. It inspects
// environment variables and command-line arguments to decide what to print
// and which exit code to use. It is NOT a real test.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	// Parse arguments after "--"
	args := os.Args
	for i, arg := range args {
		if arg == "--" {
			args = args[i+1:]
			break
		}
	}

	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "helper: not enough args")
		os.Exit(1)
	}

	command := args[0] // "systemctl"
	subcmd := args[1]  // "is-active", "start", etc.
	svcName := ""
	if len(args) > 2 {
		svcName = args[2]
	}

	// Mode: always fail
	if os.Getenv("GO_HELPER_FAIL") == "1" {
		fmt.Fprint(os.Stderr, "error")
		os.Exit(1)
	}

	// Mode: inactive service
	if os.Getenv("GO_HELPER_INACTIVE") == "1" {
		if command == "systemctl" && subcmd == "is-active" {
			fmt.Fprint(os.Stdout, "inactive\n")
			os.Exit(0)
		}
		if command == "systemctl" && subcmd == "is-enabled" {
			fmt.Fprint(os.Stdout, "disabled\n")
			os.Exit(0)
		}
		os.Exit(0)
	}

	// Mode: disabled service (active but not enabled)
	if os.Getenv("GO_HELPER_DISABLED") == "1" {
		if command == "systemctl" && subcmd == "is-active" {
			fmt.Fprint(os.Stdout, "active\n")
			os.Exit(0)
		}
		if command == "systemctl" && subcmd == "is-enabled" {
			fmt.Fprint(os.Stdout, "disabled\n")
			os.Exit(0)
		}
		os.Exit(0)
	}

	// Mode: alias-only -- primary service names fail, aliases succeed.
	// We define "primary" names as those that appear as KnownServices[].Name.
	if os.Getenv("GO_HELPER_ALIAS_ONLY") == "1" {
		primaryNames := map[string]bool{
			"mariadb":      true,
			"ssh":          true,
			"php8.3-fpm":   true,
			"cron":         true,
			"ufw":          true,
			"fail2ban":     true,
			"postfix":      true,
			"dovecot":      true,
			"redis-server": true,
			"memcached":    true,
		}
		if command == "systemctl" && (subcmd == "is-active") {
			if primaryNames[svcName] {
				fmt.Fprint(os.Stderr, "inactive")
				os.Exit(1) // simulate "not found"
			}
			// Alias name: succeed
			fmt.Fprint(os.Stdout, "active\n")
			os.Exit(0)
		}
		if command == "systemctl" && subcmd == "is-enabled" {
			if primaryNames[svcName] {
				fmt.Fprint(os.Stderr, "disabled")
				os.Exit(1)
			}
			fmt.Fprint(os.Stdout, "enabled\n")
			os.Exit(0)
		}
		os.Exit(0)
	}

	// Default mode: everything succeeds
	if command == "systemctl" {
		switch subcmd {
		case "is-active":
			fmt.Fprint(os.Stdout, "active\n")
			os.Exit(0)
		case "is-enabled":
			fmt.Fprint(os.Stdout, "enabled\n")
			os.Exit(0)
		case "start", "stop", "restart", "enable", "disable":
			os.Exit(0)
		}
	}

	os.Exit(0)
}

// withMock swaps execCommandFn and restores it via t.Cleanup.
func withMock(t *testing.T, fn func(string, ...string) *exec.Cmd) {
	t.Helper()
	orig := execCommandFn
	execCommandFn = fn
	t.Cleanup(func() { execCommandFn = orig })
}

// withGOOS overrides runtimeGOOS for the duration of the test.
func withGOOS(t *testing.T, goos string) {
	t.Helper()
	orig := runtimeGOOS
	runtimeGOOS = goos
	t.Cleanup(func() { runtimeGOOS = orig })
}

// ── Existing tests (preserved) ──────────────────────────────────────────

func TestListServices(t *testing.T) {
	result := ListServices()

	if runtime.GOOS == "windows" {
		if result != nil {
			t.Errorf("expected nil on Windows, got %v", result)
		}
		return
	}

	t.Logf("ListServices returned %d services", len(result))
	for _, svc := range result {
		if svc.Name == "" {
			t.Error("service Name should not be empty")
		}
		if svc.Display == "" {
			t.Error("service Display should not be empty")
		}
		validStates := map[string]bool{"active": true, "inactive": true, "failed": true, "activating": true, "deactivating": true}
		if !validStates[svc.Active] {
			t.Logf("unexpected Active state %q for service %q (may be platform-specific)", svc.Active, svc.Name)
		}
	}
}

func TestServiceStruct(t *testing.T) {
	svc := Service{
		Name:    "nginx",
		Display: "Nginx Web Server",
		Running: true,
		Enabled: true,
		Active:  "active",
	}

	if svc.Name != "nginx" {
		t.Errorf("expected Name 'nginx', got %q", svc.Name)
	}
	if svc.Display != "Nginx Web Server" {
		t.Errorf("expected Display 'Nginx Web Server', got %q", svc.Display)
	}
	if !svc.Running {
		t.Error("expected Running=true")
	}
	if !svc.Enabled {
		t.Error("expected Enabled=true")
	}
	if svc.Active != "active" {
		t.Errorf("expected Active 'active', got %q", svc.Active)
	}
}

func TestKnownServicesNotEmpty(t *testing.T) {
	if len(KnownServices) == 0 {
		t.Error("KnownServices should not be empty")
	}

	for _, ks := range KnownServices {
		if ks.Name == "" {
			t.Error("KnownServices entry has empty Name")
		}
		if ks.Display == "" {
			t.Error("KnownServices entry has empty Display")
		}
	}
}

// ── checkService tests ──────────────────────────────────────────────────

func TestCheckService_Active(t *testing.T) {
	withMock(t, fakeExecCommand)

	svc := checkService("nginx", "Nginx")
	if svc == nil {
		t.Fatal("expected non-nil service")
	}
	if svc.Name != "nginx" {
		t.Errorf("expected Name 'nginx', got %q", svc.Name)
	}
	if svc.Display != "Nginx" {
		t.Errorf("expected Display 'Nginx', got %q", svc.Display)
	}
	if !svc.Running {
		t.Error("expected Running=true")
	}
	if !svc.Enabled {
		t.Error("expected Enabled=true")
	}
	if svc.Active != "active" {
		t.Errorf("expected Active 'active', got %q", svc.Active)
	}
}

func TestCheckService_Inactive(t *testing.T) {
	withMock(t, fakeExecCommandInactive)

	svc := checkService("nginx", "Nginx")
	if svc == nil {
		t.Fatal("expected non-nil service")
	}
	if svc.Running {
		t.Error("expected Running=false for inactive service")
	}
	if svc.Active != "inactive" {
		t.Errorf("expected Active 'inactive', got %q", svc.Active)
	}
	if svc.Enabled {
		t.Error("expected Enabled=false for disabled service")
	}
}

func TestCheckService_NotFound(t *testing.T) {
	withMock(t, fakeExecCommandFail)

	svc := checkService("nonexistent", "Nonexistent")
	if svc != nil {
		t.Errorf("expected nil for failed is-active, got %+v", svc)
	}
}

func TestCheckService_EnabledFalse(t *testing.T) {
	withMock(t, fakeExecCommandDisabled)

	svc := checkService("nginx", "Nginx")
	if svc == nil {
		t.Fatal("expected non-nil service")
	}
	if !svc.Running {
		t.Error("expected Running=true for active service")
	}
	if svc.Enabled {
		t.Error("expected Enabled=false for disabled service")
	}
	if svc.Active != "active" {
		t.Errorf("expected Active 'active', got %q", svc.Active)
	}
}

// ── ListServices with mocks ─────────────────────────────────────────────

func TestListServices_WithMocks(t *testing.T) {
	withGOOS(t, "linux")
	withMock(t, fakeExecCommand)

	svcs := ListServices()
	if len(svcs) == 0 {
		t.Fatal("expected at least one service from mocked ListServices")
	}
	if len(svcs) != len(KnownServices) {
		t.Errorf("expected %d services (all succeed), got %d", len(KnownServices), len(svcs))
	}
	for _, svc := range svcs {
		if !svc.Running {
			t.Errorf("service %q should be Running", svc.Name)
		}
		if !svc.Enabled {
			t.Errorf("service %q should be Enabled", svc.Name)
		}
	}
}

func TestListServices_AliasFallback(t *testing.T) {
	withGOOS(t, "linux")
	withMock(t, fakeExecCommandAlias)

	svcs := ListServices()
	// Services with aliases should be found (mariadb->mysql, ssh->sshd, etc.)
	// Services without aliases (ufw, fail2ban, etc.) should NOT be found.
	if len(svcs) == 0 {
		t.Fatal("expected at least some services via aliases")
	}
	for _, svc := range svcs {
		if svc.Name == "" {
			t.Error("returned service has empty Name")
		}
	}
}

func TestListServices_AllFail(t *testing.T) {
	withGOOS(t, "linux")
	withMock(t, fakeExecCommandFail)

	svcs := ListServices()
	if len(svcs) != 0 {
		t.Errorf("expected 0 services when all fail, got %d", len(svcs))
	}
}

func TestListServices_WindowsReturnsNil(t *testing.T) {
	withGOOS(t, "windows")

	svcs := ListServices()
	if svcs != nil {
		t.Errorf("expected nil on Windows, got %v", svcs)
	}
}

// ── StartService ─────────────────────────────────────────────────────────

func TestStartService(t *testing.T) {
	withMock(t, fakeExecCommand)

	err := StartService("mariadb")
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestStartService_UnknownService(t *testing.T) {
	withMock(t, fakeExecCommand)
	if err := StartService("evil-service"); err == nil {
		t.Error("expected error for unknown service")
	}
}

func TestIsKnownServiceAlias(t *testing.T) {
	if !IsKnownService("sshd") {
		t.Fatal("expected sshd alias to be known")
	}
	if IsKnownService("evil-service") {
		t.Fatal("expected unknown service to be rejected")
	}
}

func TestStartService_Failure(t *testing.T) {
	withMock(t, fakeExecCommandFail)

	err := StartService("mariadb")
	if err == nil {
		t.Error("expected error from failing start command")
	}
}

// ── StopService ──────────────────────────────────────────────────────────

func TestStopService(t *testing.T) {
	withMock(t, fakeExecCommand)

	err := StopService("mariadb")
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestStopService_Failure(t *testing.T) {
	withMock(t, fakeExecCommandFail)

	err := StopService("mariadb")
	if err == nil {
		t.Error("expected error from failing stop command")
	}
}

func TestStopService_UnknownService(t *testing.T) {
	withMock(t, fakeExecCommand)
	if err := StopService("evil-service"); err == nil {
		t.Error("expected error for unknown service")
	}
}

// ── RestartService ───────────────────────────────────────────────────────

func TestRestartService(t *testing.T) {
	withMock(t, fakeExecCommand)

	err := RestartService("mariadb")
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestRestartService_Failure(t *testing.T) {
	withMock(t, fakeExecCommandFail)

	err := RestartService("mariadb")
	if err == nil {
		t.Error("expected error from failing restart command")
	}
}

func TestRestartService_UnknownService(t *testing.T) {
	withMock(t, fakeExecCommand)
	if err := RestartService("evil-service"); err == nil {
		t.Error("expected error for unknown service")
	}
}

// ── EnableService ────────────────────────────────────────────────────────

func TestEnableService(t *testing.T) {
	withMock(t, fakeExecCommand)

	err := EnableService("mariadb")
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestEnableService_Failure(t *testing.T) {
	withMock(t, fakeExecCommandFail)

	err := EnableService("mariadb")
	if err == nil {
		t.Error("expected error from failing enable command")
	}
}

func TestEnableService_UnknownService(t *testing.T) {
	withMock(t, fakeExecCommand)
	if err := EnableService("evil-service"); err == nil {
		t.Error("expected error for unknown service")
	}
}

// ── DisableService ───────────────────────────────────────────────────────

func TestDisableService(t *testing.T) {
	withMock(t, fakeExecCommand)

	err := DisableService("mariadb")
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestDisableService_Failure(t *testing.T) {
	withMock(t, fakeExecCommandFail)

	err := DisableService("mariadb")
	if err == nil {
		t.Error("expected error from failing disable command")
	}
}

func TestDisableService_UnknownService(t *testing.T) {
	withMock(t, fakeExecCommand)
	if err := DisableService("evil-service"); err == nil {
		t.Error("expected error for unknown service")
	}
}

// ── StartService enable-phase failure ────────────────────────────────────

func TestStartService_EnableFailure(t *testing.T) {
	// Start succeeds but enable fails. We need a mock that succeeds for "start"
	// but fails for "enable".
	callCount := 0
	mock := func(command string, args ...string) *exec.Cmd {
		callCount++
		if callCount == 1 {
			// First call: start -- succeed
			return fakeExecCommand(command, args...)
		}
		// Second call: enable -- fail
		return fakeExecCommandFail(command, args...)
	}
	withMock(t, mock)

	err := StartService("mariadb")
	if err == nil {
		t.Error("expected error when enable phase fails")
	}
}

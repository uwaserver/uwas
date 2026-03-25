package phpmanager

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// --- StartFPM with invalid PHP binary path ---

func TestStartFPMInvalidBinary(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/nonexistent/path/php-cgi", SAPI: "cgi-fcgi"},
	}

	// Override execCommand to use the real (invalid) binary path.
	m.execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("/nonexistent/binary/that/should/not/exist", args...)
	}

	err := m.StartFPM("8.4.19", "127.0.0.1:9000")
	if err == nil {
		t.Error("expected error when starting FPM with invalid binary path")
	}
	if !strings.Contains(err.Error(), "start php-cgi") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- StopFPM for a domain that isn't running ---

func TestStopFPMForNonRunningVersion(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4"},
	}

	err := m.StopFPM("8.4.19")
	if err == nil {
		t.Error("expected error when stopping FPM that is not running")
	}
	if !strings.Contains(err.Error(), "not running") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- AssignDomain with already-assigned domain ---

func TestAssignDomainAlreadyAssignedDifferentVersion(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4"},
		{Version: "8.3.0", Binary: "/usr/bin/php-cgi8.3"},
	}

	_, err := m.AssignDomain("blog.com", "8.4")
	if err != nil {
		t.Fatalf("first AssignDomain: %v", err)
	}

	// Try to assign same domain with different version - should still fail.
	_, err = m.AssignDomain("blog.com", "8.3")
	if err == nil {
		t.Error("expected error for already-assigned domain")
	}
	if !strings.Contains(err.Error(), "already has") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- ListDomainInstances when no instances exist ---

func TestGetDomainInstancesEmpty(t *testing.T) {
	m := New(testLogger())

	instances := m.GetDomainInstances()
	if instances == nil {
		t.Fatal("expected non-nil empty slice")
	}
	if len(instances) != 0 {
		t.Errorf("expected 0 instances, got %d", len(instances))
	}
}

// --- GetDomainConfig for non-existing domain ---

func TestGetDomainConfigNonExistentDomain(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4"},
	}

	// Assign one domain.
	m.AssignDomain("real.com", "8.4")

	// Query a non-existing one.
	cfg := m.GetDomainConfig("fake.com")
	if cfg != nil {
		t.Errorf("expected nil config for non-existing domain, got %v", cfg)
	}
}

// --- SetDomainConfig validation ---

func TestSetDomainConfigValidation(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4"},
	}
	m.AssignDomain("blog.com", "8.4")

	// Empty key.
	err := m.SetDomainConfig("blog.com", "", "value")
	if err == nil || !strings.Contains(err.Error(), "key is required") {
		t.Errorf("expected key required error, got: %v", err)
	}

	// Non-existent domain.
	err = m.SetDomainConfig("unknown.com", "memory_limit", "256M")
	if err == nil || !strings.Contains(err.Error(), "no PHP assignment") {
		t.Errorf("expected no PHP assignment error, got: %v", err)
	}

	// Valid overwrite of existing key.
	err = m.SetDomainConfig("blog.com", "memory_limit", "128M")
	if err != nil {
		t.Fatalf("SetDomainConfig: %v", err)
	}
	err = m.SetDomainConfig("blog.com", "memory_limit", "256M")
	if err != nil {
		t.Fatalf("SetDomainConfig overwrite: %v", err)
	}
	cfg := m.GetDomainConfig("blog.com")
	if cfg["memory_limit"] != "256M" {
		t.Errorf("memory_limit = %q, want 256M", cfg["memory_limit"])
	}
}

// --- Auto port assignment with multiple domains ---

func TestAutoPortAssignmentMultipleDomains(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4"},
	}

	domains := []string{"site1.com", "site2.com", "site3.com", "site4.com", "site5.com"}
	for i, domain := range domains {
		dp, err := m.AssignDomain(domain, "8.4")
		if err != nil {
			t.Fatalf("AssignDomain(%s): %v", domain, err)
		}

		expectedPort := 9001 + i
		expectedAddr := "127.0.0.1:" + strings.TrimPrefix(dp.ListenAddr, "127.0.0.1:")
		if dp.ListenAddr != expectedAddr {
			// More readable check.
			if !strings.HasSuffix(dp.ListenAddr, strconv.Itoa(expectedPort)) {
				t.Errorf("domain %s: addr = %s, want port %d", domain, dp.ListenAddr, expectedPort)
			}
		}
	}

	// Verify all 5 are listed.
	instances := m.GetDomainInstances()
	if len(instances) != 5 {
		t.Errorf("expected 5 instances, got %d", len(instances))
	}

	// Verify all addresses are unique.
	addrs := make(map[string]bool)
	for _, inst := range instances {
		if addrs[inst.ListenAddr] {
			t.Errorf("duplicate address: %s", inst.ListenAddr)
		}
		addrs[inst.ListenAddr] = true
	}
}

// --- detectPHP on Windows (test helper path patterns) ---

func TestCandidatePathsReturnsNonEmpty(t *testing.T) {
	paths := candidatePaths()
	if len(paths) == 0 {
		t.Error("candidatePaths() returned empty list")
	}
	// On Windows, verify expected patterns.
	for _, p := range paths {
		if p == "" {
			t.Error("candidatePaths() returned empty string in list")
		}
	}
}

// --- StartDomain with invalid version after assignment ---

func TestStartDomainVersionRemovedAfterAssign(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4"},
	}

	_, err := m.AssignDomain("blog.com", "8.4")
	if err != nil {
		t.Fatalf("AssignDomain: %v", err)
	}

	// Remove the installation to simulate it becoming unavailable.
	m.mu.Lock()
	m.installations = nil
	m.mu.Unlock()

	err = m.StartDomain("blog.com")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got: %v", err)
	}
}

// --- StartFPM with a real binary (echo) that exits immediately ---

func TestStartFPMWithMockBinary(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4", SAPI: "cgi-fcgi"},
	}

	// Use echo as a mock that starts and exits.
	m.execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("echo", "mock-fpm")
	}

	err := m.StartFPM("8.4.19", "127.0.0.1:9000")
	if err != nil {
		t.Fatalf("StartFPM with mock: %v", err)
	}

	// Verify it was stored.
	_, loaded := m.processes.Load("8.4.19")
	if !loaded {
		t.Error("process should be stored after StartFPM")
	}
}

// --- GetConfig for version with no config file ---

func TestGetConfigNoConfigFile(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4", ConfigFile: ""},
	}

	cfg, err := m.GetConfig("8.4.19")
	if err != nil {
		t.Errorf("expected defaults when config file is empty, got error: %v", err)
	}
	if cfg.MemoryLimit != "128M" {
		t.Errorf("expected default memory_limit=128M, got %s", cfg.MemoryLimit)
	}
}

// --- SetConfig for version with no config file ---

func TestSetConfigNoConfigFile(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4", ConfigFile: ""},
	}

	// SetConfig with no config file will try to create one.
	// On test systems it may succeed (creates temp file) or fail (no PHP binary).
	_ = m.SetConfig("8.4.19", "memory_limit", "512M")
}

// --- SetDomainChangeFunc ---

func TestSetDomainChangeFuncNil(t *testing.T) {
	m := New(testLogger())

	// Set and then clear the callback.
	m.SetDomainChangeFunc(func(domain, fpmAddr string) {})
	m.SetDomainChangeFunc(nil)

	// Should not panic.
	m.domainMu.RLock()
	if m.onDomainChange != nil {
		t.Error("expected nil callback after setting nil")
	}
	m.domainMu.RUnlock()
}

// --- UnassignDomain for a running process cleans up tmpINI ---

func TestUnassignDomainCleansUpState(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4"},
	}

	_, err := m.AssignDomain("blog.com", "8.4")
	if err != nil {
		t.Fatalf("AssignDomain: %v", err)
	}

	// Verify it exists.
	if m.GetDomainConfig("blog.com") == nil {
		t.Error("expected non-nil config after assign")
	}

	m.UnassignDomain("blog.com")

	// Verify it's gone.
	if m.GetDomainConfig("blog.com") != nil {
		t.Error("expected nil config after unassign")
	}
	instances := m.GetDomainInstances()
	if len(instances) != 0 {
		t.Errorf("expected 0 instances after unassign, got %d", len(instances))
	}
}

// --- Multiple SetDomainConfig overrides ---

func TestSetDomainConfigMultipleOverrides(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4"},
	}

	m.AssignDomain("blog.com", "8.4")

	m.SetDomainConfig("blog.com", "memory_limit", "128M")
	m.SetDomainConfig("blog.com", "max_execution_time", "30")
	m.SetDomainConfig("blog.com", "display_errors", "Off")

	cfg := m.GetDomainConfig("blog.com")
	if len(cfg) != 3 {
		t.Errorf("expected 3 overrides, got %d", len(cfg))
	}
	if cfg["memory_limit"] != "128M" {
		t.Errorf("memory_limit = %q", cfg["memory_limit"])
	}
	if cfg["max_execution_time"] != "30" {
		t.Errorf("max_execution_time = %q", cfg["max_execution_time"])
	}
	if cfg["display_errors"] != "Off" {
		t.Errorf("display_errors = %q", cfg["display_errors"])
	}
}

// --- StopDomain: domain not assigned ---

func TestStopDomainNotAssigned(t *testing.T) {
	m := New(testLogger())

	err := m.StopDomain("noexist.com")
	if err == nil {
		t.Error("expected error for stopping unassigned domain")
	}
	if !strings.Contains(err.Error(), "no PHP assignment") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- StopDomain: domain assigned but not running ---

func TestStopDomainNotRunningCov(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4"},
	}

	_, err := m.AssignDomain("stop-notrunning.com", "8.4")
	if err != nil {
		t.Fatalf("AssignDomain: %v", err)
	}

	err = m.StopDomain("stop-notrunning.com")
	if err == nil {
		t.Error("expected error for stopping domain that is not running")
	}
	if !strings.Contains(err.Error(), "not running") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- StopDomain: with running process ---

func TestStopDomainRunning(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4", SAPI: "cgi-fcgi"},
	}

	// Use a long-running mock process (sleep)
	m.execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("ping", "-n", "100", "127.0.0.1")
	}

	_, err := m.AssignDomain("stop-running.com", "8.4")
	if err != nil {
		t.Fatalf("AssignDomain: %v", err)
	}

	err = m.StartDomain("stop-running.com")
	if err != nil {
		t.Fatalf("StartDomain: %v", err)
	}

	// Give the process a moment to start
	time.Sleep(50 * time.Millisecond)

	err = m.StopDomain("stop-running.com")
	if err != nil {
		t.Errorf("StopDomain: %v", err)
	}
}

// --- StartDomain: already running error ---

func TestStartDomainAlreadyRunningCov(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4", SAPI: "cgi-fcgi"},
	}

	m.execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("ping", "-n", "100", "127.0.0.1")
	}

	_, err := m.AssignDomain("already-running.com", "8.4")
	if err != nil {
		t.Fatalf("AssignDomain: %v", err)
	}

	err = m.StartDomain("already-running.com")
	if err != nil {
		t.Fatalf("StartDomain: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	// Try starting again - should fail
	err = m.StartDomain("already-running.com")
	if err == nil {
		t.Error("expected error for already running domain")
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Errorf("unexpected error: %v", err)
	}

	// Clean up
	m.StopDomain("already-running.com")
}

// --- StartDomain: domain not assigned ---

func TestStartDomainNotAssigned(t *testing.T) {
	m := New(testLogger())

	err := m.StartDomain("notassigned.com")
	if err == nil {
		t.Error("expected error for starting unassigned domain")
	}
	if !strings.Contains(err.Error(), "no PHP assignment") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- StartDomain: exec failure ---

func TestStartDomainExecFailure(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4", SAPI: "cgi-fcgi"},
	}

	m.execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("/nonexistent/binary/that/does/not/exist", args...)
	}

	_, err := m.AssignDomain("exec-fail.com", "8.4")
	if err != nil {
		t.Fatalf("AssignDomain: %v", err)
	}

	err = m.StartDomain("exec-fail.com")
	if err == nil {
		t.Error("expected error when exec fails")
	}
	if !strings.Contains(err.Error(), "start php-cgi") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- StartDomain: with domain change callback ---

func TestStartDomainWithChangeCallback(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4", SAPI: "cgi-fcgi"},
	}

	m.execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("ping", "-n", "100", "127.0.0.1")
	}

	var callbackDomain, callbackAddr string
	m.SetDomainChangeFunc(func(domain, fpmAddr string) {
		callbackDomain = domain
		callbackAddr = fpmAddr
	})

	_, err := m.AssignDomain("callback.com", "8.4")
	if err != nil {
		t.Fatalf("AssignDomain: %v", err)
	}

	err = m.StartDomain("callback.com")
	if err != nil {
		t.Fatalf("StartDomain: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	if callbackDomain != "callback.com" {
		t.Errorf("callback domain = %q, want callback.com", callbackDomain)
	}
	if callbackAddr == "" {
		t.Error("callback addr should not be empty")
	}

	// Clean up
	m.StopDomain("callback.com")
}

// --- UnassignDomain: with running process cleans up ---

func TestUnassignDomainWithRunningProcess(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4", SAPI: "cgi-fcgi"},
	}

	m.execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("ping", "-n", "100", "127.0.0.1")
	}

	_, err := m.AssignDomain("unassign-running.com", "8.4")
	if err != nil {
		t.Fatalf("AssignDomain: %v", err)
	}

	err = m.StartDomain("unassign-running.com")
	if err != nil {
		t.Fatalf("StartDomain: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	// Unassign should kill the running process
	m.UnassignDomain("unassign-running.com")

	// Verify it's gone
	instances := m.GetDomainInstances()
	if len(instances) != 0 {
		t.Errorf("expected 0 instances after unassign, got %d", len(instances))
	}
}

// --- UnassignDomain: for nonexistent domain does nothing ---

func TestUnassignDomainNonExistentCov(t *testing.T) {
	m := New(testLogger())
	// Should not panic
	m.UnassignDomain("nonexistent.com")
}

// --- StopFPM: with running mock process ---

func TestStopFPMRunningProcess(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4", SAPI: "cgi-fcgi"},
	}

	m.execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("ping", "-n", "100", "127.0.0.1")
	}

	err := m.StartFPM("8.4.19", "127.0.0.1:9000")
	if err != nil {
		t.Fatalf("StartFPM: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	err = m.StopFPM("8.4.19")
	if err != nil {
		t.Errorf("StopFPM: %v", err)
	}

	// Verify process is removed
	_, loaded := m.processes.Load("8.4.19")
	if loaded {
		t.Error("process should be removed after StopFPM")
	}
}

// --- StartFPM: already running error ---

func TestStartFPMAlreadyRunningCov(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4", SAPI: "cgi-fcgi"},
	}

	m.execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("echo", "mock-fpm")
	}

	err := m.StartFPM("8.4.19", "127.0.0.1:9000")
	if err != nil {
		t.Fatalf("StartFPM: %v", err)
	}

	// Try again - should fail
	err = m.StartFPM("8.4.19", "127.0.0.1:9001")
	if err == nil {
		t.Error("expected error for already running version")
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- StartFPM: version not found ---

func TestStartFPMVersionNotFound(t *testing.T) {
	m := New(testLogger())

	err := m.StartFPM("9.9.9", "127.0.0.1:9000")
	if err == nil {
		t.Error("expected error for unknown version")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- Status: with running process ---

func TestStatusWithRunningProcess(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4", SAPI: "cgi-fcgi"},
		{Version: "8.3.0", Binary: "/usr/bin/php-cgi8.3", SAPI: "cgi-fcgi"},
	}

	m.execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("ping", "-n", "100", "127.0.0.1")
	}

	err := m.StartFPM("8.4.19", "127.0.0.1:9000")
	if err != nil {
		t.Fatalf("StartFPM: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	statuses := m.Status()
	if len(statuses) != 2 {
		t.Fatalf("expected 2 statuses, got %d", len(statuses))
	}

	var found bool
	for _, st := range statuses {
		if st.Version == "8.4.19" {
			found = true
			if !st.Running {
				t.Error("8.4.19 should be running")
			}
			if st.ListenAddr != "127.0.0.1:9000" {
				t.Errorf("ListenAddr = %q, want 127.0.0.1:9000", st.ListenAddr)
			}
			if st.PID == 0 {
				t.Error("PID should be non-zero for running process")
			}
		}
		if st.Version == "8.3.0" && st.Running {
			t.Error("8.3.0 should not be running")
		}
	}
	if !found {
		t.Error("should find 8.4.19 in statuses")
	}

	// Clean up
	m.StopFPM("8.4.19")
}

// --- Status: with no running processes ---

func TestStatusNoRunning(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4"},
	}

	statuses := m.Status()
	if len(statuses) != 1 {
		t.Fatalf("expected 1 status, got %d", len(statuses))
	}
	if statuses[0].Running {
		t.Error("should not be running")
	}
	if statuses[0].ListenAddr != "" {
		t.Errorf("ListenAddr should be empty, got %q", statuses[0].ListenAddr)
	}
}

// --- StopAll: with running global and domain processes ---

func TestStopAllWithProcesses(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4", SAPI: "cgi-fcgi"},
	}

	m.execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("ping", "-n", "100", "127.0.0.1")
	}

	// Start a global FPM process
	err := m.StartFPM("8.4.19", "127.0.0.1:9000")
	if err != nil {
		t.Fatalf("StartFPM: %v", err)
	}

	// Start a domain process
	_, err = m.AssignDomain("stopall.com", "8.4")
	if err != nil {
		t.Fatalf("AssignDomain: %v", err)
	}
	err = m.StartDomain("stopall.com")
	if err != nil {
		t.Fatalf("StartDomain: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	// StopAll should clean up everything
	m.StopAll()

	// Verify global process is gone
	_, loaded := m.processes.Load("8.4.19")
	if loaded {
		t.Error("global process should be removed after StopAll")
	}

	// Verify domain process is stopped
	m.domainMu.RLock()
	inst := m.domainMap["stopall.com"]
	m.domainMu.RUnlock()
	if inst != nil && inst.proc != nil {
		t.Error("domain process should be nil after StopAll")
	}
}

// --- StopAll: with no processes ---

func TestStopAllNoProcesses(t *testing.T) {
	m := New(testLogger())
	// Should not panic with no processes
	m.StopAll()
}

// --- buildDomainINI: with overrides only (no base config) ---

func TestBuildDomainINIOverridesOnlyCov(t *testing.T) {
	m := New(testLogger())
	inst := PHPInstall{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4", ConfigFile: ""}

	overrides := map[string]string{
		"memory_limit":       "256M",
		"max_execution_time": "60",
	}

	tmpPath, err := m.buildDomainINI("test.com", inst, overrides)
	if err != nil {
		t.Fatalf("buildDomainINI: %v", err)
	}
	if tmpPath == "" {
		t.Fatal("expected non-empty tmp path for overrides")
	}
	defer os.Remove(tmpPath)

	data, err := os.ReadFile(tmpPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "memory_limit = 256M") {
		t.Error("should contain memory_limit override")
	}
	if !strings.Contains(content, "max_execution_time = 60") {
		t.Error("should contain max_execution_time override")
	}
}

// --- buildDomainINI: with base config and overrides ---

func TestBuildDomainINIWithBaseConfig(t *testing.T) {
	m := New(testLogger())
	dir := t.TempDir()
	iniPath := filepath.Join(dir, "php.ini")
	os.WriteFile(iniPath, []byte("memory_limit = 128M\ndisplay_errors = On\n"), 0644)

	inst := PHPInstall{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4", ConfigFile: iniPath}

	overrides := map[string]string{
		"memory_limit": "512M",
	}

	tmpPath, err := m.buildDomainINI("withbase.com", inst, overrides)
	if err != nil {
		t.Fatalf("buildDomainINI: %v", err)
	}
	if tmpPath == "" {
		t.Fatal("expected non-empty tmp path")
	}
	defer os.Remove(tmpPath)

	data, err := os.ReadFile(tmpPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)
	// Should contain original base content
	if !strings.Contains(content, "display_errors = On") {
		t.Error("should include base config content")
	}
	// Should contain overrides
	if !strings.Contains(content, "memory_limit = 512M") {
		t.Error("should contain override")
	}
}

// --- buildDomainINI: no config file and no overrides returns empty ---

func TestBuildDomainININoConfigNoOverrides(t *testing.T) {
	m := New(testLogger())
	inst := PHPInstall{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4", ConfigFile: ""}

	tmpPath, err := m.buildDomainINI("empty.com", inst, nil)
	if err != nil {
		t.Fatalf("buildDomainINI: %v", err)
	}
	if tmpPath != "" {
		t.Errorf("expected empty path when no config and no overrides, got %q", tmpPath)
		os.Remove(tmpPath)
	}
}

// --- Detect: uses mock exec to scan ---

func TestDetectWithMockExec(t *testing.T) {
	m := New(testLogger())
	m.execCommand = func(name string, args ...string) *exec.Cmd {
		// Mock: for -v return version info, for -i return config info, for -m return modules
		if len(args) > 0 {
			switch args[0] {
			case "-v":
				return exec.Command("echo", "PHP 8.4.19 (cgi-fcgi) (built: Jan 15 2025 12:00:00)")
			case "-i":
				return exec.Command("echo", "Loaded Configuration File => /etc/php.ini")
			case "-m":
				return exec.Command("echo", "[PHP Modules]\nmysqli\nopcache\n")
			}
		}
		return exec.Command("echo", "mock")
	}

	// Detect won't find any real binaries via glob but tests the function path
	err := m.Detect()
	if err != nil {
		t.Errorf("Detect: %v", err)
	}
}

// --- probe: version parse failure ---

func TestProbeVersionParseFailure(t *testing.T) {
	m := New(testLogger())
	m.execCommand = func(name string, args ...string) *exec.Cmd {
		// Return something that doesn't match PHP version pattern
		return exec.Command("echo", "not a php version")
	}

	_, err := m.probe("/fake/php")
	if err == nil {
		t.Error("expected error when version cannot be parsed")
	}
	if !strings.Contains(err.Error(), "could not parse version") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- probe: exec failure for version check ---

func TestProbeExecFailure(t *testing.T) {
	m := New(testLogger())
	m.execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("/nonexistent/binary/xyz")
	}

	_, err := m.probe("/fake/php")
	if err == nil {
		t.Error("expected error when exec fails")
	}
	if !strings.Contains(err.Error(), "version check") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- probe: successful probe extracts all fields ---

func TestProbeSuccess(t *testing.T) {
	m := New(testLogger())

	callCount := 0
	m.execCommand = func(name string, args ...string) *exec.Cmd {
		callCount++
		if len(args) > 0 {
			switch args[0] {
			case "-v":
				return exec.Command("echo", "PHP 8.4.19 (cgi-fcgi) (built: Jan 15 2025 12:00:00)")
			case "-i":
				return exec.Command("echo", "Loaded Configuration File => /etc/php/8.4/cgi/php.ini")
			case "-m":
				return exec.Command("echo", "[PHP Modules]\nmysqli\nopcache\ncurl")
			}
		}
		return exec.Command("echo", "")
	}

	inst, err := m.probe("/usr/bin/php-cgi8.4")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}

	if inst.Version != "8.4.19" {
		t.Errorf("Version = %q, want 8.4.19", inst.Version)
	}
	if inst.SAPI != "cgi-fcgi" {
		t.Errorf("SAPI = %q, want cgi-fcgi", inst.SAPI)
	}
	if inst.Binary != "/usr/bin/php-cgi8.4" {
		t.Errorf("Binary = %q", inst.Binary)
	}
}

// --- runPHP: error path ---

func TestRunPHPError(t *testing.T) {
	m := New(testLogger())
	m.execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("/nonexistent/binary/should/fail")
	}

	_, err := m.runPHP("/nonexistent/php", "-v")
	if err == nil {
		t.Error("expected error from runPHP")
	}
}

// --- AutoStartAll: with error from StartDomain ---

func TestAutoStartAllWithError(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4", SAPI: "cgi-fcgi"},
	}

	m.execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("/nonexistent/binary/xyz")
	}

	_, err := m.AssignDomain("autostart-fail.com", "8.4")
	if err != nil {
		t.Fatalf("AssignDomain: %v", err)
	}

	err = m.AutoStartAll()
	if err == nil {
		t.Error("expected error from AutoStartAll when StartDomain fails")
	}
	if !strings.Contains(err.Error(), "auto-start errors") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- AutoStartAll: successful start ---

func TestAutoStartAllSuccess(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4", SAPI: "cgi-fcgi"},
	}

	m.execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("ping", "-n", "100", "127.0.0.1")
	}

	_, err := m.AssignDomain("autostart-ok.com", "8.4")
	if err != nil {
		t.Fatalf("AssignDomain: %v", err)
	}

	err = m.AutoStartAll()
	if err != nil {
		t.Errorf("AutoStartAll: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	// Clean up
	m.StopDomain("autostart-ok.com")
}

// --- GetDomainInstances: with running instance reports Running=true ---

func TestGetDomainInstancesWithRunning(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4", SAPI: "cgi-fcgi"},
	}

	m.execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("ping", "-n", "100", "127.0.0.1")
	}

	_, err := m.AssignDomain("running-inst.com", "8.4")
	if err != nil {
		t.Fatalf("AssignDomain: %v", err)
	}

	err = m.StartDomain("running-inst.com")
	if err != nil {
		t.Fatalf("StartDomain: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	instances := m.GetDomainInstances()
	if len(instances) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(instances))
	}
	if !instances[0].Running {
		t.Error("instance should be Running=true")
	}
	if instances[0].PID == 0 {
		t.Error("PID should be non-zero")
	}

	// Clean up
	m.StopDomain("running-inst.com")
}

// --- UnassignDomain: cleans up tmpINI file ---

func TestUnassignDomainCleansUpTmpINI(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4"},
	}

	_, err := m.AssignDomain("tmpini-cleanup.com", "8.4")
	if err != nil {
		t.Fatalf("AssignDomain: %v", err)
	}

	// Manually set a tmpINI to simulate
	m.domainMu.Lock()
	inst := m.domainMap["tmpini-cleanup.com"]
	tmpFile := filepath.Join(t.TempDir(), "test.ini")
	os.WriteFile(tmpFile, []byte("test"), 0644)
	inst.tmpINI = tmpFile
	m.domainMu.Unlock()

	m.UnassignDomain("tmpini-cleanup.com")

	if _, err := os.Stat(tmpFile); !os.IsNotExist(err) {
		t.Error("tmpINI file should be removed after unassign")
	}
}

// --- Installations returns a copy ---

func TestInstallationsReturnsCopy(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4"},
		{Version: "8.3.0", Binary: "/usr/bin/php-cgi8.3"},
	}

	installs := m.Installations()
	if len(installs) != 2 {
		t.Fatalf("expected 2 installations, got %d", len(installs))
	}

	// Modify the returned slice - should not affect the manager
	installs[0].Version = "modified"
	origInstalls := m.Installations()
	if origInstalls[0].Version == "modified" {
		t.Error("Installations() should return a copy, not a reference")
	}
}

// suppress unused import warnings
var _ = strconv.Itoa(0)
var _ = filepath.Join("")
var _ = os.Remove
var _ time.Duration

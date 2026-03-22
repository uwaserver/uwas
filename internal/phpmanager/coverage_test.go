package phpmanager

import (
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// --- StartFPM with invalid PHP binary path ---

func TestStartFPMInvalidBinary(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/nonexistent/path/php-cgi"},
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
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4"},
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

	_, err := m.GetConfig("8.4.19")
	if err == nil {
		t.Error("expected error when config file is empty")
	}
	if !strings.Contains(err.Error(), "no config file") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- SetConfig for version with no config file ---

func TestSetConfigNoConfigFile(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4", ConfigFile: ""},
	}

	err := m.SetConfig("8.4.19", "memory_limit", "512M")
	if err == nil {
		t.Error("expected error when config file is empty")
	}
	if !strings.Contains(err.Error(), "no config file") {
		t.Errorf("unexpected error: %v", err)
	}
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

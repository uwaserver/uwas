package phpmanager

import (
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

func TestAssignDomain(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4"},
		{Version: "8.3.0", Binary: "/usr/bin/php-cgi8.3"},
	}

	dp, err := m.AssignDomain("blog.com", "8.4")
	if err != nil {
		t.Fatalf("AssignDomain: %v", err)
	}
	if dp.Domain != "blog.com" {
		t.Errorf("domain = %q, want blog.com", dp.Domain)
	}
	if dp.Version != "8.4" {
		t.Errorf("version = %q, want 8.4", dp.Version)
	}
	if dp.ListenAddr != "127.0.0.1:9001" {
		t.Errorf("listen_addr = %q, want 127.0.0.1:9001", dp.ListenAddr)
	}
	if dp.Running {
		t.Error("should not be running yet")
	}
}

func TestAssignDomainAutoPort(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4"},
	}

	dp1, err := m.AssignDomain("site1.com", "8.4")
	if err != nil {
		t.Fatalf("AssignDomain site1: %v", err)
	}
	dp2, err := m.AssignDomain("site2.com", "8.4")
	if err != nil {
		t.Fatalf("AssignDomain site2: %v", err)
	}

	if dp1.ListenAddr == dp2.ListenAddr {
		t.Errorf("ports should differ: %s == %s", dp1.ListenAddr, dp2.ListenAddr)
	}
	if dp1.ListenAddr != "127.0.0.1:9001" {
		t.Errorf("first port = %q, want 127.0.0.1:9001", dp1.ListenAddr)
	}
	if dp2.ListenAddr != "127.0.0.1:9002" {
		t.Errorf("second port = %q, want 127.0.0.1:9002", dp2.ListenAddr)
	}
}

func TestAssignDomainDuplicate(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4"},
	}

	_, err := m.AssignDomain("blog.com", "8.4")
	if err != nil {
		t.Fatalf("first AssignDomain: %v", err)
	}

	_, err = m.AssignDomain("blog.com", "8.4")
	if err == nil {
		t.Error("expected error for duplicate domain assignment")
	}
	if !strings.Contains(err.Error(), "already has") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestAssignDomainNotFound(t *testing.T) {
	m := New(testLogger())

	_, err := m.AssignDomain("blog.com", "9.9")
	if err == nil {
		t.Error("expected error for non-existent version")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestAssignDomainEmptyDomain(t *testing.T) {
	m := New(testLogger())
	_, err := m.AssignDomain("", "8.4")
	if err == nil || !strings.Contains(err.Error(), "domain is required") {
		t.Errorf("expected domain required error, got: %v", err)
	}
}

func TestAssignDomainEmptyVersion(t *testing.T) {
	m := New(testLogger())
	_, err := m.AssignDomain("blog.com", "")
	if err == nil || !strings.Contains(err.Error(), "version is required") {
		t.Errorf("expected version required error, got: %v", err)
	}
}

func TestUnassignDomain(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4"},
	}

	_, err := m.AssignDomain("blog.com", "8.4")
	if err != nil {
		t.Fatalf("AssignDomain: %v", err)
	}

	m.UnassignDomain("blog.com")

	instances := m.GetDomainInstances()
	if len(instances) != 0 {
		t.Errorf("expected 0 instances after unassign, got %d", len(instances))
	}
}

func TestUnassignDomainNonExistent(t *testing.T) {
	m := New(testLogger())
	// Should not panic.
	m.UnassignDomain("nonexistent.com")
}

func TestGetDomainInstances(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4"},
		{Version: "8.3.0", Binary: "/usr/bin/php-cgi8.3"},
	}

	m.AssignDomain("alpha.com", "8.4")
	m.AssignDomain("beta.com", "8.3")

	instances := m.GetDomainInstances()
	if len(instances) != 2 {
		t.Fatalf("expected 2 instances, got %d", len(instances))
	}

	// Should be sorted by domain.
	if instances[0].Domain != "alpha.com" {
		t.Errorf("first domain = %q, want alpha.com", instances[0].Domain)
	}
	if instances[1].Domain != "beta.com" {
		t.Errorf("second domain = %q, want beta.com", instances[1].Domain)
	}
}

func TestSetDomainConfig(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4"},
	}

	m.AssignDomain("blog.com", "8.4")

	err := m.SetDomainConfig("blog.com", "memory_limit", "512M")
	if err != nil {
		t.Fatalf("SetDomainConfig: %v", err)
	}

	cfg := m.GetDomainConfig("blog.com")
	if cfg["memory_limit"] != "512M" {
		t.Errorf("memory_limit = %q, want 512M", cfg["memory_limit"])
	}
}

func TestSetDomainConfigNoAssignment(t *testing.T) {
	m := New(testLogger())
	err := m.SetDomainConfig("nonexistent.com", "memory_limit", "512M")
	if err == nil {
		t.Error("expected error for non-existent domain")
	}
}

func TestSetDomainConfigEmptyKey(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4"},
	}
	m.AssignDomain("blog.com", "8.4")

	err := m.SetDomainConfig("blog.com", "", "512M")
	if err == nil || !strings.Contains(err.Error(), "key is required") {
		t.Errorf("expected key required error, got: %v", err)
	}
}

func TestGetDomainConfigNonExistent(t *testing.T) {
	m := New(testLogger())
	cfg := m.GetDomainConfig("nonexistent.com")
	if cfg != nil {
		t.Errorf("expected nil config for non-existent domain, got %v", cfg)
	}
}

func TestGetDomainConfigIsCopy(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4"},
	}

	m.AssignDomain("blog.com", "8.4")
	m.SetDomainConfig("blog.com", "memory_limit", "256M")

	cfg := m.GetDomainConfig("blog.com")
	cfg["memory_limit"] = "tampered"

	// Original should be unchanged.
	cfg2 := m.GetDomainConfig("blog.com")
	if cfg2["memory_limit"] != "256M" {
		t.Errorf("config was mutated externally: %q", cfg2["memory_limit"])
	}
}

func TestStartDomainNoAssignment(t *testing.T) {
	m := New(testLogger())
	err := m.StartDomain("nonexistent.com")
	if err == nil || !strings.Contains(err.Error(), "no PHP assignment") {
		t.Errorf("expected no assignment error, got: %v", err)
	}
}

func TestStopDomainNoAssignment(t *testing.T) {
	m := New(testLogger())
	err := m.StopDomain("nonexistent.com")
	if err == nil || !strings.Contains(err.Error(), "no PHP assignment") {
		t.Errorf("expected no assignment error, got: %v", err)
	}
}

func TestStopDomainNotRunning(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4"},
	}
	m.AssignDomain("blog.com", "8.4")

	err := m.StopDomain("blog.com")
	if err == nil || !strings.Contains(err.Error(), "not running") {
		t.Errorf("expected not running error, got: %v", err)
	}
}

func TestStartDomainAlreadyRunning(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4", SAPI: "cgi-fcgi"},
	}
	m.AssignDomain("blog.com", "8.4")

	// Simulate a running process.
	m.domainMu.Lock()
	m.domainMap["blog.com"].proc = &processInfo{
		cmd:        exec.Command("echo"),
		listenAddr: "127.0.0.1:9001",
	}
	m.domainMu.Unlock()

	err := m.StartDomain("blog.com")
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Errorf("expected already running error, got: %v", err)
	}
}

func TestBuildDomainINI(t *testing.T) {
	m := New(testLogger())

	// Create a base php.ini.
	dir := t.TempDir()
	baseINI := dir + "/php.ini"
	os.WriteFile(baseINI, []byte("memory_limit = 128M\n"), 0644)

	inst := PHPInstall{
		Version:    "8.4.19",
		ConfigFile: baseINI,
	}

	overrides := map[string]string{
		"memory_limit":       "512M",
		"max_execution_time": "60",
	}

	tmpPath, err := m.buildDomainINI("blog.com", inst, overrides)
	if err != nil {
		t.Fatalf("buildDomainINI: %v", err)
	}
	defer os.Remove(tmpPath)

	if tmpPath == "" {
		t.Fatal("expected non-empty tmp path")
	}

	data, err := os.ReadFile(tmpPath)
	if err != nil {
		t.Fatalf("read tmp ini: %v", err)
	}

	content := string(data)
	// Should contain the base config content.
	if !strings.Contains(content, "memory_limit = 128M") {
		t.Error("expected base config content in temp ini")
	}
	// Should contain overrides.
	if !strings.Contains(content, "memory_limit = 512M") {
		t.Error("expected memory_limit override in temp ini")
	}
	if !strings.Contains(content, "max_execution_time = 60") {
		t.Error("expected max_execution_time override in temp ini")
	}
	// Should contain domain comment.
	if !strings.Contains(content, "blog.com") {
		t.Error("expected domain name in temp ini comments")
	}
}

func TestBuildDomainININoBaseNoOverrides(t *testing.T) {
	m := New(testLogger())
	inst := PHPInstall{Version: "8.4.19"}

	tmpPath, err := m.buildDomainINI("blog.com", inst, map[string]string{})
	if err != nil {
		t.Fatalf("buildDomainINI: %v", err)
	}
	if tmpPath != "" {
		os.Remove(tmpPath)
		t.Error("expected empty path when no base config and no overrides")
	}
}

func TestBuildDomainINIOverridesOnly(t *testing.T) {
	m := New(testLogger())
	inst := PHPInstall{Version: "8.4.19"}

	overrides := map[string]string{"memory_limit": "256M"}
	tmpPath, err := m.buildDomainINI("blog.com", inst, overrides)
	if err != nil {
		t.Fatalf("buildDomainINI: %v", err)
	}
	defer os.Remove(tmpPath)

	if tmpPath == "" {
		t.Fatal("expected non-empty tmp path with overrides")
	}

	data, _ := os.ReadFile(tmpPath)
	if !strings.Contains(string(data), "memory_limit = 256M") {
		t.Error("expected override in temp ini")
	}
}

func TestDomainChangeCallback(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4", SAPI: "cgi-fcgi"},
	}

	var calledDomain, calledAddr string
	var mu sync.Mutex
	m.SetDomainChangeFunc(func(domain, fpmAddr string) {
		mu.Lock()
		calledDomain = domain
		calledAddr = fpmAddr
		mu.Unlock()
	})

	m.AssignDomain("blog.com", "8.4")

	// Mock execCommand to use a long-running process.
	m.execCommand = func(name string, args ...string) *exec.Cmd {
		// Use a command that will start and can be killed.
		return exec.Command("echo", "test")
	}

	err := m.StartDomain("blog.com")
	if err != nil {
		t.Fatalf("StartDomain: %v", err)
	}

	mu.Lock()
	if calledDomain != "blog.com" {
		t.Errorf("callback domain = %q, want blog.com", calledDomain)
	}
	if calledAddr != "127.0.0.1:9001" {
		t.Errorf("callback addr = %q, want 127.0.0.1:9001", calledAddr)
	}
	mu.Unlock()
}

func TestAutoStartAllEmpty(t *testing.T) {
	m := New(testLogger())
	err := m.AutoStartAll()
	if err != nil {
		t.Errorf("AutoStartAll with no domains should not error, got: %v", err)
	}
}

func TestAutoStartAllErrors(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/nonexistent/php-cgi", SAPI: "cgi-fcgi"},
	}

	m.AssignDomain("blog.com", "8.4")

	// execCommand that returns a command that will fail to start.
	m.execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("/nonexistent/binary/that/does/not/exist")
	}

	err := m.AutoStartAll()
	if err == nil {
		t.Error("expected error from AutoStartAll with failing binary")
	}
	if !strings.Contains(err.Error(), "auto-start errors") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestStopAllWithDomainInstances(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4"},
	}

	m.AssignDomain("blog.com", "8.4")

	// Simulate a running process with a real PID by using exec.Command.
	m.domainMu.Lock()
	cmd := exec.Command("echo", "test")
	m.domainMap["blog.com"].proc = &processInfo{
		cmd:        cmd,
		listenAddr: "127.0.0.1:9001",
	}
	m.domainMu.Unlock()

	// StopAll should not panic.
	m.StopAll()

	// The process should be nil after StopAll.
	m.domainMu.RLock()
	inst := m.domainMap["blog.com"]
	if inst != nil && inst.proc != nil {
		t.Error("expected proc to be nil after StopAll")
	}
	m.domainMu.RUnlock()
}

func TestDomainPHPFromInstance(t *testing.T) {
	m := New(testLogger())

	inst := &domainInstance{
		domain:     "blog.com",
		version:    "8.4",
		listenAddr: "127.0.0.1:9001",
		configOverrides: map[string]string{
			"memory_limit": "512M",
		},
	}

	dp := m.domainPHPFromInstance(inst)
	if dp.Domain != "blog.com" {
		t.Errorf("domain = %q", dp.Domain)
	}
	if dp.Version != "8.4" {
		t.Errorf("version = %q", dp.Version)
	}
	if dp.ListenAddr != "127.0.0.1:9001" {
		t.Errorf("listen_addr = %q", dp.ListenAddr)
	}
	if dp.Running {
		t.Error("should not be running")
	}
	if dp.ConfigOverrides["memory_limit"] != "512M" {
		t.Errorf("config override = %q", dp.ConfigOverrides["memory_limit"])
	}

	// Mutating the returned map should not affect the instance.
	dp.ConfigOverrides["memory_limit"] = "tampered"
	if inst.configOverrides["memory_limit"] != "512M" {
		t.Error("instance config was mutated via returned DomainPHP")
	}
}

func TestDomainPHPFromInstanceRunning(t *testing.T) {
	m := New(testLogger())

	cmd := exec.Command("echo", "test")
	cmd.Start() // Start so Process is populated.

	inst := &domainInstance{
		domain:          "blog.com",
		version:         "8.4",
		listenAddr:      "127.0.0.1:9001",
		configOverrides: map[string]string{},
		proc: &processInfo{
			cmd:        cmd,
			listenAddr: "127.0.0.1:9001",
		},
	}

	dp := m.domainPHPFromInstance(inst)
	if !dp.Running {
		t.Error("should be running")
	}
	if dp.PID == 0 {
		t.Error("PID should be non-zero")
	}

	cmd.Wait() // Clean up.
}

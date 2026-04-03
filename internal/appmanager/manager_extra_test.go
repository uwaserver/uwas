package appmanager

import (
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// =============================================================================
// Monitor Process Tests (coverage for monitorProcess function)
// =============================================================================

func TestMonitorProcess_AutoRestart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process management test skipped on Windows")
	}

	dir := t.TempDir()
	log := logger.New("error", "text")
	m := New(log)

	// Create a short-lived script that exits quickly
	script := `#!/bin/sh
sleep 0.1
exit 1`
	scriptPath := dir + "/crash.sh"
	os.WriteFile(scriptPath, []byte(script), 0755)

	m.Register("autorestart.com", config.AppConfig{
		Command: scriptPath,
		Runtime: "custom",
		Port:    9001,
	}, dir)

	// Override execCommandFn to use real process
	originalExec := execCommandFn
	execCommandFn = exec.Command
	defer func() { execCommandFn = originalExec }()

	if err := m.Start("autorestart.com"); err != nil {
		t.Fatal(err)
	}

	// Wait for first start
	time.Sleep(200 * time.Millisecond)
	inst1 := m.Get("autorestart.com")
	if !inst1.Running {
		t.Fatal("expected running after first start")
	}
	firstPID := inst1.PID

	// Wait for auto-restart (process crashes after 0.1s, auto-restart after 2s backoff)
	time.Sleep(2500 * time.Millisecond)

	inst2 := m.Get("autorestart.com")
	if !inst2.Running {
		t.Error("expected auto-restart to have restarted the process")
	}
	if inst2.PID == firstPID {
		t.Error("expected different PID after restart")
	}

	m.Stop("autorestart.com")
}

func TestMonitorProcess_NoAutoRestart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process management test skipped on Windows")
	}

	dir := t.TempDir()
	m := New(nil)

	// Register without auto-restart by using a long-running command
	m.Register("noautorestart.com", config.AppConfig{
		Command: "sleep 60",
		Runtime: "custom",
		Port:    9002,
	}, dir)

	if err := m.Start("noautorestart.com"); err != nil {
		t.Fatal(err)
	}

	time.Sleep(100 * time.Millisecond)

	// Stop manually
	if err := m.Stop("noautorestart.com"); err != nil {
		t.Errorf("stop failed: %v", err)
	}

	// Verify stopped
	time.Sleep(100 * time.Millisecond)
	inst := m.Get("noautorestart.com")
	if inst.Running {
		t.Error("expected stopped after manual stop")
	}
}

func TestMonitorProcess_GracefulStop(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process management test skipped on Windows")
	}

	dir := t.TempDir()
	m := New(nil)

	// Use a script that handles SIGTERM gracefully
	script := `#!/bin/sh
trap "exit 0" TERM
sleep 60 &
wait`
	scriptPath := dir + "/graceful.sh"
	os.WriteFile(scriptPath, []byte(script), 0755)

	m.Register("graceful.com", config.AppConfig{
		Command: scriptPath,
		Runtime: "custom",
		Port:    9003,
	}, dir)

	if err := m.Start("graceful.com"); err != nil {
		t.Fatal(err)
	}

	time.Sleep(100 * time.Millisecond)

	// Graceful stop
	if err := m.Stop("graceful.com"); err != nil {
		t.Errorf("stop failed: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	inst := m.Get("graceful.com")
	if inst.Running {
		t.Error("expected stopped")
	}
}

// =============================================================================
// Instances Tests (coverage for Instances function)
// =============================================================================

func TestInstances_Empty(t *testing.T) {
	m := New(nil)

	instances := m.Instances()
	if len(instances) != 0 {
		t.Errorf("expected 0 instances, got %d", len(instances))
	}
}

func TestInstances_Multiple(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process management test skipped on Windows")
	}

	dir := t.TempDir()
	m := New(nil)

	// Register multiple apps
	domains := []string{"inst1.com", "inst2.com", "inst3.com"}
	for i, domain := range domains {
		m.Register(domain, config.AppConfig{
			Command: "sleep 60",
			Runtime: "custom",
			Port:    9100 + i,
		}, dir)
	}

	// Start only first two
	for i := 0; i < 2; i++ {
		if err := m.Start(domains[i]); err != nil {
			t.Fatalf("failed to start %s: %v", domains[i], err)
		}
	}

	time.Sleep(100 * time.Millisecond)

	instances := m.Instances()
	if len(instances) != 3 {
		t.Errorf("expected 3 instances, got %d", len(instances))
	}

	// Verify running status
	runningCount := 0
	for _, inst := range instances {
		if inst.Running {
			runningCount++
		}
	}
	if runningCount != 2 {
		t.Errorf("expected 2 running, got %d", runningCount)
	}

	// Cleanup
	m.StopAll()
}

func TestInstances_WithDetails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process management test skipped on Windows")
	}

	dir := t.TempDir()
	m := New(nil)

	m.Register("details.com", config.AppConfig{
		Command: "sleep 60",
		Runtime: "custom",
		Port:    9200,
	}, dir)

	if err := m.Start("details.com"); err != nil {
		t.Fatal(err)
	}

	time.Sleep(100 * time.Millisecond)

	instances := m.Instances()
	if len(instances) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(instances))
	}

	inst := instances[0]
	if inst.Domain != "details.com" {
		t.Errorf("expected domain details.com, got %s", inst.Domain)
	}
	if inst.Runtime != "custom" {
		t.Errorf("expected runtime custom, got %s", inst.Runtime)
	}
	if inst.Port != 9200 {
		t.Errorf("expected port 9200, got %d", inst.Port)
	}
	if !inst.Running {
		t.Error("expected running")
	}
	if inst.PID == 0 {
		t.Error("expected non-zero PID")
	}
	if inst.StartedAt == nil {
		t.Error("expected non-nil StartedAt")
	}
	if inst.Uptime == "" {
		t.Error("expected non-empty Uptime")
	}

	m.Stop("details.com")
}

// =============================================================================
// Stats Tests
// =============================================================================

func TestStats_NonExistent(t *testing.T) {
	m := New(nil)

	stats := m.Stats("nonexistent.com")
	if stats != nil {
		t.Error("expected nil stats for non-existent domain")
	}
}

func TestStats_RegisteredNotRunning(t *testing.T) {
	m := New(nil)

	m.Register("statsnotrunning.com", config.AppConfig{
		Command: "sleep 60",
		Runtime: "custom",
		Port:    9300,
	}, "/tmp")

	stats := m.Stats("statsnotrunning.com")
	if stats == nil {
		t.Fatal("expected non-nil stats")
	}
	if stats.Running {
		t.Error("expected not running")
	}
	if stats.PID != 0 {
		t.Error("expected PID to be 0")
	}
	if stats.Domain != "statsnotrunning.com" {
		t.Errorf("expected domain statsnotrunning.com, got %s", stats.Domain)
	}
}

func TestStats_Running(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process management test skipped on Windows")
	}

	dir := t.TempDir()
	m := New(nil)

	m.Register("statsrunning.com", config.AppConfig{
		Command: "sleep 60",
		Runtime: "custom",
		Port:    9400,
	}, dir)

	if err := m.Start("statsrunning.com"); err != nil {
		t.Fatal(err)
	}

	time.Sleep(100 * time.Millisecond)

	stats := m.Stats("statsrunning.com")
	if stats == nil {
		t.Fatal("expected non-nil stats")
	}
	if !stats.Running {
		t.Error("expected running")
	}
	if stats.PID == 0 {
		t.Error("expected non-zero PID")
	}
	if stats.Uptime == "" {
		t.Error("expected non-empty uptime")
	}

	m.Stop("statsrunning.com")
}

// =============================================================================
// Get Tests
// =============================================================================

func TestGet_NonExistent(t *testing.T) {
	m := New(nil)

	inst := m.Get("nonexistent.com")
	if inst != nil {
		t.Error("expected nil for non-existent domain")
	}
}

func TestGet_NotRunning(t *testing.T) {
	m := New(nil)

	m.Register("getnotrunning.com", config.AppConfig{
		Command: "sleep 60",
		Runtime: "custom",
		Port:    9500,
	}, "/tmp")

	inst := m.Get("getnotrunning.com")
	if inst == nil {
		t.Fatal("expected non-nil instance")
	}
	if inst.Running {
		t.Error("expected not running")
	}
	if inst.Domain != "getnotrunning.com" {
		t.Errorf("expected domain getnotrunning.com, got %s", inst.Domain)
	}
}

// =============================================================================
// Stop Tests (edge cases)
// =============================================================================

func TestStop_NotRunning(t *testing.T) {
	m := New(nil)

	m.Register("stopnotrunning.com", config.AppConfig{
		Command: "sleep 60",
		Runtime: "custom",
		Port:    9600,
	}, "/tmp")

	// Stop without starting
	err := m.Stop("stopnotrunning.com")
	if err == nil {
		t.Error("expected error when stopping non-running app")
	}
}

func TestStop_NonExistent(t *testing.T) {
	m := New(nil)

	err := m.Stop("nonexistent.com")
	if err == nil {
		t.Error("expected error when stopping non-existent app")
	}
}

func TestStop_AlreadyStopped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process management test skipped on Windows")
	}

	dir := t.TempDir()
	m := New(nil)

	m.Register("alreadystopped.com", config.AppConfig{
		Command: "sleep 60",
		Runtime: "custom",
		Port:    9700,
	}, dir)

	if err := m.Start("alreadystopped.com"); err != nil {
		t.Fatal(err)
	}

	time.Sleep(100 * time.Millisecond)

	// First stop should succeed
	if err := m.Stop("alreadystopped.com"); err != nil {
		t.Errorf("first stop failed: %v", err)
	}

	// Second stop should return error (already stopped)
	err := m.Stop("alreadystopped.com")
	if err == nil {
		t.Error("expected error when stopping already stopped app")
	}
}

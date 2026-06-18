package rlimit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeDomain(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"example.com", "example.com"},
		{"My-Site.COM", "my-site.com"},
		{"hello world!", "hello_world_"},
		{"a/b\\c", "a_b_c"},
		{"", ""},
		{"test@domain.com", "test_domain.com"},
	}
	for _, tt := range tests {
		if got := sanitizeDomain(tt.in); got != tt.want {
			t.Errorf("sanitizeDomain(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestApplyNoLimits(t *testing.T) {
	path, err := Apply("test.com", Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if path != "" {
		t.Errorf("expected empty path for no limits, got %q", path)
	}
}

func TestApplyLinuxWritesConfiguredLimits(t *testing.T) {
	defer saveAndRestoreHooks()()
	runtimeGOOS = func() string { return "linux" }

	var mkdirCalls []string
	writeCalls := map[string]string{}
	osMkdirAllFn = func(path string, perm os.FileMode) error {
		mkdirCalls = append(mkdirCalls, path)
		return nil
	}
	osWriteFileFn = func(name string, data []byte, perm os.FileMode) error {
		writeCalls[name] = string(data)
		return nil
	}

	path, err := Apply("test.com", Limits{CPUPercent: 50, MemoryMB: 256, PIDMax: 100})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !strings.Contains(path, "test.com") {
		t.Errorf("path = %q, want sanitized domain", path)
	}
	if len(mkdirCalls) != 1 {
		t.Errorf("mkdir calls = %d, want 1", len(mkdirCalls))
	}

	want := map[string]string{
		"cpu.max":    "50000 100000",
		"memory.max": "268435456",
		"pids.max":   "100",
	}
	for suffix, content := range want {
		if got, ok := findSuffix(writeCalls, suffix); !ok || got != content {
			t.Errorf("%s = %q, %v; want %q, true", suffix, got, ok, content)
		}
	}
}

func TestApplyLinuxMkdirError(t *testing.T) {
	defer saveAndRestoreHooks()()
	runtimeGOOS = func() string { return "linux" }
	osMkdirAllFn = func(path string, perm os.FileMode) error {
		return fmt.Errorf("permission denied")
	}

	if _, err := Apply("test.com", Limits{CPUPercent: 50}); err == nil {
		t.Fatal("expected error for mkdir failure")
	}
}

func TestAssignPIDAndRemoveLinux(t *testing.T) {
	defer saveAndRestoreHooks()()
	runtimeGOOS = func() string { return "linux" }

	var wrote string
	osWriteFileFn = func(name string, data []byte, perm os.FileMode) error {
		wrote = name + ":" + string(data)
		return nil
	}
	if err := AssignPID("/sys/fs/cgroup/uwas/test.com", 12345); err != nil {
		t.Fatalf("AssignPID: %v", err)
	}
	if !strings.Contains(wrote, "cgroup.procs:12345") {
		t.Errorf("unexpected write: %q", wrote)
	}

	var removed string
	osRemoveFn = func(name string) error {
		removed = name
		return nil
	}
	if err := Remove("test.com"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !strings.Contains(removed, "test.com") {
		t.Errorf("removed = %q, want test.com path", removed)
	}
}

func TestApplyNonLinux(t *testing.T) {
	defer saveAndRestoreHooks()()
	runtimeGOOS = func() string { return "darwin" }

	path, err := Apply("test.com", Limits{CPUPercent: 50, MemoryMB: 256, PIDMax: 100})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if path != "" {
		t.Errorf("expected empty path on non-linux, got %q", path)
	}
}

func TestApplyLinuxCPUWriteError(t *testing.T) {
	defer saveAndRestoreHooks()()
	runtimeGOOS = func() string { return "linux" }
	osMkdirAllFn = func(string, os.FileMode) error { return nil }
	osWriteFileFn = func(name string, data []byte, perm os.FileMode) error {
		if strings.HasSuffix(name, "cpu.max") {
			return fmt.Errorf("write failed")
		}
		return nil
	}

	path, err := Apply("test.com", Limits{CPUPercent: 50})
	if err == nil {
		t.Fatal("expected error for cpu.max write failure")
	}
	if !strings.Contains(err.Error(), "set cpu.max") {
		t.Errorf("err = %v, want cpu.max error", err)
	}
	if !strings.Contains(path, "test.com") {
		t.Errorf("expected non-empty path on write error, got %q", path)
	}
}

func TestApplyLinuxMemoryWriteError(t *testing.T) {
	defer saveAndRestoreHooks()()
	runtimeGOOS = func() string { return "linux" }
	osMkdirAllFn = func(string, os.FileMode) error { return nil }
	osWriteFileFn = func(name string, data []byte, perm os.FileMode) error {
		if strings.HasSuffix(name, "memory.max") {
			return fmt.Errorf("write failed")
		}
		return nil
	}

	_, err := Apply("test.com", Limits{MemoryMB: 256})
	if err == nil {
		t.Fatal("expected error for memory.max write failure")
	}
	if !strings.Contains(err.Error(), "set memory.max") {
		t.Errorf("err = %v, want memory.max error", err)
	}
}

func TestApplyLinuxPIDWriteError(t *testing.T) {
	defer saveAndRestoreHooks()()
	runtimeGOOS = func() string { return "linux" }
	osMkdirAllFn = func(string, os.FileMode) error { return nil }
	osWriteFileFn = func(name string, data []byte, perm os.FileMode) error {
		if strings.HasSuffix(name, "pids.max") {
			return fmt.Errorf("write failed")
		}
		return nil
	}

	_, err := Apply("test.com", Limits{PIDMax: 100})
	if err == nil {
		t.Fatal("expected error for pids.max write failure")
	}
	if !strings.Contains(err.Error(), "set pids.max") {
		t.Errorf("err = %v, want pids.max error", err)
	}
}

// TestApplyLinuxRealTempDir exercises the real os.MkdirAll/os.WriteFile code
// paths (not the injected hooks) by pointing them at a temp directory, so the
// actual file-writing logic and content formatting is verified end-to-end.
func TestApplyLinuxRealTempDir(t *testing.T) {
	defer saveAndRestoreHooks()()
	runtimeGOOS = func() string { return "linux" }

	base := t.TempDir()
	// Redirect the real os functions to write under the temp base by
	// rewriting the absolute cgroup path into the temp dir.
	rebase := func(name string) string {
		return filepath.Join(base, strings.TrimPrefix(name, "/"))
	}
	osMkdirAllFn = func(path string, perm os.FileMode) error {
		return os.MkdirAll(rebase(path), perm)
	}
	osWriteFileFn = func(name string, data []byte, perm os.FileMode) error {
		return os.WriteFile(rebase(name), data, perm)
	}

	path, err := Apply("Example.COM", Limits{CPUPercent: 25, MemoryMB: 128, PIDMax: 64})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	cpu, err := os.ReadFile(rebase(filepath.Join(path, "cpu.max")))
	if err != nil {
		t.Fatalf("read cpu.max: %v", err)
	}
	if string(cpu) != "25000 100000" {
		t.Errorf("cpu.max = %q, want %q", cpu, "25000 100000")
	}
	mem, err := os.ReadFile(rebase(filepath.Join(path, "memory.max")))
	if err != nil {
		t.Fatalf("read memory.max: %v", err)
	}
	if string(mem) != "134217728" {
		t.Errorf("memory.max = %q, want %q", mem, "134217728")
	}
	pids, err := os.ReadFile(rebase(filepath.Join(path, "pids.max")))
	if err != nil {
		t.Fatalf("read pids.max: %v", err)
	}
	if string(pids) != "64" {
		t.Errorf("pids.max = %q, want %q", pids, "64")
	}
}

func TestAssignPIDNonLinux(t *testing.T) {
	defer saveAndRestoreHooks()()
	runtimeGOOS = func() string { return "windows" }
	osWriteFileFn = func(string, []byte, os.FileMode) error {
		t.Fatal("write should not be called on non-linux")
		return nil
	}
	if err := AssignPID("/some/path", 123); err != nil {
		t.Fatalf("AssignPID: %v", err)
	}
}

func TestAssignPIDEmptyPath(t *testing.T) {
	defer saveAndRestoreHooks()()
	runtimeGOOS = func() string { return "linux" }
	osWriteFileFn = func(string, []byte, os.FileMode) error {
		t.Fatal("write should not be called with empty path")
		return nil
	}
	if err := AssignPID("", 123); err != nil {
		t.Fatalf("AssignPID: %v", err)
	}
}

func TestAssignPIDWriteError(t *testing.T) {
	defer saveAndRestoreHooks()()
	runtimeGOOS = func() string { return "linux" }
	osWriteFileFn = func(string, []byte, os.FileMode) error {
		return fmt.Errorf("write failed")
	}
	if err := AssignPID("/sys/fs/cgroup/uwas/test.com", 123); err == nil {
		t.Fatal("expected error from AssignPID write failure")
	}
}

func TestRemoveNonLinux(t *testing.T) {
	defer saveAndRestoreHooks()()
	runtimeGOOS = func() string { return "darwin" }
	osRemoveFn = func(string) error {
		t.Fatal("remove should not be called on non-linux")
		return nil
	}
	if err := Remove("test.com"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
}

func TestRemoveError(t *testing.T) {
	defer saveAndRestoreHooks()()
	runtimeGOOS = func() string { return "linux" }
	osRemoveFn = func(string) error {
		return fmt.Errorf("remove failed")
	}
	if err := Remove("test.com"); err == nil {
		t.Fatal("expected error from Remove failure")
	}
}

func saveAndRestoreHooks() func() {
	origOsMkdirAll := osMkdirAllFn
	origOsWriteFile := osWriteFileFn
	origOsRemove := osRemoveFn
	origRuntimeGOOS := runtimeGOOS
	return func() {
		osMkdirAllFn = origOsMkdirAll
		osWriteFileFn = origOsWriteFile
		osRemoveFn = origOsRemove
		runtimeGOOS = origRuntimeGOOS
	}
}

func findSuffix(values map[string]string, suffix string) (string, bool) {
	for path, value := range values {
		if strings.HasSuffix(path, suffix) {
			return value, true
		}
	}
	return "", false
}

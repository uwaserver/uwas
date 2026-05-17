package rlimit

import (
	"fmt"
	"os"
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

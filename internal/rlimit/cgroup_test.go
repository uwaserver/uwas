package rlimit

import (
	"fmt"
	"os"
	"runtime"
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
		{"UPPERCASE", "uppercase"},
		{"test@domain.com", "test_domain.com"},
		{"site-with-many-dots.co.uk", "site-with-many-dots.co.uk"},
		{"special#chars*here", "special_chars_here"},
	}
	for _, tt := range tests {
		got := sanitizeDomain(tt.in)
		if got != tt.want {
			t.Errorf("sanitizeDomain(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestApplyNonLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("skip on linux — would create real cgroups")
	}
	path, err := Apply("test.com", Limits{CPUPercent: 50, MemoryMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	if path != "" {
		t.Errorf("non-linux should return empty path, got %q", path)
	}
}

func TestApplyNonLinuxAllLimits(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("skip on linux")
	}
	// Test with all limits set
	path, err := Apply("test.com", Limits{CPUPercent: 100, MemoryMB: 512, PIDMax: 100})
	if err != nil {
		t.Fatal(err)
	}
	if path != "" {
		t.Errorf("non-linux should return empty path, got %q", path)
	}
}

func TestAssignPIDNonLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("skip on linux")
	}
	if err := AssignPID("/some/path", 12345); err != nil {
		t.Fatal(err)
	}
}

func TestAssignPIDEmptyPath(t *testing.T) {
	// Empty path should return nil on all platforms
	if err := AssignPID("", 12345); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveNonLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("skip on linux")
	}
	if err := Remove("test.com"); err != nil {
		t.Fatal(err)
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

func TestApplyPartialLimits(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("skip on linux")
	}
	// Test with only CPU limit
	path, err := Apply("test1.com", Limits{CPUPercent: 50})
	if err != nil {
		t.Fatal(err)
	}
	if path != "" {
		t.Errorf("non-linux should return empty path, got %q", path)
	}

	// Test with only Memory limit
	path, err = Apply("test2.com", Limits{MemoryMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	if path != "" {
		t.Errorf("non-linux should return empty path, got %q", path)
	}

	// Test with only PID limit
	path, err = Apply("test3.com", Limits{PIDMax: 50})
	if err != nil {
		t.Fatal(err)
	}
	if path != "" {
		t.Errorf("non-linux should return empty path, got %q", path)
	}
}

// ---------------------------------------------------------------------------
// Mock hooks for testing Linux behavior
// ---------------------------------------------------------------------------

// saveAndRestoreHooks returns a function that restores the original hook values.
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

// ---------------------------------------------------------------------------
// Linux behavior tests with mocks
// ---------------------------------------------------------------------------

func TestApplyLinux(t *testing.T) {
	defer saveAndRestoreHooks()()

	// Mock Linux platform
	runtimeGOOS = func() string { return "linux" }

	// Track file operations
	mkdirCalls := []string{}
	writeCalls := map[string][]byte{}

	osMkdirAllFn = func(path string, perm os.FileMode) error {
		mkdirCalls = append(mkdirCalls, path)
		return nil
	}

	osWriteFileFn = func(name string, data []byte, perm os.FileMode) error {
		writeCalls[name] = data
		return nil
	}

	path, err := Apply("test.com", Limits{CPUPercent: 50, MemoryMB: 256, PIDMax: 100})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Check path contains expected components (platform independent)
	if !strings.Contains(path, "sys") || !strings.Contains(path, "fs") || !strings.Contains(path, "cgroup") {
		t.Errorf("path = %q, expected to contain cgroup path components", path)
	}

	// Verify cgroup directory was created
	if len(mkdirCalls) != 1 {
		t.Errorf("expected 1 mkdir call, got %d", len(mkdirCalls))
	}

	// Verify all limit files were written (check with platform-independent matching)
	expectedContents := map[string]string{
		"cpu.max":    "50000 100000",
		"memory.max": "268435456",
		"pids.max":   "100",
	}

	for fileSuffix, expectedContent := range expectedContents {
		found := false
		for file, content := range writeCalls {
			if strings.HasSuffix(file, fileSuffix) {
				found = true
				if string(content) != expectedContent {
					t.Errorf("%s content = %q, want %q", fileSuffix, string(content), expectedContent)
				}
				break
			}
		}
		if !found {
			t.Errorf("expected file ending with %s to be written", fileSuffix)
		}
	}
}

func TestApplyLinuxMkdirError(t *testing.T) {
	defer saveAndRestoreHooks()()

	runtimeGOOS = func() string { return "linux" }
	osMkdirAllFn = func(path string, perm os.FileMode) error {
		return fmt.Errorf("permission denied")
	}

	_, err := Apply("test.com", Limits{CPUPercent: 50})
	if err == nil {
		t.Error("expected error for mkdir failure")
	}
	if !strings.Contains(err.Error(), "create cgroup") {
		t.Errorf("error = %v, expected 'create cgroup'", err)
	}
}

func TestApplyLinuxWriteFileError(t *testing.T) {
	defer saveAndRestoreHooks()()

	runtimeGOOS = func() string { return "linux" }
	osMkdirAllFn = func(path string, perm os.FileMode) error { return nil }
	osWriteFileFn = func(name string, data []byte, perm os.FileMode) error {
		return fmt.Errorf("write failed")
	}

	path, err := Apply("test.com", Limits{CPUPercent: 50})
	if err == nil {
		t.Error("expected error for write failure")
	}
	if path == "" {
		t.Error("expected path to be returned even on error")
	}
}

func TestAssignPIDsLinux(t *testing.T) {
	defer saveAndRestoreHooks()()

	runtimeGOOS = func() string { return "linux" }

	writeCalls := []string{}
	osWriteFileFn = func(name string, data []byte, perm os.FileMode) error {
		writeCalls = append(writeCalls, name)
		return nil
	}

	err := AssignPID("/sys/fs/cgroup/uwas/test.com", 12345)
	if err != nil {
		t.Errorf("AssignPID() error = %v", err)
	}

	if len(writeCalls) != 1 {
		t.Errorf("expected 1 write call, got %d", len(writeCalls))
	}
}

func TestRemoveLinux(t *testing.T) {
	defer saveAndRestoreHooks()()

	runtimeGOOS = func() string { return "linux" }

	removeCalls := []string{}
	osRemoveFn = func(name string) error {
		removeCalls = append(removeCalls, name)
		return nil
	}

	err := Remove("test.com")
	if err != nil {
		t.Errorf("Remove() error = %v", err)
	}

	// Check that remove was called with a path containing expected components
	if len(removeCalls) != 1 {
		t.Fatalf("expected 1 remove call, got %d", len(removeCalls))
	}
	if !strings.Contains(removeCalls[0], "test.com") {
		t.Errorf("remove called with %v, expected path containing 'test.com'", removeCalls)
	}
}

func TestRemoveLinuxError(t *testing.T) {
	defer saveAndRestoreHooks()()

	runtimeGOOS = func() string { return "linux" }
	osRemoveFn = func(name string) error {
		return fmt.Errorf("directory not empty")
	}

	err := Remove("test.com")
	if err == nil {
		t.Error("expected error when directory not empty")
	}
}

func TestApplyLinuxOnlyCPU(t *testing.T) {
	defer saveAndRestoreHooks()()

	runtimeGOOS = func() string { return "linux" }

	writeCalls := map[string][]byte{}
	osMkdirAllFn = func(path string, perm os.FileMode) error { return nil }
	osWriteFileFn = func(name string, data []byte, perm os.FileMode) error {
		writeCalls[name] = data
		return nil
	}

	_, err := Apply("cpuonly.com", Limits{CPUPercent: 25})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Find cpu.max file and verify content
	var cpuContent string
	for file, content := range writeCalls {
		if strings.HasSuffix(file, "cpu.max") {
			cpuContent = string(content)
			break
		}
	}

	if cpuContent == "" {
		t.Error("expected cpu.max to be written")
	} else if cpuContent != "25000 100000" {
		t.Errorf("cpu.max = %q, want %q", cpuContent, "25000 100000")
	}

	// Check memory.max was NOT written
	for file := range writeCalls {
		if strings.HasSuffix(file, "memory.max") {
			t.Error("memory.max should not be written when MemoryMB is 0")
		}
	}

	// Check pids.max was NOT written
	for file := range writeCalls {
		if strings.HasSuffix(file, "pids.max") {
			t.Error("pids.max should not be written when PIDMax is 0")
		}
	}
}

func TestApplyLinuxOnlyMemory(t *testing.T) {
	defer saveAndRestoreHooks()()

	runtimeGOOS = func() string { return "linux" }

	writeCalls := map[string][]byte{}
	osMkdirAllFn = func(path string, perm os.FileMode) error { return nil }
	osWriteFileFn = func(name string, data []byte, perm os.FileMode) error {
		writeCalls[name] = data
		return nil
	}

	_, err := Apply("memonly.com", Limits{MemoryMB: 512})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Find memory.max file and verify content
	var memContent string
	for file, content := range writeCalls {
		if strings.HasSuffix(file, "memory.max") {
			memContent = string(content)
			break
		}
	}

	if memContent == "" {
		t.Error("expected memory.max to be written")
	} else if memContent != "536870912" {
		t.Errorf("memory.max = %q, want %q", memContent, "536870912")
	}

	// Check cpu.max was NOT written
	for file := range writeCalls {
		if strings.HasSuffix(file, "cpu.max") {
			t.Error("cpu.max should not be written when CPUPercent is 0")
		}
	}
}

func TestAssignPIDLinuxEmptyPath(t *testing.T) {
	defer saveAndRestoreHooks()()

	runtimeGOOS = func() string { return "linux" }

	// Empty path should return nil even on Linux
	err := AssignPID("", 12345)
	if err != nil {
		t.Error("AssignPID with empty path should return nil")
	}
}

func TestSanitizeDomainSpecialChars(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"test_site.com", "test_site.com"},
		{"UPPERCASE.COM", "uppercase.com"},
		{"MixedCase.COM", "mixedcase.com"},
		{"site-with-dashes.com", "site-with-dashes.com"},
		{"site.with.dots.com", "site.with.dots.com"},
		{"site123.com", "site123.com"},
		{"!@#$%^&*()", "__________"},
		{"test~sub.com", "test_sub.com"},
	}

	for _, tt := range tests {
		got := sanitizeDomain(tt.input)
		if got != tt.expected {
			t.Errorf("sanitizeDomain(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

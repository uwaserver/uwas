package rlimit

import (
	"runtime"
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
	}
	for _, tt := range tests {
		got := sanitizeDomain(tt.in)
		if got != tt.want {
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

func TestAssignPIDNonLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("skip on linux")
	}
	if err := AssignPID("/some/path", 12345); err != nil {
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

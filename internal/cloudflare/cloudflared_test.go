package cloudflare

import (
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

func TestDetectCloudflared_VersionParsing(t *testing.T) {
	orig := execCommandFn
	defer func() { execCommandFn = orig }()
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		return fakeCmd("cloudflared version 2024.10.0 (built 2024-10-09-1820 UTC)")
	}
	// Force a non-empty path: hijack lookup by relying on whatever's on PATH.
	// We just want to exercise the version-string parse — if the binary
	// isn't installed, DetectCloudflared returns Installed=false and our
	// stub never runs. Skip in that case.
	if _, err := exec.LookPath("cloudflared"); err != nil {
		t.Skip("cloudflared not on PATH; version parsing test needs the binary present")
	}
	info := DetectCloudflared()
	if !info.Installed {
		t.Fatalf("expected installed")
	}
	if info.Version != "2024.10.0" {
		t.Fatalf("got version %q, want 2024.10.0", info.Version)
	}
}

func TestInstallCloudflared_NonLinuxRefuses(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("test exercises non-linux refusal path")
	}
	_, err := InstallCloudflared()
	if err == nil || !strings.Contains(err.Error(), "only supported on Linux") {
		t.Fatalf("expected linux-only error, got %v", err)
	}
}

// fakeCmd returns an exec.Cmd whose CombinedOutput returns the supplied text.
// We use `echo` (cross-platform-ish, available on Windows via cmd shell? actually no).
// Use Go's built-in test trick: rely on a real exec that we know prints text.
func fakeCmd(out string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.Command("cmd", "/c", "echo", out)
	}
	return exec.Command("echo", out)
}

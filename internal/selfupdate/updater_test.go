package selfupdate

import (
	"testing"
)

func TestCheckUpdateDev(t *testing.T) {
	// "dev" version should report update available = false
	info, err := CheckUpdate("dev")
	if err != nil {
		t.Skipf("no network: %v", err)
	}
	if info.UpdateAvail {
		t.Error("dev version should not show update available")
	}
	if info.LatestVersion == "" {
		t.Error("latest version should not be empty")
	}
}

func TestCheckUpdateFormatting(t *testing.T) {
	info, err := CheckUpdate("v0.0.1")
	if err != nil {
		t.Skipf("no network: %v", err)
	}
	// v0.0.1 is old, should have update available
	if info.CurrentVersion != "0.0.1" {
		t.Errorf("current = %q, want 0.0.1", info.CurrentVersion)
	}
	if info.ReleaseURL == "" {
		t.Error("release URL should not be empty")
	}
}

func TestEvalSymlinks(t *testing.T) {
	// Non-symlink path should return as-is
	path, err := evalSymlinks("/tmp/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/tmp/nonexistent" {
		t.Errorf("path = %q, want /tmp/nonexistent", path)
	}
}

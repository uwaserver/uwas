package siteuser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAddSSHKeySurfacesOwnershipFailure(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	runtimeGOOS = "linux"
	root := t.TempDir()
	webDir := filepath.Join(root, "example.com", "public_html")
	if err := os.MkdirAll(webDir, 0o755); err != nil {
		t.Fatal(err)
	}
	osMkdirAllFn = os.MkdirAll
	osReadFileFn = os.ReadFile
	osOpenFileFn = os.OpenFile
	execCommandFn = fakeExecCommandChownFail

	err := AddSSHKeyForWebDir(webDir, "example.com", testSSHKey1)
	if err == nil {
		t.Fatal("AddSSHKeyForWebDir reported success after chown failed")
	}
	if !strings.Contains(err.Error(), "chown") {
		t.Fatalf("error = %v, want ownership context", err)
	}
}

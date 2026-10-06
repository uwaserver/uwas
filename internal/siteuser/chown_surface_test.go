package siteuser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCreateUser_ChownFailureSurfaced pins the ownership-fixup contract of
// CreateUserForWebDir: the domain dir MUST end up root:root (sshd rejects
// chroot directories with loose ownership), so a failed chown has to surface
// as an error instead of returning success + credentials for an account that
// cannot work.
func TestCreateUser_ChownFailureSurfaced(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	runtimeGOOS = "linux"
	tmp := t.TempDir()
	osMkdirAllFn = os.MkdirAll
	// chown fails (the domain-dir root:root fixup); everything else succeeds.
	execCommandFn = fakeExecCommandChownFail

	sshdFile := filepath.Join(tmp, "sshd_config")
	if err := os.WriteFile(sshdFile, []byte("# sshd config\nSubsystem sftp /usr/lib/openssh/sftp-server\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sshdConfigPath = sshdFile
	osReadFileFn = os.ReadFile
	osWriteFileFn = os.WriteFile

	u, pass, err := CreateUser(tmp, "example.com")
	if err == nil {
		t.Fatal("expected the failed chown to surface as an error, got success")
	}
	if !strings.Contains(err.Error(), "chown") {
		t.Errorf("error should mention the chown failure, got: %v", err)
	}
	if u != nil {
		t.Error("expected nil user on chown failure")
	}
	if pass != "" {
		t.Error("expected empty password on chown failure")
	}
}

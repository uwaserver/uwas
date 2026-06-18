package siteuser

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// ---------------------------------------------------------------------------
// CreateUserForWebDir: empty web directory
// ---------------------------------------------------------------------------

func TestCreateUserForWebDir_EmptyWebDir(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	runtimeGOOS = "linux"

	u, pass, err := CreateUserForWebDir("   ", "example.com")
	if err == nil {
		t.Fatal("expected error for empty web directory")
	}
	if u != nil || pass != "" {
		t.Fatalf("expected nil user/empty pass, got %v / %q", u, pass)
	}
	if !strings.Contains(err.Error(), "web directory is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// validateSiteHostname: invalid character inside an otherwise-valid label.
// Underscore is not caught by the ContainsAny fast path, so it exercises the
// per-character label loop (manager.go:187-189).
// ---------------------------------------------------------------------------

func TestValidateSiteHostname_InvalidLabelChar(t *testing.T) {
	if err := validateSiteHostname("foo_bar.com"); err == nil {
		t.Fatal("expected underscore in label to be rejected")
	}
	// Sanity: a percent sign too.
	if err := validateSiteHostname("foo%.com"); err == nil {
		t.Fatal("expected '%' in label to be rejected")
	}
}

// ---------------------------------------------------------------------------
// ListUsers: stat succeeds but read fails (manager.go:147-149).
// ---------------------------------------------------------------------------

func TestListUsers_StatOKReadError(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	runtimeGOOS = "linux"
	osStatFn = func(name string) (os.FileInfo, error) {
		return nil, nil // pretend the passwd file exists
	}
	osReadFileFn = func(name string) ([]byte, error) {
		return nil, fmt.Errorf("read denied")
	}

	if users := ListUsers(); users != nil {
		t.Fatalf("expected nil on read error, got %v", users)
	}
}

// ---------------------------------------------------------------------------
// cleanSFTPStartDir: the branches that yield "" (manager.go:312-321).
// ---------------------------------------------------------------------------

func TestCleanSFTPStartDir(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"whitespace_only", "   ", ""},
		{"contains_space", "/foo bar", ""},
		{"contains_tab", "/foo\tbar", ""},
		{"root", "/", ""},
		{"dot", ".", ""},
		{"dotdot", "/..", ""},
		{"no_leading_slash", "demo", "/demo"},
		{"already_clean", "/demo", "/demo"},
		{"needs_clean", "/demo/../demo", "/demo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cleanSFTPStartDir(tt.in)
			if got != tt.want {
				t.Fatalf("cleanSFTPStartDir(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ensureSFTPConfig: managed block present and unchanged AND subsystem already
// configured -> nothing changed -> early return (manager.go:277-279).
// ---------------------------------------------------------------------------

func TestEnsureSFTPConfig_ManagedBlockUnchanged(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	tmp := t.TempDir()
	sshdFile := filepath.Join(tmp, "sshd_config")

	// Build the sshd_config so that replaceManagedSFTPBlock's reconstruction of
	// uwas-a's block is byte-identical to what's on disk (next == content,
	// changed stays false). The block must be delimited by a following managed
	// marker so the scan does not absorb trailing lines.
	block := renderSFTPMatchBlock("uwas-a", "/var/www/a", "/public_html")
	repl := strings.Split(strings.Trim(block, "\n"), "\n")
	pre := []string{"Subsystem sftp internal-sftp"}
	post := []string{"# UWAS SFTP user: uwas-b", "Match User uwas-b", "    ChrootDirectory /b", ""}
	all := append(append(append([]string{}, pre...), repl...), post...)
	if err := os.WriteFile(sshdFile, []byte(strings.Join(all, "\n")), 0644); err != nil {
		t.Fatal(err)
	}
	sshdConfigPath = sshdFile
	osReadFileFn = os.ReadFile

	// The managed block is unchanged and subsystem is already configured, so the
	// early `if !changed { return }` path (manager.go:277-279) must run with no
	// write and no reload.
	wrote := false
	osWriteFileFn = func(name string, data []byte, perm os.FileMode) error {
		wrote = true
		return os.WriteFile(name, data, perm)
	}
	execCommandFn = func(command string, args ...string) *exec.Cmd {
		t.Errorf("reload should not run when nothing changed: %s %v", command, args)
		return fakeExecCommand(command, args...)
	}

	ensureSFTPConfig("uwas-a", "/var/www/a", "/public_html")

	if wrote {
		t.Fatal("expected no write when managed block is unchanged")
	}
}

// ---------------------------------------------------------------------------
// ensureSFTPConfig: write fails in the managed-block-replace branch
// (manager.go:280-282) — also exercises reload fallback when ssh reload fails.
// ---------------------------------------------------------------------------

func TestEnsureSFTPConfig_ManagedBlockWriteError(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	tmp := t.TempDir()
	sshdFile := filepath.Join(tmp, "sshd_config")
	old := "Subsystem sftp internal-sftp\n" +
		"# UWAS SFTP user: uwas-example--com\n" +
		"Match User uwas-example--com\n" +
		"    ChrootDirectory /old/path\n" +
		"    ForceCommand internal-sftp\n" +
		"    AllowTcpForwarding no\n" +
		"    X11Forwarding no\n"
	if err := os.WriteFile(sshdFile, []byte(old), 0644); err != nil {
		t.Fatal(err)
	}
	sshdConfigPath = sshdFile
	osReadFileFn = os.ReadFile

	reloaded := false
	osWriteFileFn = func(name string, data []byte, perm os.FileMode) error {
		reloaded = true
		return fmt.Errorf("write denied")
	}
	execCommandFn = func(command string, args ...string) *exec.Cmd {
		t.Errorf("reload should not be attempted when write fails: %s %v", command, args)
		return fakeExecCommand(command, args...)
	}

	ensureSFTPConfig("uwas-example--com", "/new/path", "/public_html")
	if !reloaded {
		t.Fatal("expected write to be attempted")
	}
}

// ---------------------------------------------------------------------------
// ensureSFTPConfig: managed-block replace succeeds but `systemctl reload ssh`
// fails, triggering the sshd fallback (manager.go:283-285).
// ---------------------------------------------------------------------------

func TestEnsureSFTPConfig_ManagedBlockReloadFallback(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	tmp := t.TempDir()
	sshdFile := filepath.Join(tmp, "sshd_config")
	old := "Subsystem sftp internal-sftp\n" +
		"# UWAS SFTP user: uwas-example--com\n" +
		"Match User uwas-example--com\n" +
		"    ChrootDirectory /old/path\n" +
		"    ForceCommand internal-sftp\n" +
		"    AllowTcpForwarding no\n" +
		"    X11Forwarding no\n"
	if err := os.WriteFile(sshdFile, []byte(old), 0644); err != nil {
		t.Fatal(err)
	}
	sshdConfigPath = sshdFile
	osReadFileFn = os.ReadFile
	osWriteFileFn = os.WriteFile

	var calls []string
	execCommandFn = func(command string, args ...string) *exec.Cmd {
		calls = append(calls, strings.Join(append([]string{command}, args...), " "))
		// "reload ssh" fails, "reload sshd" succeeds.
		if len(args) >= 2 && args[0] == "reload" && args[1] == "ssh" {
			return fakeExecCommandFail(command, args...)
		}
		return fakeExecCommand(command, args...)
	}

	ensureSFTPConfig("uwas-example--com", "/new/path", "/public_html")

	if len(calls) != 2 {
		t.Fatalf("expected ssh+sshd reload attempts, got %v", calls)
	}
	if !strings.Contains(calls[1], "reload sshd") {
		t.Fatalf("expected sshd fallback, got %v", calls)
	}
}

// ---------------------------------------------------------------------------
// ensureSFTPConfig: write fails in the main (add-new-block) branch
// (manager.go:300-302).
// ---------------------------------------------------------------------------

func TestEnsureSFTPConfig_AddBlockWriteError(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	tmp := t.TempDir()
	sshdFile := filepath.Join(tmp, "sshd_config")
	// Subsystem already correct so we only add a Match block (no managed block).
	if err := os.WriteFile(sshdFile, []byte("Subsystem sftp internal-sftp\n"), 0644); err != nil {
		t.Fatal(err)
	}
	sshdConfigPath = sshdFile
	osReadFileFn = os.ReadFile

	osWriteFileFn = func(name string, data []byte, perm os.FileMode) error {
		return fmt.Errorf("write denied")
	}
	execCommandFn = func(command string, args ...string) *exec.Cmd {
		t.Errorf("reload should not run after write failure: %s %v", command, args)
		return fakeExecCommand(command, args...)
	}

	ensureSFTPConfig("uwas-newuser", "/var/www/new", "/public_html")
}

// ---------------------------------------------------------------------------
// replaceManagedSFTPBlock: marker present but NOT followed by the matching
// "Match User" line (manager.go:347-348), and a marker for a different user
// is skipped (manager.go:353-354 break-on-next-marker exercised indirectly).
// ---------------------------------------------------------------------------

func TestReplaceManagedSFTPBlock_MarkerWithoutMatchUser(t *testing.T) {
	content := "# UWAS SFTP user: uwas-example--com\n" +
		"SomethingElse here\n" + // not the expected "Match User" line
		"# end\n"
	block := renderSFTPMatchBlock("uwas-example--com", "/x", "")
	got, ok := replaceManagedSFTPBlock(content, "uwas-example--com", block)
	if ok {
		t.Fatal("expected ok=false when marker is not followed by Match User line")
	}
	if got != content {
		t.Fatal("content should be unchanged")
	}
}

func TestReplaceManagedSFTPBlock_StopsAtNextMarker(t *testing.T) {
	// Two managed blocks back-to-back. Replacing the first must stop scanning at
	// the second block's marker (manager.go:353-354) rather than swallowing it.
	content := "# UWAS SFTP user: uwas-a\n" +
		"Match User uwas-a\n" +
		"    ChrootDirectory /old/a\n" +
		"# UWAS SFTP user: uwas-b\n" +
		"Match User uwas-b\n" +
		"    ChrootDirectory /b\n"
	block := renderSFTPMatchBlock("uwas-a", "/new/a", "")
	got, ok := replaceManagedSFTPBlock(content, "uwas-a", block)
	if !ok {
		t.Fatal("expected replacement to occur")
	}
	if strings.Contains(got, "/old/a") {
		t.Fatalf("old chroot for uwas-a still present:\n%s", got)
	}
	if !strings.Contains(got, "ChrootDirectory /new/a") {
		t.Fatalf("new chroot for uwas-a missing:\n%s", got)
	}
	// The second block must be intact.
	if !strings.Contains(got, "# UWAS SFTP user: uwas-b") || !strings.Contains(got, "ChrootDirectory /b") {
		t.Fatalf("uwas-b block was damaged:\n%s", got)
	}
}

// ---------------------------------------------------------------------------
// AddSSHKeyForWebDir: invalid public key after a successful mkdir
// (sshkey.go:35-37).
// ---------------------------------------------------------------------------

func TestAddSSHKey_InvalidKey(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	runtimeGOOS = "linux"
	osMkdirAllFn = func(path string, perm os.FileMode) error { return nil }

	err := AddSSHKeyForWebDir(filepath.Join("/var/www", "example.com", "public_html"),
		"example.com", "this is not a valid ssh key")
	if err == nil {
		t.Fatal("expected error for invalid SSH public key")
	}
	if !strings.Contains(err.Error(), "invalid SSH public key") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// AddSSHKeyForWebDir: WriteString to authorized_keys fails (sshkey.go:53-55).
// We hand back a read-only file descriptor so the append write errors.
// ---------------------------------------------------------------------------

func TestAddSSHKey_WriteError(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	runtimeGOOS = "linux"
	tmp := t.TempDir()
	osMkdirAllFn = os.MkdirAll
	osReadFileFn = os.ReadFile
	execCommandFn = fakeExecCommand

	// Create a real file we can open read-only; writing to it then fails.
	roPath := filepath.Join(tmp, "readonly_keys")
	if err := os.WriteFile(roPath, []byte(""), 0600); err != nil {
		t.Fatal(err)
	}
	osOpenFileFn = func(name string, flag int, perm os.FileMode) (*os.File, error) {
		return os.OpenFile(roPath, os.O_RDONLY, 0600)
	}

	err := AddSSHKeyForWebDir(filepath.Join(tmp, "example.com", "public_html"),
		"example.com", testSSHKey1)
	if err == nil {
		t.Fatal("expected write error")
	}
	if !strings.Contains(err.Error(), "write SSH key") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// RemoveSSHKeyForWebDir: empty identifier (sshkey.go:70-72).
// ---------------------------------------------------------------------------

func TestRemoveSSHKey_EmptyIdentifier(t *testing.T) {
	err := RemoveSSHKeyForWebDir(filepath.Join("/var/www", "example.com", "public_html"),
		"example.com", "   ")
	if err == nil {
		t.Fatal("expected error for empty key identifier")
	}
	if !strings.Contains(err.Error(), "empty key identifier") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// RemoveSSHKeyForWebDir: match by SHA256 fingerprint (sshkey.go:101-103).
// ---------------------------------------------------------------------------

func TestRemoveSSHKey_ByFingerprint(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	tmp := t.TempDir()
	domain := "example.com"
	sshDir := filepath.Join(tmp, domain, ".ssh")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		t.Fatal(err)
	}
	authKeys := filepath.Join(sshDir, "authorized_keys")
	if err := os.WriteFile(authKeys, []byte(testSSHKey1+"\n"+testSSHKey2+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	osReadFileFn = os.ReadFile
	osWriteFileFn = os.WriteFile

	// Compute the SHA256 fingerprint of key1 and remove by that.
	parsed, _, _, _, err := ssh.ParseAuthorizedKey([]byte(testSSHKey1))
	if err != nil {
		t.Fatalf("parse test key: %v", err)
	}
	fp := ssh.FingerprintSHA256(parsed)

	if err := RemoveSSHKeyForWebDir(filepath.Join(tmp, domain, "public_html"), domain, fp); err != nil {
		t.Fatalf("remove by fingerprint: %v", err)
	}

	data, _ := os.ReadFile(authKeys)
	content := string(data)
	// key1's canonical form should be gone; key2 should remain.
	canon1 := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(parsed)))
	if strings.Contains(content, canon1) {
		t.Errorf("key1 should have been removed by fingerprint:\n%s", content)
	}
	p2, _, _, _, _ := ssh.ParseAuthorizedKey([]byte(testSSHKey2))
	canon2 := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(p2)))
	if !strings.Contains(content, canon2) {
		t.Errorf("key2 should still be present:\n%s", content)
	}
}

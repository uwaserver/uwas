package siteuser

// F40000: CreateUserForWebDir runs `chown -R <user>:www-data` over the whole
// web directory. UWAS writes wp-config.php 0600 (installer, harden, debug
// mode, fix-permissions), so after the owner changes to the site user the file
// is readable only by that user: PHP-FPM runs as www-data (group www-data, not
// owner) and can no longer read its own configuration. Giving the SFTP account
// to a domain that already runs WordPress took the site down. The group read
// bit restores what www-data had before, without opening the file to others.

import (
	"os"
	"path/filepath"
	"testing"
)

func createForWebDir(t *testing.T, webDir string) {
	t.Helper()
	snap := saveHooks()
	t.Cleanup(func() { restoreHooks(snap) })
	runtimeGOOS = "linux"
	execCommandFn = fakeExecCommand
	osMkdirAllFn = os.MkdirAll
	osReadFileFn = os.ReadFile
	osWriteFileFn = os.WriteFile
	sshd := filepath.Join(t.TempDir(), "sshd_config")
	if err := os.WriteFile(sshd, []byte("Subsystem sftp internal-sftp\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sshdConfigPath = sshd
	if _, _, err := CreateUserForWebDir(webDir, "wp.example.com"); err != nil {
		t.Fatalf("CreateUserForWebDir: %v", err)
	}
}

func TestCreateUserForWebDirKeepsWPConfigReadableByWebGroup(t *testing.T) {
	webDir := filepath.Join(t.TempDir(), "wp.example.com", "public_html")
	if err := os.MkdirAll(webDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(webDir, "wp-config.php")
	other := filepath.Join(webDir, "private.env")
	index := filepath.Join(webDir, "index.php")
	for p, m := range map[string]os.FileMode{cfg: 0o600, other: 0o600, index: 0o644} {
		if err := os.WriteFile(p, []byte("x"), m); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, m); err != nil {
			t.Fatal(err)
		}
	}

	createForWebDir(t, webDir)

	fi, err := os.Stat(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("EXPECTED: wp-config.php mode 0640 (group www-data can read)\nACTUAL: %04o", fi.Mode().Perm())
	if fi.Mode().Perm() != 0o640 {
		t.Fatalf("PROBLEM CONFIRMED: wp-config.php is %04o after the owner became the site user; www-data can no longer read it", fi.Mode().Perm())
	}
	// Controls: only wp-config.php is relaxed; the site's other files keep
	// their modes.
	for p, want := range map[string]os.FileMode{other: 0o600, index: 0o644} {
		if got, _ := os.Stat(p); got.Mode().Perm() != want {
			t.Errorf("%s mode = %04o, want %04o", filepath.Base(p), got.Mode().Perm(), want)
		}
	}
}

func TestCreateUserForWebDirWPConfigSymlinkNotFollowed(t *testing.T) {
	root := t.TempDir()
	victim := filepath.Join(root, "victim.conf")
	if err := os.WriteFile(victim, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	webDir := filepath.Join(root, "wp.example.com", "public_html")
	if err := os.MkdirAll(webDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(webDir, "wp-config.php")); err != nil {
		t.Fatal(err)
	}

	createForWebDir(t, webDir)

	if fi, _ := os.Stat(victim); fi.Mode().Perm() != 0o600 {
		t.Fatalf("a symlinked wp-config.php made the target %04o, want 0600", fi.Mode().Perm())
	}
}

func TestCreateUserForWebDirWithoutWPConfig(t *testing.T) {
	webDir := filepath.Join(t.TempDir(), "plain.example.com", "public_html")
	if err := os.MkdirAll(webDir, 0o755); err != nil {
		t.Fatal(err)
	}
	createForWebDir(t, webDir) // must not fail when there is no wp-config.php
}

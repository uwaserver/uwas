package siteuser

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// helpers: save/restore hook globals so tests are isolated
// ---------------------------------------------------------------------------

type hookSnapshot struct {
	goos              string
	execCommand       func(string, ...string) *exec.Cmd
	readFile          func(string) ([]byte, error)
	writeFile         func(string, []byte, os.FileMode) error
	mkdirAll          func(string, os.FileMode) error
	stat              func(string) (os.FileInfo, error)
	openFile          func(string, int, os.FileMode) (*os.File, error)
	sshdConfig        string
	passwd            string
	netInterfaceAddrs func() ([]net.Addr, error)
}

func saveHooks() hookSnapshot {
	return hookSnapshot{
		goos:              runtimeGOOS,
		execCommand:       execCommandFn,
		readFile:          osReadFileFn,
		writeFile:         osWriteFileFn,
		mkdirAll:          osMkdirAllFn,
		stat:              osStatFn,
		openFile:          osOpenFileFn,
		sshdConfig:        sshdConfigPath,
		passwd:            passwdPath,
		netInterfaceAddrs: netInterfaceAddrsFn,
	}
}

func restoreHooks(s hookSnapshot) {
	runtimeGOOS = s.goos
	execCommandFn = s.execCommand
	osReadFileFn = s.readFile
	osWriteFileFn = s.writeFile
	osMkdirAllFn = s.mkdirAll
	osStatFn = s.stat
	osOpenFileFn = s.openFile
	sshdConfigPath = s.sshdConfig
	passwdPath = s.passwd
	netInterfaceAddrsFn = s.netInterfaceAddrs
}

// fakeExecCommand builds a *exec.Cmd that always succeeds (exit 0) without
// actually running the binary. We point it at the test binary itself with a
// sentinel env var; TestHelperProcess handles the dispatch.
func fakeExecCommand(command string, args ...string) *exec.Cmd {
	cs := []string{"-test.run=TestHelperProcess", "--", command}
	cs = append(cs, args...)
	cmd := exec.Command(os.Args[0], cs...)
	cmd.Env = []string{"GO_WANT_HELPER_PROCESS=1"}
	return cmd
}

// fakeExecCommandFail builds a *exec.Cmd that always fails (exit 1).
func fakeExecCommandFail(command string, args ...string) *exec.Cmd {
	cs := []string{"-test.run=TestHelperProcess", "--", command}
	cs = append(cs, args...)
	cmd := exec.Command(os.Args[0], cs...)
	cmd.Env = []string{"GO_WANT_HELPER_PROCESS=1", "GO_HELPER_FAIL=1"}
	return cmd
}



// fakeExecCommandUseraddFailIDFail makes useradd fail AND id fail (user doesn't exist).
func fakeExecCommandUseraddFailIDFail(command string, args ...string) *exec.Cmd {
	cs := []string{"-test.run=TestHelperProcess", "--", command}
	cs = append(cs, args...)
	cmd := exec.Command(os.Args[0], cs...)
	if command == "useradd" || command == "id" {
		cmd.Env = []string{"GO_WANT_HELPER_PROCESS=1", "GO_HELPER_FAIL=1"}
	} else {
		cmd.Env = []string{"GO_WANT_HELPER_PROCESS=1"}
	}
	return cmd
}

// fakeExecCommandChpasswdFail makes chpasswd fail.
func fakeExecCommandChpasswdFail(command string, args ...string) *exec.Cmd {
	cs := []string{"-test.run=TestHelperProcess", "--", command}
	cs = append(cs, args...)
	cmd := exec.Command(os.Args[0], cs...)
	if command == "chpasswd" {
		cmd.Env = []string{"GO_WANT_HELPER_PROCESS=1", "GO_HELPER_FAIL=1"}
	} else {
		cmd.Env = []string{"GO_WANT_HELPER_PROCESS=1"}
	}
	return cmd
}

// fakeExecCommandIDNotExists makes "id" fail (user not found), everything else succeeds.
func fakeExecCommandIDNotExists(command string, args ...string) *exec.Cmd {
	cs := []string{"-test.run=TestHelperProcess", "--", command}
	cs = append(cs, args...)
	cmd := exec.Command(os.Args[0], cs...)
	if command == "id" {
		cmd.Env = []string{"GO_WANT_HELPER_PROCESS=1", "GO_HELPER_FAIL=1"}
	} else {
		cmd.Env = []string{"GO_WANT_HELPER_PROCESS=1"}
	}
	return cmd
}

// TestHelperProcess is the child-side dispatcher for exec.Cmd fakes.
// It is not a real test — it exits immediately when invoked normally.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	if os.Getenv("GO_HELPER_FAIL") == "1" {
		os.Exit(1)
	}
	os.Exit(0)
}

// ---------------------------------------------------------------------------
// Tests: manager.go — PrepareWebRoot
// ---------------------------------------------------------------------------

func TestPrepareWebRoot_Windows(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	runtimeGOOS = "windows"
	tmp := t.TempDir()
	osMkdirAllFn = os.MkdirAll

	dir, err := PrepareWebRoot(tmp, "example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := filepath.Join(tmp, "example.com")
	if dir != expected {
		t.Errorf("got dir %q, want %q", dir, expected)
	}
}

func TestPrepareWebRoot_Success(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	runtimeGOOS = "linux"
	tmp := t.TempDir()
	osMkdirAllFn = os.MkdirAll
	execCommandFn = fakeExecCommand // chown/chmod are no-ops

	dir, err := PrepareWebRoot(tmp, "example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := filepath.Join(tmp, "example.com", "public_html")
	if dir != expected {
		t.Errorf("got dir %q, want %q", dir, expected)
	}
	// Verify directory was created
	if _, err := os.Stat(expected); os.IsNotExist(err) {
		t.Error("public_html directory was not created")
	}
}

func TestPrepareWebRoot_MkdirFail(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	runtimeGOOS = "linux"
	osMkdirAllFn = func(path string, perm os.FileMode) error {
		return fmt.Errorf("mkdir fail")
	}

	_, err := PrepareWebRoot("/nonexistent", "example.com")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "create web root") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Tests: manager.go — CreateUser
// ---------------------------------------------------------------------------

func TestCreateUser_Windows(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	runtimeGOOS = "windows"

	u, pass, err := CreateUser("/var/www", "example.com")
	if err == nil {
		t.Fatal("expected error on Windows")
	}
	if u != nil {
		t.Error("expected nil user on Windows")
	}
	if pass != "" {
		t.Error("expected empty password on Windows")
	}
	if !strings.Contains(err.Error(), "not supported on Windows") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestCreateUser_Success(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	runtimeGOOS = "linux"
	tmp := t.TempDir()
	osMkdirAllFn = os.MkdirAll
	execCommandFn = fakeExecCommand // all commands succeed

	// Set up a fake sshd_config
	sshdFile := filepath.Join(tmp, "sshd_config")
	os.WriteFile(sshdFile, []byte("# sshd config\nSubsystem sftp /usr/lib/openssh/sftp-server\n"), 0644)
	sshdConfigPath = sshdFile
	osReadFileFn = os.ReadFile
	osWriteFileFn = os.WriteFile

	u, pass, err := CreateUser(tmp, "example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u == nil {
		t.Fatal("expected non-nil user")
	}
	if u.Username != "uwas-example--com" {
		t.Errorf("got username %q, want %q", u.Username, "uwas-example--com")
	}
	if u.Domain != "example.com" {
		t.Errorf("got domain %q, want %q", u.Domain, "example.com")
	}
	if pass == "" {
		t.Error("expected non-empty password")
	}
	if len(pass) != 24 {
		t.Errorf("expected 24-char password, got %d", len(pass))
	}

	// Verify sshd_config was updated
	data, _ := os.ReadFile(sshdFile)
	content := string(data)
	if !strings.Contains(content, "Subsystem sftp internal-sftp") {
		t.Error("sshd_config should contain 'Subsystem sftp internal-sftp'")
	}
	if !strings.Contains(content, "Match User uwas-example--com") {
		t.Error("sshd_config should contain Match User block")
	}
}

func TestCreateUser_AlreadyExists(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	runtimeGOOS = "linux"
	tmp := t.TempDir()
	osMkdirAllFn = os.MkdirAll

	// useradd fails (exit 1) but id succeeds (user already exists) —
	// this means CreateUser should proceed past the useradd error.
	var useradded bool
	execCommandFn = func(command string, args ...string) *exec.Cmd {
		cs := []string{"-test.run=TestHelperProcess", "--", command}
		cs = append(cs, args...)
		cmd := exec.Command(os.Args[0], cs...)
		if command == "useradd" {
			useradded = true
			cmd.Env = []string{"GO_WANT_HELPER_PROCESS=1", "GO_HELPER_FAIL=1"}
		} else {
			cmd.Env = []string{"GO_WANT_HELPER_PROCESS=1"}
		}
		return cmd
	}

	sshdFile := filepath.Join(tmp, "sshd_config")
	os.WriteFile(sshdFile, []byte("Subsystem sftp internal-sftp\n"), 0644)
	sshdConfigPath = sshdFile
	osReadFileFn = os.ReadFile
	osWriteFileFn = os.WriteFile

	u, _, err := CreateUser(tmp, "test.org")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !useradded {
		t.Error("expected useradd to be called")
	}
	if u == nil {
		t.Fatal("expected non-nil user")
	}
	if u.Username != "uwas-test--org" {
		t.Errorf("got username %q, want %q", u.Username, "uwas-test--org")
	}
}

func TestCreateUser_UseraddFailAndUserNotExists(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	runtimeGOOS = "linux"
	tmp := t.TempDir()
	osMkdirAllFn = os.MkdirAll
	execCommandFn = fakeExecCommandUseraddFailIDFail

	_, _, err := CreateUser(tmp, "fail.com")
	if err == nil {
		t.Fatal("expected error when useradd fails and user doesn't exist")
	}
	if !strings.Contains(err.Error(), "create user") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCreateUser_ChpasswdFail(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	runtimeGOOS = "linux"
	tmp := t.TempDir()
	osMkdirAllFn = os.MkdirAll
	execCommandFn = fakeExecCommandChpasswdFail

	_, _, err := CreateUser(tmp, "chpass.com")
	if err == nil {
		t.Fatal("expected error when chpasswd fails")
	}
	if !strings.Contains(err.Error(), "set password") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCreateUser_MkdirFail(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	runtimeGOOS = "linux"
	osMkdirAllFn = func(path string, perm os.FileMode) error {
		return fmt.Errorf("mkdir fail")
	}

	_, _, err := CreateUser("/nonexistent", "example.com")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "create directories") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Tests: manager.go — DeleteUser
// ---------------------------------------------------------------------------

func TestDeleteUser_Windows(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	runtimeGOOS = "windows"

	err := DeleteUser("example.com")
	if err != nil {
		t.Fatalf("expected nil error on Windows, got: %v", err)
	}
}

func TestDeleteUser_Success(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	runtimeGOOS = "linux"
	// id succeeds (user exists), userdel succeeds
	execCommandFn = fakeExecCommand

	err := DeleteUser("example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDeleteUser_UserNotExists(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	runtimeGOOS = "linux"
	// id fails (user does not exist) — should return nil
	execCommandFn = fakeExecCommandIDNotExists

	err := DeleteUser("nouser.com")
	if err != nil {
		t.Fatalf("expected nil when user doesn't exist, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Tests: manager.go — ListUsers
// ---------------------------------------------------------------------------

func TestListUsers_Windows(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	runtimeGOOS = "windows"

	users := ListUsers()
	if users != nil {
		t.Errorf("expected nil on Windows, got %v", users)
	}
}

func TestListUsers_Success(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	runtimeGOOS = "linux"
	tmp := t.TempDir()
	passwd := filepath.Join(tmp, "passwd")
	content := `root:x:0:0:root:/root:/bin/bash
daemon:x:1:1:daemon:/usr/sbin:/usr/sbin/nologin
uwas-example--com:x:1001:33::/var/www/example.com:/usr/sbin/nologin
uwas-test--org:x:1002:33::/var/www/test.org:/usr/sbin/nologin
nobody:x:65534:65534:nobody:/nonexistent:/usr/sbin/nologin
`
	os.WriteFile(passwd, []byte(content), 0644)
	passwdPath = passwd
	osReadFileFn = os.ReadFile

	users := ListUsers()
	if len(users) != 2 {
		t.Fatalf("expected 2 users, got %d", len(users))
	}
	if users[0].Username != "uwas-example--com" {
		t.Errorf("users[0].Username = %q, want %q", users[0].Username, "uwas-example--com")
	}
	if users[0].Domain != "example.com" {
		t.Errorf("users[0].Domain = %q, want %q", users[0].Domain, "example.com")
	}
	if users[0].HomeDir != "/var/www/example.com" {
		t.Errorf("users[0].HomeDir = %q, want %q", users[0].HomeDir, "/var/www/example.com")
	}
	if users[0].WebDir != filepath.Join("/var/www/example.com", "public_html") {
		t.Errorf("users[0].WebDir = %q", users[0].WebDir)
	}
	if users[1].Username != "uwas-test--org" {
		t.Errorf("users[1].Username = %q, want %q", users[1].Username, "uwas-test--org")
	}
	if users[1].Domain != "test.org" {
		t.Errorf("users[1].Domain = %q, want %q", users[1].Domain, "test.org")
	}
}

func TestListUsers_Empty(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	runtimeGOOS = "linux"
	tmp := t.TempDir()
	passwd := filepath.Join(tmp, "passwd")
	content := `root:x:0:0:root:/root:/bin/bash
daemon:x:1:1:daemon:/usr/sbin:/usr/sbin/nologin
`
	os.WriteFile(passwd, []byte(content), 0644)
	passwdPath = passwd
	osReadFileFn = os.ReadFile

	users := ListUsers()
	if len(users) != 0 {
		t.Errorf("expected 0 users, got %d", len(users))
	}
}

func TestListUsers_ReadError(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	runtimeGOOS = "linux"
	passwdPath = "/nonexistent/path/passwd"
	osReadFileFn = os.ReadFile

	users := ListUsers()
	if users != nil {
		t.Errorf("expected nil on read error, got %v", users)
	}
}

// ---------------------------------------------------------------------------
// Tests: manager.go — ensureSFTPConfig
// ---------------------------------------------------------------------------

func TestEnsureSFTPConfig_AddsSubsystemAndMatch(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	tmp := t.TempDir()
	sshdFile := filepath.Join(tmp, "sshd_config")
	os.WriteFile(sshdFile, []byte("# basic sshd config\nPort 22\n"), 0644)
	sshdConfigPath = sshdFile
	osReadFileFn = os.ReadFile
	osWriteFileFn = os.WriteFile
	execCommandFn = fakeExecCommand

	ensureSFTPConfig("uwas-example--com", "/var/www/example.com")

	data, err := os.ReadFile(sshdFile)
	if err != nil {
		t.Fatalf("failed to read sshd_config: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "Subsystem sftp internal-sftp") {
		t.Error("expected Subsystem sftp internal-sftp to be added")
	}
	if !strings.Contains(content, "Match User uwas-example--com") {
		t.Error("expected Match User block to be added")
	}
	if !strings.Contains(content, "ChrootDirectory /var/www/example.com") {
		t.Error("expected ChrootDirectory to be set")
	}
}

func TestEnsureSFTPConfig_ReplacesExistingSubsystem(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	tmp := t.TempDir()
	sshdFile := filepath.Join(tmp, "sshd_config")
	os.WriteFile(sshdFile, []byte("Port 22\nSubsystem sftp /usr/lib/openssh/sftp-server\nPasswordAuthentication yes\n"), 0644)
	sshdConfigPath = sshdFile
	osReadFileFn = os.ReadFile
	osWriteFileFn = os.WriteFile
	execCommandFn = fakeExecCommand

	ensureSFTPConfig("uwas-test--org", "/var/www/test.org")

	data, _ := os.ReadFile(sshdFile)
	content := string(data)
	if !strings.Contains(content, "Subsystem sftp internal-sftp") {
		t.Error("expected Subsystem sftp internal-sftp")
	}
	if !strings.Contains(content, "# disabled by UWAS") {
		t.Error("expected old Subsystem line to be commented out")
	}
}

func TestEnsureSFTPConfig_AlreadyConfigured(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	tmp := t.TempDir()
	sshdFile := filepath.Join(tmp, "sshd_config")
	original := "Subsystem sftp internal-sftp\nMatch User uwas-example--com\n    ChrootDirectory /var/www/example.com\n"
	os.WriteFile(sshdFile, []byte(original), 0644)
	sshdConfigPath = sshdFile
	osReadFileFn = os.ReadFile

	// Track if write was called (it should NOT be, since nothing changed)
	writeCalled := false
	osWriteFileFn = func(name string, data []byte, perm os.FileMode) error {
		writeCalled = true
		return os.WriteFile(name, data, perm)
	}
	execCommandFn = fakeExecCommand

	ensureSFTPConfig("uwas-example--com", "/var/www/example.com")

	if writeCalled {
		t.Error("expected no write when config already correct")
	}
}

func TestEnsureSFTPConfig_ReadError(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	sshdConfigPath = "/nonexistent/sshd_config"
	osReadFileFn = os.ReadFile

	// Should not panic — just return early
	ensureSFTPConfig("uwas-test--org", "/var/www/test.org")
}

func TestEnsureSFTPConfig_SshdReloadFallback(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	tmp := t.TempDir()
	sshdFile := filepath.Join(tmp, "sshd_config")
	os.WriteFile(sshdFile, []byte("# empty\n"), 0644)
	sshdConfigPath = sshdFile
	osReadFileFn = os.ReadFile
	osWriteFileFn = os.WriteFile

	// Track systemctl calls: "ssh" should fail, then "sshd" should be tried
	var reloadCmds []string
	execCommandFn = func(command string, args ...string) *exec.Cmd {
		if command == "systemctl" && len(args) >= 2 {
			reloadCmds = append(reloadCmds, args[1])
		}
		cs := []string{"-test.run=TestHelperProcess", "--", command}
		cs = append(cs, args...)
		cmd := exec.Command(os.Args[0], cs...)
		if command == "systemctl" && len(args) >= 2 && args[1] == "ssh" {
			cmd.Env = []string{"GO_WANT_HELPER_PROCESS=1", "GO_HELPER_FAIL=1"}
		} else {
			cmd.Env = []string{"GO_WANT_HELPER_PROCESS=1"}
		}
		return cmd
	}

	ensureSFTPConfig("uwas-test--org", "/var/www/test.org")

	if len(reloadCmds) < 2 {
		t.Fatalf("expected at least 2 reload attempts, got %d: %v", len(reloadCmds), reloadCmds)
	}
	if reloadCmds[0] != "ssh" {
		t.Errorf("first reload should try 'ssh', got %q", reloadCmds[0])
	}
	if reloadCmds[1] != "sshd" {
		t.Errorf("second reload should try 'sshd', got %q", reloadCmds[1])
	}
}

// ---------------------------------------------------------------------------
// Tests: sshkey.go — AddSSHKey
// ---------------------------------------------------------------------------

func TestAddSSHKey_Windows(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	runtimeGOOS = "windows"

	err := AddSSHKey("/var/www", "example.com", "ssh-rsa AAAA...")
	if err == nil {
		t.Fatal("expected error on Windows")
	}
	if !strings.Contains(err.Error(), "not supported on Windows") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestAddSSHKey_Success(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	runtimeGOOS = "linux"
	tmp := t.TempDir()
	osMkdirAllFn = os.MkdirAll
	osReadFileFn = os.ReadFile
	osOpenFileFn = os.OpenFile
	execCommandFn = fakeExecCommand // chown no-ops

	key := "ssh-rsa AAAAB3NzaC1yc2EAAA test@host"
	err := AddSSHKey(tmp, "example.com", key)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify key was written
	authKeys := filepath.Join(tmp, "example.com", ".ssh", "authorized_keys")
	data, err := os.ReadFile(authKeys)
	if err != nil {
		t.Fatalf("failed to read authorized_keys: %v", err)
	}
	if !strings.Contains(string(data), key) {
		t.Error("authorized_keys should contain the added key")
	}
}

func TestAddSSHKey_Duplicate(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	runtimeGOOS = "linux"
	tmp := t.TempDir()
	osMkdirAllFn = os.MkdirAll
	osReadFileFn = os.ReadFile
	osOpenFileFn = os.OpenFile
	execCommandFn = fakeExecCommand

	key := "ssh-rsa AAAAB3NzaC1yc2EAAA test@host"

	// Add key first time
	err := AddSSHKey(tmp, "example.com", key)
	if err != nil {
		t.Fatalf("first add failed: %v", err)
	}

	// Add same key again — should return nil without duplicating
	err = AddSSHKey(tmp, "example.com", key)
	if err != nil {
		t.Fatalf("duplicate add returned error: %v", err)
	}

	// Verify key appears only once
	authKeys := filepath.Join(tmp, "example.com", ".ssh", "authorized_keys")
	data, _ := os.ReadFile(authKeys)
	count := strings.Count(string(data), key)
	if count != 1 {
		t.Errorf("key should appear exactly once, found %d times", count)
	}
}

func TestAddSSHKey_MkdirFail(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	runtimeGOOS = "linux"
	osMkdirAllFn = func(path string, perm os.FileMode) error {
		return fmt.Errorf("mkdir fail")
	}

	err := AddSSHKey("/var/www", "example.com", "ssh-rsa AAAA...")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "create .ssh") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestAddSSHKey_MultipleKeys(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	runtimeGOOS = "linux"
	tmp := t.TempDir()
	osMkdirAllFn = os.MkdirAll
	osReadFileFn = os.ReadFile
	osOpenFileFn = os.OpenFile
	execCommandFn = fakeExecCommand

	key1 := "ssh-rsa AAAAB3NzaC1yc2EAAA user1@host"
	key2 := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5 user2@host"

	AddSSHKey(tmp, "example.com", key1)
	AddSSHKey(tmp, "example.com", key2)

	authKeys := filepath.Join(tmp, "example.com", ".ssh", "authorized_keys")
	data, _ := os.ReadFile(authKeys)
	content := string(data)
	if !strings.Contains(content, key1) {
		t.Error("should contain key1")
	}
	if !strings.Contains(content, key2) {
		t.Error("should contain key2")
	}
}

// ---------------------------------------------------------------------------
// Tests: sshkey.go — RemoveSSHKey
// ---------------------------------------------------------------------------

func TestRemoveSSHKey_Success(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	tmp := t.TempDir()
	domain := "example.com"
	sshDir := filepath.Join(tmp, domain, ".ssh")
	os.MkdirAll(sshDir, 0700)
	authKeys := filepath.Join(sshDir, "authorized_keys")

	key1 := "ssh-rsa AAAAB3NzaC1yc2EAAA user1@host"
	key2 := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5 user2@host"
	os.WriteFile(authKeys, []byte(key1+"\n"+key2+"\n"), 0600)

	osReadFileFn = os.ReadFile
	osWriteFileFn = os.WriteFile

	// Remove key1 by matching a substring (fingerprint-like)
	err := RemoveSSHKey(tmp, domain, "AAAAB3NzaC1yc2EAAA")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(authKeys)
	content := string(data)
	if strings.Contains(content, key1) {
		t.Error("key1 should have been removed")
	}
	if !strings.Contains(content, key2) {
		t.Error("key2 should still be present")
	}
}

func TestRemoveSSHKey_NotFound(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	tmp := t.TempDir()
	domain := "example.com"
	sshDir := filepath.Join(tmp, domain, ".ssh")
	os.MkdirAll(sshDir, 0700)
	authKeys := filepath.Join(sshDir, "authorized_keys")

	key1 := "ssh-rsa AAAAB3NzaC1yc2EAAA user1@host"
	os.WriteFile(authKeys, []byte(key1+"\n"), 0600)

	osReadFileFn = os.ReadFile
	osWriteFileFn = os.WriteFile

	err := RemoveSSHKey(tmp, domain, "nonexistent-fingerprint")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Key should still be there
	data, _ := os.ReadFile(authKeys)
	if !strings.Contains(string(data), key1) {
		t.Error("key1 should still be present")
	}
}

func TestRemoveSSHKey_NoFile(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	osReadFileFn = os.ReadFile

	// No authorized_keys file — should return nil
	err := RemoveSSHKey("/nonexistent", "example.com", "fingerprint")
	if err != nil {
		t.Fatalf("expected nil when no keys file, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Tests: sshkey.go — ListSSHKeys
// ---------------------------------------------------------------------------

func TestListSSHKeys_Success(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	tmp := t.TempDir()
	domain := "example.com"
	sshDir := filepath.Join(tmp, domain, ".ssh")
	os.MkdirAll(sshDir, 0700)
	authKeys := filepath.Join(sshDir, "authorized_keys")

	key1 := "ssh-rsa AAAAB3NzaC1yc2EAAA user1@host"
	key2 := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5 user2@host"
	os.WriteFile(authKeys, []byte("# comment line\n"+key1+"\n"+key2+"\n\n"), 0600)

	osReadFileFn = os.ReadFile

	keys := ListSSHKeys(tmp, domain)
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(keys))
	}
	if keys[0] != key1 {
		t.Errorf("keys[0] = %q, want %q", keys[0], key1)
	}
	if keys[1] != key2 {
		t.Errorf("keys[1] = %q, want %q", keys[1], key2)
	}
}

func TestListSSHKeys_Empty(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	tmp := t.TempDir()
	domain := "example.com"
	sshDir := filepath.Join(tmp, domain, ".ssh")
	os.MkdirAll(sshDir, 0700)
	authKeys := filepath.Join(sshDir, "authorized_keys")
	os.WriteFile(authKeys, []byte(""), 0600)

	osReadFileFn = os.ReadFile

	keys := ListSSHKeys(tmp, domain)
	if len(keys) != 0 {
		t.Errorf("expected 0 keys, got %d", len(keys))
	}
}

func TestListSSHKeys_NoFile(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	osReadFileFn = os.ReadFile

	keys := ListSSHKeys("/nonexistent", "example.com")
	if keys != nil {
		t.Errorf("expected nil when no file, got %v", keys)
	}
}

func TestListSSHKeys_Windows(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	// ListSSHKeys doesn't have a Windows guard — it just reads files.
	// On a non-existent path it returns nil.
	osReadFileFn = func(name string) ([]byte, error) {
		return nil, fmt.Errorf("not found")
	}

	keys := ListSSHKeys("/var/www", "example.com")
	if keys != nil {
		t.Errorf("expected nil, got %v", keys)
	}
}

// ---------------------------------------------------------------------------
// Tests: manager.go — UserStruct fields
// ---------------------------------------------------------------------------

func TestUserStruct(t *testing.T) {
	u := User{
		Username: "uwas-example--com",
		Domain:   "example.com",
		HomeDir:  "/var/www/example.com",
		WebDir:   "/var/www/example.com/public_html",
	}

	if u.Username != "uwas-example--com" {
		t.Errorf("expected Username 'uwas-example--com', got %q", u.Username)
	}
	if u.Domain != "example.com" {
		t.Errorf("expected Domain 'example.com', got %q", u.Domain)
	}
	if u.HomeDir != "/var/www/example.com" {
		t.Errorf("expected HomeDir '/var/www/example.com', got %q", u.HomeDir)
	}
	if u.WebDir != "/var/www/example.com/public_html" {
		t.Errorf("expected WebDir '/var/www/example.com/public_html', got %q", u.WebDir)
	}
}

// ---------------------------------------------------------------------------
// Tests: manager.go — domainToUsername (kept from original)
// ---------------------------------------------------------------------------

func TestDomainToUsername(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"example.com", "uwas-example--com"},
		{"www.example.com", "uwas-www--example--com"},
		{"a.b.c", "uwas-a--b--c"},
		{"EXAMPLE.COM", "uwas-example--com"},
		{"short", "uwas-short"},
	}

	for _, tt := range tests {
		got := domainToUsername(tt.input)
		if got != tt.want {
			t.Errorf("domainToUsername(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestDomainToUsernameTruncation(t *testing.T) {
	long := "very-long-subdomain.example.com"
	got := domainToUsername(long)
	if len(got) > 32 {
		t.Errorf("domainToUsername(%q) length = %d, want <= 32", long, len(got))
	}
	if got[:5] != "uwas-" {
		t.Errorf("domainToUsername result should start with 'uwas-', got %q", got)
	}
}

// ---------------------------------------------------------------------------
// Tests: manager.go — generatePassword (kept from original)
// ---------------------------------------------------------------------------

func TestGeneratePasswordUniqueness(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		p := generatePassword()
		if len(p) == 0 {
			t.Fatal("generatePassword returned empty string")
		}
		if seen[p] {
			t.Fatalf("generatePassword produced duplicate: %q", p)
		}
		seen[p] = true
	}
}

func TestGeneratePasswordLength(t *testing.T) {
	p := generatePassword()
	if len(p) != 24 {
		t.Errorf("expected password length 24, got %d", len(p))
	}
}

// ---------------------------------------------------------------------------
// Tests: manager.go — userExists
// ---------------------------------------------------------------------------

func TestUserExists_True(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	execCommandFn = fakeExecCommand // id succeeds
	if !userExists("testuser") {
		t.Error("expected userExists to return true")
	}
}

func TestUserExists_False(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	execCommandFn = fakeExecCommandFail // id fails
	if userExists("testuser") {
		t.Error("expected userExists to return false")
	}
}

// ---------------------------------------------------------------------------
// Tests: manager.go — GetServerIP
// ---------------------------------------------------------------------------

func TestGetServerIP(t *testing.T) {
	ip := GetServerIP()
	if ip == "" {
		t.Error("expected non-empty IP")
	}
	// On any machine this should return either a real IP or the fallback
	if ip != "your-server-ip" {
		// It returned a real IP — verify it looks like an IP
		if !strings.Contains(ip, ".") {
			t.Errorf("expected an IPv4 address, got %q", ip)
		}
	}
}

func TestGetServerIP_Error(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	netInterfaceAddrsFn = func() ([]net.Addr, error) {
		return nil, fmt.Errorf("network error")
	}

	ip := GetServerIP()
	if ip != "your-server-ip" {
		t.Errorf("expected fallback 'your-server-ip', got %q", ip)
	}
}

// fakeAddr implements net.Addr for testing.
type fakeAddr struct {
	network string
	str     string
}

func (a fakeAddr) Network() string { return a.network }
func (a fakeAddr) String() string  { return a.str }

func TestGetServerIP_OnlyLoopback(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	// Return only loopback addresses — GetServerIP should fall through to fallback
	netInterfaceAddrsFn = func() ([]net.Addr, error) {
		return []net.Addr{
			&net.IPNet{IP: net.IPv4(127, 0, 0, 1), Mask: net.CIDRMask(8, 32)},
		}, nil
	}

	ip := GetServerIP()
	if ip != "your-server-ip" {
		t.Errorf("expected fallback 'your-server-ip', got %q", ip)
	}
}

func TestGetServerIP_OnlyIPv6(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	// Return only an IPv6 address — To4() returns nil so should fall through
	netInterfaceAddrsFn = func() ([]net.Addr, error) {
		return []net.Addr{
			&net.IPNet{IP: net.ParseIP("::1"), Mask: net.CIDRMask(128, 128)},
			&net.IPNet{IP: net.ParseIP("fe80::1"), Mask: net.CIDRMask(64, 128)},
		}, nil
	}

	ip := GetServerIP()
	if ip != "your-server-ip" {
		t.Errorf("expected fallback 'your-server-ip', got %q", ip)
	}
}

func TestGetServerIP_NonIPNetAddr(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	// Return a non-*net.IPNet addr — the type assertion should fail, skip it
	netInterfaceAddrsFn = func() ([]net.Addr, error) {
		return []net.Addr{
			fakeAddr{network: "tcp", str: "192.168.1.1:80"},
		}, nil
	}

	ip := GetServerIP()
	if ip != "your-server-ip" {
		t.Errorf("expected fallback 'your-server-ip', got %q", ip)
	}
}

func TestGetServerIP_ValidIPv4(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	netInterfaceAddrsFn = func() ([]net.Addr, error) {
		return []net.Addr{
			&net.IPNet{IP: net.IPv4(127, 0, 0, 1), Mask: net.CIDRMask(8, 32)},
			&net.IPNet{IP: net.IPv4(10, 0, 0, 5), Mask: net.CIDRMask(24, 32)},
		}, nil
	}

	ip := GetServerIP()
	if ip != "10.0.0.5" {
		t.Errorf("expected '10.0.0.5', got %q", ip)
	}
}

// ---------------------------------------------------------------------------
// Tests: sshkey.go — AddSSHKey OpenFile error
// ---------------------------------------------------------------------------

func TestAddSSHKey_OpenFileFail(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	runtimeGOOS = "linux"
	tmp := t.TempDir()
	osMkdirAllFn = os.MkdirAll
	osReadFileFn = os.ReadFile
	// Make OpenFile fail
	osOpenFileFn = func(name string, flag int, perm os.FileMode) (*os.File, error) {
		return nil, fmt.Errorf("open fail")
	}

	err := AddSSHKey(tmp, "example.com", "ssh-rsa AAAA test@host")
	if err == nil {
		t.Fatal("expected error when OpenFile fails")
	}
	if !strings.Contains(err.Error(), "open fail") {
		t.Errorf("unexpected error: %v", err)
	}
}

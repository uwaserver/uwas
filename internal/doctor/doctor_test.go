package doctor

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ── Test helpers ─────────────────────────────────────────

// saveAndRestoreHooks saves all hook variables and restores them on t.Cleanup.
func saveAndRestoreHooks(t *testing.T) {
	t.Helper()
	origGOOS := runtimeGOOS
	origGOARCH := runtimeGOARCH
	origExecCommand := execCommandFn
	origExecLookPath := execLookPathFn
	origNetListen := netListenFn
	origNetDialTimeout := netDialTimeoutFn
	origNetLookupHost := netLookupHostFn
	origOsStat := osStatFn
	origOsReadDir := osReadDirFn
	origOsReadFile := osReadFileFn
	origOsMkdirAll := osMkdirAllFn
	origTimeSleep := timeSleepFn
	t.Cleanup(func() {
		runtimeGOOS = origGOOS
		runtimeGOARCH = origGOARCH
		execCommandFn = origExecCommand
		execLookPathFn = origExecLookPath
		netListenFn = origNetListen
		netDialTimeoutFn = origNetDialTimeout
		netLookupHostFn = origNetLookupHost
		osStatFn = origOsStat
		osReadDirFn = origOsReadDir
		osReadFileFn = origOsReadFile
		osMkdirAllFn = origOsMkdirAll
		timeSleepFn = origTimeSleep
	})
}

// fakeCmd returns a *exec.Cmd that immediately succeeds (exits 0) with given stdout.
// It uses the TestHelperProcess pattern.
func fakeCmd(stdout string) func(string, ...string) *exec.Cmd {
	return func(name string, args ...string) *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--")
		cmd.Env = append(os.Environ(), "GO_TEST_HELPER=1", "GO_TEST_STDOUT="+stdout)
		return cmd
	}
}

// fakeCmdRouter routes by the command name and returns different output for different commands.
func fakeCmdRouter(routes map[string]string) func(string, ...string) *exec.Cmd {
	return func(name string, args ...string) *exec.Cmd {
		// Build a key from the command + first arg for more specific matching
		key := filepath.Base(name)
		if len(args) > 0 {
			key = key + " " + args[0]
		}
		stdout := ""
		if v, ok := routes[key]; ok {
			stdout = v
		} else if v, ok := routes[filepath.Base(name)]; ok {
			stdout = v
		}
		cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--")
		cmd.Env = append(os.Environ(), "GO_TEST_HELPER=1", "GO_TEST_STDOUT="+stdout)
		return cmd
	}
}

// fakeCmdFail returns a *exec.Cmd that will fail (exit 1).
func fakeCmdFail() func(string, ...string) *exec.Cmd {
	return func(name string, args ...string) *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--")
		cmd.Env = append(os.Environ(), "GO_TEST_HELPER=1", "GO_TEST_EXIT=1")
		return cmd
	}
}

// fakeCmdRouterWithFail routes by command, some succeed and some fail.
func fakeCmdRouterWithFail(routes map[string]string, failKeys map[string]bool) func(string, ...string) *exec.Cmd {
	return func(name string, args ...string) *exec.Cmd {
		key := filepath.Base(name)
		if len(args) > 0 {
			key = key + " " + args[0]
		}
		// Check fail keys
		if failKeys[key] || failKeys[filepath.Base(name)] {
			cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--")
			cmd.Env = append(os.Environ(), "GO_TEST_HELPER=1", "GO_TEST_EXIT=1")
			return cmd
		}
		stdout := ""
		if v, ok := routes[key]; ok {
			stdout = v
		} else if v, ok := routes[filepath.Base(name)]; ok {
			stdout = v
		}
		cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--")
		cmd.Env = append(os.Environ(), "GO_TEST_HELPER=1", "GO_TEST_STDOUT="+stdout)
		return cmd
	}
}

// TestHelperProcess is used by fakeCmd to simulate command execution.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_TEST_HELPER") != "1" {
		return
	}
	if os.Getenv("GO_TEST_EXIT") == "1" {
		os.Exit(1)
	}
	fmt.Fprint(os.Stdout, os.Getenv("GO_TEST_STDOUT"))
	os.Exit(0)
}

// fakeLookPath returns a function that succeeds for names in found, fails for others.
func fakeLookPath(found map[string]bool) func(string) (string, error) {
	return func(name string) (string, error) {
		if found[name] {
			return "/usr/bin/" + name, nil
		}
		return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
	}
}

// fakeStat returns a function that succeeds for paths in existing, not-exist for others.
func fakeStat(existing map[string]bool) func(string) (os.FileInfo, error) {
	return func(name string) (os.FileInfo, error) {
		if existing[name] {
			return fakeFileInfo{name: filepath.Base(name), isDir: true}, nil
		}
		return nil, &os.PathError{Op: "stat", Path: name, Err: os.ErrNotExist}
	}
}

type fakeFileInfo struct {
	name  string
	isDir bool
}

func (f fakeFileInfo) Name() string      { return f.name }
func (f fakeFileInfo) Size() int64       { return 0 }
func (f fakeFileInfo) Mode() fs.FileMode { return 0755 }
func (f fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (f fakeFileInfo) IsDir() bool       { return f.isDir }
func (f fakeFileInfo) Sys() interface{}  { return nil }

// fakeListener implements net.Listener for testing.
type fakeListener struct{}

func (fakeListener) Accept() (net.Conn, error) { return nil, nil }
func (fakeListener) Close() error              { return nil }
func (fakeListener) Addr() net.Addr            { return fakeAddr{} }

type fakeAddr struct{}

func (fakeAddr) Network() string { return "tcp" }
func (fakeAddr) String() string  { return "0.0.0.0:0" }

// fakeConn implements net.Conn for testing.
type fakeConn struct{}

func (fakeConn) Read([]byte) (int, error)         { return 0, nil }
func (fakeConn) Write([]byte) (int, error)        { return 0, nil }
func (fakeConn) Close() error                     { return nil }
func (fakeConn) LocalAddr() net.Addr              { return fakeAddr{} }
func (fakeConn) RemoteAddr() net.Addr             { return fakeAddr{} }
func (fakeConn) SetDeadline(time.Time) error      { return nil }
func (fakeConn) SetReadDeadline(time.Time) error  { return nil }
func (fakeConn) SetWriteDeadline(time.Time) error { return nil }

// fakeDirEntry implements fs.DirEntry for testing.
type fakeDirEntry struct {
	name  string
	isDir bool
}

func (f fakeDirEntry) Name() string               { return f.name }
func (f fakeDirEntry) IsDir() bool                { return f.isDir }
func (f fakeDirEntry) Type() fs.FileMode          { return 0 }
func (f fakeDirEntry) Info() (fs.FileInfo, error)  { return fakeFileInfo{name: f.name}, nil }

// ── Run() tests ──────────────────────────────────────────

func TestRun_BasicOptions(t *testing.T) {
	saveAndRestoreHooks(t)
	setupAllMocksDefault(t)

	r := Run(Options{})
	if r == nil {
		t.Fatal("Run returned nil")
	}
	if len(r.Checks) != 15 {
		t.Errorf("expected 15 checks, got %d", len(r.Checks))
	}
	if r.Summary == "" {
		t.Error("summary is empty")
	}
	if !strings.Contains(r.Summary, "ok") {
		t.Errorf("summary should contain 'ok': %s", r.Summary)
	}
}

func TestRun_WithAutoFix(t *testing.T) {
	saveAndRestoreHooks(t)
	setupAllMocksDefault(t)

	r := Run(Options{AutoFix: true})
	if r == nil {
		t.Fatal("Run returned nil")
	}
	if len(r.Checks) != 15 {
		t.Errorf("expected 15 checks, got %d", len(r.Checks))
	}
	// Should have summary with auto-fixed count
	if !strings.Contains(r.Summary, "auto-fixed") {
		t.Errorf("summary should mention auto-fixed: %s", r.Summary)
	}
}

// setupAllMocksDefault sets up all mocks so Run() won't touch real system.
func setupAllMocksDefault(t *testing.T) {
	t.Helper()
	runtimeGOOS = "linux"
	runtimeGOARCH = "amd64"

	// net.Listen: succeed (ports available)
	netListenFn = func(network, address string) (net.Listener, error) {
		return fakeListener{}, nil
	}

	// net.DialTimeout: fail (no php-fpm sockets)
	netDialTimeoutFn = func(network, address string, timeout time.Duration) (net.Conn, error) {
		return nil, errors.New("connection refused")
	}

	// net.LookupHost: succeed
	netLookupHostFn = func(host string) ([]string, error) {
		return []string{"1.2.3.4"}, nil
	}

	// os.Stat: nothing exists
	osStatFn = func(name string) (os.FileInfo, error) {
		return nil, &os.PathError{Op: "stat", Path: name, Err: os.ErrNotExist}
	}

	// os.ReadDir: empty
	osReadDirFn = func(name string) ([]fs.DirEntry, error) {
		return nil, &os.PathError{Op: "readdir", Path: name, Err: os.ErrNotExist}
	}

	// os.ReadFile: fail
	osReadFileFn = func(name string) ([]byte, error) {
		return nil, &os.PathError{Op: "readfile", Path: name, Err: os.ErrNotExist}
	}

	// os.MkdirAll: succeed
	osMkdirAllFn = func(path string, perm os.FileMode) error {
		return nil
	}

	// exec.Command: always succeed with empty output
	execCommandFn = fakeCmd("")

	// exec.LookPath: nothing found
	execLookPathFn = fakeLookPath(map[string]bool{})

	// time.Sleep: no-op
	timeSleepFn = func(d time.Duration) {}
}

// ── checkOS tests ────────────────────────────────────────

func TestCheckOS_Linux(t *testing.T) {
	saveAndRestoreHooks(t)
	runtimeGOOS = "linux"
	runtimeGOARCH = "amd64"

	c := checkOS()
	if c.Status != StatusOK {
		t.Errorf("expected StatusOK, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "Linux amd64") {
		t.Errorf("unexpected message: %s", c.Message)
	}
}

func TestCheckOS_NonLinux(t *testing.T) {
	saveAndRestoreHooks(t)
	runtimeGOOS = "windows"
	runtimeGOARCH = "amd64"

	c := checkOS()
	if c.Status != StatusWarn {
		t.Errorf("expected StatusWarn, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "windows/amd64") {
		t.Errorf("unexpected message: %s", c.Message)
	}
}

// ── checkPorts tests ─────────────────────────────────────

func TestCheckPorts_Available(t *testing.T) {
	saveAndRestoreHooks(t)
	netListenFn = func(network, address string) (net.Listener, error) {
		return fakeListener{}, nil
	}

	c := checkPorts()
	if c.Status != StatusOK {
		t.Errorf("expected StatusOK, got %s", c.Status)
	}
	if c.Message != "Available" {
		t.Errorf("unexpected message: %s", c.Message)
	}
}

func TestCheckPorts_InUse(t *testing.T) {
	saveAndRestoreHooks(t)
	netListenFn = func(network, address string) (net.Listener, error) {
		return nil, fmt.Errorf("listen tcp %s: bind: address already in use", address)
	}

	c := checkPorts()
	if c.Status != StatusWarn {
		t.Errorf("expected StatusWarn, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "port :80 in use") {
		t.Errorf("should report port 80 in use: %s", c.Message)
	}
	if !strings.Contains(c.Message, "port :443 in use") {
		t.Errorf("should report port 443 in use: %s", c.Message)
	}
}

func TestCheckPorts_ErrorWithoutBindKeyword(t *testing.T) {
	saveAndRestoreHooks(t)
	// Error that doesn't contain "address already in use" or "bind"
	netListenFn = func(network, address string) (net.Listener, error) {
		return nil, fmt.Errorf("permission denied")
	}

	c := checkPorts()
	// Should be OK because the error doesn't match the bind pattern
	if c.Status != StatusOK {
		t.Errorf("expected StatusOK (non-bind error), got %s", c.Status)
	}
}

// ── checkPHPFPM tests ────────────────────────────────────

func TestCheckPHPFPM_Running(t *testing.T) {
	saveAndRestoreHooks(t)
	osStatFn = fakeStat(map[string]bool{"/run/php/php8.3-fpm.sock": true})
	netDialTimeoutFn = func(network, address string, timeout time.Duration) (net.Conn, error) {
		return fakeConn{}, nil
	}
	execLookPathFn = fakeLookPath(map[string]bool{})
	execCommandFn = fakeCmd("")

	c := checkPHPFPM(false)
	if c.Status != StatusOK {
		t.Errorf("expected StatusOK, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "/run/php/php8.3-fpm.sock") {
		t.Errorf("unexpected message: %s", c.Message)
	}
}

func TestCheckPHPFPM_NotRunning_NoSocket(t *testing.T) {
	saveAndRestoreHooks(t)
	osStatFn = fakeStat(map[string]bool{}) // no sockets exist
	execLookPathFn = fakeLookPath(map[string]bool{})
	execCommandFn = fakeCmd("")
	netDialTimeoutFn = func(network, address string, timeout time.Duration) (net.Conn, error) {
		return nil, errors.New("connection refused")
	}

	c := checkPHPFPM(false)
	if c.Status != StatusFail {
		t.Errorf("expected StatusFail, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "No PHP-FPM or PHP-CGI found") {
		t.Errorf("unexpected message: %s", c.Message)
	}
}

func TestCheckPHPFPM_SocketExistsNotListening_NoAutoFix(t *testing.T) {
	saveAndRestoreHooks(t)
	osStatFn = fakeStat(map[string]bool{"/run/php/php8.3-fpm.sock": true})
	netDialTimeoutFn = func(network, address string, timeout time.Duration) (net.Conn, error) {
		return nil, errors.New("connection refused")
	}
	execLookPathFn = fakeLookPath(map[string]bool{})
	execCommandFn = fakeCmd("")

	c := checkPHPFPM(false)
	if c.Status != StatusFail {
		t.Errorf("expected StatusFail, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "exists but not listening") {
		t.Errorf("unexpected message: %s", c.Message)
	}
}

func TestCheckPHPFPM_AutoFix_SocketRestart_Success(t *testing.T) {
	saveAndRestoreHooks(t)
	osStatFn = fakeStat(map[string]bool{"/run/php/php8.3-fpm.sock": true})
	dialCount := 0
	netDialTimeoutFn = func(network, address string, timeout time.Duration) (net.Conn, error) {
		dialCount++
		if dialCount == 1 {
			return nil, errors.New("connection refused") // first try fails
		}
		return fakeConn{}, nil // after restart succeeds
	}
	execCommandFn = fakeCmd("")
	execLookPathFn = fakeLookPath(map[string]bool{})

	c := checkPHPFPM(true)
	if c.Status != StatusFixed {
		t.Errorf("expected StatusFixed, got %s", c.Status)
	}
	if !strings.Contains(c.Fix, "systemctl start php8.3-fpm") {
		t.Errorf("unexpected fix: %s", c.Fix)
	}
}

func TestCheckPHPFPM_AutoFix_SocketRestart_Failure(t *testing.T) {
	saveAndRestoreHooks(t)
	osStatFn = fakeStat(map[string]bool{"/run/php/php8.3-fpm.sock": true})
	netDialTimeoutFn = func(network, address string, timeout time.Duration) (net.Conn, error) {
		return nil, errors.New("connection refused") // always fails
	}
	execCommandFn = fakeCmd("")
	execLookPathFn = fakeLookPath(map[string]bool{})

	c := checkPHPFPM(true)
	if c.Status != StatusFail {
		t.Errorf("expected StatusFail, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "exists but not listening") {
		t.Errorf("unexpected message: %s", c.Message)
	}
}

func TestCheckPHPFPM_SocketExistsNoVersion_AutoFix(t *testing.T) {
	saveAndRestoreHooks(t)
	// php-fpm.sock has no version → extractPHPVersion returns ""
	osStatFn = fakeStat(map[string]bool{"/run/php/php-fpm.sock": true})
	netDialTimeoutFn = func(network, address string, timeout time.Duration) (net.Conn, error) {
		return nil, errors.New("connection refused")
	}
	execCommandFn = fakeCmd("")
	execLookPathFn = fakeLookPath(map[string]bool{})

	c := checkPHPFPM(true)
	// ver == "" so autofix cannot determine the service name, falls through
	if c.Status != StatusFail {
		t.Errorf("expected StatusFail, got %s", c.Status)
	}
}

func TestCheckPHPFPM_InstalledNotRunning_NoAutoFix(t *testing.T) {
	saveAndRestoreHooks(t)
	osStatFn = fakeStat(map[string]bool{}) // no socket files
	execLookPathFn = fakeLookPath(map[string]bool{"php-fpm8.3": true})
	execCommandFn = fakeCmd("")
	netDialTimeoutFn = func(network, address string, timeout time.Duration) (net.Conn, error) {
		return nil, errors.New("connection refused")
	}

	c := checkPHPFPM(false)
	if c.Status != StatusFail {
		t.Errorf("expected StatusFail, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "installed but not running") {
		t.Errorf("unexpected message: %s", c.Message)
	}
}

func TestCheckPHPFPM_InstalledAutoFix_StartSuccess(t *testing.T) {
	saveAndRestoreHooks(t)
	osStatFn = fakeStat(map[string]bool{}) // no socket files
	execLookPathFn = fakeLookPath(map[string]bool{"php-fpm8.3": true})
	execCommandFn = fakeCmd("")
	timeSleepFn = func(d time.Duration) {} // no-op

	// After start, first socket check succeeds
	dialCount := 0
	netDialTimeoutFn = func(network, address string, timeout time.Duration) (net.Conn, error) {
		dialCount++
		if dialCount == 1 {
			return fakeConn{}, nil // first socket check succeeds
		}
		return nil, errors.New("connection refused")
	}

	c := checkPHPFPM(true)
	if c.Status != StatusFixed {
		t.Errorf("expected StatusFixed, got %s", c.Status)
	}
}

func TestCheckPHPFPM_InstalledAutoFix_StartFailure(t *testing.T) {
	saveAndRestoreHooks(t)
	osStatFn = fakeStat(map[string]bool{})
	execLookPathFn = fakeLookPath(map[string]bool{"php-fpm8.3": true})
	execCommandFn = fakeCmd("")
	timeSleepFn = func(d time.Duration) {}
	netDialTimeoutFn = func(network, address string, timeout time.Duration) (net.Conn, error) {
		return nil, errors.New("connection refused") // always fails
	}

	c := checkPHPFPM(true)
	if c.Status != StatusFail {
		t.Errorf("expected StatusFail, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "installed but not running") {
		t.Errorf("unexpected message: %s", c.Message)
	}
}

func TestCheckPHPFPM_OnlyCGI(t *testing.T) {
	saveAndRestoreHooks(t)
	osStatFn = fakeStat(map[string]bool{})
	execLookPathFn = fakeLookPath(map[string]bool{"php-cgi8.3": true})
	execCommandFn = fakeCmd("")
	netDialTimeoutFn = func(network, address string, timeout time.Duration) (net.Conn, error) {
		return nil, errors.New("connection refused")
	}

	c := checkPHPFPM(false)
	if c.Status != StatusWarn {
		t.Errorf("expected StatusWarn, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "php-cgi") {
		t.Errorf("unexpected message: %s", c.Message)
	}
}

// ── checkPHPModules tests ────────────────────────────────

func TestCheckPHPModules_AllPresent(t *testing.T) {
	saveAndRestoreHooks(t)
	execCommandFn = fakeCmd("mysqli\ncurl\ngd\nmbstring\nxml\nzip\n")

	c := checkPHPModules()
	if c.Status != StatusOK {
		t.Errorf("expected StatusOK, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "6 required modules present") {
		t.Errorf("unexpected message: %s", c.Message)
	}
}

func TestCheckPHPModules_Missing(t *testing.T) {
	saveAndRestoreHooks(t)
	execCommandFn = fakeCmd("mysqli\ncurl\n") // missing gd, mbstring, xml, zip

	c := checkPHPModules()
	if c.Status != StatusWarn {
		t.Errorf("expected StatusWarn, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "gd") {
		t.Errorf("should list gd as missing: %s", c.Message)
	}
	if !strings.Contains(c.Message, "zip") {
		t.Errorf("should list zip as missing: %s", c.Message)
	}
}

func TestCheckPHPModules_NoPHP(t *testing.T) {
	saveAndRestoreHooks(t)
	execCommandFn = fakeCmdFail()

	c := checkPHPModules()
	if c.Status != StatusWarn {
		t.Errorf("expected StatusWarn, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "Could not check") {
		t.Errorf("unexpected message: %s", c.Message)
	}
}

// ── checkMySQL tests ─────────────────────────────────────

func TestCheckMySQL_Running(t *testing.T) {
	saveAndRestoreHooks(t)
	execCommandFn = fakeCmdRouter(map[string]string{
		"systemctl is-active": "active\n",
	})
	execLookPathFn = fakeLookPath(map[string]bool{})

	c := checkMySQL(false)
	if c.Status != StatusOK {
		t.Errorf("expected StatusOK, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "Running") {
		t.Errorf("unexpected message: %s", c.Message)
	}
}

func TestCheckMySQL_NotInstalled(t *testing.T) {
	saveAndRestoreHooks(t)
	execCommandFn = fakeCmd("")
	execLookPathFn = fakeLookPath(map[string]bool{})

	c := checkMySQL(false)
	if c.Status != StatusWarn {
		t.Errorf("expected StatusWarn, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "Not installed") {
		t.Errorf("unexpected message: %s", c.Message)
	}
}

func TestCheckMySQL_NotRunning_NoAutoFix(t *testing.T) {
	saveAndRestoreHooks(t)
	execCommandFn = fakeCmd("") // systemctl returns empty (not active)
	execLookPathFn = fakeLookPath(map[string]bool{"mariadb": true, "dpkg": true})
	osStatFn = fakeStat(map[string]bool{}) // data dir missing

	c := checkMySQL(false)
	if c.Status != StatusFail {
		t.Errorf("expected StatusFail, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "Installed but not running") {
		t.Errorf("unexpected message: %s", c.Message)
	}
}

func TestCheckMySQL_NotRunning_WithIssues(t *testing.T) {
	saveAndRestoreHooks(t)
	execCommandFn = fakeCmdRouter(map[string]string{
		"dpkg --audit": "broken package found\n",
	})
	execLookPathFn = fakeLookPath(map[string]bool{"mysql": true, "dpkg": true})
	// data dir missing, socket dirs missing
	osStatFn = fakeStat(map[string]bool{})

	c := checkMySQL(false)
	if c.Status != StatusFail {
		t.Errorf("expected StatusFail, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "data directory") {
		t.Errorf("should mention data directory: %s", c.Message)
	}
	if !strings.Contains(c.Message, "dpkg has broken packages") {
		t.Errorf("should mention dpkg: %s", c.Message)
	}
}

func TestCheckMySQL_NotRunning_NoIssues(t *testing.T) {
	saveAndRestoreHooks(t)
	execCommandFn = fakeCmd("") // dpkg --audit returns empty
	execLookPathFn = fakeLookPath(map[string]bool{"mysqld": true, "dpkg": true})
	// All dirs exist
	osStatFn = fakeStat(map[string]bool{"/var/lib/mysql": true, "/run/mysqld": true, "/var/run/mysqld": true})

	c := checkMySQL(false)
	if c.Status != StatusFail {
		t.Errorf("expected StatusFail, got %s", c.Status)
	}
	if c.Message != "Installed but not running" {
		t.Errorf("unexpected message: %q", c.Message)
	}
}

func TestCheckMySQL_AutoFix_NotInstalled_AptInstallSuccess(t *testing.T) {
	saveAndRestoreHooks(t)

	// Track calls to systemctl is-active. First two calls (mariadb, mysql in the
	// initial "is running?" loop) must return not-active. The call after apt install
	// must return active.
	isActiveCount := 0
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		key := filepath.Base(name)
		if len(args) > 0 {
			key = key + " " + args[0]
		}
		if key == "systemctl is-active" {
			isActiveCount++
			if isActiveCount <= 2 {
				return fakeCmd("")(name, args...) // not active
			}
			return fakeCmd("active\n")(name, args...) // active after install
		}
		return fakeCmd("")(name, args...)
	}
	execLookPathFn = fakeLookPath(map[string]bool{"apt": true})

	c := checkMySQL(true)
	if c.Status != StatusFixed {
		t.Errorf("expected StatusFixed, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "Installed and started") {
		t.Errorf("unexpected message: %s", c.Message)
	}
}

func TestCheckMySQL_AutoFix_NotInstalled_AptInstallFail(t *testing.T) {
	saveAndRestoreHooks(t)
	execCommandFn = fakeCmd("") // systemctl is-active returns empty
	execLookPathFn = fakeLookPath(map[string]bool{"apt": true})

	c := checkMySQL(true)
	if c.Status != StatusWarn {
		t.Errorf("expected StatusWarn, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "Not installed") {
		t.Errorf("unexpected message: %s", c.Message)
	}
}

func TestCheckMySQL_AutoFix_NotInstalled_NoApt(t *testing.T) {
	saveAndRestoreHooks(t)
	execCommandFn = fakeCmd("")
	execLookPathFn = fakeLookPath(map[string]bool{}) // no apt, no mysql

	c := checkMySQL(true)
	if c.Status != StatusWarn {
		t.Errorf("expected StatusWarn, got %s", c.Status)
	}
}

func TestCheckMySQL_AutoFix_FullRepairSuccess(t *testing.T) {
	saveAndRestoreHooks(t)
	osMkdirAllFn = func(path string, perm os.FileMode) error { return nil }

	callCount := 0
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		callCount++
		key := filepath.Base(name)
		if len(args) > 0 {
			key = key + " " + args[0]
		}
		// systemctl start should succeed (for the repair step)
		if key == "systemctl start" {
			return fakeCmd("")(name, args...)
		}
		return fakeCmd("")(name, args...)
	}

	execLookPathFn = fakeLookPath(map[string]bool{
		"mariadb":            true,
		"dpkg":               true,
		"mariadb-install-db": true,
	})
	osStatFn = fakeStat(map[string]bool{}) // everything missing

	c := checkMySQL(true)
	if c.Status != StatusFixed {
		t.Errorf("expected StatusFixed, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "Fully repaired") {
		t.Errorf("unexpected message: %s", c.Message)
	}
	if !strings.Contains(c.Fix, "dpkg/apt fixed") {
		t.Errorf("fix log should mention dpkg: %s", c.Fix)
	}
}

func TestCheckMySQL_AutoFix_FullRepairFail(t *testing.T) {
	saveAndRestoreHooks(t)
	osMkdirAllFn = func(path string, perm os.FileMode) error { return nil }

	// systemctl start always fails
	execCommandFn = fakeCmdRouterWithFail(
		map[string]string{},
		map[string]bool{"systemctl start": true},
	)
	execLookPathFn = fakeLookPath(map[string]bool{
		"mariadbd": true,
		"dpkg":     true,
	})
	osStatFn = fakeStat(map[string]bool{})

	c := checkMySQL(true)
	if c.Status != StatusFail {
		t.Errorf("expected StatusFail, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "Repair attempted but service still won't start") {
		t.Errorf("unexpected message: %s", c.Message)
	}
}

func TestCheckMySQL_AutoFix_NoDpkg(t *testing.T) {
	saveAndRestoreHooks(t)
	osMkdirAllFn = func(path string, perm os.FileMode) error { return nil }
	execCommandFn = fakeCmd("")
	// installed but no dpkg, no install-db binaries
	execLookPathFn = fakeLookPath(map[string]bool{"mysql": true})
	osStatFn = fakeStat(map[string]bool{})

	c := checkMySQL(true)
	// No dpkg means step 2 skipped, no install-db means step 4 skipped
	// systemctl start succeeds (fakeCmd returns exit 0)
	if c.Status != StatusFixed {
		t.Errorf("expected StatusFixed, got %s", c.Status)
	}
	// Fix should NOT contain dpkg
	if strings.Contains(c.Fix, "dpkg") {
		t.Errorf("fix should not mention dpkg when dpkg not found: %s", c.Fix)
	}
}

func TestCheckMySQL_AutoFix_WithMysqlInstallDb(t *testing.T) {
	saveAndRestoreHooks(t)
	osMkdirAllFn = func(path string, perm os.FileMode) error { return nil }
	execCommandFn = fakeCmd("")
	// mariadb-install-db not found, but mysql_install_db found
	execLookPathFn = fakeLookPath(map[string]bool{
		"mysqld":           true,
		"mysql_install_db": true,
	})
	osStatFn = fakeStat(map[string]bool{})

	c := checkMySQL(true)
	if c.Status != StatusFixed {
		t.Errorf("expected StatusFixed, got %s", c.Status)
	}
	if !strings.Contains(c.Fix, "initialized database") {
		t.Errorf("fix should mention initialized database: %s", c.Fix)
	}
}

// ── checkWebRoot tests ───────────────────────────────────

func TestCheckWebRoot_Exists(t *testing.T) {
	saveAndRestoreHooks(t)
	osStatFn = fakeStat(map[string]bool{"/var/www": true})

	c := checkWebRoot("", false)
	if c.Status != StatusOK {
		t.Errorf("expected StatusOK, got %s", c.Status)
	}
	if c.Message != "/var/www" {
		t.Errorf("unexpected message: %s", c.Message)
	}
}

func TestCheckWebRoot_CustomPath_Exists(t *testing.T) {
	saveAndRestoreHooks(t)
	osStatFn = fakeStat(map[string]bool{"/srv/web": true})

	c := checkWebRoot("/srv/web", false)
	if c.Status != StatusOK {
		t.Errorf("expected StatusOK, got %s", c.Status)
	}
}

func TestCheckWebRoot_Missing(t *testing.T) {
	saveAndRestoreHooks(t)
	osStatFn = fakeStat(map[string]bool{})

	c := checkWebRoot("/var/www", false)
	if c.Status != StatusFail {
		t.Errorf("expected StatusFail, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "does not exist") {
		t.Errorf("unexpected message: %s", c.Message)
	}
}

func TestCheckWebRoot_AutoFix(t *testing.T) {
	saveAndRestoreHooks(t)
	osStatFn = fakeStat(map[string]bool{})
	osMkdirAllFn = func(path string, perm os.FileMode) error { return nil }
	execCommandFn = fakeCmd("")

	c := checkWebRoot("/var/www", true)
	if c.Status != StatusFixed {
		t.Errorf("expected StatusFixed, got %s", c.Status)
	}
	if !strings.Contains(c.Fix, "mkdir -p") {
		t.Errorf("unexpected fix: %s", c.Fix)
	}
}

func TestCheckWebRoot_DefaultPath(t *testing.T) {
	saveAndRestoreHooks(t)
	osStatFn = fakeStat(map[string]bool{})

	c := checkWebRoot("", false) // empty path defaults to /var/www
	if c.Status != StatusFail {
		t.Errorf("expected StatusFail, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "/var/www") {
		t.Errorf("should default to /var/www: %s", c.Message)
	}
}

// ── checkConfigFile tests ────────────────────────────────

func TestCheckConfigFile_Exists(t *testing.T) {
	saveAndRestoreHooks(t)
	osStatFn = fakeStat(map[string]bool{"/etc/uwas/uwas.yaml": true})

	c := checkConfigFile("/etc/uwas/uwas.yaml")
	if c.Status != StatusOK {
		t.Errorf("expected StatusOK, got %s", c.Status)
	}
}

func TestCheckConfigFile_Missing(t *testing.T) {
	saveAndRestoreHooks(t)
	osStatFn = fakeStat(map[string]bool{})

	c := checkConfigFile("/etc/uwas/uwas.yaml")
	if c.Status != StatusFail {
		t.Errorf("expected StatusFail, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "not found") {
		t.Errorf("unexpected message: %s", c.Message)
	}
}

func TestCheckConfigFile_EmptyPath_FoundDefault(t *testing.T) {
	saveAndRestoreHooks(t)
	osStatFn = fakeStat(map[string]bool{"/opt/uwas/uwas.yaml": true})

	c := checkConfigFile("")
	if c.Status != StatusOK {
		t.Errorf("expected StatusOK, got %s", c.Status)
	}
	if c.Message != "/opt/uwas/uwas.yaml" {
		t.Errorf("unexpected message: %s", c.Message)
	}
}

func TestCheckConfigFile_EmptyPath_NoneFound(t *testing.T) {
	saveAndRestoreHooks(t)
	osStatFn = fakeStat(map[string]bool{})

	c := checkConfigFile("")
	if c.Status != StatusWarn {
		t.Errorf("expected StatusWarn, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "No config file found") {
		t.Errorf("unexpected message: %s", c.Message)
	}
}

// ── checkDomainsDir tests ────────────────────────────────

func TestCheckDomainsDir_WithDomains(t *testing.T) {
	saveAndRestoreHooks(t)
	osReadDirFn = func(name string) ([]fs.DirEntry, error) {
		return []fs.DirEntry{
			fakeDirEntry{name: "example.com.yaml"},
			fakeDirEntry{name: "test.org.yaml"},
			fakeDirEntry{name: "readme.txt"}, // not yaml
		}, nil
	}

	c := checkDomainsDir("/etc/uwas/uwas.yaml")
	if c.Status != StatusOK {
		t.Errorf("expected StatusOK, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "2 domain(s)") {
		t.Errorf("expected 2 domains: %s", c.Message)
	}
}

func TestCheckDomainsDir_Empty(t *testing.T) {
	saveAndRestoreHooks(t)
	osReadDirFn = func(name string) ([]fs.DirEntry, error) {
		return []fs.DirEntry{}, nil
	}

	c := checkDomainsDir("/etc/uwas/uwas.yaml")
	if c.Status != StatusOK {
		t.Errorf("expected StatusOK, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "0 domain(s)") {
		t.Errorf("expected 0 domains: %s", c.Message)
	}
}

func TestCheckDomainsDir_Missing(t *testing.T) {
	saveAndRestoreHooks(t)
	osReadDirFn = func(name string) ([]fs.DirEntry, error) {
		return nil, &os.PathError{Op: "readdir", Path: name, Err: os.ErrNotExist}
	}

	c := checkDomainsDir("")
	if c.Status != StatusWarn {
		t.Errorf("expected StatusWarn, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "not found") {
		t.Errorf("unexpected message: %s", c.Message)
	}
}

func TestCheckDomainsDir_DefaultPath(t *testing.T) {
	saveAndRestoreHooks(t)
	osReadDirFn = func(name string) ([]fs.DirEntry, error) {
		if name != "domains.d" {
			t.Errorf("expected default path 'domains.d', got %q", name)
		}
		return nil, errors.New("not found")
	}

	checkDomainsDir("")
}

// ── checkSSLCerts tests ──────────────────────────────────

func TestCheckSSLCerts_WithCerts(t *testing.T) {
	saveAndRestoreHooks(t)
	osStatFn = fakeStat(map[string]bool{"/etc/uwas/certs": true})
	osReadDirFn = func(name string) ([]fs.DirEntry, error) {
		return []fs.DirEntry{
			fakeDirEntry{name: "example.com.crt"},
			fakeDirEntry{name: "example.com.key"},
			fakeDirEntry{name: "test.org.crt"},
			fakeDirEntry{name: "test.org.key"},
		}, nil
	}

	c := checkSSLCerts("")
	if c.Status != StatusOK {
		t.Errorf("expected StatusOK, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "2 certificate(s)") {
		t.Errorf("expected 2 certs: %s", c.Message)
	}
}

func TestCheckSSLCerts_NoCertsDir(t *testing.T) {
	saveAndRestoreHooks(t)
	osStatFn = fakeStat(map[string]bool{})

	c := checkSSLCerts("")
	if c.Status != StatusWarn {
		t.Errorf("expected StatusWarn, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "No certs directory") {
		t.Errorf("unexpected message: %s", c.Message)
	}
}

func TestCheckSSLCerts_WithConfigPath(t *testing.T) {
	saveAndRestoreHooks(t)
	// Use filepath.Join so the key matches what the code computes on this OS.
	certsDir := filepath.Join(filepath.Dir("/opt/uwas/uwas.yaml"), "certs")
	osStatFn = fakeStat(map[string]bool{certsDir: true})
	osReadDirFn = func(name string) ([]fs.DirEntry, error) {
		return []fs.DirEntry{}, nil
	}

	c := checkSSLCerts("/opt/uwas/uwas.yaml")
	if c.Status != StatusOK {
		t.Errorf("expected StatusOK, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "0 certificate(s)") {
		t.Errorf("unexpected message: %s", c.Message)
	}
}

// ── checkFirewall tests ──────────────────────────────────

func TestCheckFirewall_Active(t *testing.T) {
	saveAndRestoreHooks(t)
	execCommandFn = fakeCmd("Status: active\nTo\t\tAction\tFrom\n80/tcp\tALLOW\tAnywhere\n443/tcp\tALLOW\tAnywhere\n")

	c := checkFirewall()
	if c.Status != StatusOK {
		t.Errorf("expected StatusOK, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "Active, ports 80/443 allowed") {
		t.Errorf("unexpected message: %s", c.Message)
	}
}

func TestCheckFirewall_Inactive(t *testing.T) {
	saveAndRestoreHooks(t)
	execCommandFn = fakeCmd("Status: inactive\n")

	c := checkFirewall()
	if c.Status != StatusWarn {
		t.Errorf("expected StatusWarn, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "inactive") {
		t.Errorf("unexpected message: %s", c.Message)
	}
}

func TestCheckFirewall_NotInstalled(t *testing.T) {
	saveAndRestoreHooks(t)
	execCommandFn = fakeCmdFail()

	c := checkFirewall()
	if c.Status != StatusWarn {
		t.Errorf("expected StatusWarn, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "ufw not available") {
		t.Errorf("unexpected message: %s", c.Message)
	}
}

func TestCheckFirewall_MissingPorts(t *testing.T) {
	saveAndRestoreHooks(t)
	execCommandFn = fakeCmd("Status: active\nTo\tAction\tFrom\n22/tcp\tALLOW\tAnywhere\n")

	c := checkFirewall()
	if c.Status != StatusWarn {
		t.Errorf("expected StatusWarn, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "may not be allowed") {
		t.Errorf("unexpected message: %s", c.Message)
	}
}

// ── checkDiskSpace tests ─────────────────────────────────

func TestCheckDiskSpace_OK(t *testing.T) {
	saveAndRestoreHooks(t)
	execCommandFn = fakeCmd("Filesystem      Size  Used Avail Use% Mounted on\n/dev/sda1       100G   42G   58G  42% /\n")

	c := checkDiskSpace()
	if c.Status != StatusOK {
		t.Errorf("expected StatusOK, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "42%") {
		t.Errorf("unexpected message: %s", c.Message)
	}
	if !strings.Contains(c.Message, "58G") {
		t.Errorf("should show available: %s", c.Message)
	}
}

func TestCheckDiskSpace_Error(t *testing.T) {
	saveAndRestoreHooks(t)
	execCommandFn = fakeCmdFail()

	c := checkDiskSpace()
	if c.Status != StatusWarn {
		t.Errorf("expected StatusWarn, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "Could not check") {
		t.Errorf("unexpected message: %s", c.Message)
	}
}

func TestCheckDiskSpace_ShortOutput(t *testing.T) {
	saveAndRestoreHooks(t)
	execCommandFn = fakeCmd("Filesystem\n") // only 1 line, no data line

	c := checkDiskSpace()
	if c.Status != StatusOK {
		t.Errorf("expected StatusOK, got %s", c.Status)
	}
	if c.Message != "OK" {
		t.Errorf("unexpected message: %s", c.Message)
	}
}

func TestCheckDiskSpace_TooFewFields(t *testing.T) {
	saveAndRestoreHooks(t)
	execCommandFn = fakeCmd("Filesystem Size\n/dev/sda1 100G\n") // only 2 fields on line 2

	c := checkDiskSpace()
	if c.Status != StatusOK {
		t.Errorf("expected StatusOK, got %s", c.Status)
	}
	if c.Message != "OK" {
		t.Errorf("unexpected message: %s", c.Message)
	}
}

// ── checkMemory tests ────────────────────────────────────

func TestCheckMemory_OK(t *testing.T) {
	saveAndRestoreHooks(t)
	execCommandFn = fakeCmd("              total        used        free      shared  buff/cache   available\nMem:          16000        8000        4000         200        4000        7800\n")

	c := checkMemory()
	if c.Status != StatusOK {
		t.Errorf("expected StatusOK, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "Total: 16000MB") {
		t.Errorf("unexpected message: %s", c.Message)
	}
}

func TestCheckMemory_Error(t *testing.T) {
	saveAndRestoreHooks(t)
	execCommandFn = fakeCmdFail()

	c := checkMemory()
	if c.Status != StatusWarn {
		t.Errorf("expected StatusWarn, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "Could not check") {
		t.Errorf("unexpected message: %s", c.Message)
	}
}

func TestCheckMemory_ShortOutput(t *testing.T) {
	saveAndRestoreHooks(t)
	execCommandFn = fakeCmd("total\n") // only header, no data

	c := checkMemory()
	if c.Status != StatusOK {
		t.Errorf("expected StatusOK, got %s", c.Status)
	}
	if c.Message != "OK" {
		t.Errorf("unexpected message: %s", c.Message)
	}
}

func TestCheckMemory_TooFewFields(t *testing.T) {
	saveAndRestoreHooks(t)
	execCommandFn = fakeCmd("header\nMem: 16000\n") // only 2 fields

	c := checkMemory()
	if c.Status != StatusOK {
		t.Errorf("expected StatusOK, got %s", c.Status)
	}
	if c.Message != "OK" {
		t.Errorf("unexpected message: %s", c.Message)
	}
}

// ── checkOpenFiles tests ─────────────────────────────────

func TestCheckOpenFiles_OK(t *testing.T) {
	saveAndRestoreHooks(t)
	execCommandFn = fakeCmd("65536\n")

	c := checkOpenFiles()
	if c.Status != StatusOK {
		t.Errorf("expected StatusOK, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "ulimit -n: 65536") {
		t.Errorf("unexpected message: %s", c.Message)
	}
}

func TestCheckOpenFiles_UlimitFails_ProcExists(t *testing.T) {
	saveAndRestoreHooks(t)
	execCommandFn = fakeCmdFail()
	osReadFileFn = func(name string) ([]byte, error) {
		return []byte("1048576\n"), nil
	}

	c := checkOpenFiles()
	if c.Status != StatusOK {
		t.Errorf("expected StatusOK, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "System max: 1048576") {
		t.Errorf("unexpected message: %s", c.Message)
	}
}

func TestCheckOpenFiles_BothFail(t *testing.T) {
	saveAndRestoreHooks(t)
	execCommandFn = fakeCmdFail()
	osReadFileFn = func(name string) ([]byte, error) {
		return nil, errors.New("no such file")
	}

	c := checkOpenFiles()
	if c.Status != StatusWarn {
		t.Errorf("expected StatusWarn, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "Could not check") {
		t.Errorf("unexpected message: %s", c.Message)
	}
}

// ── checkTimeSync tests ──────────────────────────────────

func TestCheckTimeSync_Synced(t *testing.T) {
	saveAndRestoreHooks(t)
	execCommandFn = fakeCmd("yes\n")

	c := checkTimeSync()
	if c.Status != StatusOK {
		t.Errorf("expected StatusOK, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "NTP synchronized") {
		t.Errorf("unexpected message: %s", c.Message)
	}
}

func TestCheckTimeSync_NotSynced(t *testing.T) {
	saveAndRestoreHooks(t)
	execCommandFn = fakeCmd("no\n")

	c := checkTimeSync()
	if c.Status != StatusWarn {
		t.Errorf("expected StatusWarn, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "NTP not synchronized") {
		t.Errorf("unexpected message: %s", c.Message)
	}
}

func TestCheckTimeSync_Error(t *testing.T) {
	saveAndRestoreHooks(t)
	execCommandFn = fakeCmdFail()

	c := checkTimeSync()
	if c.Status != StatusWarn {
		t.Errorf("expected StatusWarn, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "Could not check NTP") {
		t.Errorf("unexpected message: %s", c.Message)
	}
}

// ── checkDNS tests ───────────────────────────────────────

func TestCheckDNS_OK(t *testing.T) {
	saveAndRestoreHooks(t)
	netLookupHostFn = func(host string) ([]string, error) {
		return []string{"1.2.3.4"}, nil
	}

	c := checkDNS()
	if c.Status != StatusOK {
		t.Errorf("expected StatusOK, got %s", c.Status)
	}
	if c.Message != "Working" {
		t.Errorf("unexpected message: %s", c.Message)
	}
}

func TestCheckDNS_Failure(t *testing.T) {
	saveAndRestoreHooks(t)
	netLookupHostFn = func(host string) ([]string, error) {
		return nil, errors.New("no such host")
	}

	c := checkDNS()
	if c.Status != StatusFail {
		t.Errorf("expected StatusFail, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "Cannot resolve") {
		t.Errorf("unexpected message: %s", c.Message)
	}
}

// ── Helper function tests ────────────────────────────────

func TestExtractPHPVersion(t *testing.T) {
	tests := []struct{ input, want string }{
		{"/run/php/php8.3-fpm.sock", "8.3"},
		{"/run/php/php8.4-fpm.sock", "8.4"},
		{"/run/php/php-fpm.sock", ""},
		{"/var/run/php8.2-fpm.sock", "8.2"},
		{"/run/php/php8.1-fpm.sock", "8.1"},
		{"php8.0-fpm.sock", "8.0"},
		{"fpm.sock", ""},         // no php prefix
		{"php-fpm.sock", ""},     // no version
		{"php1-fpm.sock", ""},    // too short
	}
	for _, tt := range tests {
		got := extractPHPVersion(tt.input)
		if got != tt.want {
			t.Errorf("extractPHPVersion(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestModulesToPackages(t *testing.T) {
	tests := []struct {
		input []string
		want  string
	}{
		{[]string{"mysqli", "curl", "GD"}, "php-mysqli php-curl php-gd"},
		{[]string{"mbstring"}, "php-mbstring"},
		{[]string{}, ""},
	}
	for _, tt := range tests {
		got := modulesToPackages(tt.input)
		if got != tt.want {
			t.Errorf("modulesToPackages(%v) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ── Run summary count tests ──────────────────────────────

func TestRun_SummaryCounts(t *testing.T) {
	saveAndRestoreHooks(t)

	runtimeGOOS = "linux"
	runtimeGOARCH = "amd64"

	// Ports available
	netListenFn = func(network, address string) (net.Listener, error) {
		return fakeListener{}, nil
	}

	// DNS works
	netLookupHostFn = func(host string) ([]string, error) {
		return []string{"1.2.3.4"}, nil
	}

	// No PHP sockets, no php-fpm, no php-cgi
	osStatFn = fakeStat(map[string]bool{})
	execLookPathFn = fakeLookPath(map[string]bool{})
	netDialTimeoutFn = func(network, address string, timeout time.Duration) (net.Conn, error) {
		return nil, errors.New("connection refused")
	}

	// All exec commands fail
	execCommandFn = fakeCmdFail()
	osReadDirFn = func(name string) ([]fs.DirEntry, error) {
		return nil, errors.New("not found")
	}
	osReadFileFn = func(name string) ([]byte, error) {
		return nil, errors.New("not found")
	}
	osMkdirAllFn = func(path string, perm os.FileMode) error { return nil }
	timeSleepFn = func(d time.Duration) {}

	r := Run(Options{})

	// Verify we have all 15 checks
	if len(r.Checks) != 15 {
		t.Errorf("expected 15 checks, got %d", len(r.Checks))
	}

	// Verify summary is well-formed
	if !strings.Contains(r.Summary, "ok") &&
		!strings.Contains(r.Summary, "warnings") &&
		!strings.Contains(r.Summary, "failures") {
		t.Errorf("malformed summary: %s", r.Summary)
	}

	// Count statuses manually and verify they match
	ok, warn, fail, fixed := 0, 0, 0, 0
	for _, c := range r.Checks {
		switch c.Status {
		case StatusOK:
			ok++
		case StatusWarn:
			warn++
		case StatusFail:
			fail++
		case StatusFixed:
			fixed++
		}
	}
	expected := fmt.Sprintf("%d ok, %d warnings, %d failures, %d auto-fixed", ok, warn, fail, fixed)
	if r.Summary != expected {
		t.Errorf("summary mismatch: got %q, want %q", r.Summary, expected)
	}
}

// ── checkConfigFile with first default path found ────────

func TestCheckConfigFile_EmptyPath_FirstMatch(t *testing.T) {
	saveAndRestoreHooks(t)
	osStatFn = fakeStat(map[string]bool{"/etc/uwas/uwas.yaml": true})

	c := checkConfigFile("")
	if c.Status != StatusOK {
		t.Errorf("expected StatusOK, got %s", c.Status)
	}
	if c.Message != "/etc/uwas/uwas.yaml" {
		t.Errorf("expected first match, got: %s", c.Message)
	}
}

func TestCheckConfigFile_EmptyPath_ThirdMatch(t *testing.T) {
	saveAndRestoreHooks(t)
	osStatFn = fakeStat(map[string]bool{"./uwas.yaml": true})

	c := checkConfigFile("")
	if c.Status != StatusOK {
		t.Errorf("expected StatusOK, got %s", c.Status)
	}
	if c.Message != "./uwas.yaml" {
		t.Errorf("expected third default path, got: %s", c.Message)
	}
}

// ── checkSSLCerts edge case: dir exists but ReadDir fails ─

func TestCheckSSLCerts_DirExistsReadDirFails(t *testing.T) {
	saveAndRestoreHooks(t)
	osStatFn = fakeStat(map[string]bool{"/etc/uwas/certs": true})
	osReadDirFn = func(name string) ([]fs.DirEntry, error) {
		return nil, errors.New("permission denied")
	}

	c := checkSSLCerts("")
	if c.Status != StatusOK {
		t.Errorf("expected StatusOK, got %s", c.Status)
	}
	// nil entries / 2 = 0
	if !strings.Contains(c.Message, "0 certificate(s)") {
		t.Errorf("unexpected message: %s", c.Message)
	}
}

// ── checkFirewall edge: active but only 80 allowed ───────

func TestCheckFirewall_Only80Allowed(t *testing.T) {
	saveAndRestoreHooks(t)
	execCommandFn = fakeCmd("Status: active\n80/tcp ALLOW Anywhere\n")

	c := checkFirewall()
	if c.Status != StatusWarn {
		t.Errorf("expected StatusWarn, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "may not be allowed") {
		t.Errorf("unexpected message: %s", c.Message)
	}
}

func TestCheckFirewall_Only443Allowed(t *testing.T) {
	saveAndRestoreHooks(t)
	execCommandFn = fakeCmd("Status: active\n443/tcp ALLOW Anywhere\n")

	c := checkFirewall()
	if c.Status != StatusWarn {
		t.Errorf("expected StatusWarn, got %s", c.Status)
	}
}

// ── checkPHPFPM edge: first socket (8.4) matches ─────────

func TestCheckPHPFPM_FirstSocket84(t *testing.T) {
	saveAndRestoreHooks(t)
	osStatFn = fakeStat(map[string]bool{"/run/php/php8.4-fpm.sock": true})
	netDialTimeoutFn = func(network, address string, timeout time.Duration) (net.Conn, error) {
		return fakeConn{}, nil
	}
	execLookPathFn = fakeLookPath(map[string]bool{})
	execCommandFn = fakeCmd("")

	c := checkPHPFPM(false)
	if c.Status != StatusOK {
		t.Errorf("expected StatusOK, got %s", c.Status)
	}
	if !strings.Contains(c.Message, "8.4") {
		t.Errorf("should pick 8.4 socket: %s", c.Message)
	}
}

// ── Report.add test ──────────────────────────────────────

func TestReportAdd(t *testing.T) {
	r := &Report{}
	r.add(Check{Name: "test1", Status: StatusOK, Message: "ok"})
	r.add(Check{Name: "test2", Status: StatusFail, Message: "fail"})

	if len(r.Checks) != 2 {
		t.Errorf("expected 2 checks, got %d", len(r.Checks))
	}
	if r.Checks[0].Name != "test1" {
		t.Errorf("first check name: %s", r.Checks[0].Name)
	}
	if r.Checks[1].Status != StatusFail {
		t.Errorf("second check status: %s", r.Checks[1].Status)
	}
}

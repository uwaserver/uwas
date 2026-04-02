package database

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

// ── Helpers ──────────────────────────────────────────────────────────────────

// saveHooks snapshots every hook and returns a restore func for t.Cleanup.
func saveHooks(t *testing.T) {
	t.Helper()
	origGOOS := runtimeGOOS
	origCmd := execCommandFn
	origLook := execLookPathFn
	origMySQL := runMySQLFn
	origStat := osStatFn
	origRead := osReadFileFn
	origMkdir := osMkdirAllFn
	origRmAll := osRemoveAllFn

	t.Cleanup(func() {
		runtimeGOOS = origGOOS
		execCommandFn = origCmd
		execLookPathFn = origLook
		runMySQLFn = origMySQL
		osStatFn = origStat
		osReadFileFn = origRead
		osMkdirAllFn = origMkdir
		osRemoveAllFn = origRmAll
	})
}

// fakeCmd returns an *exec.Cmd that, when executed, writes stdout and exits
// with the given code.  It uses the TestHelperProcess trick.
func fakeCmd(stdout string, exitCode int) func(string, ...string) *exec.Cmd {
	return func(name string, args ...string) *exec.Cmd {
		cs := []string{"-test.run=TestHelperProcess", "--", name}
		cs = append(cs, args...)
		cmd := exec.Command(os.Args[0], cs...)
		cmd.Env = append(os.Environ(),
			"GO_WANT_HELPER_PROCESS=1",
			fmt.Sprintf("HELPER_STDOUT=%s", stdout),
			fmt.Sprintf("HELPER_EXIT_CODE=%d", exitCode),
		)
		return cmd
	}
}

// fakeCmdRouter routes different executables to different fake outputs.
// Keys are executable basenames (e.g. "systemctl", "dpkg").
type cmdRoute struct {
	stdout   string
	exitCode int
}

func fakeCmdRouter(routes map[string]cmdRoute, fallback cmdRoute) func(string, ...string) *exec.Cmd {
	return func(name string, args ...string) *exec.Cmd {
		// Extract basename for matching
		key := name
		parts := strings.Split(name, "/")
		if len(parts) > 0 {
			key = parts[len(parts)-1]
		}
		// Also try matching "name arg1" for more specific routes
		if len(args) > 0 {
			specific := key + " " + args[0]
			if r, ok := routes[specific]; ok {
				return buildHelperCmd(name, args, r.stdout, r.exitCode)
			}
		}
		if r, ok := routes[key]; ok {
			return buildHelperCmd(name, args, r.stdout, r.exitCode)
		}
		return buildHelperCmd(name, args, fallback.stdout, fallback.exitCode)
	}
}

func buildHelperCmd(name string, args []string, stdout string, exitCode int) *exec.Cmd {
	cs := []string{"-test.run=TestHelperProcess", "--", name}
	cs = append(cs, args...)
	cmd := exec.Command(os.Args[0], cs...)
	cmd.Env = append(os.Environ(),
		"GO_WANT_HELPER_PROCESS=1",
		fmt.Sprintf("HELPER_STDOUT=%s", stdout),
		fmt.Sprintf("HELPER_EXIT_CODE=%d", exitCode),
	)
	return cmd
}

// lookPathFound returns a LookPath mock that succeeds for listed binaries.
func lookPathFound(bins ...string) func(string) (string, error) {
	m := make(map[string]bool)
	for _, b := range bins {
		m[b] = true
	}
	return func(file string) (string, error) {
		if m[file] {
			return "/usr/bin/" + file, nil
		}
		return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
	}
}

// lookPathNone is a LookPath mock that always fails.
func lookPathNone(file string) (string, error) {
	return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
}

// noopMkdirAll is a no-op MkdirAll mock.
func noopMkdirAll(_ string, _ fs.FileMode) error { return nil }

// noopRemoveAll is a no-op RemoveAll mock.
func noopRemoveAll(_ string) error { return nil }

// fakeStatAlways succeeds with a fake FileInfo.
func fakeStatAlways(_ string) (fs.FileInfo, error) {
	return fakeFileInfo{}, nil
}

// fakeStatNever always fails.
func fakeStatNever(name string) (fs.FileInfo, error) {
	return nil, fmt.Errorf("stat %s: no such file", name)
}

// fakeStatFor succeeds only for paths in the given set.
func fakeStatFor(paths ...string) func(string) (fs.FileInfo, error) {
	m := make(map[string]bool)
	for _, p := range paths {
		m[p] = true
	}
	return func(name string) (fs.FileInfo, error) {
		if m[name] {
			return fakeFileInfo{}, nil
		}
		return nil, fmt.Errorf("stat %s: no such file", name)
	}
}

type fakeFileInfo struct{}

func (fakeFileInfo) Name() string      { return "fake" }
func (fakeFileInfo) Size() int64       { return 0 }
func (fakeFileInfo) Mode() fs.FileMode { return 0755 }
func (fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (fakeFileInfo) IsDir() bool       { return true }
func (fakeFileInfo) Sys() any          { return nil }

// ── TestHelperProcess ─────────────────────────────────────────────────────

// TestHelperProcess is the helper subprocess used by fakeCmd / fakeCmdRouter.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	stdout := os.Getenv("HELPER_STDOUT")
	exitCode := 0
	fmt.Sscanf(os.Getenv("HELPER_EXIT_CODE"), "%d", &exitCode)

	fmt.Fprint(os.Stdout, stdout)
	os.Exit(exitCode)
}

// ══════════════════════════════════════════════════════════════════════════════
// GetStatus tests
// ══════════════════════════════════════════════════════════════════════════════

func TestGetStatus_Windows(t *testing.T) {
	// Original test — verifies Windows short-circuit without mocking.
	st := GetStatus()
	if runtime.GOOS == "windows" {
		if st.Backend != "none" {
			t.Errorf("expected backend 'none' on Windows, got %q", st.Backend)
		}
		if st.Installed {
			t.Error("expected Installed=false on Windows")
		}
	}
}

func TestGetStatus_LinuxInstalled(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "linux"
	execLookPathFn = lookPathFound("mariadb")
	execCommandFn = fakeCmd("mariadb  Ver 15.1 Distrib 10.11.6-MariaDB", 0)

	st := GetStatus()
	if !st.Installed {
		t.Fatal("expected Installed=true")
	}
	if st.Backend != "mariadb" {
		t.Errorf("expected backend mariadb, got %q", st.Backend)
	}
	if !strings.Contains(st.Version, "MariaDB") {
		t.Errorf("expected version containing MariaDB, got %q", st.Version)
	}
}

func TestGetStatus_LinuxInstalledMySQL(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "linux"
	// mariadb not found, mysql found
	execLookPathFn = lookPathFound("mysql", "mysqladmin")
	execCommandFn = fakeCmd("mysql  Ver 8.0.35", 0)

	st := GetStatus()
	if !st.Installed {
		t.Fatal("expected Installed=true")
	}
	if st.Backend != "mysql" {
		t.Errorf("expected backend mysql, got %q", st.Backend)
	}
}

func TestGetStatus_LinuxNotInstalled(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "linux"
	execLookPathFn = lookPathNone

	st := GetStatus()
	if st.Installed {
		t.Error("expected Installed=false")
	}
	if st.Backend != "none" {
		t.Errorf("expected backend none, got %q", st.Backend)
	}
}

func TestGetStatus_LinuxRunning(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "linux"
	execLookPathFn = lookPathFound("mariadb", "mysqladmin")
	execCommandFn = fakeCmd("mysqld is alive", 0)

	st := GetStatus()
	if !st.Running {
		t.Error("expected Running=true")
	}
}

func TestGetStatus_LinuxNotRunning(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "linux"
	// mariadb found but all ping methods fail
	callCount := 0
	execLookPathFn = func(file string) (string, error) {
		if file == "mariadb" {
			return "/usr/bin/mariadb", nil
		}
		return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
	}
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		callCount++
		// version call succeeds (first call), but all CombinedOutput calls for ping fail
		return fakeCmd("some output", 1)(name, args...)
	}

	st := GetStatus()
	if st.Running {
		t.Error("expected Running=false when all ping methods fail")
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// StartService tests
// ══════════════════════════════════════════════════════════════════════════════

func TestStartService_Windows(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "windows"
	err := StartService()
	if err == nil || !strings.Contains(err.Error(), "Windows") {
		t.Fatalf("expected Windows error, got %v", err)
	}
}

func TestStartService_Success(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "linux"
	osMkdirAllFn = noopMkdirAll
	execCommandFn = fakeCmd("", 0)

	err := StartService()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStartService_Failure(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "linux"
	osMkdirAllFn = noopMkdirAll
	execCommandFn = fakeCmd("failed to start", 1)

	err := StartService()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "could not start") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// StopService tests
// ══════════════════════════════════════════════════════════════════════════════

func TestStopService_Windows(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "windows"
	err := StopService()
	if err == nil || !strings.Contains(err.Error(), "Windows") {
		t.Fatalf("expected Windows error, got %v", err)
	}
}

func TestStopService_Success(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "linux"
	execCommandFn = fakeCmd("", 0)

	err := StopService()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStopService_ForceFallback(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "linux"
	// systemctl stop always fails; pkill runs as fallback
	execCommandFn = fakeCmd("stop failed", 1)

	err := StopService()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "force killed") {
		t.Errorf("expected force killed message, got %v", err)
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// RestartService tests
// ══════════════════════════════════════════════════════════════════════════════

func TestRestartService_Windows(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "windows"
	err := RestartService()
	if err == nil || !strings.Contains(err.Error(), "Windows") {
		t.Fatalf("expected Windows error, got %v", err)
	}
}

func TestRestartService_Success(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "linux"
	execCommandFn = fakeCmd("", 0)

	err := RestartService()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRestartService_Failure(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "linux"
	execCommandFn = fakeCmd("restart failed", 1)

	err := RestartService()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "could not restart") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// RepairService tests
// ══════════════════════════════════════════════════════════════════════════════

func TestRepairService_Windows(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "windows"
	_, err := RepairService()
	if err == nil || !strings.Contains(err.Error(), "Windows") {
		t.Fatalf("expected Windows error, got %v", err)
	}
}

func TestRepairService_Success(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "linux"
	osMkdirAllFn = noopMkdirAll
	execLookPathFn = lookPathFound("dpkg", "mariadb-install-db")
	// systemctl start mariadb succeeds
	execCommandFn = fakeCmd("ok", 0)
	runMySQLFn = func(sql string) (string, error) { return "", nil }

	logOut, err := RepairService()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(logOut, "Basic security applied") {
		t.Errorf("expected security applied log, got %q", logOut)
	}
}

func TestRepairService_StartFails(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "linux"
	osMkdirAllFn = noopMkdirAll
	execLookPathFn = lookPathNone // no dpkg, no install-db
	execCommandFn = fakeCmd("fail", 1)

	_, err := RepairService()
	if err == nil {
		t.Fatal("expected error when start fails")
	}
	if !strings.Contains(err.Error(), "could not start") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// ForceUninstall tests
// ══════════════════════════════════════════════════════════════════════════════

func TestForceUninstall_Windows(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "windows"
	_, err := ForceUninstall()
	if err == nil || !strings.Contains(err.Error(), "Windows") {
		t.Fatalf("expected Windows error, got %v", err)
	}
}

func TestForceUninstall_Success(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "linux"
	osMkdirAllFn = noopMkdirAll
	osRemoveAllFn = noopRemoveAll
	// dpkg found, dpkg -l returns packages
	execLookPathFn = lookPathFound("dpkg")
	execCommandFn = fakeCmdRouter(map[string]cmdRoute{
		"dpkg -l": {stdout: "ii  mariadb-server  1:10.11  amd64  DB\nii  mysql-common  5.8  all  common\n", exitCode: 0},
	}, cmdRoute{stdout: "", exitCode: 0})
	osStatFn = fakeStatAlways

	logOut, err := ForceUninstall()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(logOut, "purge mariadb-server") {
		t.Errorf("expected purge of mariadb-server in log, got %q", logOut)
	}
	if !strings.Contains(logOut, "purge mysql-common") {
		t.Errorf("expected purge of mysql-common in log, got %q", logOut)
	}
	if !strings.Contains(logOut, "Removed /var/lib/mysql") {
		t.Errorf("expected Removed message in log, got %q", logOut)
	}
}

func TestForceUninstall_NoDpkg(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "linux"
	osMkdirAllFn = noopMkdirAll
	osRemoveAllFn = noopRemoveAll
	execLookPathFn = lookPathNone
	execCommandFn = fakeCmd("", 0)
	osStatFn = fakeStatNever

	logOut, err := ForceUninstall()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should still have cleanup messages
	if !strings.Contains(logOut, "Killed all DB processes") {
		t.Errorf("expected kill message, got %q", logOut)
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// UninstallService tests
// ══════════════════════════════════════════════════════════════════════════════

func TestUninstallService_Windows(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "windows"
	_, err := UninstallService()
	if err == nil || !strings.Contains(err.Error(), "Windows") {
		t.Fatalf("expected Windows error, got %v", err)
	}
}

func TestUninstallService_AptSuccess(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "linux"
	osMkdirAllFn = noopMkdirAll
	osRemoveAllFn = noopRemoveAll
	execLookPathFn = lookPathFound("apt")
	execCommandFn = fakeCmd("apt purge done", 0)
	osStatFn = fakeStatAlways

	logOut, err := UninstallService()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(logOut, "Service stopped") {
		t.Errorf("expected Service stopped, got %q", logOut)
	}
	if !strings.Contains(logOut, "daemon-reload done") {
		t.Errorf("expected daemon-reload, got %q", logOut)
	}
}

func TestUninstallService_DnfSuccess(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "linux"
	osMkdirAllFn = noopMkdirAll
	osRemoveAllFn = noopRemoveAll
	// apt not found, dnf found
	execLookPathFn = lookPathFound("dnf")
	execCommandFn = fakeCmd("dnf remove done", 0)
	osStatFn = fakeStatNever

	logOut, err := UninstallService()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(logOut, "dnf remove done") {
		t.Errorf("expected dnf output, got %q", logOut)
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// DiagnoseService tests
// ══════════════════════════════════════════════════════════════════════════════

func TestDiagnoseService(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "linux"
	execCommandFn = fakeCmdRouter(map[string]cmdRoute{
		"systemctl is-active": {stdout: "active", exitCode: 0},
		"journalctl":         {stdout: "Mar 26 server started", exitCode: 0},
		"df":                 {stdout: "Filesystem  Size  Used  Avail  Use%  Mounted\n/dev/sda1  50G  10G  40G  20%  /", exitCode: 0},
	}, cmdRoute{stdout: "", exitCode: 0})
	osStatFn = fakeStatFor("/run/mysqld/mysqld.sock", "/var/lib/mysql")
	osReadFileFn = func(name string) ([]byte, error) {
		if name == "/run/mysqld/mysqld.pid" {
			return []byte("12345"), nil
		}
		return nil, fmt.Errorf("not found")
	}

	diag := DiagnoseService()

	if diag["service_name"] != "mariadb" {
		t.Errorf("expected service_name=mariadb, got %v", diag["service_name"])
	}
	if diag["service_status"] != "active" {
		t.Errorf("expected active, got %v", diag["service_status"])
	}
	if diag["socket"] != "/run/mysqld/mysqld.sock" {
		t.Errorf("expected socket path, got %v", diag["socket"])
	}
	if diag["pid"] != "12345" {
		t.Errorf("expected pid=12345, got %v", diag["pid"])
	}
	if diag["disk"] == nil {
		t.Error("expected disk info")
	}
	if diag["data_dir_mode"] == nil {
		t.Error("expected data_dir_mode")
	}
}

func TestDiagnoseService_Missing(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "linux"
	execCommandFn = fakeCmd("", 1)
	osStatFn = fakeStatNever
	osReadFileFn = func(string) ([]byte, error) { return nil, fmt.Errorf("not found") }

	diag := DiagnoseService()
	if diag["data_dir"] != "missing" {
		t.Errorf("expected data_dir=missing, got %v", diag["data_dir"])
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// ListDatabases tests
// ══════════════════════════════════════════════════════════════════════════════

func TestListDatabases(t *testing.T) {
	saveHooks(t)
	runMySQLFn = func(sql string) (string, error) {
		return "wordpress\t2.50\t12\nshop\t0.80\t5\n", nil
	}

	dbs, err := ListDatabases()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(dbs) != 2 {
		t.Fatalf("expected 2 databases, got %d", len(dbs))
	}
	if dbs[0].Name != "wordpress" {
		t.Errorf("expected wordpress, got %q", dbs[0].Name)
	}
	if dbs[0].Size != "2.50 MB" {
		t.Errorf("expected 2.50 MB, got %q", dbs[0].Size)
	}
	if dbs[0].Tables != 12 {
		t.Errorf("expected 12 tables, got %d", dbs[0].Tables)
	}
	if dbs[1].Name != "shop" {
		t.Errorf("expected shop, got %q", dbs[1].Name)
	}
}

func TestListDatabases_Empty(t *testing.T) {
	saveHooks(t)
	runMySQLFn = func(sql string) (string, error) { return "", nil }

	dbs, err := ListDatabases()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(dbs) != 0 {
		t.Errorf("expected 0 databases, got %d", len(dbs))
	}
}

func TestListDatabases_Error(t *testing.T) {
	saveHooks(t)
	runMySQLFn = func(sql string) (string, error) { return "", fmt.Errorf("connection refused") }

	_, err := ListDatabases()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestListDatabases_SkipsHeader(t *testing.T) {
	saveHooks(t)
	runMySQLFn = func(sql string) (string, error) {
		return "SCHEMA_NAME\tsize_mb\ttable_count\nwp\t1.00\t5\n", nil
	}

	dbs, err := ListDatabases()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(dbs) != 1 {
		t.Fatalf("expected 1 db (header skipped), got %d", len(dbs))
	}
	if dbs[0].Name != "wp" {
		t.Errorf("expected wp, got %q", dbs[0].Name)
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// CreateDatabase tests
// ══════════════════════════════════════════════════════════════════════════════

func TestCreateDatabase_EmptyName(t *testing.T) {
	saveHooks(t)
	_, err := CreateDatabase("", "", "", "")
	if err == nil || !strings.Contains(err.Error(), "name required") {
		t.Fatalf("expected name required error, got %v", err)
	}
}

func TestCreateDatabase_Success(t *testing.T) {
	saveHooks(t)
	var capturedSQL string
	runMySQLFn = func(sql string) (string, error) {
		capturedSQL = sql
		return "", nil
	}

	res, err := CreateDatabase("testdb", "testuser", "s3cret", "10.0.0.1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Name != "testdb" || res.User != "testuser" || res.Password != "s3cret" || res.Host != "10.0.0.1" {
		t.Errorf("unexpected result: %+v", res)
	}
	if !strings.Contains(capturedSQL, "`testdb`") {
		t.Errorf("expected backticked db name in SQL, got %q", capturedSQL)
	}
}

func TestCreateDatabase_Defaults(t *testing.T) {
	saveHooks(t)
	runMySQLFn = func(sql string) (string, error) { return "", nil }

	res, err := CreateDatabase("mydb", "", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.User != "mydb" {
		t.Errorf("expected user=mydb (defaulted from name), got %q", res.User)
	}
	if res.Host != "localhost" {
		t.Errorf("expected host=localhost, got %q", res.Host)
	}
	if len(res.Password) != 32 { // 16 bytes hex = 32 chars
		t.Errorf("expected 32-char generated password, got %d chars", len(res.Password))
	}
}

func TestCreateDatabase_CustomPassword(t *testing.T) {
	saveHooks(t)
	runMySQLFn = func(sql string) (string, error) { return "", nil }

	res, err := CreateDatabase("db1", "usr1", "custom_pw!", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Password != "custom_pw!" {
		t.Errorf("expected custom_pw!, got %q", res.Password)
	}
}

func TestCreateDatabase_RunMySQLError(t *testing.T) {
	saveHooks(t)
	runMySQLFn = func(sql string) (string, error) { return "", fmt.Errorf("access denied") }

	_, err := CreateDatabase("testdb", "", "", "")
	if err == nil || !strings.Contains(err.Error(), "access denied") {
		t.Fatalf("expected access denied error, got %v", err)
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// DropDatabase tests
// ══════════════════════════════════════════════════════════════════════════════

func TestDropDatabase_Success(t *testing.T) {
	saveHooks(t)
	var capturedSQL string
	runMySQLFn = func(sql string) (string, error) {
		capturedSQL = sql
		return "", nil
	}

	err := DropDatabase("testdb", "testuser", "10.0.0.1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(capturedSQL, "`testdb`") {
		t.Errorf("expected backticked name in SQL")
	}
	if !strings.Contains(capturedSQL, "'testuser'@'10.0.0.1'") {
		t.Errorf("expected user@host in SQL, got %q", capturedSQL)
	}
}

func TestDropDatabase_Defaults(t *testing.T) {
	saveHooks(t)
	var capturedSQL string
	runMySQLFn = func(sql string) (string, error) {
		capturedSQL = sql
		return "", nil
	}

	err := DropDatabase("mydb", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(capturedSQL, "'mydb'@'localhost'") {
		t.Errorf("expected default user=name and host=localhost, got %q", capturedSQL)
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// ChangePassword tests
// ══════════════════════════════════════════════════════════════════════════════

func TestChangePassword_Success(t *testing.T) {
	saveHooks(t)
	var capturedSQL string
	runMySQLFn = func(sql string) (string, error) {
		capturedSQL = sql
		return "", nil
	}

	err := ChangePassword("admin", "10.0.0.1", "newpass123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(capturedSQL, "'admin'@'10.0.0.1'") {
		t.Errorf("expected user@host in SQL, got %q", capturedSQL)
	}
	if !strings.Contains(capturedSQL, "newpass123") {
		t.Errorf("expected new password in SQL")
	}
}

func TestChangePassword_DefaultHost(t *testing.T) {
	saveHooks(t)
	var capturedSQL string
	runMySQLFn = func(sql string) (string, error) {
		capturedSQL = sql
		return "", nil
	}

	err := ChangePassword("admin", "", "pw")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(capturedSQL, "'admin'@'localhost'") {
		t.Errorf("expected default host=localhost, got %q", capturedSQL)
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// ListUsers tests
// ══════════════════════════════════════════════════════════════════════════════

func TestListUsers_Success(t *testing.T) {
	saveHooks(t)
	runMySQLFn = func(sql string) (string, error) {
		return "alice\tlocalhost\nbob\t%\n", nil
	}

	users, err := ListUsers()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("expected 2 users, got %d", len(users))
	}
	if users[0].User != "alice" || users[0].Host != "localhost" {
		t.Errorf("unexpected user[0]: %+v", users[0])
	}
	if users[1].User != "bob" || users[1].Host != "%" {
		t.Errorf("unexpected user[1]: %+v", users[1])
	}
}

func TestListUsers_Error(t *testing.T) {
	saveHooks(t)
	runMySQLFn = func(sql string) (string, error) { return "", fmt.Errorf("no connection") }

	_, err := ListUsers()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestListUsers_Empty(t *testing.T) {
	saveHooks(t)
	runMySQLFn = func(sql string) (string, error) { return "", nil }

	users, err := ListUsers()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(users) != 0 {
		t.Errorf("expected 0 users, got %d", len(users))
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// ExportDatabase tests
// ══════════════════════════════════════════════════════════════════════════════

func TestExportDatabase_Success(t *testing.T) {
	saveHooks(t)
	execLookPathFn = lookPathFound("mariadb-dump")
	execCommandFn = fakeCmd("-- MariaDB dump\nCREATE TABLE...", 0)

	data, err := ExportDatabase("testdb")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(data), "MariaDB dump") {
		t.Errorf("expected dump data, got %q", string(data))
	}
}

func TestExportDatabase_NotFound(t *testing.T) {
	saveHooks(t)
	execLookPathFn = lookPathNone

	_, err := ExportDatabase("testdb")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not found or failed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestExportDatabase_FallbackToNoUser(t *testing.T) {
	saveHooks(t)
	execLookPathFn = lookPathFound("mariadb-dump")
	callN := 0
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		callN++
		if callN == 1 {
			// First call (with -u root) fails
			return fakeCmd("", 1)(name, args...)
		}
		// Second call (without -u root) succeeds
		return fakeCmd("-- dump via fallback", 0)(name, args...)
	}

	data, err := ExportDatabase("testdb")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(data), "fallback") {
		t.Errorf("expected fallback data, got %q", string(data))
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// ImportDatabase tests
// ══════════════════════════════════════════════════════════════════════════════

func TestImportDatabase_Success(t *testing.T) {
	saveHooks(t)
	execLookPathFn = lookPathFound("mariadb")
	execCommandFn = fakeCmd("", 0)

	err := ImportDatabase("testdb", []byte("CREATE TABLE t(id INT);"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestImportDatabase_NotFound(t *testing.T) {
	saveHooks(t)
	execLookPathFn = lookPathNone

	err := ImportDatabase("testdb", []byte("data"))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not found or import failed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestImportDatabase_FallbackToNoUser(t *testing.T) {
	saveHooks(t)
	execLookPathFn = lookPathFound("mysql")
	callN := 0
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		callN++
		if callN == 1 {
			return fakeCmd("", 1)(name, args...)
		}
		return fakeCmd("", 0)(name, args...)
	}

	err := ImportDatabase("testdb", []byte("CREATE TABLE t(id INT);"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// InstallMySQL tests
// ══════════════════════════════════════════════════════════════════════════════

func TestInstallMySQL_Windows(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "windows"
	_, err := InstallMySQL()
	if err == nil || !strings.Contains(err.Error(), "Windows") {
		t.Fatalf("expected Windows error, got %v", err)
	}
}

func TestInstallMySQL_AptSuccess(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "linux"
	execLookPathFn = lookPathFound("apt")
	execCommandFn = fakeCmd("installed mariadb", 0)
	runMySQLFn = func(sql string) (string, error) { return "", nil }

	out, err := InstallMySQL()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "installed mariadb") {
		t.Errorf("expected install output, got %q", out)
	}
}

func TestInstallMySQL_DnfSuccess(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "linux"
	execLookPathFn = lookPathFound("dnf") // apt not found, dnf found
	execCommandFn = fakeCmd("installed via dnf", 0)

	out, err := InstallMySQL()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "installed via dnf") {
		t.Errorf("expected dnf output, got %q", out)
	}
}

func TestInstallMySQL_NoPackageManager(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "linux"
	execLookPathFn = lookPathNone

	_, err := InstallMySQL()
	if err == nil || !strings.Contains(err.Error(), "no supported package manager") {
		t.Fatalf("expected no package manager error, got %v", err)
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// generateDBPassword tests
// ══════════════════════════════════════════════════════════════════════════════

func TestGenerateDBPassword(t *testing.T) {
	p1 := generateDBPassword()
	p2 := generateDBPassword()

	if len(p1) != 32 {
		t.Errorf("expected 32-char hex password, got %d chars: %q", len(p1), p1)
	}
	if p1 == p2 {
		t.Error("expected unique passwords")
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// escapeSQL tests (extended)
// ══════════════════════════════════════════════════════════════════════════════

func TestEscapeSQL_Quotes(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"simple", "simple"},
		{"", ""},
		{"hello world", "hello world"},
		// Correct order: escape backslashes FIRST, then quotes.
		// "it's" → no backslash → "it's" → "it\'s"
		{"it's", "it\\'s"},
		// "a\b" → "a\\b" → no quote → "a\\b"
		{"a\\b", "a\\\\b"},
		// Both: "a\'b" → "a\\\\'b" → "a\\\\'b" (already safe)
		// Null bytes stripped
		{"null\x00byte", "nullbyte"},
	}

	for _, tt := range tests {
		got := escapeSQL(tt.input)
		if got != tt.want {
			t.Errorf("escapeSQL(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// collectDBDiagnostics tests
// ══════════════════════════════════════════════════════════════════════════════

func TestCollectDBDiagnostics(t *testing.T) {
	saveHooks(t)
	execCommandFn = fakeCmd("Mar 26 12:00 mariadbd[1234]: ready for connections", 0)

	out := collectDBDiagnostics()
	if !strings.Contains(out, "journalctl -u mariadb") {
		t.Errorf("expected journal header, got %q", out)
	}
	if !strings.Contains(out, "ready for connections") {
		t.Errorf("expected journal content, got %q", out)
	}
}

func TestCollectDBDiagnostics_NoOutput(t *testing.T) {
	saveHooks(t)
	// Output < 10 bytes, so it should not be included
	execCommandFn = fakeCmd("short", 0)

	out := collectDBDiagnostics()
	if out != "" {
		t.Errorf("expected empty diagnostics, got %q", out)
	}
}

func TestCollectDBDiagnostics_CommandFails(t *testing.T) {
	saveHooks(t)
	execCommandFn = fakeCmd("", 1)

	out := collectDBDiagnostics()
	if out != "" {
		t.Errorf("expected empty diagnostics on failure, got %q", out)
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// Backtick tests (already exists, but keeping for coverage completeness)
// ══════════════════════════════════════════════════════════════════════════════

func TestBacktick(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"mydb", "`mydb`"},
		{"my`db", "`my``db`"},
		{"", "``"},
		{"test_db", "`test_db`"},
		{"`", "````"},
	}

	for _, tt := range tests {
		got := backtick(tt.input)
		if got != tt.want {
			t.Errorf("backtick(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// Struct tests (originally existing)
// ══════════════════════════════════════════════════════════════════════════════

func TestCreateResultStruct(t *testing.T) {
	r := CreateResult{
		Name:     "testdb",
		User:     "testuser",
		Password: "secret",
		Host:     "localhost",
	}

	if r.Name != "testdb" {
		t.Errorf("expected Name 'testdb', got %q", r.Name)
	}
	if r.User != "testuser" {
		t.Errorf("expected User 'testuser', got %q", r.User)
	}
	if r.Password != "secret" {
		t.Errorf("expected Password 'secret', got %q", r.Password)
	}
	if r.Host != "localhost" {
		t.Errorf("expected Host 'localhost', got %q", r.Host)
	}
}

func TestDBInfoStruct(t *testing.T) {
	info := DBInfo{
		Name:   "mydb",
		User:   "myuser",
		Host:   "localhost",
		Size:   "10 MB",
		Tables: 5,
	}

	if info.Name != "mydb" {
		t.Errorf("expected Name 'mydb', got %q", info.Name)
	}
	if info.Tables != 5 {
		t.Errorf("expected Tables 5, got %d", info.Tables)
	}
}

func TestStatusStruct(t *testing.T) {
	st := Status{
		Installed: true,
		Running:   true,
		Version:   "10.5.0",
		Backend:   "mariadb",
	}

	if !st.Installed {
		t.Error("expected Installed=true")
	}
	if st.Backend != "mariadb" {
		t.Errorf("expected Backend 'mariadb', got %q", st.Backend)
	}
}

func TestDBUserStruct(t *testing.T) {
	u := DBUser{User: "admin", Host: "localhost"}
	if u.User != "admin" {
		t.Errorf("expected admin, got %q", u.User)
	}
	if u.Host != "localhost" {
		t.Errorf("expected localhost, got %q", u.Host)
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// runMySQL (internal) direct tests — exercises the actual function body
// ══════════════════════════════════════════════════════════════════════════════

func TestRunMySQL_DirectSuccess(t *testing.T) {
	saveHooks(t)
	// Point runMySQLFn back to the real runMySQL so the actual body executes.
	runMySQLFn = runMySQL
	osMkdirAllFn = noopMkdirAll
	execLookPathFn = lookPathFound("mariadb")
	// Method 1 (direct) succeeds
	execCommandFn = fakeCmd("query result", 0)
	osStatFn = fakeStatNever

	out, err := runMySQLFn("SELECT 1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "query result") {
		t.Errorf("expected query result, got %q", out)
	}
}

func TestRunMySQL_NoUserFallback(t *testing.T) {
	saveHooks(t)
	runMySQLFn = runMySQL
	osMkdirAllFn = noopMkdirAll
	execLookPathFn = lookPathFound("mariadb")
	osStatFn = fakeStatNever
	callN := 0
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		callN++
		if callN <= 2 {
			// chown + first method (with -u root) fails
			return fakeCmd("", 1)(name, args...)
		}
		if callN == 3 {
			// chown for second dir
			return fakeCmd("", 1)(name, args...)
		}
		// fallback method (without -u root) succeeds
		return fakeCmd("fallback result", 0)(name, args...)
	}

	out, err := runMySQLFn("SELECT 1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "fallback result") {
		t.Errorf("expected fallback result, got %q", out)
	}
}

func TestRunMySQL_SocketFallback(t *testing.T) {
	saveHooks(t)
	runMySQLFn = runMySQL
	osMkdirAllFn = noopMkdirAll
	execLookPathFn = lookPathFound("mysql")
	// Socket exists
	osStatFn = fakeStatFor("/run/mysqld/mysqld.sock")
	callN := 0
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		callN++
		// chown calls (2) + direct method fail + no-user fallback fail + socket succeeds
		if callN <= 4 {
			return fakeCmd("fail", 1)(name, args...)
		}
		return fakeCmd("socket result", 0)(name, args...)
	}

	out, err := runMySQLFn("SELECT 1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "socket result") {
		t.Errorf("expected socket result, got %q", out)
	}
}

func TestRunMySQL_AllMethodsFail(t *testing.T) {
	saveHooks(t)
	runMySQLFn = runMySQL
	osMkdirAllFn = noopMkdirAll
	execLookPathFn = lookPathFound("mysql")
	osStatFn = fakeStatNever // no sockets
	execCommandFn = fakeCmd("error output", 1)

	_, err := runMySQLFn("SELECT 1")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "mysql error") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunMySQL_NoClient(t *testing.T) {
	saveHooks(t)
	runMySQLFn = runMySQL
	osMkdirAllFn = noopMkdirAll
	execLookPathFn = lookPathNone
	execCommandFn = fakeCmd("", 0) // for chown calls

	_, err := runMySQLFn("SELECT 1")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "neither mariadb nor mysql") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// Additional edge-case tests for remaining coverage gaps
// ══════════════════════════════════════════════════════════════════════════════

func TestListDatabases_OnlyNameField(t *testing.T) {
	saveHooks(t)
	// Line with only one field (name only, no size or tables)
	runMySQLFn = func(sql string) (string, error) {
		return "singledb\n", nil
	}

	dbs, err := ListDatabases()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(dbs) != 1 {
		t.Fatalf("expected 1 db, got %d", len(dbs))
	}
	if dbs[0].Name != "singledb" {
		t.Errorf("expected singledb, got %q", dbs[0].Name)
	}
	if dbs[0].Size != "" {
		t.Errorf("expected empty size, got %q", dbs[0].Size)
	}
}

func TestListDatabases_WhitespaceOnlyLine(t *testing.T) {
	saveHooks(t)
	// A line of only spaces/tabs — Fields returns empty slice, triggers len(fields)<1
	runMySQLFn = func(sql string) (string, error) {
		return "realdb\t1.00\t3\n   \t  \n", nil
	}

	dbs, err := ListDatabases()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(dbs) != 1 {
		t.Fatalf("expected 1 db (whitespace line skipped), got %d", len(dbs))
	}
}

func TestImportDatabase_AllMethodsFail(t *testing.T) {
	saveHooks(t)
	execLookPathFn = lookPathFound("mysql")
	// Both direct and sudo calls fail
	execCommandFn = fakeCmd("import error", 1)

	err := ImportDatabase("testdb", []byte("data"))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not found or import failed") {
		t.Errorf("unexpected error: %v", err)
	}
}

// Test ValidDBIdentifier
func TestValidDBIdentifier(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"empty", "", false},
		{"too_long", strings.Repeat("a", 65), false},
		{"valid_lowercase", "mydb", true},
		{"valid_uppercase", "MyDB", true},
		{"valid_mixed", "My_DB_123", true},
		{"valid_with_dash", "my-db", true},
		{"invalid_space", "my db", false},
		{"invalid_special", "my@db", false},
		{"valid_underscore", "my_db", true},
		{"valid_numeric", "db123", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ValidDBIdentifier(tt.input)
			if result != tt.expected {
				t.Errorf("ValidDBIdentifier(%q) = %v, expected %v", tt.input, result, tt.expected)
			}
		})
	}
}

// Test EscapeSQL
func TestEscapeSQL(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "hello"},
		{"it's", "it\\'s"},
		{"backslash\\", "backslash\\\\"},
		{"quote\"", "quote\\\""},
		{"null\x00char", "nullchar"},
		{"complex\\'\"", "complex\\\\\\'\\\""},
	}

	for _, tt := range tests {
		result := EscapeSQL(tt.input)
		if result != tt.expected {
			t.Errorf("EscapeSQL(%q) = %q, expected %q", tt.input, result, tt.expected)
		}
	}
}

// Test BacktickID
func TestBacktickID(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"mydb", "`mydb`"},
		{"my`db", "`my``db`"},
		{"`backtick", "```backtick`"},
		{"backtick`", "`backtick```"},
		{"`multiple`backticks`", "```multiple``backticks```"},
	}

	for _, tt := range tests {
		result := BacktickID(tt.input)
		if result != tt.expected {
			t.Errorf("BacktickID(%q) = %q, expected %q", tt.input, result, tt.expected)
		}
	}
}

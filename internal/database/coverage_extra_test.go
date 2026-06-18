package database

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// ══════════════════════════════════════════════════════════════════════════════
// RunSQL — delegates to runMySQLFn
// ══════════════════════════════════════════════════════════════════════════════

func TestRunSQL(t *testing.T) {
	saveHooks(t)
	var captured string
	runMySQLFn = func(sql string) (string, error) {
		captured = sql
		return "out", nil
	}
	out, err := RunSQL("SELECT 42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "out" {
		t.Errorf("expected out, got %q", out)
	}
	if captured != "SELECT 42" {
		t.Errorf("expected SQL forwarded, got %q", captured)
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// DatabaseExists
// ══════════════════════════════════════════════════════════════════════════════

func TestDatabaseExists_InvalidName(t *testing.T) {
	saveHooks(t)
	// invalid identifier -> (false, nil) without touching runMySQLFn
	runMySQLFn = func(string) (string, error) {
		t.Fatal("runMySQLFn should not be called for invalid name")
		return "", nil
	}
	ok, err := DatabaseExists("bad name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected false for invalid name")
	}
}

func TestDatabaseExists_Found(t *testing.T) {
	saveHooks(t)
	runMySQLFn = func(sql string) (string, error) {
		if !strings.Contains(sql, "SCHEMA_NAME = 'mydb'") {
			t.Errorf("unexpected SQL: %q", sql)
		}
		return "SCHEMA_NAME\nmydb\n", nil
	}
	ok, err := DatabaseExists("mydb")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("expected database to exist")
	}
}

func TestDatabaseExists_NotFound(t *testing.T) {
	saveHooks(t)
	runMySQLFn = func(string) (string, error) { return "", nil }
	ok, err := DatabaseExists("mydb")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected database to not exist")
	}
}

func TestDatabaseExists_HeaderOnly(t *testing.T) {
	saveHooks(t)
	// Only the header line and blanks — should be skipped, resulting in not-found.
	runMySQLFn = func(string) (string, error) { return "SCHEMA_NAME\n\n", nil }
	ok, err := DatabaseExists("mydb")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected not found when only header present")
	}
}

func TestDatabaseExists_QueryError(t *testing.T) {
	saveHooks(t)
	runMySQLFn = func(string) (string, error) { return "", fmt.Errorf("client missing") }
	ok, err := DatabaseExists("mydb")
	if err == nil {
		t.Fatal("expected error")
	}
	if ok {
		t.Error("expected false on error")
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// GetStatus — version command failure branch
// ══════════════════════════════════════════════════════════════════════════════

func TestGetStatus_VersionCommandFails(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "linux"
	execLookPathFn = lookPathFound("mariadb")
	// --version fails; ping methods also fail. Installed should still be true,
	// Version stays empty (line 65 false branch), Running=false.
	execCommandFn = fakeCmd("", 1)

	st := GetStatus()
	if !st.Installed {
		t.Fatal("expected Installed=true even when --version fails")
	}
	if st.Version != "" {
		t.Errorf("expected empty version on failure, got %q", st.Version)
	}
	if st.Running {
		t.Error("expected Running=false")
	}
}

func TestGetStatus_WindowsShortCircuit(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "windows"
	st := GetStatus()
	if st.Backend != "none" {
		t.Errorf("expected backend none on windows, got %q", st.Backend)
	}
	if st.Installed {
		t.Error("expected Installed=false on windows")
	}
}

func TestGetStatus_PingSucceedsButNoAliveToken(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "linux"
	execLookPathFn = lookPathFound("mariadb", "mysqladmin")
	// All exec commands exit 0 but output contains neither "alive" nor "1",
	// so the line-90 condition's right-hand side is false -> Running stays false.
	execCommandFn = fakeCmd("pong", 0)

	st := GetStatus()
	if !st.Installed {
		t.Fatal("expected Installed=true")
	}
	if st.Running {
		t.Error("expected Running=false when ping output lacks alive/1 token")
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// CreateDatabase — invalid identifiers
// ══════════════════════════════════════════════════════════════════════════════

func TestCreateDatabase_InvalidName(t *testing.T) {
	saveHooks(t)
	_, err := CreateDatabase("bad name", "", "", "")
	if err == nil || !strings.Contains(err.Error(), "invalid database name") {
		t.Fatalf("expected invalid database name error, got %v", err)
	}
}

func TestCreateDatabase_InvalidUser(t *testing.T) {
	saveHooks(t)
	_, err := CreateDatabase("gooddb", "bad user", "", "")
	if err == nil || !strings.Contains(err.Error(), "invalid username") {
		t.Fatalf("expected invalid username error, got %v", err)
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// DropDatabase — invalid name + error path
// ══════════════════════════════════════════════════════════════════════════════

func TestDropDatabase_InvalidName(t *testing.T) {
	saveHooks(t)
	err := DropDatabase("bad name", "", "")
	if err == nil || !strings.Contains(err.Error(), "invalid database name") {
		t.Fatalf("expected invalid database name error, got %v", err)
	}
}

func TestDropDatabase_RunMySQLError(t *testing.T) {
	saveHooks(t)
	runMySQLFn = func(string) (string, error) { return "", fmt.Errorf("denied") }
	err := DropDatabase("gooddb", "u", "localhost")
	if err == nil || !strings.Contains(err.Error(), "drop database") {
		t.Fatalf("expected drop database error, got %v", err)
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// ChangePassword — error path
// ══════════════════════════════════════════════════════════════════════════════

func TestChangePassword_RunMySQLError(t *testing.T) {
	saveHooks(t)
	runMySQLFn = func(string) (string, error) { return "", fmt.Errorf("denied") }
	err := ChangePassword("admin", "localhost", "pw")
	if err == nil || !strings.Contains(err.Error(), "change password") {
		t.Fatalf("expected change password error, got %v", err)
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// ExportDatabase — invalid name + mysqldump fallback chain
// ══════════════════════════════════════════════════════════════════════════════

func TestExportDatabase_InvalidName(t *testing.T) {
	saveHooks(t)
	_, err := ExportDatabase("bad name")
	if err == nil || !strings.Contains(err.Error(), "invalid database name") {
		t.Fatalf("expected invalid database name error, got %v", err)
	}
}

func TestExportDatabase_MysqldumpFallback(t *testing.T) {
	saveHooks(t)
	// mariadb-dump not found; mysqldump found and succeeds on first try.
	execLookPathFn = lookPathFound("mysqldump")
	execCommandFn = fakeCmd("-- mysqldump output", 0)

	data, err := ExportDatabase("testdb")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(data), "mysqldump output") {
		t.Errorf("expected mysqldump output, got %q", string(data))
	}
}

func TestExportDatabase_BothAttemptsFailThenNotFound(t *testing.T) {
	saveHooks(t)
	// mariadb-dump found, but both -u root and no-user attempts fail; then
	// mysqldump not found -> final error.
	execLookPathFn = lookPathFound("mariadb-dump")
	execCommandFn = fakeCmd("err", 1)

	_, err := ExportDatabase("testdb")
	if err == nil || !strings.Contains(err.Error(), "not found or failed") {
		t.Fatalf("expected not found or failed error, got %v", err)
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// ImportDatabase — invalid name
// ══════════════════════════════════════════════════════════════════════════════

func TestImportDatabase_InvalidName(t *testing.T) {
	saveHooks(t)
	err := ImportDatabase("bad name", []byte("data"))
	if err == nil || !strings.Contains(err.Error(), "invalid database name") {
		t.Fatalf("expected invalid database name error, got %v", err)
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// ConfigureRemoteAccess — validation + default branches
// ══════════════════════════════════════════════════════════════════════════════

func TestConfigureRemoteAccess_Windows(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "windows"
	_, err := ConfigureRemoteAccess("u", "%", "p", "")
	if err == nil || !strings.Contains(err.Error(), "Windows") {
		t.Fatalf("expected Windows error, got %v", err)
	}
}

func TestConfigureRemoteAccess_EmptyUser(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "linux"
	_, err := ConfigureRemoteAccess("", "%", "p", "")
	if err == nil || !strings.Contains(err.Error(), "user is required") {
		t.Fatalf("expected user required error, got %v", err)
	}
}

func TestConfigureRemoteAccess_InvalidUser(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "linux"
	_, err := ConfigureRemoteAccess("bad user", "%", "p", "")
	if err == nil || !strings.Contains(err.Error(), "invalid username") {
		t.Fatalf("expected invalid username error, got %v", err)
	}
}

func TestConfigureRemoteAccess_InvalidHost(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "linux"
	_, err := ConfigureRemoteAccess("u", "bad\nhost", "p", "")
	if err == nil || !strings.Contains(err.Error(), "invalid host") {
		t.Fatalf("expected invalid host error, got %v", err)
	}
}

func TestConfigureRemoteAccess_InvalidDatabase(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "linux"
	_, err := ConfigureRemoteAccess("u", "%", "p", "bad db")
	if err == nil || !strings.Contains(err.Error(), "invalid database name") {
		t.Fatalf("expected invalid database name error, got %v", err)
	}
}

func TestConfigureRemoteAccess_DefaultsHostAndPassword(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "linux"
	execLookPathFn = lookPathFound("mariadb")
	execCommandFn = fakeCmd("", 0) // systemctl restart succeeds
	// No config file exists -> falls through to default path candidate[0].
	osStatFn = fakeStatNever
	osReadFileFn = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	osMkdirAllFn = noopMkdirAll
	var capturedSQL string
	osWriteFileFn = func(string, []byte, fs.FileMode) error { return nil }
	runMySQLFn = func(sql string) (string, error) {
		capturedSQL = sql
		return "", nil
	}

	// host="" -> defaults to "%", password="" -> generated, no databaseName.
	res, err := ConfigureRemoteAccess("remote", "", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Host != "%" {
		t.Errorf("expected host defaulted to %%, got %q", res.Host)
	}
	if len(res.Password) != 32 {
		t.Errorf("expected generated 32-char password, got %d", len(res.Password))
	}
	if strings.Contains(capturedSQL, "GRANT ALL PRIVILEGES") {
		t.Errorf("did not expect GRANT when no database given: %q", capturedSQL)
	}
}

func TestConfigureRemoteAccess_RunMySQLError(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "linux"
	osStatFn = fakeStatNever
	osReadFileFn = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	osMkdirAllFn = noopMkdirAll
	osWriteFileFn = func(string, []byte, fs.FileMode) error { return nil }
	runMySQLFn = func(string) (string, error) { return "", fmt.Errorf("denied") }

	_, err := ConfigureRemoteAccess("u", "%", "p", "")
	if err == nil || !strings.Contains(err.Error(), "create remote user") {
		t.Fatalf("expected create remote user error, got %v", err)
	}
}

func TestConfigureRemoteAccess_RestartFails(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "linux"
	osStatFn = fakeStatNever
	osReadFileFn = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	osMkdirAllFn = noopMkdirAll
	osWriteFileFn = func(string, []byte, fs.FileMode) error { return nil }
	runMySQLFn = func(string) (string, error) { return "", nil }
	// systemctl restart fails for all services.
	execCommandFn = fakeCmd("restart failed", 1)

	_, err := ConfigureRemoteAccess("u", "%", "p", "")
	if err == nil || !strings.Contains(err.Error(), "restart failed") {
		t.Fatalf("expected restart failed error, got %v", err)
	}
}

func TestConfigureRemoteAccess_SetBindAddressReadError(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "linux"
	// Config file "exists" (stat succeeds) but read fails with a non-NotExist error.
	osStatFn = fakeStatFor("/etc/mysql/mariadb.conf.d/50-server.cnf")
	osReadFileFn = func(string) ([]byte, error) { return nil, fmt.Errorf("permission denied") }
	runMySQLFn = func(string) (string, error) { return "", nil }

	_, err := ConfigureRemoteAccess("u", "%", "p", "")
	if err == nil || !strings.Contains(err.Error(), "read mysql config") {
		t.Fatalf("expected read mysql config error, got %v", err)
	}
}

func TestConfigureRemoteAccess_MkdirError(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "linux"
	osStatFn = fakeStatNever
	osReadFileFn = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	osMkdirAllFn = func(string, fs.FileMode) error { return fmt.Errorf("mkdir denied") }
	runMySQLFn = func(string) (string, error) { return "", nil }

	_, err := ConfigureRemoteAccess("u", "%", "p", "")
	if err == nil || !strings.Contains(err.Error(), "create mysql config dir") {
		t.Fatalf("expected create mysql config dir error, got %v", err)
	}
}

func TestConfigureRemoteAccess_WriteError(t *testing.T) {
	saveHooks(t)
	runtimeGOOS = "linux"
	osStatFn = fakeStatNever
	osReadFileFn = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	osMkdirAllFn = noopMkdirAll
	osWriteFileFn = func(string, []byte, fs.FileMode) error { return fmt.Errorf("write denied") }
	runMySQLFn = func(string) (string, error) { return "", nil }

	_, err := ConfigureRemoteAccess("u", "%", "p", "")
	if err == nil || !strings.Contains(err.Error(), "write mysql config") {
		t.Fatalf("expected write mysql config error, got %v", err)
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// setBindAddressAllInterfaces — existing config file path selection
// ══════════════════════════════════════════════════════════════════════════════

func TestSetBindAddress_PicksExistingCandidate(t *testing.T) {
	saveHooks(t)
	// Second candidate exists -> path selection loop picks it.
	const chosen = "/etc/mysql/mysql.conf.d/mysqld.cnf"
	osStatFn = fakeStatFor(chosen)
	osReadFileFn = func(path string) ([]byte, error) {
		if path != chosen {
			t.Errorf("expected read of %q, got %q", chosen, path)
		}
		return []byte("[mysqld]\n"), nil
	}
	osMkdirAllFn = noopMkdirAll
	var writtenPath string
	osWriteFileFn = func(path string, _ []byte, _ fs.FileMode) error {
		writtenPath = path
		return nil
	}

	path, err := setBindAddressAllInterfaces()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != chosen || writtenPath != chosen {
		t.Errorf("expected chosen path %q, got returned=%q written=%q", chosen, path, writtenPath)
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// rewriteBindAddress — remaining branches
// ══════════════════════════════════════════════════════════════════════════════

func TestRewriteBindAddress_EmptyInput(t *testing.T) {
	got := rewriteBindAddress("")
	if !strings.Contains(got, "[mysqld]") || !strings.Contains(got, "bind-address = 0.0.0.0") {
		t.Errorf("expected default mysqld section, got %q", got)
	}
}

func TestRewriteBindAddress_NoMysqldSection(t *testing.T) {
	// Non-empty input without a [mysqld] section -> appends one at the end.
	in := "[client]\nport = 3306\n"
	got := rewriteBindAddress(in)
	if !strings.Contains(got, "[mysqld]") || !strings.Contains(got, "bind-address = 0.0.0.0") {
		t.Errorf("expected appended mysqld section, got %q", got)
	}
	if !strings.Contains(got, "[client]") {
		t.Errorf("expected original [client] preserved, got %q", got)
	}
}

func TestRewriteBindAddress_SectionTransitionInsertsBind(t *testing.T) {
	// [mysqld] section has no bind-address, then a new section starts ->
	// bind-address inserted before the next section header.
	in := "[mysqld]\nmax_connections = 100\n[client]\nport = 3306\n"
	got := rewriteBindAddress(in)
	idxBind := strings.Index(got, "bind-address = 0.0.0.0")
	idxClient := strings.Index(got, "[client]")
	if idxBind < 0 || idxClient < 0 {
		t.Fatalf("expected both bind-address and [client], got %q", got)
	}
	if idxBind > idxClient {
		t.Errorf("expected bind-address inserted before [client], got %q", got)
	}
}

func TestRewriteBindAddress_ServerSectionAlias(t *testing.T) {
	// [server] is treated like [mysqld] (MariaDB layout).
	in := "[server]\nuser = mysql\n"
	got := rewriteBindAddress(in)
	if !strings.Contains(got, "bind-address = 0.0.0.0") {
		t.Errorf("expected bind-address in [server] section, got %q", got)
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// runMySQLOnHost — Docker/TCP path (host + password set)
// ══════════════════════════════════════════════════════════════════════════════

func TestRunMySQLOnHost_TCPSuccess(t *testing.T) {
	saveHooks(t)
	execLookPathFn = lookPathFound("mariadb")
	var capturedArgs []string
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		capturedArgs = args
		return fakeCmd("tcp result", 0)(name, args...)
	}

	out, err := runMySQLOnHost("SELECT 1", "127.0.0.1", 3307, "secret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "tcp result") {
		t.Errorf("expected tcp result, got %q", out)
	}
	joined := strings.Join(capturedArgs, " ")
	if !strings.Contains(joined, "-psecret") || !strings.Contains(joined, "-P 3307") {
		t.Errorf("expected password + port args, got %q", joined)
	}
}

func TestRunMySQLOnHost_TCPNoPort(t *testing.T) {
	saveHooks(t)
	execLookPathFn = lookPathFound("mysql")
	var capturedArgs []string
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		capturedArgs = args
		return fakeCmd("ok", 0)(name, args...)
	}

	_, err := runMySQLOnHost("SELECT 1", "127.0.0.1", 0, "pw")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(strings.Join(capturedArgs, " "), "-P ") {
		t.Errorf("did not expect -P arg when port=0, got %v", capturedArgs)
	}
}

func TestRunMySQLOnHost_TCPError(t *testing.T) {
	saveHooks(t)
	execLookPathFn = lookPathFound("mariadb")
	execCommandFn = fakeCmd("connection refused", 1)

	_, err := runMySQLOnHost("SELECT 1", "127.0.0.1", 3307, "secret")
	if err == nil || !strings.Contains(err.Error(), "TCP error") {
		t.Fatalf("expected TCP error, got %v", err)
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// Docker SQL functions
// ══════════════════════════════════════════════════════════════════════════════

func TestContainerNameSafe(t *testing.T) {
	got := containerName_safe("my_db-1; rm -rf /")
	// Spaces, ';', '/' stripped; letters, digits, '_' and '-' kept.
	if got != "my_db-1rm-rf" {
		t.Errorf("containerName_safe sanitization = %q", got)
	}
}

func TestDockerDBExecSQL_MariaDB(t *testing.T) {
	saveDockerHook(t)
	dockerExecCommandFn = fakeDockerCmdRouter(map[string]cmdRoute{
		"inspect": {stdout: "mariadb:11", exitCode: 0},
		"exec":    {stdout: "row1\nrow2", exitCode: 0},
	}, cmdRoute{stdout: "", exitCode: 0})

	out, err := DockerDBExecSQL("web", "SELECT 1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "row1") {
		t.Errorf("expected rows, got %q", out)
	}
}

func TestDockerDBExecSQL_AlreadyPrefixed(t *testing.T) {
	saveDockerHook(t)
	var inspectName string
	dockerExecCommandFn = func(name string, args ...string) *exec.Cmd {
		if len(args) > 0 && args[0] == "inspect" {
			inspectName = args[len(args)-1]
			return fakeDockerCmd("mysql:8", 0)(name, args...)
		}
		return fakeDockerCmd("ok", 0)(name, args...)
	}

	_, err := DockerDBExecSQL("uwas-db-web", "SELECT 1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inspectName != "uwas-db-web" {
		t.Errorf("expected name not double-prefixed, got %q", inspectName)
	}
}

func TestDockerDBExecSQL_Postgres(t *testing.T) {
	saveDockerHook(t)
	var capturedArgs []string
	dockerExecCommandFn = func(name string, args ...string) *exec.Cmd {
		if len(args) > 0 && args[0] == "inspect" {
			return fakeDockerCmd("postgres:16", 0)(name, args...)
		}
		capturedArgs = args
		return fakeDockerCmd("pg result", 0)(name, args...)
	}

	out, err := DockerDBExecSQL("pg", "SELECT 1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "pg result") {
		t.Errorf("expected pg result, got %q", out)
	}
	if !strings.Contains(strings.Join(capturedArgs, " "), "psql") {
		t.Errorf("expected psql command for postgres, got %v", capturedArgs)
	}
}

func TestDockerDBExecSQL_InspectFails(t *testing.T) {
	saveDockerHook(t)
	dockerExecCommandFn = fakeDockerCmd("no such container", 1)

	_, err := DockerDBExecSQL("missing", "SELECT 1")
	if err == nil || !strings.Contains(err.Error(), "docker inspect") {
		t.Fatalf("expected docker inspect error, got %v", err)
	}
}

func TestDockerDBExecSQL_ExecFails(t *testing.T) {
	saveDockerHook(t)
	dockerExecCommandFn = func(name string, args ...string) *exec.Cmd {
		if len(args) > 0 && args[0] == "inspect" {
			return fakeDockerCmd("mariadb:11", 0)(name, args...)
		}
		return fakeDockerCmd("exec boom", 1)(name, args...)
	}

	_, err := DockerDBExecSQL("web", "SELECT 1")
	if err == nil || !strings.Contains(err.Error(), "docker exec sql") {
		t.Fatalf("expected docker exec sql error, got %v", err)
	}
}

func TestDockerDBListDatabases_Success(t *testing.T) {
	saveDockerHook(t)
	dockerExecCommandFn = func(name string, args ...string) *exec.Cmd {
		if len(args) > 0 && args[0] == "inspect" {
			return fakeDockerCmd("mariadb:11", 0)(name, args...)
		}
		return fakeDockerCmd("appdb\nshop\n", 0)(name, args...)
	}

	dbs, err := DockerDBListDatabases("web")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(dbs) != 2 || dbs[0].Name != "appdb" || dbs[1].Name != "shop" {
		t.Errorf("unexpected dbs: %+v", dbs)
	}
}

func TestDockerDBListDatabases_SkipsEmptyLines(t *testing.T) {
	saveDockerHook(t)
	dockerExecCommandFn = func(name string, args ...string) *exec.Cmd {
		if len(args) > 0 && args[0] == "inspect" {
			return fakeDockerCmd("mariadb:11", 0)(name, args...)
		}
		// Blank line between two names -> skipped by the `line == ""` branch.
		return fakeDockerCmd("appdb\n\nshop\n", 0)(name, args...)
	}

	dbs, err := DockerDBListDatabases("web")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(dbs) != 2 {
		t.Fatalf("expected 2 dbs (blank line skipped), got %d", len(dbs))
	}
}

func TestDockerDBListDatabases_Error(t *testing.T) {
	saveDockerHook(t)
	dockerExecCommandFn = fakeDockerCmd("inspect fail", 1)

	_, err := DockerDBListDatabases("web")
	if err == nil || !strings.Contains(err.Error(), "list databases in container") {
		t.Fatalf("expected list databases error, got %v", err)
	}
}

func TestDockerDBCreateDatabase_Success(t *testing.T) {
	saveDockerHook(t)
	dockerExecCommandFn = func(name string, args ...string) *exec.Cmd {
		if len(args) > 0 && args[0] == "inspect" {
			return fakeDockerCmd("mariadb:11", 0)(name, args...)
		}
		return fakeDockerCmd("", 0)(name, args...)
	}

	res, err := DockerDBCreateDatabase("web", "appdb", "appuser", "pw")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Name != "appdb" || res.User != "appuser" || res.Password != "pw" || res.Host != "web" {
		t.Errorf("unexpected result: %+v", res)
	}
}

func TestDockerDBCreateDatabase_Defaults(t *testing.T) {
	saveDockerHook(t)
	dockerExecCommandFn = func(name string, args ...string) *exec.Cmd {
		if len(args) > 0 && args[0] == "inspect" {
			return fakeDockerCmd("mariadb:11", 0)(name, args...)
		}
		return fakeDockerCmd("", 0)(name, args...)
	}

	res, err := DockerDBCreateDatabase("web", "appdb", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.User != "appdb" {
		t.Errorf("expected user defaulted to db name, got %q", res.User)
	}
	if len(res.Password) != 32 {
		t.Errorf("expected generated password, got %d chars", len(res.Password))
	}
}

func TestDockerDBCreateDatabase_InvalidDBName(t *testing.T) {
	saveDockerHook(t)
	_, err := DockerDBCreateDatabase("web", "bad name", "u", "p")
	if err == nil || !strings.Contains(err.Error(), "invalid database name") {
		t.Fatalf("expected invalid database name error, got %v", err)
	}
}

func TestDockerDBCreateDatabase_InvalidUser(t *testing.T) {
	saveDockerHook(t)
	_, err := DockerDBCreateDatabase("web", "gooddb", "bad user", "p")
	if err == nil || !strings.Contains(err.Error(), "invalid database user") {
		t.Fatalf("expected invalid database user error, got %v", err)
	}
}

func TestDockerDBCreateDatabase_ExecError(t *testing.T) {
	saveDockerHook(t)
	dockerExecCommandFn = func(name string, args ...string) *exec.Cmd {
		if len(args) > 0 && args[0] == "inspect" {
			return fakeDockerCmd("mariadb:11", 0)(name, args...)
		}
		return fakeDockerCmd("boom", 1)(name, args...)
	}

	_, err := DockerDBCreateDatabase("web", "appdb", "u", "p")
	if err == nil || !strings.Contains(err.Error(), "create database") {
		t.Fatalf("expected create database error, got %v", err)
	}
}

func TestDockerDBDropDatabase_Success(t *testing.T) {
	saveDockerHook(t)
	dockerExecCommandFn = func(name string, args ...string) *exec.Cmd {
		if len(args) > 0 && args[0] == "inspect" {
			return fakeDockerCmd("mariadb:11", 0)(name, args...)
		}
		return fakeDockerCmd("", 0)(name, args...)
	}

	if err := DockerDBDropDatabase("web", "appdb"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDockerDBDropDatabase_InvalidName(t *testing.T) {
	saveDockerHook(t)
	err := DockerDBDropDatabase("web", "bad name")
	if err == nil || !strings.Contains(err.Error(), "invalid database name") {
		t.Fatalf("expected invalid database name error, got %v", err)
	}
}

func TestDockerDBDropDatabase_ExecError(t *testing.T) {
	saveDockerHook(t)
	dockerExecCommandFn = func(name string, args ...string) *exec.Cmd {
		if len(args) > 0 && args[0] == "inspect" {
			return fakeDockerCmd("mariadb:11", 0)(name, args...)
		}
		return fakeDockerCmd("boom", 1)(name, args...)
	}

	err := DockerDBDropDatabase("web", "appdb")
	if err == nil || !strings.Contains(err.Error(), "drop database") {
		t.Fatalf("expected drop database error, got %v", err)
	}
}

func TestDockerDBExport_Success(t *testing.T) {
	saveDockerHook(t)
	dockerExecCommandFn = fakeDockerCmd("-- dump output", 0)

	out, err := DockerDBExport("web", "appdb")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "dump output") {
		t.Errorf("expected dump output, got %q", out)
	}
}

func TestDockerDBExport_AllDatabases(t *testing.T) {
	saveDockerHook(t)
	var capturedArgs []string
	dockerExecCommandFn = func(name string, args ...string) *exec.Cmd {
		capturedArgs = args
		return fakeDockerCmd("-- all dbs", 0)(name, args...)
	}

	out, err := DockerDBExport("uwas-db-web", "--all-databases")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "all dbs") {
		t.Errorf("expected output, got %q", out)
	}
	if !strings.Contains(strings.Join(capturedArgs, " "), "--all-databases") {
		t.Errorf("expected --all-databases arg, got %v", capturedArgs)
	}
}

func TestDockerDBExport_Error(t *testing.T) {
	saveDockerHook(t)
	dockerExecCommandFn = fakeDockerCmd("", 1)

	_, err := DockerDBExport("web", "appdb")
	if err == nil || !strings.Contains(err.Error(), "docker mysqldump") {
		t.Fatalf("expected docker mysqldump error, got %v", err)
	}
}

func TestDockerDBImport_Success(t *testing.T) {
	saveDockerHook(t)
	dockerExecCommandFn = fakeDockerCmd("", 0)

	if err := DockerDBImport("web", "appdb", "CREATE TABLE t(id INT);"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDockerDBImport_AlreadyPrefixed(t *testing.T) {
	saveDockerHook(t)
	var fullName string
	dockerExecCommandFn = func(name string, args ...string) *exec.Cmd {
		// docker exec -i <fullName> sh -c ...
		if len(args) >= 3 {
			fullName = args[2]
		}
		return fakeDockerCmd("", 0)(name, args...)
	}

	if err := DockerDBImport("uwas-db-web", "appdb", "data"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fullName != "uwas-db-web" {
		t.Errorf("expected name not double-prefixed, got %q", fullName)
	}
}

func TestDockerDBImport_Error(t *testing.T) {
	saveDockerHook(t)
	dockerExecCommandFn = fakeDockerCmd("import boom", 1)

	err := DockerDBImport("web", "appdb", "data")
	if err == nil || !strings.Contains(err.Error(), "docker import") {
		t.Fatalf("expected docker import error, got %v", err)
	}
}

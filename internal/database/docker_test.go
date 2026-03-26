package database

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// ── Docker helpers ───────────────────────────────────────────────────────────

func saveDockerHook(t *testing.T) {
	t.Helper()
	orig := dockerExecCommandFn
	t.Cleanup(func() { dockerExecCommandFn = orig })
}

func fakeDockerCmd(stdout string, exitCode int) func(string, ...string) *exec.Cmd {
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

// fakeDockerCmdRouter routes by first docker subcommand.
func fakeDockerCmdRouter(routes map[string]cmdRoute, fallback cmdRoute) func(string, ...string) *exec.Cmd {
	return func(name string, args ...string) *exec.Cmd {
		// The first arg after "docker" is the subcommand
		subcmd := ""
		if len(args) > 0 {
			subcmd = args[0]
		}
		if r, ok := routes[subcmd]; ok {
			return buildHelperCmd(name, args, r.stdout, r.exitCode)
		}
		return buildHelperCmd(name, args, fallback.stdout, fallback.exitCode)
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// imageForEngine tests
// ══════════════════════════════════════════════════════════════════════════════

func TestImageForEngine(t *testing.T) {
	tests := []struct {
		engine DockerDBEngine
		want   string
	}{
		{EngineMariaDB, "mariadb:11"},
		{EngineMySQL, "mysql:8"},
		{EnginePostgreSQL, "postgres:16"},
		{DockerDBEngine("redis"), ""},
	}
	for _, tt := range tests {
		got := imageForEngine(tt.engine)
		if got != tt.want {
			t.Errorf("imageForEngine(%q) = %q, want %q", tt.engine, got, tt.want)
		}
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// defaultPort tests
// ══════════════════════════════════════════════════════════════════════════════

func TestDefaultPort(t *testing.T) {
	tests := []struct {
		engine DockerDBEngine
		want   int
	}{
		{EngineMariaDB, 3306},
		{EngineMySQL, 3306},
		{EnginePostgreSQL, 5432},
	}
	for _, tt := range tests {
		got := defaultPort(tt.engine)
		if got != tt.want {
			t.Errorf("defaultPort(%q) = %d, want %d", tt.engine, got, tt.want)
		}
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// volumePath tests
// ══════════════════════════════════════════════════════════════════════════════

func TestVolumePath(t *testing.T) {
	tests := []struct {
		engine DockerDBEngine
		want   string
	}{
		{EngineMariaDB, "mysql"},
		{EngineMySQL, "mysql"},
		{EnginePostgreSQL, "postgresql/data"},
	}
	for _, tt := range tests {
		got := volumePath(tt.engine)
		if got != tt.want {
			t.Errorf("volumePath(%q) = %q, want %q", tt.engine, got, tt.want)
		}
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// DockerAvailable tests
// ══════════════════════════════════════════════════════════════════════════════

func TestDockerAvailable_Success(t *testing.T) {
	saveDockerHook(t)
	dockerExecCommandFn = fakeDockerCmd("24.0.7", 0)

	if !DockerAvailable() {
		t.Error("expected DockerAvailable=true")
	}
}

func TestDockerAvailable_Failure(t *testing.T) {
	saveDockerHook(t)
	dockerExecCommandFn = fakeDockerCmd("", 1)

	if DockerAvailable() {
		t.Error("expected DockerAvailable=false")
	}
}

func TestDockerAvailable_EmptyOutput(t *testing.T) {
	saveDockerHook(t)
	dockerExecCommandFn = fakeDockerCmd("", 0)

	if DockerAvailable() {
		t.Error("expected DockerAvailable=false for empty output")
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// DockerVersion tests
// ══════════════════════════════════════════════════════════════════════════════

func TestDockerVersion(t *testing.T) {
	saveDockerHook(t)
	dockerExecCommandFn = fakeDockerCmd("24.0.7", 0)

	v := DockerVersion()
	if v != "24.0.7" {
		t.Errorf("expected 24.0.7, got %q", v)
	}
}

func TestDockerVersion_Empty(t *testing.T) {
	saveDockerHook(t)
	dockerExecCommandFn = fakeDockerCmd("", 1)

	v := DockerVersion()
	if v != "" {
		t.Errorf("expected empty string, got %q", v)
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// CreateDockerDB tests
// ══════════════════════════════════════════════════════════════════════════════

func TestCreateDockerDB_Success(t *testing.T) {
	saveDockerHook(t)
	dockerExecCommandFn = fakeDockerCmdRouter(map[string]cmdRoute{
		"ps":  {stdout: "", exitCode: 0},    // no existing container
		"run": {stdout: "abc123def456xx", exitCode: 0}, // new container ID
	}, cmdRoute{stdout: "", exitCode: 0})

	c, err := CreateDockerDB(EngineMariaDB, "test1", 3307, "rootpw", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.ID != "abc123def456" {
		t.Errorf("expected truncated ID abc123def456, got %q", c.ID)
	}
	if c.Name != "uwas-db-test1" {
		t.Errorf("expected name uwas-db-test1, got %q", c.Name)
	}
	if c.Engine != EngineMariaDB {
		t.Errorf("expected mariadb engine, got %q", c.Engine)
	}
	if c.Port != 3307 {
		t.Errorf("expected port 3307, got %d", c.Port)
	}
	if !c.Running {
		t.Error("expected Running=true")
	}
}

func TestCreateDockerDB_UnsupportedEngine(t *testing.T) {
	saveDockerHook(t)
	_, err := CreateDockerDB(DockerDBEngine("redis"), "test1", 6379, "pw", "")
	if err == nil || !strings.Contains(err.Error(), "unsupported engine") {
		t.Fatalf("expected unsupported engine error, got %v", err)
	}
}

func TestCreateDockerDB_AlreadyExists(t *testing.T) {
	saveDockerHook(t)
	dockerExecCommandFn = fakeDockerCmdRouter(map[string]cmdRoute{
		"ps": {stdout: "existing_container_id", exitCode: 0},
	}, cmdRoute{stdout: "", exitCode: 0})

	_, err := CreateDockerDB(EngineMariaDB, "test1", 3307, "pw", "")
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected already exists error, got %v", err)
	}
}

func TestCreateDockerDB_WithDataDir(t *testing.T) {
	saveDockerHook(t)
	dockerExecCommandFn = fakeDockerCmdRouter(map[string]cmdRoute{
		"ps":  {stdout: "", exitCode: 0},
		"run": {stdout: "shortid", exitCode: 0},
	}, cmdRoute{stdout: "", exitCode: 0})

	c, err := CreateDockerDB(EngineMariaDB, "test2", 3308, "pw", "/data/mysql")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.DataDir != "/data/mysql" {
		t.Errorf("expected datadir /data/mysql, got %q", c.DataDir)
	}
}

func TestCreateDockerDB_PostgreSQL(t *testing.T) {
	saveDockerHook(t)
	dockerExecCommandFn = fakeDockerCmdRouter(map[string]cmdRoute{
		"ps":  {stdout: "", exitCode: 0},
		"run": {stdout: "pgcontainerid1", exitCode: 0},
	}, cmdRoute{stdout: "", exitCode: 0})

	c, err := CreateDockerDB(EnginePostgreSQL, "pgdb", 5433, "pgpass", "/data/pg")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Engine != EnginePostgreSQL {
		t.Errorf("expected postgresql engine, got %q", c.Engine)
	}
	if c.Image != "postgres:16" {
		t.Errorf("expected postgres:16 image, got %q", c.Image)
	}
	if c.Port != 5433 {
		t.Errorf("expected port 5433, got %d", c.Port)
	}
}

func TestCreateDockerDB_MySQL(t *testing.T) {
	saveDockerHook(t)
	dockerExecCommandFn = fakeDockerCmdRouter(map[string]cmdRoute{
		"ps":  {stdout: "", exitCode: 0},
		"run": {stdout: "mysqlcontainer", exitCode: 0},
	}, cmdRoute{stdout: "", exitCode: 0})

	c, err := CreateDockerDB(EngineMySQL, "mydb", 3309, "pw", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Image != "mysql:8" {
		t.Errorf("expected mysql:8 image, got %q", c.Image)
	}
}

func TestCreateDockerDB_RunFails(t *testing.T) {
	saveDockerHook(t)
	dockerExecCommandFn = fakeDockerCmdRouter(map[string]cmdRoute{
		"ps":  {stdout: "", exitCode: 0},
		"run": {stdout: "port already in use", exitCode: 1},
	}, cmdRoute{stdout: "", exitCode: 0})

	_, err := CreateDockerDB(EngineMariaDB, "test1", 3307, "pw", "")
	if err == nil || !strings.Contains(err.Error(), "docker run") {
		t.Fatalf("expected docker run error, got %v", err)
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// ListDockerDBs tests
// ══════════════════════════════════════════════════════════════════════════════

func TestListDockerDBs_Success(t *testing.T) {
	saveDockerHook(t)
	jsonLine1 := `{"ID":"abc123","Names":"uwas-db-web","Image":"mariadb:11","Status":"Up 2 hours","Ports":"0.0.0.0:3307->3306/tcp","State":"running"}`
	jsonLine2 := `{"ID":"def456","Names":"uwas-db-pg","Image":"postgres:16","Status":"Exited (0) 1 hour ago","Ports":"","State":"exited"}`
	dockerExecCommandFn = fakeDockerCmd(jsonLine1+"\n"+jsonLine2, 0)

	containers, err := ListDockerDBs()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(containers) != 2 {
		t.Fatalf("expected 2 containers, got %d", len(containers))
	}
	if containers[0].ID != "abc123" {
		t.Errorf("expected ID abc123, got %q", containers[0].ID)
	}
	if containers[0].Engine != EngineMariaDB {
		t.Errorf("expected mariadb engine, got %q", containers[0].Engine)
	}
	if !containers[0].Running {
		t.Error("expected first container to be running")
	}
	if containers[1].Engine != EnginePostgreSQL {
		t.Errorf("expected postgresql engine, got %q", containers[1].Engine)
	}
	if containers[1].Running {
		t.Error("expected second container not running")
	}
}

func TestListDockerDBs_MySQL(t *testing.T) {
	saveDockerHook(t)
	jsonLine := `{"ID":"m1","Names":"uwas-db-my","Image":"mysql:8","Status":"Up","Ports":"","State":"running"}`
	dockerExecCommandFn = fakeDockerCmd(jsonLine, 0)

	containers, err := ListDockerDBs()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(containers))
	}
	if containers[0].Engine != EngineMySQL {
		t.Errorf("expected mysql engine, got %q", containers[0].Engine)
	}
}

func TestListDockerDBs_Empty(t *testing.T) {
	saveDockerHook(t)
	dockerExecCommandFn = fakeDockerCmd("", 0)

	containers, err := ListDockerDBs()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(containers) != 0 {
		t.Errorf("expected 0 containers, got %d", len(containers))
	}
}

func TestListDockerDBs_Error(t *testing.T) {
	saveDockerHook(t)
	dockerExecCommandFn = fakeDockerCmd("", 1)

	_, err := ListDockerDBs()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "docker ps") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestListDockerDBs_BadJSON(t *testing.T) {
	saveDockerHook(t)
	dockerExecCommandFn = fakeDockerCmd("not json at all\n{bad", 0)

	containers, err := ListDockerDBs()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Bad JSON lines are silently skipped
	if len(containers) != 0 {
		t.Errorf("expected 0 containers (bad json skipped), got %d", len(containers))
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// StartDockerDB tests
// ══════════════════════════════════════════════════════════════════════════════

func TestStartDockerDB_Success(t *testing.T) {
	saveDockerHook(t)
	dockerExecCommandFn = fakeDockerCmd("uwas-db-test", 0)

	err := StartDockerDB("test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStartDockerDB_Failure(t *testing.T) {
	saveDockerHook(t)
	dockerExecCommandFn = fakeDockerCmd("no such container", 1)

	err := StartDockerDB("nonexistent")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "docker start") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// StopDockerDB tests
// ══════════════════════════════════════════════════════════════════════════════

func TestStopDockerDB_Success(t *testing.T) {
	saveDockerHook(t)
	dockerExecCommandFn = fakeDockerCmd("uwas-db-test", 0)

	err := StopDockerDB("test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStopDockerDB_Failure(t *testing.T) {
	saveDockerHook(t)
	dockerExecCommandFn = fakeDockerCmd("no such container", 1)

	err := StopDockerDB("nonexistent")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "docker stop") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// RemoveDockerDB tests
// ══════════════════════════════════════════════════════════════════════════════

func TestRemoveDockerDB_Success(t *testing.T) {
	saveDockerHook(t)
	dockerExecCommandFn = fakeDockerCmd("", 0)

	err := RemoveDockerDB("test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRemoveDockerDB_Failure(t *testing.T) {
	saveDockerHook(t)
	dockerExecCommandFn = fakeDockerCmd("no such container", 1)

	err := RemoveDockerDB("nonexistent")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "docker rm") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// DockerDBContainer struct test
// ══════════════════════════════════════════════════════════════════════════════

func TestDockerDBContainerStruct(t *testing.T) {
	c := DockerDBContainer{
		ID:       "abc123",
		Name:     "uwas-db-test",
		Engine:   EngineMariaDB,
		Image:    "mariadb:11",
		Port:     3307,
		Status:   "Up 2 hours",
		Running:  true,
		RootPass: "secret",
		DataDir:  "/data",
	}
	if c.ID != "abc123" {
		t.Errorf("expected ID abc123, got %q", c.ID)
	}
	if c.Engine != EngineMariaDB {
		t.Errorf("expected mariadb engine, got %q", c.Engine)
	}
	if c.RootPass != "secret" {
		t.Errorf("expected secret root pass, got %q", c.RootPass)
	}
}

func TestContainerPrefix(t *testing.T) {
	if containerPrefix != "uwas-db-" {
		t.Errorf("expected uwas-db- prefix, got %q", containerPrefix)
	}
}

func TestEngineConstants(t *testing.T) {
	if EngineMariaDB != "mariadb" {
		t.Errorf("expected mariadb, got %q", EngineMariaDB)
	}
	if EngineMySQL != "mysql" {
		t.Errorf("expected mysql, got %q", EngineMySQL)
	}
	if EnginePostgreSQL != "postgresql" {
		t.Errorf("expected postgresql, got %q", EnginePostgreSQL)
	}
}

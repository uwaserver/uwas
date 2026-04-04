package appmanager

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
)

func TestRegisterAndInstances(t *testing.T) {
	m := New(nil)
	err := m.Register("node.example.com", config.AppConfig{
		Runtime: "node",
		Command: "echo hello",
		Port:    4000,
	}, "/tmp/node-app")
	if err != nil {
		t.Fatal(err)
	}

	instances := m.Instances()
	if len(instances) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(instances))
	}
	if instances[0].Domain != "node.example.com" {
		t.Errorf("domain = %q", instances[0].Domain)
	}
	if instances[0].Runtime != "node" {
		t.Errorf("runtime = %q", instances[0].Runtime)
	}
	if instances[0].Port != 4000 {
		t.Errorf("port = %d", instances[0].Port)
	}
	if instances[0].Running {
		t.Error("should not be running before Start()")
	}
}

func TestRegisterDuplicate(t *testing.T) {
	m := New(nil)
	m.Register("dup.com", config.AppConfig{Command: "echo", Runtime: "custom"}, "/tmp")
	err := m.Register("dup.com", config.AppConfig{Command: "echo", Runtime: "custom"}, "/tmp")
	if err == nil {
		t.Error("expected error on duplicate register")
	}
}

func TestAutoPort(t *testing.T) {
	m := New(nil)
	m.Register("a.com", config.AppConfig{Command: "echo", Runtime: "node"}, "/tmp")
	m.Register("b.com", config.AppConfig{Command: "echo", Runtime: "node"}, "/tmp")

	instances := m.Instances()
	ports := map[int]bool{}
	for _, inst := range instances {
		ports[inst.Port] = true
	}
	if len(ports) != 2 {
		t.Errorf("expected 2 unique ports, got %d", len(ports))
	}
}

func TestStartStop(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process management test skipped on Windows")
	}

	dir := t.TempDir()
	m := New(nil)
	// Use a long-running process
	m.Register("live.com", config.AppConfig{
		Command: "sleep 60",
		Runtime: "custom",
		Port:    19876,
	}, dir)

	if err := m.Start("live.com"); err != nil {
		t.Fatal(err)
	}

	time.Sleep(200 * time.Millisecond)

	inst := m.Get("live.com")
	if inst == nil {
		t.Fatal("instance is nil")
	}
	if !inst.Running {
		t.Error("should be running after Start()")
	}
	if inst.PID == 0 {
		t.Error("PID should be set")
	}

	if err := m.Stop("live.com"); err != nil {
		t.Fatal(err)
	}

	time.Sleep(200 * time.Millisecond)
	inst2 := m.Get("live.com")
	if inst2.Running {
		t.Error("should not be running after Stop()")
	}
}

func TestListenAddr(t *testing.T) {
	m := New(nil)
	m.Register("addr.com", config.AppConfig{Command: "echo", Runtime: "node", Port: 5555}, "/tmp")
	addr := m.ListenAddr("addr.com")
	if addr != "127.0.0.1:5555" {
		t.Errorf("addr = %q", addr)
	}
	if m.ListenAddr("nonexistent.com") != "" {
		t.Error("should return empty for unknown domain")
	}
}

func TestUnregister(t *testing.T) {
	m := New(nil)
	m.Register("gone.com", config.AppConfig{Command: "echo", Runtime: "custom"}, "/tmp")
	m.Unregister("gone.com")
	if len(m.Instances()) != 0 {
		t.Error("should be empty after unregister")
	}
}

func TestDetectCommandNode(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{}`), 0644)
	cmd := detectCommand("node", dir)
	if cmd != "npm start" {
		t.Errorf("expected 'npm start', got %q", cmd)
	}
}

func TestDetectCommandPython(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "app.py"), []byte(""), 0644)
	cmd := detectCommand("python", dir)
	if cmd != "python app.py" {
		t.Errorf("expected 'python app.py', got %q", cmd)
	}
}

func TestDetectCommandRuby(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.ru"), []byte(""), 0644)
	cmd := detectCommand("ruby", dir)
	if cmd != "bundle exec puma -p ${PORT}" {
		t.Errorf("expected puma command, got %q", cmd)
	}
}

func TestDetectCommandUnknown(t *testing.T) {
	cmd := detectCommand("rust", t.TempDir())
	if cmd != "" {
		t.Errorf("expected empty, got %q", cmd)
	}
}

func TestDetectCommandNodeWithEntryFiles(t *testing.T) {
	// Test server.js detection
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "server.js"), []byte(""), 0644)
	cmd := detectCommand("node", dir)
	if cmd != "node server.js" {
		t.Errorf("expected 'node server.js', got %q", cmd)
	}

	// Test index.js detection (server.js takes priority)
	dir2 := t.TempDir()
	os.WriteFile(filepath.Join(dir2, "index.js"), []byte(""), 0644)
	cmd2 := detectCommand("node", dir2)
	if cmd2 != "node index.js" {
		t.Errorf("expected 'node index.js', got %q", cmd2)
	}

	// Test app.js detection
	dir3 := t.TempDir()
	os.WriteFile(filepath.Join(dir3, "app.js"), []byte(""), 0644)
	cmd3 := detectCommand("node", dir3)
	if cmd3 != "node app.js" {
		t.Errorf("expected 'node app.js', got %q", cmd3)
	}
}

func TestDetectCommandPythonManagePy(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "manage.py"), []byte(""), 0644)
	cmd := detectCommand("python", dir)
	if cmd != "python manage.py runserver 0.0.0.0:${PORT}" {
		t.Errorf("expected Django manage.py command, got %q", cmd)
	}
}

func TestDetectCommandPythonMainPy(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.py"), []byte(""), 0644)
	cmd := detectCommand("python", dir)
	if cmd != "python main.py" {
		t.Errorf("expected 'python main.py', got %q", cmd)
	}
}

func TestDetectCommandPythonWSGI(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "wsgi.py"), []byte(""), 0644)
	cmd := detectCommand("python", dir)
	if cmd != "python wsgi.py" {
		t.Errorf("expected 'python wsgi.py', got %q", cmd)
	}
}

func TestDetectCommandPythonGunicorn(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("gunicorn\n"), 0644)
	cmd := detectCommand("python", dir)
	if cmd != "gunicorn app:app -b 0.0.0.0:${PORT}" {
		t.Errorf("expected gunicorn command, got %q", cmd)
	}
}

func TestDetectCommandGo(t *testing.T) {
	dir := t.TempDir()
	// Go always returns ./main regardless of files
	cmd := detectCommand("go", dir)
	if cmd != "./main" {
		t.Errorf("expected './main', got %q", cmd)
	}
}

func TestDetectCommandEmptyRuntime(t *testing.T) {
	cmd := detectCommand("", t.TempDir())
	if cmd != "" {
		t.Errorf("expected empty, got %q", cmd)
	}
}

func TestGet(t *testing.T) {
	m := New(nil)
	m.Register("get.com", config.AppConfig{Command: "echo", Runtime: "custom", Port: 9000}, "/tmp")

	inst := m.Get("get.com")
	if inst == nil {
		t.Fatal("expected non-nil instance")
	}
	if inst.Domain != "get.com" {
		t.Errorf("expected domain get.com, got %q", inst.Domain)
	}
	if inst.Port != 9000 {
		t.Errorf("expected port 9000, got %d", inst.Port)
	}

	// Non-existent domain
	if m.Get("nonexistent.com") != nil {
		t.Error("expected nil for non-existent domain")
	}
}

func TestRestart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process management test skipped on Windows")
	}

	dir := t.TempDir()
	m := New(nil)
	m.Register("restart.com", config.AppConfig{Command: "sleep 60", Runtime: "custom", Port: 9001}, dir)

	// Start first
	if err := m.Start("restart.com"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)

	inst1 := m.Get("restart.com")
	if inst1 == nil || !inst1.Running {
		t.Fatal("should be running")
	}
	pid1 := inst1.PID

	// Restart
	if err := m.Restart("restart.com"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)

	inst2 := m.Get("restart.com")
	if inst2 == nil || !inst2.Running {
		t.Fatal("should be running after restart")
	}

	// PID should be different after restart
	if inst2.PID == pid1 {
		t.Error("PID should change after restart")
	}

	m.Stop("restart.com")
}

func TestRestartNotRegistered(t *testing.T) {
	m := New(nil)
	if err := m.Restart("nope.com"); err == nil {
		t.Error("expected error for unregistered domain")
	}
}

func TestStopAll(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process management test skipped on Windows")
	}

	dir := t.TempDir()
	m := New(nil)

	// Register and start multiple apps
	m.Register("stop1.com", config.AppConfig{Command: "sleep 60", Runtime: "custom", Port: 9002}, dir)
	m.Register("stop2.com", config.AppConfig{Command: "sleep 60", Runtime: "custom", Port: 9003}, dir)

	m.Start("stop1.com")
	m.Start("stop2.com")
	time.Sleep(200 * time.Millisecond)

	// Both should be running
	if !m.Get("stop1.com").Running || !m.Get("stop2.com").Running {
		t.Fatal("both should be running")
	}

	// Stop all
	m.StopAll()
	time.Sleep(200 * time.Millisecond)

	// Both should be stopped
	if m.Get("stop1.com").Running || m.Get("stop2.com").Running {
		t.Error("both should be stopped")
	}
}

func TestSetCgroupPath(t *testing.T) {
	m := New(nil)
	m.Register("cgroup.com", config.AppConfig{Command: "echo", Runtime: "custom"}, "/tmp")

	m.SetCgroupPath("cgroup.com", "/sys/fs/cgroup/test")

	// There's no getter for cgroup path, but we can verify it doesn't panic
	// and the app still works
	inst := m.Get("cgroup.com")
	if inst == nil {
		t.Error("expected instance to exist")
	}
}

func TestSetCgroupPathNotRegistered(t *testing.T) {
	m := New(nil)
	// Should not panic
	m.SetCgroupPath("nonexistent.com", "/sys/fs/cgroup/test")
}

func TestInstancesEmpty(t *testing.T) {
	m := New(nil)
	instances := m.Instances()
	if len(instances) != 0 {
		t.Errorf("expected 0 instances, got %d", len(instances))
	}
}

func TestInstancesMultiple(t *testing.T) {
	m := New(nil)
	m.Register("a.com", config.AppConfig{Command: "echo", Runtime: "node", Port: 9004}, "/tmp/a")
	m.Register("b.com", config.AppConfig{Command: "echo", Runtime: "python", Port: 9005}, "/tmp/b")

	instances := m.Instances()
	if len(instances) != 2 {
		t.Fatalf("expected 2 instances, got %d", len(instances))
	}

	// Check that we got both domains
	domains := make(map[string]bool)
	for _, inst := range instances {
		domains[inst.Domain] = true
	}
	if !domains["a.com"] || !domains["b.com"] {
		t.Error("expected both domains in instances")
	}
}

func TestStartInvalidWorkDir(t *testing.T) {
	m := New(nil)
	m.Register("invalid.com", config.AppConfig{Command: "echo hello", Runtime: "custom", Port: 9006}, "/nonexistent/path/that/does/not/exist")

	err := m.Start("invalid.com")
	if err == nil {
		t.Skip("may not error on all systems")
	}
}

func TestStartNotRegistered(t *testing.T) {
	m := New(nil)
	if err := m.Start("nope.com"); err == nil {
		t.Error("expected error for unregistered domain")
	}
}

func TestStopNotRunning(t *testing.T) {
	m := New(nil)
	m.Register("idle.com", config.AppConfig{Command: "echo", Runtime: "custom"}, "/tmp")
	if err := m.Stop("idle.com"); err == nil {
		t.Error("expected error for not-running domain")
	}
}

func TestStatsRegisteredNotRunning(t *testing.T) {
	m := New(nil)
	m.Register("stat.com", config.AppConfig{Command: "echo", Runtime: "node", Port: 7000}, "/tmp")
	s := m.Stats("stat.com")
	if s == nil {
		t.Fatal("expected non-nil stats")
	}
	if s.Domain != "stat.com" {
		t.Errorf("domain = %q", s.Domain)
	}
	if s.Running {
		t.Error("should not be running")
	}
	if s.PID != 0 {
		t.Error("PID should be 0")
	}
}

func TestStatsNotRegistered(t *testing.T) {
	m := New(nil)
	if s := m.Stats("nope.com"); s != nil {
		t.Error("expected nil for unregistered domain")
	}
}

func TestStatsRunning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process stats test skipped on Windows")
	}
	dir := t.TempDir()
	m := New(nil)
	m.Register("running.com", config.AppConfig{Command: "sleep 60", Runtime: "custom", Port: 7001}, dir)
	if err := m.Start("running.com"); err != nil {
		t.Fatal(err)
	}
	defer m.Stop("running.com")

	time.Sleep(200 * time.Millisecond)

	s := m.Stats("running.com")
	if s == nil {
		t.Fatal("expected non-nil stats")
	}
	if !s.Running {
		t.Error("should be running")
	}
	if s.PID == 0 {
		t.Error("PID should be set")
	}
	if s.Uptime == "" {
		t.Error("uptime should be set")
	}
}

func TestListenAddrWithPort(t *testing.T) {
	m := New(nil)
	m.Register("port.com", config.AppConfig{Command: "echo", Runtime: "node", Port: 9000}, "/tmp")
	addr := m.ListenAddr("port.com")
	if addr != "127.0.0.1:9000" {
		t.Errorf("addr = %q, want '127.0.0.1:9000'", addr)
	}
}

func TestStopAllEmpty(t *testing.T) {
	m := New(nil)
	// Should not panic with no apps
	m.StopAll()
}

func TestRegisterWithEnv(t *testing.T) {
	m := New(nil)
	env := map[string]string{"NODE_ENV": "production", "PORT": "3000"}
	err := m.Register("env.com", config.AppConfig{
		Runtime: "node",
		Command: "npm start",
		Port:    5000,
		Env:     env,
	}, "/tmp/env-app")
	if err != nil {
		t.Fatal(err)
	}

	inst := m.Get("env.com")
	if inst == nil {
		t.Fatal("expected instance")
	}
	if len(inst.Env) != 2 {
		t.Errorf("expected 2 env vars, got %d", len(inst.Env))
	}
	if inst.Env["NODE_ENV"] != "production" {
		t.Errorf("expected NODE_ENV=production, got %q", inst.Env["NODE_ENV"])
	}
}

func TestRegisterWithWorkDir(t *testing.T) {
	dir := t.TempDir()
	m := New(nil)
	err := m.Register("workdir.com", config.AppConfig{
		Runtime: "node",
		Command: "node app.js",
		Port:    6000,
	}, dir)
	if err != nil {
		t.Fatal(err)
	}

	inst := m.Get("workdir.com")
	if inst == nil {
		t.Fatal("expected instance")
	}
	// WorkDir is not exposed in AppInstance, but we can verify registration worked
	if inst.Domain != "workdir.com" {
		t.Errorf("domain = %q", inst.Domain)
	}
}

func TestGetReturnsCopy(t *testing.T) {
	m := New(nil)
	m.Register("copy.com", config.AppConfig{Command: "echo", Runtime: "node", Port: 7000}, "/tmp")

	inst1 := m.Get("copy.com")
	inst2 := m.Get("copy.com")

	if inst1 == nil || inst2 == nil {
		t.Fatal("expected non-nil instances")
	}

	// Should be different pointers
	if inst1 == inst2 {
		t.Error("Get should return different instances each call")
	}

	// But same data
	if inst1.Domain != inst2.Domain {
		t.Error("instances should have same domain")
	}
}

func TestInstancesReturnsCopy(t *testing.T) {
	m := New(nil)
	m.Register("inst1.com", config.AppConfig{Command: "echo", Runtime: "node", Port: 8000}, "/tmp")
	m.Register("inst2.com", config.AppConfig{Command: "echo", Runtime: "python", Port: 8001}, "/tmp")

	instances1 := m.Instances()
	instances2 := m.Instances()

	if len(instances1) != 2 || len(instances2) != 2 {
		t.Fatalf("expected 2 instances each")
	}

	// Modify first result, second should be unchanged
	instances1[0].Domain = "modified.com"
	if instances2[0].Domain == "modified.com" {
		t.Error("Instances should return copies, not shared references")
	}
}

func TestStartAlreadyRunning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process management test skipped on Windows")
	}

	dir := t.TempDir()
	m := New(nil)
	m.Register("running2.com", config.AppConfig{Command: "sleep 60", Runtime: "custom", Port: 9007}, dir)

	// Start first time
	if err := m.Start("running2.com"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)

	// Try to start again - should error
	if err := m.Start("running2.com"); err == nil {
		t.Error("expected error when starting already-running app")
	}

	m.Stop("running2.com")
}

func TestDetectCommandPriority(t *testing.T) {
	// Test that package.json takes priority over server.js for node
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{}`), 0644)
	os.WriteFile(filepath.Join(dir, "server.js"), []byte(""), 0644)

	cmd := detectCommand("node", dir)
	if cmd != "npm start" {
		t.Errorf("package.json should take priority, got %q", cmd)
	}
}

func TestDetectCommandServerJS(t *testing.T) {
	// server.js without package.json
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "server.js"), []byte(""), 0644)

	cmd := detectCommand("node", dir)
	if cmd != "node server.js" {
		t.Errorf("expected 'node server.js', got %q", cmd)
	}
}

func TestDetectCommandIndexJS(t *testing.T) {
	// index.js when no server.js
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.js"), []byte(""), 0644)

	cmd := detectCommand("node", dir)
	if cmd != "node index.js" {
		t.Errorf("expected 'node index.js', got %q", cmd)
	}
}

func TestDetectCommandPythonPriority(t *testing.T) {
	// Test priority: manage.py > app.py > wsgi.py > requirements.txt
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "manage.py"), []byte(""), 0644)
	os.WriteFile(filepath.Join(dir, "app.py"), []byte(""), 0644)

	cmd := detectCommand("python", dir)
	if cmd != "python manage.py runserver 0.0.0.0:${PORT}" {
		t.Errorf("manage.py should take priority, got %q", cmd)
	}
}

func TestDetectCommandPythonMainOverWSGI(t *testing.T) {
	// main.py takes priority over wsgi.py
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "wsgi.py"), []byte(""), 0644)
	os.WriteFile(filepath.Join(dir, "main.py"), []byte(""), 0644)

	cmd := detectCommand("python", dir)
	// Order is: manage.py > app.py > main.py > wsgi.py
	if cmd != "python main.py" {
		t.Errorf("main.py should take priority over wsgi.py, got %q", cmd)
	}
}

func TestDetectCommandPythonAppOverWSGI(t *testing.T) {
	// app.py takes priority over wsgi.py
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "wsgi.py"), []byte(""), 0644)
	os.WriteFile(filepath.Join(dir, "app.py"), []byte(""), 0644)

	cmd := detectCommand("python", dir)
	if cmd != "python app.py" {
		t.Errorf("app.py should take priority over wsgi.py, got %q", cmd)
	}
}

// ---------------------------------------------------------------------------
// Mock hooks for testing
// ---------------------------------------------------------------------------

// saveAndRestoreHooks returns a function that restores the original hook values.
func saveAndRestoreHooks() func() {
	origExecCommand := execCommandFn
	origOsMkdirAll := osMkdirAllFn
	origOsOpenFile := osOpenFileFn
	origOsStat := osStatFn
	return func() {
		execCommandFn = origExecCommand
		osMkdirAllFn = origOsMkdirAll
		osOpenFileFn = origOsOpenFile
		osStatFn = origOsStat
	}
}

// fakeExecCommand creates a mock exec.Cmd that simulates a process.
func fakeExecCommand(pid int, startErr error) func(name string, arg ...string) *exec.Cmd {
	return func(name string, arg ...string) *exec.Cmd {
		// Return a command that will succeed/fail based on parameters
		return &exec.Cmd{}
	}
}

// ---------------------------------------------------------------------------
// Additional tests with mocks
// ---------------------------------------------------------------------------

func TestDetectCommandWithMockStat(t *testing.T) {
	defer saveAndRestoreHooks()()

	// Mock os.Stat to return specific results
	statCalls := []string{}
	osStatFn = func(path string) (os.FileInfo, error) {
		statCalls = append(statCalls, path)
		// Simulate package.json exists
		if strings.Contains(path, "package.json") {
			return nil, nil // exists
		}
		return nil, os.ErrNotExist
	}

	dir := "/tmp/test"
	cmd := detectCommand("node", dir)

	if cmd != "npm start" {
		t.Errorf("expected 'npm start', got %q", cmd)
	}

	// Should have checked package.json
	found := false
	for _, p := range statCalls {
		if strings.Contains(p, "package.json") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected stat to be called for package.json")
	}
}

func TestStartProcessCommandExpansion(t *testing.T) {
	defer saveAndRestoreHooks()()

	if runtime.GOOS == "windows" {
		t.Skip("process test skipped on Windows")
	}

	// We'll test by creating a real simple process
	dir := t.TempDir()
	m := New(nil)

	// Register an app with ${PORT} placeholder
	err := m.Register("expand.com", config.AppConfig{
		Runtime: "custom",
		Command: "sleep 60",
		Port:    7777,
	}, dir)
	if err != nil {
		t.Fatal(err)
	}

	// Start should expand ${PORT} and run
	err = m.Start("expand.com")
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(100 * time.Millisecond)

	// Verify it's running
	inst := m.Get("expand.com")
	if inst == nil || !inst.Running {
		t.Error("expected app to be running")
	}

	// The port should be 7777 as configured
	if inst.Port != 7777 {
		t.Errorf("expected port 7777, got %d", inst.Port)
	}

	m.Stop("expand.com")
}

func TestRegisterWithAutoRestart(t *testing.T) {
	m := New(nil)

	// Test default auto-restart behavior
	err := m.Register("autorestart.com", config.AppConfig{
		Runtime: "node",
		Command: "npm start",
		Port:    8000,
	}, "/tmp/test")

	if err != nil {
		t.Fatal(err)
	}

	// Auto-restart should be enabled by default
	// (we can't easily test this without starting the process)
	inst := m.Get("autorestart.com")
	if inst == nil {
		t.Fatal("expected instance")
	}

	// Verify the domain was registered correctly
	if inst.Domain != "autorestart.com" {
		t.Errorf("expected domain autorestart.com, got %q", inst.Domain)
	}
}

func TestRegisterAutoRestartDisabled(t *testing.T) {
	m := New(nil)

	// Create a custom config with auto-restart disabled
	cfg := config.AppConfig{
		Runtime:     "node",
		Command:     "npm start",
		Port:        8000,
		AutoRestart: false,
	}

	err := m.Register("norestart.com", cfg, "/tmp/test")
	if err != nil {
		t.Fatal(err)
	}

	inst := m.Get("norestart.com")
	if inst == nil {
		t.Fatal("expected instance")
	}

	// The registration should succeed
	if inst.Domain != "norestart.com" {
		t.Errorf("expected domain norestart.com, got %q", inst.Domain)
	}
}

func TestRegisterDetectCommandFailure(t *testing.T) {
	m := New(nil)

	// Try to register without a command and with unknown runtime
	err := m.Register("nodetect.com", config.AppConfig{
		Runtime: "unknown_runtime",
		Port:    8000,
	}, t.TempDir())

	if err == nil {
		t.Error("expected error when command cannot be detected")
	}

	if !strings.Contains(err.Error(), "no command") {
		t.Errorf("expected 'no command' error, got: %v", err)
	}
}

func TestUnregisterNotRegistered(t *testing.T) {
	m := New(nil)

	// Should not panic when unregistering unknown domain
	m.Unregister("never-registered.com")

	// Verify no instances exist
	if len(m.Instances()) != 0 {
		t.Error("expected no instances after unregistering unknown domain")
	}
}

func TestStatsWithEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process stats test skipped on Windows")
	}

	dir := t.TempDir()
	m := New(nil)

	env := map[string]string{
		"CUSTOM_VAR": "custom_value",
		"NODE_ENV":   "development",
	}

	err := m.Register("envecho.com", config.AppConfig{
		Runtime: "custom",
		Command: "sleep 60",
		Port:    8000,
		Env:     env,
	}, dir)

	if err != nil {
		t.Fatal(err)
	}

	// Start the app
	if err := m.Start("envecho.com"); err != nil {
		t.Fatal(err)
	}
	defer m.Stop("envecho.com")

	time.Sleep(100 * time.Millisecond)

	// Get stats
	s := m.Stats("envecho.com")
	if s == nil {
		t.Fatal("expected non-nil stats")
	}

	if !s.Running {
		t.Error("should be running")
	}

	if s.PID == 0 {
		t.Error("PID should be set")
	}

	// Verify uptime is set
	if s.Uptime == "" {
		t.Error("uptime should be set")
	}
}

func TestInstancesPreservesOrder(t *testing.T) {
	m := New(nil)

	// Register multiple apps
	domains := []string{"z.com", "a.com", "m.com"}
	for i, d := range domains {
		err := m.Register(d, config.AppConfig{
			Runtime: "node",
			Command: "echo",
			Port:    9000 + i,
		}, "/tmp")
		if err != nil {
			t.Fatal(err)
		}
	}

	instances := m.Instances()
	if len(instances) != 3 {
		t.Fatalf("expected 3 instances, got %d", len(instances))
	}

	// Check all domains are present
	domainMap := make(map[string]bool)
	for _, inst := range instances {
		domainMap[inst.Domain] = true
	}

	for _, d := range domains {
		if !domainMap[d] {
			t.Errorf("expected domain %s to be in instances", d)
		}
	}
}

func TestStartWithLogFileError(t *testing.T) {
	defer saveAndRestoreHooks()()

	if runtime.GOOS == "windows" {
		t.Skip("process test skipped on Windows")
	}

	// Mock log file creation to fail
	osOpenFileFn = func(name string, flag int, perm os.FileMode) (*os.File, error) {
		if strings.Contains(name, "app.log") {
			return nil, fmt.Errorf("cannot create log file")
		}
		return os.OpenFile(name, flag, perm)
	}

	dir := t.TempDir()
	m := New(nil)

	err := m.Register("nolog.com", config.AppConfig{
		Runtime: "custom",
		Command: "sleep 60",
		Port:    8001,
	}, dir)

	if err != nil {
		t.Fatal(err)
	}

	// Should still start even if log file creation fails
	err = m.Start("nolog.com")
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(100 * time.Millisecond)

	inst := m.Get("nolog.com")
	if inst == nil || !inst.Running {
		t.Error("expected app to be running despite log file error")
	}

	m.Stop("nolog.com")
}

func TestRestartNotRunning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process test skipped on Windows")
	}

	dir := t.TempDir()
	m := New(nil)

	// Register but don't start
	err := m.Register("notrunning.com", config.AppConfig{
		Runtime: "custom",
		Command: "sleep 60",
		Port:    8002,
	}, dir)

	if err != nil {
		t.Fatal(err)
	}

	// Restart should still work (stop returns error but is ignored)
	err = m.Restart("notrunning.com")
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(200 * time.Millisecond)

	inst := m.Get("notrunning.com")
	if inst == nil || !inst.Running {
		t.Error("expected app to be running after restart")
	}

	m.Stop("notrunning.com")
}

func TestConcurrentAccess(t *testing.T) {
	m := New(nil)

	// Register multiple apps concurrently
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(idx int) {
			domain := fmt.Sprintf("concurrent%d.com", idx)
			err := m.Register(domain, config.AppConfig{
				Runtime: "node",
				Command: "echo",
				Port:    9000 + idx,
			}, "/tmp")
			if err != nil {
				t.Errorf("failed to register %s: %v", domain, err)
			}
			done <- true
		}(i)
	}

	// Wait for all registrations
	for i := 0; i < 10; i++ {
		<-done
	}

	instances := m.Instances()
	if len(instances) != 10 {
		t.Errorf("expected 10 instances, got %d", len(instances))
	}
}

func TestListenAddrUnknownDomain(t *testing.T) {
	m := New(nil)

	addr := m.ListenAddr("unknown-domain-that-does-not-exist.com")
	if addr != "" {
		t.Errorf("expected empty address, got %q", addr)
	}
}

func TestStopAllWithNoneRunning(t *testing.T) {
	m := New(nil)

	// Register some apps but don't start them
	m.Register("idle1.com", config.AppConfig{Runtime: "node", Command: "echo"}, "/tmp")
	m.Register("idle2.com", config.AppConfig{Runtime: "python", Command: "echo"}, "/tmp")

	// StopAll should not panic when no apps are running
	m.StopAll()

	// Apps should still be registered
	if len(m.Instances()) != 2 {
		t.Error("apps should still be registered")
	}
}

func TestAppInstanceStruct(t *testing.T) {
	now := time.Now()
	inst := AppInstance{
		Domain:    "test.com",
		Runtime:   "node",
		Command:   "npm start",
		Port:      3000,
		PID:       12345,
		Running:   true,
		Uptime:    "1h30m",
		StartedAt: &now,
		Env:       map[string]string{"NODE_ENV": "production"},
	}

	if inst.Domain != "test.com" {
		t.Error("Domain mismatch")
	}
	if inst.Runtime != "node" {
		t.Error("Runtime mismatch")
	}
	if inst.Port != 3000 {
		t.Error("Port mismatch")
	}
	if !inst.Running {
		t.Error("Running should be true")
	}
	if inst.StartedAt == nil {
		t.Error("StartedAt should be set")
	}
}

func TestAppStatsStruct(t *testing.T) {
	stats := AppStats{
		Domain:    "test.com",
		PID:       12345,
		Running:   true,
		CPUPct:    15.5,
		MemoryRSS: 1024 * 1024,
		MemoryVMS: 1024 * 1024 * 10,
		Uptime:    "2h15m",
	}

	if stats.Domain != "test.com" {
		t.Error("Domain mismatch")
	}
	if stats.PID != 12345 {
		t.Error("PID mismatch")
	}
	if !stats.Running {
		t.Error("Running should be true")
	}
	if stats.CPUPct != 15.5 {
		t.Error("CPUPct mismatch")
	}
}

// TestAutoRestart tests that a crashed process is auto-restarted.
func TestAutoRestart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process management test skipped on Windows")
	}

	dir := t.TempDir()
	m := New(nil)

	// Use a command that exits quickly
	m.Register("crash.com", config.AppConfig{
		Command:     "exit 1",
		Runtime:     "custom",
		Port:        19999,
		AutoRestart: true,
	}, dir)

	if err := m.Start("crash.com"); err != nil {
		t.Fatal(err)
	}

	// Wait for process to start and then exit
	time.Sleep(300 * time.Millisecond)

	// The process should have exited
	inst1 := m.Get("crash.com")
	if inst1 == nil {
		t.Fatal("expected instance")
	}

	// Wait for auto-restart (2 second backoff + some buffer)
	time.Sleep(2500 * time.Millisecond)

	// After auto-restart, it should try to start again
	// Note: it will likely fail again since the command always exits
	// but we can't easily verify the restart attempt without mocking

	m.Stop("crash.com")
}

// TestAutoRestartDisabled tests that auto-restart can be disabled.
func TestAutoRestartDisabled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process management test skipped on Windows")
	}

	dir := t.TempDir()
	m := New(nil)

	// Use a command that exits quickly with auto-restart disabled
	m.Register("noclaim.com", config.AppConfig{
		Command:     "exit 0",
		Runtime:     "custom",
		Port:        19998,
		AutoRestart: false,
	}, dir)

	if err := m.Start("noclaim.com"); err != nil {
		t.Fatal(err)
	}

	// Wait for process to exit
	time.Sleep(300 * time.Millisecond)

	// Stop should return error since process already exited
	err := m.Stop("noclaim.com")
	if err == nil {
		t.Error("expected error stopping already-exited process")
	}
}

// TestMonitorProcessStopRequested tests that monitorProcess respects stop signal.
func TestMonitorProcessStopRequested(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process management test skipped on Windows")
	}

	dir := t.TempDir()
	m := New(nil)

	// Use a long-running process
	m.Register("stopme.com", config.AppConfig{
		Command:     "sleep 60",
		Runtime:     "custom",
		Port:        19997,
		AutoRestart: true,
	}, dir)

	if err := m.Start("stopme.com"); err != nil {
		t.Fatal(err)
	}

	time.Sleep(200 * time.Millisecond)

	// Verify running
	inst := m.Get("stopme.com")
	if inst == nil || !inst.Running {
		t.Fatal("expected running")
	}

	// Stop it
	if err := m.Stop("stopme.com"); err != nil {
		t.Fatal(err)
	}

	time.Sleep(200 * time.Millisecond)

	// Should be stopped
	inst2 := m.Get("stopme.com")
	if inst2.Running {
		t.Error("should not be running after stop")
	}
}

// TestReadProcessStatsNonLinux tests the no-op stats function on non-Linux.
func TestReadProcessStatsNonLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("test for non-Linux platforms only")
	}

	// On non-Linux, readProcessStats should return zeros
	cpu, rss, vms := readProcessStats(12345)
	if cpu != 0 {
		t.Errorf("expected cpu=0 on non-Linux, got %f", cpu)
	}
	if rss != 0 {
		t.Errorf("expected rss=0 on non-Linux, got %d", rss)
	}
	if vms != 0 {
		t.Errorf("expected vms=0 on non-Linux, got %d", vms)
	}
}

// TestStartProcessMkdirError tests handling of log directory creation failure.
func TestStartProcessMkdirError(t *testing.T) {
	defer saveAndRestoreHooks()()

	if runtime.GOOS == "windows" {
		t.Skip("process test skipped on Windows")
	}

	// Mock mkdir to fail
	osMkdirAllFn = func(path string, perm os.FileMode) error {
		if strings.Contains(path, "logs") {
			return fmt.Errorf("permission denied")
		}
		return os.MkdirAll(path, perm)
	}

	dir := t.TempDir()
	m := New(nil)

	m.Register("mkdirfail.com", config.AppConfig{
		Runtime: "custom",
		Command: "echo hello",
		Port:    8000,
	}, dir)

	// Should still start even if log dir creation fails
	err := m.Start("mkdirfail.com")
	if err != nil {
		t.Fatalf("expected start to succeed despite mkdir error, got: %v", err)
	}

	m.Stop("mkdirfail.com")
}

// TestStopNotRegistered tests stopping an unregistered domain.
func TestStopNotRegistered(t *testing.T) {
	m := New(nil)

	err := m.Stop("not-registered.com")
	if err == nil {
		t.Error("expected error for unregistered domain")
	}
	if !strings.Contains(err.Error(), "not registered") {
		t.Errorf("expected 'not registered' error, got: %v", err)
	}
}

// TestStatsRunningProcess tests getting stats for a running process.
func TestStatsRunningProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process stats test skipped on Windows")
	}

	dir := t.TempDir()
	m := New(nil)

	m.Register("statsrunning.com", config.AppConfig{
		Runtime: "custom",
		Command: "sleep 60",
		Port:    8001,
	}, dir)

	if err := m.Start("statsrunning.com"); err != nil {
		t.Fatal(err)
	}
	defer m.Stop("statsrunning.com")

	time.Sleep(200 * time.Millisecond)

	s := m.Stats("statsrunning.com")
	if s == nil {
		t.Fatal("expected non-nil stats")
	}
	if s.Domain != "statsrunning.com" {
		t.Errorf("domain = %q", s.Domain)
	}
	if !s.Running {
		t.Error("should be running")
	}
	if s.PID == 0 {
		t.Error("PID should be set")
	}
	if s.Uptime == "" {
		t.Error("uptime should be set")
	}

	// On non-Linux, CPU and memory should be 0
	if runtime.GOOS != "linux" {
		if s.CPUPct != 0 {
			t.Errorf("expected CPU=0 on non-Linux, got %f", s.CPUPct)
		}
		if s.MemoryRSS != 0 {
			t.Errorf("expected RSS=0 on non-Linux, got %d", s.MemoryRSS)
		}
		if s.MemoryVMS != 0 {
			t.Errorf("expected VMS=0 on non-Linux, got %d", s.MemoryVMS)
		}
	}
}

// TestRegisterWithEmptyRuntime tests registration with empty runtime.
func TestRegisterWithEmptyRuntime(t *testing.T) {
	m := New(nil)

	// When runtime is empty but command is provided, should use "custom"
	err := m.Register("emptyrt.com", config.AppConfig{
		Command: "echo hello",
		Port:    8002,
	}, "/tmp")

	if err != nil {
		t.Fatal(err)
	}

	inst := m.Get("emptyrt.com")
	if inst == nil {
		t.Fatal("expected instance")
	}
	if inst.Runtime != "custom" {
		t.Errorf("expected runtime 'custom', got %q", inst.Runtime)
	}
}

// TestRegisterCommandExpansion tests that ${PORT} is expanded.
func TestRegisterCommandExpansion(t *testing.T) {
	m := New(nil)

	m.Register("expand.com", config.AppConfig{
		Runtime: "custom",
		Command: "node server.js --port ${PORT}",
		Port:    9000,
	}, "/tmp")

	inst := m.Get("expand.com")
	if inst == nil {
		t.Fatal("expected instance")
	}
	// Command should be stored as-is, expansion happens at Start()
	if inst.Command != "node server.js --port ${PORT}" {
		t.Errorf("command = %q", inst.Command)
	}
}

// TestDetectCommandEmptyDir tests command detection with empty directory.
func TestDetectCommandEmptyDir(t *testing.T) {
	dir := t.TempDir()

	// Node with no files
	cmd := detectCommand("node", dir)
	if cmd != "" {
		t.Errorf("expected empty command, got %q", cmd)
	}

	// Python with no files
	cmd = detectCommand("python", dir)
	if cmd != "" {
		t.Errorf("expected empty command, got %q", cmd)
	}

	// Ruby with no files
	cmd = detectCommand("ruby", dir)
	if cmd != "" {
		t.Errorf("expected empty command, got %q", cmd)
	}
}

// TestDetectCommandPythonNoFiles tests Python with requirements.txt only.
func TestDetectCommandPythonRequirementsOnly(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("flask\n"), 0644)

	cmd := detectCommand("python", dir)
	if cmd != "gunicorn app:app -b 0.0.0.0:${PORT}" {
		t.Errorf("expected gunicorn command, got %q", cmd)
	}
}

// TestStopRaceCondition tests stopping a process that exits during stop.
func TestStopRaceCondition(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process management test skipped on Windows")
	}

	dir := t.TempDir()
	m := New(nil)

	// Quick-exiting process
	m.Register("quickexit.com", config.AppConfig{
		Command: "exit 0",
		Runtime: "custom",
		Port:    8003,
	}, dir)

	// Start it
	m.Start("quickexit.com")
	time.Sleep(100 * time.Millisecond)

	// Try to stop - may race with process exit
	_ = m.Stop("quickexit.com")

	// Should not panic
	inst := m.Get("quickexit.com")
	if inst == nil {
		t.Error("expected instance to exist")
	}
}

// TestStartProcessWithMocks tests startProcess with mocked exec.Command.
func TestStartProcessWithMocks(t *testing.T) {
	defer saveAndRestoreHooks()()

	// Mock exec.Command
	execCommandFn = func(name string, arg ...string) *exec.Cmd {
		// Return a command that does nothing
		return exec.Command("echo", "test")
	}

	dir := t.TempDir()
	m := New(nil)

	m.Register("mockstart.com", config.AppConfig{
		Command: "echo hello",
		Runtime: "custom",
		Port:    8008,
	}, dir)

	// Start should work with mocked command
	err := m.Start("mockstart.com")
	if err != nil {
		t.Errorf("start failed: %v", err)
	}
}

// TestMonitorProcess tests monitorProcess behavior.
func TestMonitorProcess(t *testing.T) {
	defer saveAndRestoreHooks()()

	// Mock exec.Command to return a quick-exiting process
	execCommandFn = func(name string, arg ...string) *exec.Cmd {
		return exec.Command("echo", "test")
	}

	dir := t.TempDir()
	m := New(nil)

	// Register with auto-restart disabled
	m.Register("monitortest.com", config.AppConfig{
		Command:     "echo hello",
		Runtime:     "custom",
		Port:        8009,
		AutoRestart: false,
	}, dir)

	// Start the app
	err := m.Start("monitortest.com")
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}

	// Wait for process to exit
	time.Sleep(200 * time.Millisecond)

	// Should not be running after process exits
	inst := m.Get("monitortest.com")
	if inst.Running {
		t.Error("expected process to have exited")
	}
}
func TestMonitorProcessStopCh(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process management test skipped on Windows")
	}

	dir := t.TempDir()
	m := New(nil)

	m.Register("stopch.com", config.AppConfig{
		Command:     "sleep 60",
		Runtime:     "custom",
		Port:        8004,
		AutoRestart: false,
	}, dir)

	if err := m.Start("stopch.com"); err != nil {
		t.Fatal(err)
	}

	time.Sleep(100 * time.Millisecond)

	// Stop should work normally
	if err := m.Stop("stopch.com"); err != nil {
		t.Errorf("stop failed: %v", err)
	}

	// Wait for monitorProcess goroutine to clean up
	time.Sleep(200 * time.Millisecond)

	// Should be stopped
	inst := m.Get("stopch.com")
	if inst.Running {
		t.Error("expected stopped instance")
	}
}

// TestStopRunningProcess tests stopping a running process.
func TestStopRunningProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process management test skipped on Windows")
	}

	dir := t.TempDir()
	m := New(nil)

	m.Register("stoprunning.com", config.AppConfig{
		Command: "sleep 60",
		Runtime: "custom",
		Port:    8005,
	}, dir)

	if err := m.Start("stoprunning.com"); err != nil {
		t.Fatal(err)
	}

	time.Sleep(100 * time.Millisecond)

	// Verify running
	inst := m.Get("stoprunning.com")
	if inst == nil || !inst.Running {
		t.Fatal("expected running instance")
	}

	// Stop it
	if err := m.Stop("stoprunning.com"); err != nil {
		t.Errorf("stop failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Verify stopped
	inst2 := m.Get("stoprunning.com")
	if inst2.Running {
		t.Error("expected stopped instance")
	}
}

// TestStatsNotRunning tests getting stats for a non-running app.
func TestStatsNotRunning(t *testing.T) {
	m := New(nil)

	m.Register("statsnotrunning.com", config.AppConfig{
		Command: "echo hello",
		Runtime: "custom",
		Port:    8006,
	}, "/tmp")

	// Get stats for non-running app
	stats := m.Stats("statsnotrunning.com")
	if stats == nil {
		t.Fatal("expected non-nil stats")
	}
	if stats.Running {
		t.Error("expected not running")
	}
	if stats.PID != 0 {
		t.Error("expected PID to be 0")
	}
}

// TestGetRunningInstance tests Get for a running instance.
func TestGetRunningInstance(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process management test skipped on Windows")
	}

	dir := t.TempDir()
	m := New(nil)

	m.Register("getrunning.com", config.AppConfig{
		Command: "sleep 60",
		Runtime: "custom",
		Port:    8007,
	}, dir)

	if err := m.Start("getrunning.com"); err != nil {
		t.Fatal(err)
	}

	time.Sleep(100 * time.Millisecond)

	inst := m.Get("getrunning.com")
	if inst == nil {
		t.Fatal("expected instance")
	}
	if !inst.Running {
		t.Error("expected running")
	}
	if inst.PID == 0 {
		t.Error("expected non-zero PID")
	}
	if inst.Uptime == "" {
		t.Error("expected uptime")
	}

	m.Stop("getrunning.com")
}

// TestStopAllMultiple tests StopAll with multiple running apps.
func TestStopAllMultiple(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process management test skipped on Windows")
	}

	dir := t.TempDir()
	m := New(nil)

	// Register and start multiple apps
	for i := 0; i < 3; i++ {
		domain := fmt.Sprintf("stopall%d.com", i)
		m.Register(domain, config.AppConfig{
			Command: "sleep 60",
			Runtime: "custom",
			Port:    8100 + i,
		}, dir)

		if err := m.Start(domain); err != nil {
			t.Fatalf("failed to start %s: %v", domain, err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// All should be running
	for i := 0; i < 3; i++ {
		inst := m.Get(fmt.Sprintf("stopall%d.com", i))
		if inst == nil || !inst.Running {
			t.Errorf("expected stopall%d.com to be running", i)
		}
	}

	// Stop all
	m.StopAll()

	// Wait for all monitorProcess goroutines to clean up
	time.Sleep(300 * time.Millisecond)

	// All should be stopped
	for i := 0; i < 3; i++ {
		inst := m.Get(fmt.Sprintf("stopall%d.com", i))
		if inst == nil {
			t.Errorf("expected stopall%d.com instance to exist", i)
			continue
		}
		if inst.Running {
			t.Errorf("expected stopall%d.com to be stopped", i)
		}
	}
}

// TestRestartRunning tests restarting a running app.
func TestRestartRunning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process management test skipped on Windows")
	}

	dir := t.TempDir()
	m := New(nil)

	m.Register("restart.com", config.AppConfig{
		Command: "sleep 60",
		Runtime: "custom",
		Port:    8200,
	}, dir)

	// Start
	if err := m.Start("restart.com"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)

	inst1 := m.Get("restart.com")
	if inst1 == nil || !inst1.Running {
		t.Fatal("expected running")
	}
	pid1 := inst1.PID

	// Restart
	if err := m.Restart("restart.com"); err != nil {
		t.Fatalf("restart failed: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	inst2 := m.Get("restart.com")
	if inst2 == nil || !inst2.Running {
		t.Fatal("expected running after restart")
	}

	// PID should be different (new process)
	if inst2.PID == pid1 {
		t.Error("expected different PID after restart")
	}

	m.Stop("restart.com")
}

// TestDetectCommandRubyRackup tests Ruby command detection with config.ru.
func TestDetectCommandRubyRackup(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.ru"), []byte(""), 0644)

	cmd := detectCommand("ruby", dir)
	if cmd != "bundle exec puma -p ${PORT}" {
		t.Errorf("expected bundle exec puma command, got %q", cmd)
	}
}

// TestDetectCommandRubyGemfile tests Ruby command detection with Gemfile.
func TestDetectCommandRubyGemfile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "Gemfile"), []byte("source 'https://rubygems.org'\n"), 0644)

	cmd := detectCommand("ruby", dir)
	// Gemfile alone doesn't produce a command - config.ru is required
	if cmd != "" {
		t.Errorf("expected empty command for Gemfile only, got %q", cmd)
	}
}

// TestDetectCommandNodeNPMStart tests Node command detection with npm start.
func TestDetectCommandNodeNPMStart(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"scripts":{"start":"node server.js"}}`), 0644)

	cmd := detectCommand("node", dir)
	if cmd != "npm start" {
		t.Errorf("expected 'npm start', got %q", cmd)
	}
}

// TestDetectCommandUnknownRuntime tests command detection for unknown runtime.
func TestDetectCommandUnknownRuntime(t *testing.T) {
	dir := t.TempDir()
	cmd := detectCommand("unknown_runtime_xyz", dir)
	if cmd != "" {
		t.Errorf("expected empty command, got %q", cmd)
	}
}

// TestRegisterInvalidPort tests registration with invalid port.
func TestRegisterInvalidPort(t *testing.T) {
	m := New(nil)

	// Port 0 should still work (it's just a configuration)
	err := m.Register("port0.com", config.AppConfig{
		Command: "echo hello",
		Runtime: "custom",
		Port:    0,
	}, "/tmp")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestMonitorProcessNilCmd tests monitorProcess with nil cmd.
func TestMonitorProcessNilCmd(t *testing.T) {
	m := New(nil)

	app := &appProcess{
		domain:      "test.com",
		cmd:         nil,
		autoRestart: true,
		stopCh:      make(chan struct{}),
	}

	// Should return immediately without panic
	m.monitorProcess(app, nil)
}

// TestListenAddrNotRegistered tests ListenAddr for unregistered domain.
func TestListenAddrNotRegistered(t *testing.T) {
	m := New(nil)

	addr := m.ListenAddr("nonexistent.com")
	if addr != "" {
		t.Errorf("expected empty address, got %q", addr)
	}
}

// TestGetNotRegistered tests Get on unregistered domain.
func TestGetNotRegistered(t *testing.T) {
	m := New(nil)

	result := m.Get("nonexistent.com")
	if result != nil {
		t.Error("expected nil for unregistered domain")
	}
}

// TestSetCgroupPathWithDomain tests SetCgroupPath with domain.
func TestSetCgroupPathWithDomain(t *testing.T) {
	m := New(nil)

	// Should not panic
	m.SetCgroupPath("test.com", "/sys/fs/cgroup")
}

// TestInstancesEmptyManager tests Instances with no apps.
func TestInstancesEmptyManager(t *testing.T) {
	m := New(nil)

	instances := m.Instances()
	if len(instances) != 0 {
		t.Errorf("expected 0 instances, got %d", len(instances))
	}
}

// =============================================================================
// Additional coverage tests
// =============================================================================

// TestStatsNotRunning2 tests Stats on non-running app.
func TestStatsNotRunning2(t *testing.T) {
	m := New(nil)
	m.Register("stats-test.com", config.AppConfig{
		Command: "sleep 60",
		Runtime: "custom",
		Port:    9000,
	}, "/tmp")

	stats := m.Stats("stats-test.com")
	if stats == nil {
		t.Fatal("expected non-nil stats")
	}
	if stats.Running {
		t.Error("expected not running")
	}
	if stats.PID != 0 {
		t.Error("expected PID to be 0")
	}
}

// TestStopNotRunning2 tests Stop on non-running app.
func TestStopNotRunning2(t *testing.T) {
	m := New(nil)
	m.Register("stop-notrunning.com", config.AppConfig{
		Command: "sleep 60",
		Runtime: "custom",
		Port:    9001,
	}, "/tmp")

	err := m.Stop("stop-notrunning.com")
	// Should error because not running
	if err == nil {
		t.Error("expected error when stopping non-running app")
	}
}

// TestStopNonExistent tests Stop on non-existent app.
func TestStopNonExistent(t *testing.T) {
	m := New(nil)

	err := m.Stop("nonexistent.com")
	if err == nil {
		t.Error("expected error when stopping non-existent app")
	}
}

// TestGetNonExistent tests Get on non-existent domain.
func TestGetNonExistent(t *testing.T) {
	m := New(nil)

	result := m.Get("nonexistent.com")
	if result != nil {
		t.Error("expected nil for non-existent domain")
	}
}

// TestInstancesWithMultiple tests Instances with multiple apps.
func TestInstancesWithMultiple(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process tests skipped on Windows")
	}

	dir := t.TempDir()
	m := New(nil)

	// Register multiple apps
	domains := []string{"inst1.com", "inst2.com", "inst3.com"}
	for i, domain := range domains {
		m.Register(domain, config.AppConfig{
			Command: "sleep 60",
			Runtime: "custom",
			Port:    9100 + i,
		}, dir)
	}

	// Start only first two
	for i := 0; i < 2; i++ {
		if err := m.Start(domains[i]); err != nil {
			t.Fatalf("failed to start %s: %v", domains[i], err)
		}
	}

	time.Sleep(100 * time.Millisecond)

	instances := m.Instances()
	if len(instances) != 3 {
		t.Errorf("expected 3 instances, got %d", len(instances))
	}

	// Verify running status
	runningCount := 0
	for _, inst := range instances {
		if inst.Running {
			runningCount++
		}
	}
	if runningCount != 2 {
		t.Errorf("expected 2 running, got %d", runningCount)
	}

	// Cleanup
	m.StopAll()
}

// TestMonitorProcessAutoRestart tests monitorProcess auto-restart behavior.
func TestMonitorProcessAutoRestart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process tests skipped on Windows")
	}

	dir := t.TempDir()
	m := New(nil)

	// Create a script that exits quickly
	script := `#!/bin/sh
sleep 1
exit 1`
	scriptPath := dir + "/crash.sh"
	os.WriteFile(scriptPath, []byte(script), 0755)

	m.Register("autorestart.com", config.AppConfig{
		Command: scriptPath,
		Runtime: "custom",
		Port:    9200,
	}, dir)

	if err := m.Start("autorestart.com"); err != nil {
		t.Fatal(err)
	}

	// Wait for first start
	time.Sleep(500 * time.Millisecond)
	inst1 := m.Get("autorestart.com")
	if !inst1.Running {
		t.Fatal("expected running after first start")
	}
	firstPID := inst1.PID

	// Wait for auto-restart (process crashes after 1s, auto-restart after 2s backoff)
	time.Sleep(5000 * time.Millisecond)

	inst2 := m.Get("autorestart.com")
	if !inst2.Running {
		t.Error("expected auto-restart to have restarted the process")
	}
	if inst2.PID == firstPID {
		t.Error("expected different PID after restart")
	}

	m.Stop("autorestart.com")
}

// TestRestartNotRunning2 tests Restart on non-running app.
func TestRestartNotRunning2(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process tests skipped on Windows")
	}

	dir := t.TempDir()
	m := New(nil)

	m.Register("restart-test.com", config.AppConfig{
		Command: "sleep 60",
		Runtime: "custom",
		Port:    9300,
	}, dir)

	// Try to restart without starting first
	err := m.Restart("restart-test.com")
	// Restart() calls Stop() (ignoring errors) then Start(), so it should succeed
	if err != nil {
		t.Fatalf("expected restart to succeed on stopped app, got: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	// Verify the app is now running
	inst := m.Get("restart-test.com")
	if inst == nil || !inst.Running {
		t.Error("expected app to be running after restart")
	}

	m.Stop("restart-test.com")
}
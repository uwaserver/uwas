package apps

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/logger"
)

// ---------------------------------------------------------------------------
// Helper-process pattern for mocking execCommandFn (docker / shell commands).
//
// TestHelperProcess is re-invoked as a subprocess by mockExecCommand. It is
// not a real test; it only does work when GO_APPS_HELPER=1 is set. The
// behaviour is driven entirely by env vars so the package never spawns a real
// docker daemon, never blocks indefinitely, and never leaks a process beyond
// the lifetime of the test that started it.
// ---------------------------------------------------------------------------

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_APPS_HELPER") != "1" {
		return
	}
	if out := os.Getenv("GO_APPS_HELPER_STDOUT"); out != "" {
		fmt.Fprint(os.Stdout, out)
	}
	if ms := os.Getenv("GO_APPS_HELPER_SLEEP_MS"); ms != "" {
		var d int
		fmt.Sscanf(ms, "%d", &d)
		time.Sleep(time.Duration(d) * time.Millisecond)
	}
	code := 0
	if os.Getenv("GO_APPS_HELPER_EXIT") == "1" {
		code = 1
	}
	os.Exit(code)
}

// mockExecCommand returns a function suitable for assigning to execCommandFn.
// Each produced *exec.Cmd re-invokes TestHelperProcess with the supplied env
// knobs. The original command name + args are recorded into calls (guarded by
// mu) so tests can assert on what was invoked.
func mockExecCommand(mu *sync.Mutex, calls *[][]string, env ...string) func(string, ...string) *exec.Cmd {
	return func(name string, arg ...string) *exec.Cmd {
		mu.Lock()
		*calls = append(*calls, append([]string{name}, arg...))
		mu.Unlock()
		cs := []string{"-test.run=TestHelperProcess", "--"}
		cmd := exec.Command(os.Args[0], cs...)
		cmd.Env = append(os.Environ(), "GO_APPS_HELPER=1")
		cmd.Env = append(cmd.Env, env...)
		return cmd
	}
}

// restoreHooks snapshots the package-level exec/fs hooks and registers a
// cleanup that puts them back, so a test that overrides them can't bleed into
// the next.
func restoreHooks(t *testing.T) {
	t.Helper()
	oExec, oMkdir, oOpen, oStat, oPortFree := execCommandFn, osMkdirAllFn, osOpenFileFn, osStatFn, isPortFreeFn
	t.Cleanup(func() {
		execCommandFn = oExec
		osMkdirAllFn = oMkdir
		osOpenFileFn = oOpen
		osStatFn = oStat
		isPortFreeFn = oPortFree
	})
}

func quietLog() *logger.Logger { return logger.New("error", "text") }

// ---------------------------------------------------------------------------
// app.go
// ---------------------------------------------------------------------------

func TestExposedPorts(t *testing.T) {
	if got := (*App)(nil).ExposedPorts(); got != nil {
		t.Fatalf("nil app ExposedPorts = %v, want nil", got)
	}
	a := &App{Port: 3000, Ports: []int{3000, 5173, 0, 5173, 8080}}
	got := a.ExposedPorts()
	want := []int{3000, 5173, 8080}
	if len(got) != len(want) {
		t.Fatalf("ExposedPorts = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ExposedPorts = %v, want %v", got, want)
		}
	}
	// Zero primary port: should be skipped, only extras returned.
	a2 := &App{Port: 0, Ports: []int{4000}}
	if got := a2.ExposedPorts(); len(got) != 1 || got[0] != 4000 {
		t.Fatalf("ExposedPorts(no primary) = %v, want [4000]", got)
	}
}

func TestValidateRuntimeAndPortBranches(t *testing.T) {
	// Missing runtime.
	if err := (&App{Name: "x"}).Validate(); err == nil || !strings.Contains(err.Error(), "runtime is required") {
		t.Fatalf("empty runtime err = %v", err)
	}
	// Unknown runtime.
	if err := (&App{Name: "x", Runtime: "rust"}).Validate(); err == nil || !strings.Contains(err.Error(), "unknown runtime") {
		t.Fatalf("unknown runtime err = %v", err)
	}
	// Primary port out of range.
	if err := (&App{Name: "x", Runtime: RuntimeNode, Port: 70000}).Validate(); err == nil || !strings.Contains(err.Error(), "out of range") {
		t.Fatalf("port range err = %v", err)
	}
	// Negative port.
	if err := (&App{Name: "x", Runtime: RuntimeNode, Port: -1}).Validate(); err == nil {
		t.Fatalf("negative port should fail")
	}
	// Exposed (extra) port out of range.
	if err := (&App{Name: "x", Runtime: RuntimeNode, Ports: []int{70000}}).Validate(); err == nil || !strings.Contains(err.Error(), "exposed port") {
		t.Fatalf("exposed port range err = %v", err)
	}
	// Docker build-context-only (no image) passes image-or-build check.
	if err := (&App{Name: "x", Runtime: RuntimeDocker, Docker: DockerSpec{ContainerPort: 80, Build: DockerBuild{Context: "."}}}).Validate(); err != nil {
		t.Fatalf("docker build-context app should validate: %v", err)
	}
}

// ---------------------------------------------------------------------------
// manager.go — simple state accessors
// ---------------------------------------------------------------------------

func TestNewManagerNilStore(t *testing.T) {
	m := NewManager(nil, nil)
	if m.store == nil {
		t.Fatal("NewManager(nil) should construct a default store")
	}
	if m.store.Dir != DefaultDir {
		t.Fatalf("default store dir = %q, want %q", m.store.Dir, DefaultDir)
	}
}

func TestStateTransitions(t *testing.T) {
	m := NewManager(NewStore(t.TempDir()), nil)
	if s := m.State("ghost"); s != StateNotRegistered {
		t.Fatalf("unknown app state = %v, want NotRegistered", s)
	}
	if err := m.Register(&App{Name: "svc", Runtime: RuntimeCustom, Command: "true"}); err != nil {
		t.Fatal(err)
	}
	if s := m.State("svc"); s != StateStopped {
		t.Fatalf("registered-stopped state = %v, want Stopped", s)
	}
	// Mark running via docker hook (no real process).
	m.mu.Lock()
	m.procs["svc"].runtimeKind = RuntimeDocker
	m.procs["svc"].dockerID = "fake"
	m.mu.Unlock()
	if s := m.State("svc"); s != StateRunning {
		t.Fatalf("running state = %v, want Running", s)
	}
}

func TestInstancesAndGet(t *testing.T) {
	m := NewManager(NewStore(t.TempDir()), nil)
	if got := m.Get("none"); got != nil {
		t.Fatalf("Get unknown = %v, want nil", got)
	}
	if err := m.Register(&App{Name: "a1", Runtime: RuntimeCustom, Command: "true"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Register(&App{Name: "a2", Runtime: RuntimeCustom, Command: "true"}); err != nil {
		t.Fatal(err)
	}
	insts := m.Instances()
	if len(insts) != 2 {
		t.Fatalf("Instances len = %d, want 2", len(insts))
	}
	for _, in := range insts {
		if in.Running {
			t.Fatalf("instance %q should not be running", in.Name)
		}
	}
}

func TestInstanceFromProcessDockerRunning(t *testing.T) {
	m := NewManager(NewStore(t.TempDir()), nil)
	app := &App{Name: "d", Runtime: RuntimeDocker, Docker: DockerSpec{Image: "nginx", ContainerPort: 80}}
	m.mu.Lock()
	m.procs["d"] = &process{
		name:        "d",
		app:         app,
		runtimeKind: RuntimeDocker,
		port:        9000,
		dockerID:    "abc123",
		startedAt:   time.Now().Add(-3 * time.Second),
		stopCh:      make(chan struct{}),
	}
	m.mu.Unlock()
	inst := m.Get("d")
	if inst == nil || !inst.Running {
		t.Fatalf("docker instance should be running: %#v", inst)
	}
	if inst.DockerImage != "nginx" {
		t.Fatalf("DockerImage = %q, want nginx", inst.DockerImage)
	}
	if inst.Uptime == "" || inst.StartedAt == nil {
		t.Fatalf("running docker inst should report uptime/started: %#v", inst)
	}
}

// ---------------------------------------------------------------------------
// Unregister / Restart / StopAll
// ---------------------------------------------------------------------------

func TestUnregisterRemovesAppAndFile(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(NewStore(dir), nil)
	if err := m.Register(&App{Name: "gone", Runtime: RuntimeCustom, Command: "true"}); err != nil {
		t.Fatal(err)
	}
	if !m.Store().Exists("gone") {
		t.Fatal("file should exist after register")
	}
	if err := m.Unregister("gone"); err != nil {
		t.Fatalf("unregister: %v", err)
	}
	if m.Get("gone") != nil {
		t.Fatal("app should be gone from memory")
	}
	if m.Store().Exists("gone") {
		t.Fatal("file should be deleted")
	}
	// Idempotent: unregistering again is fine.
	if err := m.Unregister("gone"); err != nil {
		t.Fatalf("second unregister: %v", err)
	}
}

func TestRestartUnregistered(t *testing.T) {
	m := NewManager(NewStore(t.TempDir()), nil)
	// Stop on a missing app is an error swallowed by Restart; Start then
	// returns the not-registered error.
	err := m.Restart("nope")
	if err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("Restart unknown = %v, want not-registered", err)
	}
}

func TestRestartCustomApp(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh")
	}
	dir := t.TempDir()
	m := NewManager(NewStore(dir), nil)
	if err := m.Register(&App{Name: "r", Runtime: RuntimeCustom, Command: "sleep 2", WorkDir: dir}); err != nil {
		t.Fatal(err)
	}
	if err := m.Start("r"); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := m.Restart("r"); err != nil {
		t.Fatalf("restart: %v", err)
	}
	defer m.Stop("r")
	if inst := m.Get("r"); inst == nil || !inst.Running {
		t.Fatalf("app should be running after restart: %#v", inst)
	}
}

// ---------------------------------------------------------------------------
// Start error branches
// ---------------------------------------------------------------------------

func TestStartUnregistered(t *testing.T) {
	m := NewManager(NewStore(t.TempDir()), nil)
	if err := m.Start("nope"); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("Start unknown = %v", err)
	}
}

func TestStartDisabledRejected(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(NewStore(dir), nil)
	if err := m.Register(&App{Name: "off", Runtime: RuntimeCustom, Command: "true", Disabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := m.Start("off"); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("Start disabled = %v, want disabled error", err)
	}
}

func TestStartAlreadyRunning(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(NewStore(dir), nil)
	if err := m.Register(&App{Name: "dup", Runtime: RuntimeDocker, Docker: DockerSpec{Image: "x", ContainerPort: 80}}); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.procs["dup"].dockerID = "running"
	m.mu.Unlock()
	if err := m.Start("dup"); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("Start running = %v, want already-running", err)
	}
}

func TestStopUnregistered(t *testing.T) {
	m := NewManager(NewStore(t.TempDir()), nil)
	if err := m.Stop("nope"); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("Stop unknown = %v", err)
	}
}

func TestStopAlreadyDownNative(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(NewStore(dir), nil)
	if err := m.Register(&App{Name: "idle", Runtime: RuntimeCustom, Command: "true", WorkDir: dir}); err != nil {
		t.Fatal(err)
	}
	// Stop when never started: stopLocked sees cmd==nil → returns nil.
	if err := m.Stop("idle"); err != nil {
		t.Fatalf("Stop idle = %v, want nil", err)
	}
}

// ---------------------------------------------------------------------------
// registerLocked port-conflict + allocateFreePortLocked
// ---------------------------------------------------------------------------

func TestRegisterPortConflictAutoAssigns(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(NewStore(dir), quietLog())
	if err := m.Register(&App{Name: "first", Runtime: RuntimeCustom, Command: "true", Port: 3500}); err != nil {
		t.Fatal(err)
	}
	// Second app requests the same port → should auto-assign a different one.
	if err := m.Register(&App{Name: "second", Runtime: RuntimeCustom, Command: "true", Port: 3500}); err != nil {
		t.Fatal(err)
	}
	a := m.Get("first")
	b := m.Get("second")
	if a.Port != 3500 {
		t.Fatalf("first port = %d, want 3500", a.Port)
	}
	if b.Port == 3500 || b.Port == 0 {
		t.Fatalf("second port = %d, want auto-assigned distinct", b.Port)
	}
}

func TestRegisterAutoAssignsWhenPortZero(t *testing.T) {
	restoreHooks(t)
	// Force allocateFreePortLocked to accept the first candidate quickly.
	isPortFreeFn = func(int) bool { return true }
	dir := t.TempDir()
	m := NewManager(NewStore(dir), nil)
	if err := m.Register(&App{Name: "auto", Runtime: RuntimeCustom, Command: "true"}); err != nil {
		t.Fatal(err)
	}
	if got := m.Get("auto").Port; got < 3001 {
		t.Fatalf("auto port = %d, want >= 3001", got)
	}
}

func TestAllocateFreePortSkipsTakenAndBound(t *testing.T) {
	restoreHooks(t)
	dir := t.TempDir()
	m := NewManager(NewStore(dir), nil)
	m.nextPort = 4000
	// 4000 is "taken" by an existing managed proc; 4001 is "bound";
	// 4002 is free.
	m.mu.Lock()
	m.procs["x"] = &process{name: "x", port: 4000, stopCh: make(chan struct{})}
	m.mu.Unlock()
	isPortFreeFn = func(p int) bool { return p >= 4002 }
	m.mu.Lock()
	got := m.allocateFreePortLocked()
	m.mu.Unlock()
	if got != 4002 {
		t.Fatalf("allocateFreePort = %d, want 4002", got)
	}
}

func TestAllocateFreePortExhaustsAttempts(t *testing.T) {
	restoreHooks(t)
	dir := t.TempDir()
	m := NewManager(NewStore(dir), nil)
	m.nextPort = 5000
	// Never free → loop exhausts maxAttempts and returns the final port.
	isPortFreeFn = func(int) bool { return false }
	m.mu.Lock()
	got := m.allocateFreePortLocked()
	m.mu.Unlock()
	if got != 6000 { // 5000 + 1000 attempts
		t.Fatalf("exhausted allocate = %d, want 6000", got)
	}
}

// ---------------------------------------------------------------------------
// detectCommand — every runtime branch
// ---------------------------------------------------------------------------

func TestDetectCommandAllRuntimes(t *testing.T) {
	t.Run("node-entrypoints", func(t *testing.T) {
		for _, f := range []string{"server.js", "index.js", "app.js"} {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, f), []byte("//"), 0644); err != nil {
				t.Fatal(err)
			}
			if got := detectCommand("node", dir); got != "node "+f {
				t.Fatalf("node %s detect = %q", f, got)
			}
		}
	})
	t.Run("python", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "manage.py"), []byte("#"), 0644); err != nil {
			t.Fatal(err)
		}
		if got := detectCommand("python", dir); !strings.Contains(got, "manage.py runserver") {
			t.Fatalf("django detect = %q", got)
		}
		for _, f := range []string{"app.py", "main.py", "wsgi.py"} {
			d := t.TempDir()
			os.WriteFile(filepath.Join(d, f), []byte("#"), 0644)
			if got := detectCommand("python", d); got != "python "+f {
				t.Fatalf("python %s detect = %q", f, got)
			}
		}
		d := t.TempDir()
		os.WriteFile(filepath.Join(d, "requirements.txt"), []byte("flask"), 0644)
		if got := detectCommand("python", d); !strings.Contains(got, "gunicorn") {
			t.Fatalf("gunicorn detect = %q", got)
		}
	})
	t.Run("ruby", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "config.ru"), []byte("#"), 0644)
		if got := detectCommand("ruby", dir); !strings.Contains(got, "puma") {
			t.Fatalf("ruby detect = %q", got)
		}
		if got := detectCommand("ruby", t.TempDir()); got != "" {
			t.Fatalf("ruby empty = %q, want empty", got)
		}
	})
	t.Run("go", func(t *testing.T) {
		if got := detectCommand("go", t.TempDir()); got != "./main" {
			t.Fatalf("go detect = %q", got)
		}
	})
	t.Run("unknown", func(t *testing.T) {
		if got := detectCommand("custom", t.TempDir()); got != "" {
			t.Fatalf("custom detect = %q, want empty", got)
		}
	})
	t.Run("node-empty", func(t *testing.T) {
		if got := detectCommand("node", t.TempDir()); got != "" {
			t.Fatalf("node empty = %q, want empty", got)
		}
	})
}

func TestDetectNodePackageCommandErrors(t *testing.T) {
	// Missing package.json → empty.
	if got := detectNodePackageCommand(t.TempDir()); got != "" {
		t.Fatalf("missing pkg = %q", got)
	}
	// Invalid JSON → empty.
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "package.json"), []byte("{not json"), 0644)
	if got := detectNodePackageCommand(dir); got != "" {
		t.Fatalf("bad json = %q", got)
	}
}

// ---------------------------------------------------------------------------
// validateShellCommand
// ---------------------------------------------------------------------------

func TestValidateShellCommand(t *testing.T) {
	if err := validateShellCommand("node index.js --port 3000"); err != nil {
		t.Fatalf("clean command rejected: %v", err)
	}
	for _, bad := range []string{
		"echo hi\nrm -rf /",
		"echo\x00",
		"cat $(whoami)",
		"a | b",
		"a > b",
		"a < b",
		"a; b",
		"a && b",
		"a || b",
		"echo `id`",
	} {
		if err := validateShellCommand(bad); err == nil {
			t.Errorf("command %q should be rejected", bad)
		}
	}
}

// ---------------------------------------------------------------------------
// readRuntimePortFile / runtimePortFile / adoptDiscoveredPort
// ---------------------------------------------------------------------------

func TestRuntimePortFileEdgeCases(t *testing.T) {
	if got := runtimePortFile("", "x"); got != "" {
		t.Fatalf("empty workDir = %q", got)
	}
	if got := runtimePortFile("/w", ""); got != "" {
		t.Fatalf("empty name = %q", got)
	}
	if got := runtimePortFile("/var/lib/uwas/apps/x", "x"); got == "" {
		t.Fatal("valid inputs should yield a path")
	}
}

func TestReadRuntimePortFile(t *testing.T) {
	if _, ok := readRuntimePortFile(""); ok {
		t.Fatal("empty path should be not-ok")
	}
	if _, ok := readRuntimePortFile("/nonexistent/zzz.port"); ok {
		t.Fatal("missing file should be not-ok")
	}
	dir := t.TempDir()
	// Empty content.
	empty := filepath.Join(dir, "empty.port")
	os.WriteFile(empty, []byte("   \n"), 0644)
	if _, ok := readRuntimePortFile(empty); ok {
		t.Fatal("empty content should be not-ok")
	}
	// Non-numeric.
	bad := filepath.Join(dir, "bad.port")
	os.WriteFile(bad, []byte("abc"), 0644)
	if _, ok := readRuntimePortFile(bad); ok {
		t.Fatal("non-numeric should be not-ok")
	}
	// Out of range.
	oor := filepath.Join(dir, "oor.port")
	os.WriteFile(oor, []byte("99999"), 0644)
	if _, ok := readRuntimePortFile(oor); ok {
		t.Fatal("out-of-range should be not-ok")
	}
	// Valid.
	good := filepath.Join(dir, "good.port")
	os.WriteFile(good, []byte("8080\n"), 0644)
	if got, ok := readRuntimePortFile(good); !ok || got != 8080 {
		t.Fatalf("valid read = %d,%v", got, ok)
	}
}

func TestAdoptDiscoveredPortBranches(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(NewStore(dir), quietLog())

	// nil process.
	if err := m.adoptDiscoveredPort(nil, 9000); err == nil {
		t.Fatal("nil process should error")
	}

	app := &App{Name: "ad", Runtime: RuntimeNode, Port: 3001, WorkDir: dir}
	if err := m.Store().Save(app); err != nil {
		t.Fatal(err)
	}
	p := &process{name: "ad", app: app, runtimeKind: RuntimeNode, port: 3001, workDir: dir, stopCh: make(chan struct{})}
	m.mu.Lock()
	m.procs["ad"] = p
	m.mu.Unlock()

	// Same port → no-op.
	if err := m.adoptDiscoveredPort(p, 3001); err != nil {
		t.Fatalf("same port adopt: %v", err)
	}

	// Conflict with another app that exposes the target port.
	other := &process{name: "other", app: &App{Name: "other", Port: 7777}, port: 7777, stopCh: make(chan struct{})}
	m.mu.Lock()
	m.procs["other"] = other
	m.mu.Unlock()
	if err := m.adoptDiscoveredPort(p, 7777); err == nil || !strings.Contains(err.Error(), "already owned") {
		t.Fatalf("conflict adopt = %v, want already-owned", err)
	}

	// Successful adoption persists the new port.
	if err := m.adoptDiscoveredPort(p, 4321); err != nil {
		t.Fatalf("adopt: %v", err)
	}
	saved, _ := m.Store().Get("ad")
	if saved == nil || saved.Port != 4321 {
		t.Fatalf("adopted port not persisted: %#v", saved)
	}
}

func TestAdoptDiscoveredPortSaveFailureRollsBack(t *testing.T) {
	restoreHooks(t)
	dir := t.TempDir()
	m := NewManager(NewStore(dir), nil)
	app := &App{Name: "rb", Runtime: RuntimeNode, Port: 3001, WorkDir: dir}
	if err := m.Store().Save(app); err != nil {
		t.Fatal(err)
	}
	p := &process{name: "rb", app: app, runtimeKind: RuntimeNode, port: 3001, workDir: dir, stopCh: make(chan struct{})}
	m.mu.Lock()
	m.procs["rb"] = p
	m.mu.Unlock()

	// Make the store dir unwritable by pointing it at a path whose parent
	// is a file — Save's EnsureDir/WriteFile fails.
	badFile := filepath.Join(dir, "afile")
	os.WriteFile(badFile, []byte("x"), 0644)
	m.Store().Dir = filepath.Join(badFile, "sub")

	if err := m.adoptDiscoveredPort(p, 9999); err == nil {
		t.Fatal("adopt should fail when save fails")
	}
	if p.port != 3001 {
		t.Fatalf("port should roll back to 3001, got %d", p.port)
	}
}

// ---------------------------------------------------------------------------
// ensureStartPortAvailable — already-free fast path & save-failure rollback
// ---------------------------------------------------------------------------

func TestEnsureStartPortAvailableNoPort(t *testing.T) {
	m := NewManager(NewStore(t.TempDir()), nil)
	p := &process{name: "np", port: 0, stopCh: make(chan struct{})}
	if err := m.ensureStartPortAvailable(p); err != nil {
		t.Fatalf("port 0 should be a no-op: %v", err)
	}
}

func TestEnsureStartPortAvailableAlreadyFree(t *testing.T) {
	restoreHooks(t)
	isPortFreeFn = func(int) bool { return true }
	m := NewManager(NewStore(t.TempDir()), nil)
	p := &process{name: "ok", port: 3001, stopCh: make(chan struct{})}
	if err := m.ensureStartPortAvailable(p); err != nil {
		t.Fatalf("free port should pass fast: %v", err)
	}
	if p.port != 3001 {
		t.Fatalf("free port should not change, got %d", p.port)
	}
}

func TestEnsureStartPortAvailableSaveFailureRollsBack(t *testing.T) {
	restoreHooks(t)
	dir := t.TempDir()
	m := NewManager(NewStore(dir), nil)
	app := &App{Name: "es", Runtime: RuntimeNode, Port: 3001, WorkDir: dir}
	if err := m.Store().Save(app); err != nil {
		t.Fatal(err)
	}
	p := &process{name: "es", app: app, runtimeKind: RuntimeNode, port: 3001, workDir: dir, stopCh: make(chan struct{})}
	m.mu.Lock()
	m.procs["es"] = p
	m.mu.Unlock()

	// Port always "occupied" so it tries to reassign + save; break the store.
	isPortFreeFn = func(int) bool { return false }
	badFile := filepath.Join(dir, "afile")
	os.WriteFile(badFile, []byte("x"), 0644)
	m.Store().Dir = filepath.Join(badFile, "sub")

	if err := m.ensureStartPortAvailable(p); err == nil {
		t.Fatal("should fail when replacement save fails")
	}
	if p.port != 3001 {
		t.Fatalf("port should roll back, got %d", p.port)
	}
}

// ---------------------------------------------------------------------------
// WaitListening misc branches
// ---------------------------------------------------------------------------

func TestWaitListeningUnregistered(t *testing.T) {
	m := NewManager(NewStore(t.TempDir()), nil)
	if err := m.WaitListening("nope", time.Second); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("WaitListening unknown = %v", err)
	}
}

func TestWaitListeningNoPort(t *testing.T) {
	m := NewManager(NewStore(t.TempDir()), nil)
	m.mu.Lock()
	m.procs["np"] = &process{name: "np", runtimeKind: RuntimeNode, port: 0, dockerID: "x", stopCh: make(chan struct{})}
	m.procs["np"].runtimeKind = RuntimeDocker // running via dockerID, but port 0
	m.mu.Unlock()
	if err := m.WaitListening("np", time.Second); err == nil || !strings.Contains(err.Error(), "no port assigned") {
		t.Fatalf("WaitListening no-port = %v", err)
	}
}

// ---------------------------------------------------------------------------
// tailLogFile happy path
// ---------------------------------------------------------------------------

func TestTailLogFileTruncates(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "log")
	os.WriteFile(p, []byte("0123456789abcdef"), 0644)
	if got := tailLogFile(p, 4); got != "cdef" {
		t.Fatalf("tail = %q, want cdef", got)
	}
	if got := tailLogFile(p, 0); got != "" {
		t.Fatalf("zero lastN = %q, want empty", got)
	}
}

// ---------------------------------------------------------------------------
// Stats — docker path (mocked) and native running path
// ---------------------------------------------------------------------------

func TestStatsDockerRunning(t *testing.T) {
	// readDockerStats uses the real `docker` binary (not execCommandFn);
	// with a bogus container ID it returns zeroes. We assert the path is
	// exercised and Running/Uptime get set.
	m := NewManager(NewStore(t.TempDir()), nil)
	m.mu.Lock()
	m.procs["d"] = &process{
		name:        "d",
		runtimeKind: RuntimeDocker,
		dockerID:    "nonexistent-container-xyz",
		port:        9000,
		startedAt:   time.Now().Add(-2 * time.Second),
		stopCh:      make(chan struct{}),
	}
	m.mu.Unlock()
	s := m.Stats("d")
	if s == nil || !s.Running {
		t.Fatalf("docker stats should be running: %#v", s)
	}
	if s.Uptime == "" {
		t.Fatalf("docker stats should report uptime")
	}
}

// ---------------------------------------------------------------------------
// Store — Names, EnsureDir errors, Get/Save error branches
// ---------------------------------------------------------------------------

func TestStoreNames(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if len(s.Names()) != 0 {
		t.Fatal("fresh store should have no names")
	}
	for _, n := range []string{"zeta", "alpha", "mid"} {
		if err := s.Save(&App{Name: n, Runtime: RuntimeCustom, Command: "true"}); err != nil {
			t.Fatal(err)
		}
	}
	names := s.Names()
	want := []string{"alpha", "mid", "zeta"}
	if len(names) != 3 {
		t.Fatalf("names = %v", names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("names = %v, want %v (sorted)", names, want)
		}
	}
}

func TestStoreSaveNil(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.Save(nil); err == nil {
		t.Fatal("save nil should error")
	}
}

func TestStoreSaveInvalidApp(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.Save(&App{Name: "bad name", Runtime: RuntimeNode}); err == nil {
		t.Fatal("save invalid name should error")
	}
}

func TestStoreGetInvalidName(t *testing.T) {
	s := NewStore(t.TempDir())
	if _, err := s.Get("bad/name"); err == nil {
		t.Fatal("get invalid name should error")
	}
}

func TestStoreGetMissing(t *testing.T) {
	s := NewStore(t.TempDir())
	app, err := s.Get("absent")
	if err != nil || app != nil {
		t.Fatalf("missing get = %v,%v, want nil,nil", app, err)
	}
}

func TestStoreGetNameMismatch(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	os.WriteFile(filepath.Join(dir, "mm.yaml"), []byte("name: other\nruntime: node\ncommand: node x.js\n"), 0600)
	if _, err := s.Get("mm"); err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("name mismatch = %v", err)
	}
}

func TestStoreGetFillsBlankName(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	os.WriteFile(filepath.Join(dir, "blank.yaml"), []byte("runtime: custom\ncommand: ./run\n"), 0600)
	app, err := s.Get("blank")
	if err != nil {
		t.Fatalf("get blank-name: %v", err)
	}
	if app == nil || app.Name != "blank" {
		t.Fatalf("blank name should default to filename, got %#v", app)
	}
}

func TestStoreGetInvalidSchema(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	// Valid name match, but missing runtime → Validate fails.
	os.WriteFile(filepath.Join(dir, "noschema.yaml"), []byte("name: noschema\n"), 0600)
	if _, err := s.Get("noschema"); err == nil {
		t.Fatal("invalid schema should error")
	}
}

func TestStoreExistsInvalidName(t *testing.T) {
	s := NewStore(t.TempDir())
	if s.Exists("bad/name") {
		t.Fatal("invalid name Exists should be false")
	}
}

func TestStoreDeleteInvalidName(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.Delete("bad/name"); err == nil {
		t.Fatal("delete invalid name should error")
	}
}

func TestStoreLoadBlankNameAdopted(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	os.WriteFile(filepath.Join(dir, "anon.yaml"), []byte("runtime: custom\ncommand: ./run\n"), 0600)
	// Also drop a .yml file and a directory to exercise the skip branches.
	os.WriteFile(filepath.Join(dir, "ymlapp.yml"), []byte("name: ymlapp\nruntime: custom\ncommand: ./r\n"), 0600)
	os.Mkdir(filepath.Join(dir, "subdir"), 0755)
	os.WriteFile(filepath.Join(dir, "notyaml.txt"), []byte("ignored"), 0600)
	apps, skipped, err := s.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(skipped) != 0 {
		t.Fatalf("unexpected skips: %v", skipped)
	}
	found := map[string]bool{}
	for _, a := range apps {
		found[a.Name] = true
	}
	if !found["anon"] || !found["ymlapp"] {
		t.Fatalf("expected anon + ymlapp loaded, got %v", found)
	}
}

func TestStoreLoadReadDirError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod-based unreadable dir does not block root")
	}
	dir := t.TempDir()
	locked := filepath.Join(dir, "appsd")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	s := NewStore(locked)
	if _, _, err := s.Load(); err == nil {
		t.Fatal("Load should fail when the apps dir is unreadable")
	}
}

func TestStoreEnsureDirError(t *testing.T) {
	dir := t.TempDir()
	// Make a file where the dir should be → MkdirAll fails.
	f := filepath.Join(dir, "afile")
	os.WriteFile(f, []byte("x"), 0644)
	s := NewStore(filepath.Join(f, "sub"))
	if err := s.EnsureDir(); err == nil {
		t.Fatal("EnsureDir over a file should error")
	}
	if _, _, err := s.Load(); err == nil {
		t.Fatal("Load should propagate EnsureDir error")
	}
}

func TestStoreSaveRenameFailure(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	app := &App{Name: "rf", Runtime: RuntimeCustom, Command: "true"}
	if err := s.Save(app); err != nil {
		t.Fatal(err)
	}
	// Replace the target path with a directory so os.Rename fails.
	target := filepath.Join(dir, "rf.yaml")
	os.Remove(target)
	os.Mkdir(target, 0755)
	if err := s.Save(app); err == nil {
		t.Fatal("save should fail when target is a directory")
	}
}

// ---------------------------------------------------------------------------
// LoadAll — refresh existing snapshot path
// ---------------------------------------------------------------------------

func TestLoadAllRefreshesExistingSnapshot(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	m := NewManager(s, nil)
	if err := s.Save(&App{Name: "ld", Runtime: RuntimeCustom, Command: "v1", WorkDir: dir}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.LoadAll(); err != nil {
		t.Fatal(err)
	}
	// Edit on disk, reload: existing proc should refresh its snapshot.
	if err := s.Save(&App{Name: "ld", Runtime: RuntimeCustom, Command: "v2", WorkDir: dir}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.LoadAll(); err != nil {
		t.Fatal(err)
	}
	m.mu.RLock()
	got := m.procs["ld"].app.Command
	m.mu.RUnlock()
	if got != "v2" {
		t.Fatalf("snapshot not refreshed: command = %q, want v2", got)
	}
}

func TestLoadAllPropagatesStoreError(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "afile")
	os.WriteFile(f, []byte("x"), 0644)
	s := NewStore(filepath.Join(f, "sub"))
	m := NewManager(s, nil)
	if _, _, err := m.LoadAll(); err == nil {
		t.Fatal("LoadAll should propagate store error")
	}
}

// ---------------------------------------------------------------------------
// docker.go — mocked exec paths
// ---------------------------------------------------------------------------

func TestStartDockerNoApp(t *testing.T) {
	m := NewManager(NewStore(t.TempDir()), nil)
	p := &process{name: "x", runtimeKind: RuntimeDocker, stopCh: make(chan struct{})}
	if err := m.startDocker(p); err == nil || !strings.Contains(err.Error(), "no app definition") {
		t.Fatalf("startDocker no app = %v", err)
	}
}

func TestStartDockerMissingContainerPort(t *testing.T) {
	restoreHooks(t)
	var mu sync.Mutex
	var calls [][]string
	execCommandFn = mockExecCommand(&mu, &calls)
	m := NewManager(NewStore(t.TempDir()), nil)
	// Hand-edited file slips past Validate: container_port 0.
	app := &App{Name: "noport", Runtime: RuntimeDocker, Docker: DockerSpec{Image: "nginx"}}
	p := &process{name: "noport", app: app, runtimeKind: RuntimeDocker, port: 9000, stopCh: make(chan struct{})}
	if err := m.startDocker(p); err == nil || !strings.Contains(err.Error(), "container_port is required") {
		t.Fatalf("startDocker no container_port = %v", err)
	}
}

// NOTE: startDocker's full success/immediate-exit path is exercised by the
// real-daemon lifecycle test (docker_real_test.go). It is deliberately NOT
// driven here via mocked exec because startDocker spawns a watchDocker
// goroutine that, under the immediate-exit branch, races startDocker's own
// `p.dockerID = ""` write (a pre-existing prod pattern). Forcing that path
// with a fake container id trips the race detector. We cover startDocker's
// early-return branches (TestStartDockerNoApp / TestStartDockerMissing
// ContainerPort) and its build/run/probe success path via the real daemon.

func TestBuildImageFailure(t *testing.T) {
	restoreHooks(t)
	var mu sync.Mutex
	var calls [][]string
	execCommandFn = mockExecCommand(&mu, &calls, "GO_APPS_HELPER_EXIT=1")
	dir := t.TempDir()
	m := NewManager(NewStore(dir), nil)
	app := &App{Name: "bf", Runtime: RuntimeDocker, WorkDir: filepath.Join(dir, "src"),
		Docker: DockerSpec{ContainerPort: 80, Build: DockerBuild{Context: "/abs/ctx"}}}
	p := &process{name: "bf", app: app, runtimeKind: RuntimeDocker, port: 9300, workDir: app.WorkDir, stopCh: make(chan struct{})}
	if err := m.buildImage(p, "img:latest"); err == nil || !strings.Contains(err.Error(), "docker build failed") {
		t.Fatalf("buildImage failure = %v", err)
	}
}

func TestBuildImageSuccess(t *testing.T) {
	restoreHooks(t)
	var mu sync.Mutex
	var calls [][]string
	execCommandFn = mockExecCommand(&mu, &calls)
	dir := t.TempDir()
	m := NewManager(NewStore(dir), quietLog())
	app := &App{Name: "bs", Runtime: RuntimeDocker, WorkDir: filepath.Join(dir, "src"),
		Docker: DockerSpec{ContainerPort: 80, Build: DockerBuild{Context: "rel", Dockerfile: "Containerfile", Args: map[string]string{"A": "1"}, Target: "final"}}}
	// buildImage sets cmd.Dir to the resolved context; it must exist.
	if err := os.MkdirAll(filepath.Join(app.WorkDir, "rel"), 0755); err != nil {
		t.Fatal(err)
	}
	p := &process{name: "bs", app: app, runtimeKind: RuntimeDocker, port: 9400, workDir: app.WorkDir, stopCh: make(chan struct{})}
	if err := m.buildImage(p, "img:tag"); err != nil {
		t.Fatalf("buildImage success: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) == 0 {
		t.Fatal("expected a buildx call")
	}
	joined := strings.Join(calls[0], " ")
	for _, want := range []string{"buildx", "build", "--tag img:tag", "--file", "--build-arg A=1", "--target final"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("build args missing %q: %v", want, calls[0])
		}
	}
}

func TestStopDockerLocked(t *testing.T) {
	restoreHooks(t)
	m := NewManager(NewStore(t.TempDir()), nil)

	// No dockerID → no-op.
	p := &process{name: "s1", runtimeKind: RuntimeDocker, stopCh: make(chan struct{})}
	m.mu.Lock()
	err := m.stopDockerLocked(p)
	m.mu.Unlock()
	if err != nil {
		t.Fatalf("stopDockerLocked no-id = %v", err)
	}

	// With dockerID: stopDockerLocked uses the real `docker stop` (not the
	// hook). For a non-existent container it fails internally but the
	// method is best-effort and returns nil while clearing dockerID.
	p2 := &process{name: "s2", runtimeKind: RuntimeDocker, dockerID: "nope-xyz", stopCh: make(chan struct{})}
	m.mu.Lock()
	err = m.stopDockerLocked(p2)
	m.mu.Unlock()
	if err != nil {
		t.Fatalf("stopDockerLocked = %v, want nil (best effort)", err)
	}
	if p2.dockerID != "" {
		t.Fatalf("dockerID should be cleared, got %q", p2.dockerID)
	}
}

func TestStopDockerViaStop(t *testing.T) {
	restoreHooks(t)
	m := NewManager(NewStore(t.TempDir()), quietLog())
	app := &App{Name: "sd", Runtime: RuntimeDocker, Docker: DockerSpec{Image: "x", ContainerPort: 80}}
	m.mu.Lock()
	m.procs["sd"] = &process{name: "sd", app: app, runtimeKind: RuntimeDocker, dockerID: "fake", stopCh: make(chan struct{})}
	m.mu.Unlock()
	if err := m.Stop("sd"); err != nil {
		t.Fatalf("Stop docker = %v", err)
	}
	if m.State("sd") != StateStopped {
		t.Fatal("docker app should be stopped")
	}
}

func TestWatchDockerEmptyID(t *testing.T) {
	m := NewManager(NewStore(t.TempDir()), nil)
	p := &process{name: "w", runtimeKind: RuntimeDocker, stopCh: make(chan struct{})}
	// Empty id returns immediately; must not panic or block.
	m.watchDocker(p, "", p.stopCh)
}

func TestWatchDockerStopSignalled(t *testing.T) {
	restoreHooks(t)
	m := NewManager(NewStore(t.TempDir()), nil)
	app := &App{Name: "ws", Runtime: RuntimeDocker, Docker: DockerSpec{Image: "x", ContainerPort: 80}}
	p := &process{name: "ws", app: app, runtimeKind: RuntimeDocker, dockerID: "nope-xyz",
		autoRestart: true, startedAt: time.Now(), stopCh: make(chan struct{})}
	// Close stopCh first so watchDocker takes the graceful-stop branch after
	// `docker wait` returns (the container doesn't exist → returns fast).
	close(p.stopCh)
	m.watchDocker(p, "nope-xyz", p.stopCh)
	if p.dockerID != "" {
		t.Fatalf("watchDocker stop branch should clear dockerID, got %q", p.dockerID)
	}
}

func TestWatchDockerGivesUpAfterCrashloop(t *testing.T) {
	restoreHooks(t)
	m := NewManager(NewStore(t.TempDir()), quietLog())
	app := &App{Name: "wc", Runtime: RuntimeDocker, Docker: DockerSpec{Image: "x", ContainerPort: 80}}
	p := &process{
		name:         "wc",
		app:          app,
		runtimeKind:  RuntimeDocker,
		dockerID:     "nope-xyz",
		autoRestart:  true,
		restartCount: crashloopMaxRestarts - 1, // one more crash → give up
		startedAt:    time.Now(),               // uptime < healthy window → counts as crash
		stopCh:       make(chan struct{}),
	}
	// `docker wait nope-xyz` (real) returns quickly (unknown container).
	// stopCh open so it proceeds to crashloop bookkeeping and gives up.
	m.watchDocker(p, "nope-xyz", p.stopCh)
	if !p.crashloopGave {
		t.Fatalf("watchDocker should give up after crashloop, restartCount=%d", p.restartCount)
	}
}

func TestWatchDockerNoRestartWhenDisabled(t *testing.T) {
	restoreHooks(t)
	m := NewManager(NewStore(t.TempDir()), nil)
	app := &App{Name: "wn", Runtime: RuntimeDocker, Docker: DockerSpec{Image: "x", ContainerPort: 80}}
	p := &process{name: "wn", app: app, runtimeKind: RuntimeDocker, dockerID: "nope-xyz",
		autoRestart: false, startedAt: time.Now(), stopCh: make(chan struct{})}
	m.watchDocker(p, "nope-xyz", p.stopCh)
	if p.dockerID != "" {
		t.Fatalf("dockerID should be cleared, got %q", p.dockerID)
	}
	if p.crashloopGave {
		t.Fatal("non-autorestart watcher should not set crashloopGave")
	}
}

func TestDockerContainerRunningFalse(t *testing.T) {
	// Real docker inspect against a non-existent container → false.
	if dockerContainerRunning("uwas-app-definitely-not-here-xyz") {
		t.Fatal("nonexistent container should report not-running")
	}
}

func TestDockerRunArgsSkipsNonPositivePort(t *testing.T) {
	p := &process{
		name: "z",
		app: &App{
			Name: "z", Runtime: RuntimeDocker, Port: 3000,
			Ports:  []int{0, -5, 8080}, // non-positive entries must be skipped
			Docker: DockerSpec{Image: "img", ContainerPort: 80},
		},
		port: 3000,
	}
	_, extra := dockerRunArgs(p, "uwas-app-z", "img", 80)
	if len(extra) != 1 || extra[0] != 8080 {
		t.Fatalf("extraPorts = %v, want [8080] (non-positive skipped)", extra)
	}
}

func TestDockerRunArgsWithVolumesAndExtraArgs(t *testing.T) {
	p := &process{
		name: "v",
		app: &App{
			Name: "v", Runtime: RuntimeDocker, Port: 3000,
			Docker: DockerSpec{
				Image: "img", ContainerPort: 80,
				Volumes:   []string{"/host:/cont"},
				ExtraArgs: []string{"--cap-add", "NET_ADMIN"},
			},
		},
		port: 3000,
		env:  map[string]string{"K": "V"},
	}
	args, _ := dockerRunArgs(p, "uwas-app-v", "img", 80)
	joined := strings.Join(args, " ")
	for _, want := range []string{"-v /host:/cont", "--cap-add NET_ADMIN", "K=V", "127.0.0.1:3000:80"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args missing %q: %v", want, args)
		}
	}
}

// ---------------------------------------------------------------------------
// gracefulKill nil-safe path (unix)
// ---------------------------------------------------------------------------

func TestGracefulKillNil(t *testing.T) {
	if err := gracefulKill(nil, "x"); err != nil {
		t.Fatalf("gracefulKill(nil) = %v", err)
	}
	if err := gracefulKill(&exec.Cmd{}, "x"); err != nil {
		t.Fatalf("gracefulKill(no process) = %v", err)
	}
}

func TestGracefulKillTerminatesShellProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh + SIGTERM")
	}
	// A process that handles SIGTERM and exits promptly exercises the
	// SIGTERM → exits-within-grace path (no SIGKILL needed).
	cmd := exec.Command("sh", "-c", "trap 'exit 0' TERM; sleep 30")
	configureProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	// Reap in the background so the zombie is collected after gracefulKill.
	go func() { _ = cmd.Wait() }()
	time.Sleep(100 * time.Millisecond)
	if err := gracefulKill(cmd, "graceful"); err != nil {
		t.Fatalf("gracefulKill: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Register error/replace branches
// ---------------------------------------------------------------------------

func TestRegisterNil(t *testing.T) {
	m := NewManager(NewStore(t.TempDir()), nil)
	if err := m.Register(nil); err == nil || !strings.Contains(err.Error(), "nil app") {
		t.Fatalf("Register(nil) = %v", err)
	}
}

func TestRegisterInvalidApp(t *testing.T) {
	m := NewManager(NewStore(t.TempDir()), nil)
	if err := m.Register(&App{Name: "bad name", Runtime: RuntimeNode}); err == nil {
		t.Fatal("Register invalid name should error")
	}
}

func TestRegisterSaveFailure(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "afile")
	os.WriteFile(f, []byte("x"), 0644)
	m := NewManager(NewStore(filepath.Join(f, "sub")), nil)
	if err := m.Register(&App{Name: "x", Runtime: RuntimeCustom, Command: "true"}); err == nil {
		t.Fatal("Register should fail when store save fails")
	}
}

func TestRegisterReplacesExistingRunning(t *testing.T) {
	restoreHooks(t)
	dir := t.TempDir()
	m := NewManager(NewStore(dir), quietLog())
	app := &App{Name: "rep", Runtime: RuntimeDocker, Docker: DockerSpec{Image: "x", ContainerPort: 80}}
	if err := m.Register(app); err != nil {
		t.Fatal(err)
	}
	// Mark the existing proc running so Register's replace path calls stop.
	m.mu.Lock()
	m.procs["rep"].dockerID = "fake"
	m.mu.Unlock()
	// Re-register: existing proc is stopped (stopDockerLocked, best-effort)
	// then replaced with a fresh process.
	if err := m.Register(app); err != nil {
		t.Fatalf("re-register: %v", err)
	}
	if m.State("rep") != StateStopped {
		t.Fatal("re-registered app should be stopped (not auto-started)")
	}
}

// ---------------------------------------------------------------------------
// Start dispatch to docker + ensureStartPortAvailable error
// ---------------------------------------------------------------------------

// Start's dispatch to startDocker is covered by the real-daemon lifecycle test;
// see the note above TestBuildImageFailure for why it is not driven with mocked
// exec here (watchDocker vs. startDocker dockerID race under -race).

func TestStartReturnsEnsurePortError(t *testing.T) {
	restoreHooks(t)
	dir := t.TempDir()
	m := NewManager(NewStore(dir), nil)
	app := &App{Name: "ep", Runtime: RuntimeNode, Port: 3001, WorkDir: dir}
	if err := m.Register(app); err != nil {
		t.Fatal(err)
	}
	// Port always occupied → ensureStartPortAvailable tries to reassign +
	// save; break the store so the save (and thus Start) fails.
	isPortFreeFn = func(int) bool { return false }
	badFile := filepath.Join(dir, "afile")
	os.WriteFile(badFile, []byte("x"), 0644)
	m.Store().Dir = filepath.Join(badFile, "sub")
	if err := m.Start("ep"); err == nil {
		t.Fatal("Start should propagate ensureStartPortAvailable error")
	}
}

// ---------------------------------------------------------------------------
// ListenAddrForPort not-running / unregistered
// ---------------------------------------------------------------------------

func TestListenAddrForPortNotRoutable(t *testing.T) {
	m := NewManager(NewStore(t.TempDir()), nil)
	// Unregistered.
	if got := m.ListenAddrForPort("nope", 0); got != "" {
		t.Fatalf("unregistered ListenAddr = %q, want empty", got)
	}
	// Registered but stopped.
	if err := m.Register(&App{Name: "stp", Runtime: RuntimeCustom, Command: "true"}); err != nil {
		t.Fatal(err)
	}
	if got := m.ListenAddr("stp"); got != "" {
		t.Fatalf("stopped ListenAddr = %q, want empty", got)
	}
}

// ---------------------------------------------------------------------------
// scaffold.go — error + every runtime branch
// ---------------------------------------------------------------------------

func TestScaffoldDemoNilAndMissingWorkdir(t *testing.T) {
	if _, err := ScaffoldDemo(nil); err == nil {
		t.Fatal("nil app should error")
	}
	if _, err := ScaffoldDemo(&App{Name: "x", Runtime: RuntimeNode}); err == nil {
		t.Fatal("missing workdir should error")
	}
}

func TestScaffoldDemoUnknownRuntimeNoFiles(t *testing.T) {
	dir := t.TempDir()
	ok, err := ScaffoldDemo(&App{Name: "c", Runtime: RuntimeCustom, WorkDir: dir})
	if err != nil {
		t.Fatalf("custom scaffold: %v", err)
	}
	if ok {
		t.Fatal("custom runtime has no demo files; should return false")
	}
}

func TestScaffoldDemoPythonAndRuby(t *testing.T) {
	for _, tc := range []struct {
		rt   Runtime
		file string
		cmd  string
	}{
		{RuntimePython, "app.py", ""},
		{RuntimeRuby, "app.rb", "ruby app.rb"},
	} {
		dir := t.TempDir()
		app := &App{Name: "s", Runtime: tc.rt, WorkDir: dir}
		ok, err := ScaffoldDemo(app)
		if err != nil || !ok {
			t.Fatalf("%s scaffold ok=%v err=%v", tc.rt, ok, err)
		}
		if _, err := os.Stat(filepath.Join(dir, tc.file)); err != nil {
			t.Fatalf("%s file missing: %v", tc.rt, err)
		}
		if tc.cmd != "" && app.Command != tc.cmd {
			t.Fatalf("%s command = %q, want %q", tc.rt, app.Command, tc.cmd)
		}
	}
}

func TestScaffoldDemoSkipsWhenCommandPreset(t *testing.T) {
	dir := t.TempDir()
	app := &App{Name: "preset", Runtime: RuntimeGo, WorkDir: dir, Command: "./mybin"}
	ok, err := ScaffoldDemo(app)
	if err != nil || !ok {
		t.Fatalf("go scaffold ok=%v err=%v", ok, err)
	}
	if app.Command != "./mybin" {
		t.Fatalf("preset command should be preserved, got %q", app.Command)
	}
}

// ---------------------------------------------------------------------------
// monitorNative — crashloop bookkeeping branches (logger present)
// ---------------------------------------------------------------------------

// runDeadCmd returns a started-then-exited *exec.Cmd so monitorNative's
// cmd.Wait() returns immediately with a non-zero error.
func runDeadCmd(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := execCommandFn("sh", "-c", "exit 1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start dead cmd: %v", err)
	}
	return cmd
}

func TestMonitorNativeNilCmd(t *testing.T) {
	m := NewManager(NewStore(t.TempDir()), quietLog())
	p := &process{name: "nilcmd", runtimeKind: RuntimeCustom, stopCh: make(chan struct{})}
	// cmd == nil → monitorNative returns immediately without touching state.
	m.monitorNative(p, nil, nil, p.stopCh)
}

func TestMonitorNativeNoAutoRestart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh")
	}
	m := NewManager(NewStore(t.TempDir()), quietLog())
	cmd := runDeadCmd(t)
	p := &process{name: "na", runtimeKind: RuntimeCustom, autoRestart: false,
		cmd: cmd, startedAt: time.Now(), stopCh: make(chan struct{})}
	m.monitorNative(p, cmd, nil, p.stopCh)
	if p.cmd != nil {
		t.Fatal("cmd should be cleared")
	}
	if p.restartCount != 0 {
		t.Fatalf("no-autorestart should not count crashes, got %d", p.restartCount)
	}
}

func TestMonitorNativeGivesUpAfterCrashloop(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh")
	}
	m := NewManager(NewStore(t.TempDir()), quietLog())
	cmd := runDeadCmd(t)
	p := &process{
		name: "giveup", runtimeKind: RuntimeCustom, autoRestart: true,
		cmd: cmd, restartCount: crashloopMaxRestarts - 1,
		startedAt: time.Now(), stopCh: make(chan struct{}),
	}
	m.monitorNative(p, cmd, nil, p.stopCh)
	if !p.crashloopGave {
		t.Fatalf("should give up; restartCount=%d gave=%v", p.restartCount, p.crashloopGave)
	}
}

func TestMonitorNativeHealthyResetThenStop(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh")
	}
	m := NewManager(NewStore(t.TempDir()), quietLog())
	cmd := runDeadCmd(t)
	p := &process{
		name: "healthy", runtimeKind: RuntimeCustom, autoRestart: true,
		cmd: cmd, restartCount: 5,
		startedAt: time.Now().Add(-2 * crashloopHealthyWindow), // long uptime → reset
		stopCh:    make(chan struct{}),
	}
	done := make(chan struct{})
	go func() {
		m.monitorNative(p, cmd, nil, p.stopCh)
		close(done)
	}()
	// monitor resets restartCount to 0, computes a 2s backoff, then blocks
	// on the timer/stopCh. Close stopCh to make it return without an actual
	// restart (no real process spawned).
	time.Sleep(50 * time.Millisecond)
	close(p.stopCh)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("monitorNative did not return after stop")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if p.restartCount != 0 {
		t.Fatalf("long uptime should reset restartCount, got %d", p.restartCount)
	}
}

func TestMonitorNativeBackoffInterruptedByStop(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh")
	}
	m := NewManager(NewStore(t.TempDir()), quietLog())
	cmd := runDeadCmd(t)
	p := &process{
		name: "backoff", runtimeKind: RuntimeCustom, autoRestart: true,
		cmd: cmd, restartCount: 2, // → 3 after increment, delay 8s; logged ">1"
		startedAt: time.Now(), stopCh: make(chan struct{}),
	}
	done := make(chan struct{})
	go func() {
		m.monitorNative(p, cmd, nil, p.stopCh)
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	close(p.stopCh) // interrupt the backoff timer → returns without restart
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("monitorNative backoff did not return after stop")
	}
}

func TestMonitorNativeAutoRestartsAfterBackoff(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh")
	}
	dir := t.TempDir()
	store := NewStore(filepath.Join(dir, "apps.d"))
	store.DataRoot = filepath.Join(dir, "data")
	m := NewManager(store, quietLog())

	workDir := filepath.Join(store.DataRoot, "ar")
	if err := os.MkdirAll(workDir, 0755); err != nil {
		t.Fatal(err)
	}
	app := &App{Name: "ar", Runtime: RuntimeCustom, Command: "sleep 5", Port: 0, WorkDir: workDir}
	p := &process{
		name: "ar", app: app, runtimeKind: RuntimeCustom, command: "sleep 5",
		workDir: workDir, autoRestart: true, restartCount: 0,
		startedAt: time.Now(), stopCh: make(chan struct{}),
	}
	m.mu.Lock()
	m.procs["ar"] = p
	m.mu.Unlock()
	defer m.Stop("ar")

	cmd := runDeadCmd(t)
	done := make(chan struct{})
	go func() {
		// restartCount becomes 1 (short uptime), backoff 2s, then startNative
		// spawns a real `sleep 5` and the monitor returns.
		m.monitorNative(p, cmd, nil, p.stopCh)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(6 * time.Second):
		t.Fatal("monitorNative auto-restart did not complete")
	}
	// After auto-restart the app should be running again.
	if inst := m.Get("ar"); inst == nil || !inst.Running {
		t.Fatalf("app should be running after auto-restart: %#v", inst)
	}
}

func TestStartNativeCmdStartFailure(t *testing.T) {
	restoreHooks(t)
	dir := t.TempDir()
	m := NewManager(NewStore(dir), quietLog())
	// Return a cmd pointing at a binary that does not exist so cmd.Start()
	// fails (covers the start-error branch in startNative).
	execCommandFn = func(name string, arg ...string) *exec.Cmd {
		return exec.Command(filepath.Join(dir, "no-such-binary-xyz"))
	}
	p := &process{
		name: "sf", runtimeKind: RuntimeCustom, command: "anything",
		port: 3001, workDir: dir, stopCh: make(chan struct{}),
	}
	m.mu.Lock()
	m.procs["sf"] = p
	m.mu.Unlock()
	if err := m.startNative(p); err == nil || !strings.Contains(err.Error(), "start:") {
		t.Fatalf("startNative cmd.Start failure = %v, want start error", err)
	}
}

// ---------------------------------------------------------------------------
// gracefulKill — SIGKILL ladder (process ignores SIGTERM)
// ---------------------------------------------------------------------------

func TestGracefulKillEscalatesToSIGKILL(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh + signals")
	}
	// Trap+ignore SIGTERM so gracefulKill must escalate to SIGKILL after the
	// grace window. The 3s grace is unavoidable here (matches prod ladder).
	cmd := exec.Command("sh", "-c", "trap '' TERM; while true; do sleep 0.2; done")
	configureProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	go func() { _ = cmd.Wait() }()
	time.Sleep(100 * time.Millisecond)
	if err := gracefulKill(cmd, "stubborn"); err != nil {
		t.Fatalf("gracefulKill: %v", err)
	}
}

// ---------------------------------------------------------------------------
// startNative — logger-present branches + env passthrough
// ---------------------------------------------------------------------------

func TestStartNativeWithLoggerAndEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh")
	}
	dir := t.TempDir()
	store := NewStore(filepath.Join(dir, "apps.d"))
	store.DataRoot = filepath.Join(dir, "data")
	m := NewManager(store, quietLog())
	app := &App{
		Name:    "envapp",
		Runtime: RuntimeCustom,
		Command: "sleep 2",
		WorkDir: filepath.Join(store.DataRoot, "envapp"),
		Env:     map[string]string{"CUSTOM_VAR": "hello"},
	}
	if err := m.Register(app); err != nil {
		t.Fatal(err)
	}
	if err := m.Start("envapp"); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer m.Stop("envapp")
	if inst := m.Get("envapp"); inst == nil || !inst.Running {
		t.Fatalf("app should be running: %#v", inst)
	}
}

func TestStartNativeInvalidCommandAfterPortSub(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(NewStore(dir), nil)
	p := &process{
		name: "bad", runtimeKind: RuntimeCustom,
		command: "node app.js | tee out.log", // pipe → validateShellCommand rejects
		port:    3001, workDir: dir, stopCh: make(chan struct{}),
	}
	m.mu.Lock()
	m.procs["bad"] = p
	m.mu.Unlock()
	if err := m.startNative(p); err == nil || !strings.Contains(err.Error(), "invalid command") {
		t.Fatalf("startNative invalid cmd = %v", err)
	}
}

// ---------------------------------------------------------------------------
// Stats native running path + readProcStats against the test process itself
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// StartAll branches: skip already-running, log start failure
// ---------------------------------------------------------------------------

func TestStartAllSkipsRunningAndLogsFailures(t *testing.T) {
	restoreHooks(t)
	dir := t.TempDir()
	m := NewManager(NewStore(dir), quietLog())

	// One docker app already "running" → StartAll skips it.
	running := &App{Name: "running", Runtime: RuntimeDocker, Docker: DockerSpec{Image: "x", ContainerPort: 80}}
	if err := m.Register(running); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.procs["running"].dockerID = "alive"
	m.mu.Unlock()

	// One native node app with no command + empty workdir → Start fails,
	// exercising the error-log branch.
	bad := &App{Name: "badstart", Runtime: RuntimeNode, WorkDir: filepath.Join(dir, "empty")}
	if err := m.Register(bad); err != nil {
		t.Fatal(err)
	}

	m.StartAll()
	defer m.StopAll()

	if !m.Get("running").Running {
		t.Fatal("running app should remain running (skipped)")
	}
	if m.Get("badstart").Running {
		t.Fatal("bad app should have failed to start")
	}
}

// ---------------------------------------------------------------------------
// isSameProcessRunning — name present but pointer mismatch
// ---------------------------------------------------------------------------

func TestIsSameProcessRunningPointerMismatch(t *testing.T) {
	m := NewManager(NewStore(t.TempDir()), nil)
	current := &process{name: "p", runtimeKind: RuntimeDocker, dockerID: "x", stopCh: make(chan struct{})}
	m.mu.Lock()
	m.procs["p"] = current
	m.mu.Unlock()
	stale := &process{name: "p", stopCh: make(chan struct{})}
	if m.isSameProcessRunning("p", stale) {
		t.Fatal("stale pointer should not match the registered process")
	}
	if !m.isSameProcessRunning("p", current) {
		t.Fatal("current pointer should match and be running")
	}
}

// ---------------------------------------------------------------------------
// WaitListening — native process exit attaches log tail + adopt conflict error
// ---------------------------------------------------------------------------

func TestWaitListeningNativeExitIncludesLogTail(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(filepath.Join(dir, "apps.d"))
	store.DataRoot = filepath.Join(dir, "data")
	m := NewManager(store, nil)

	workDir := filepath.Join(store.DataRoot, "nat")
	// Pick a free port nobody listens on.
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	app := &App{Name: "nat", Runtime: RuntimeNode, Port: port, WorkDir: workDir}
	// Write a native log file so the exit path's tailLogFile returns content.
	logDir := filepath.Join(filepath.Dir(workDir), "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "nat.log"), []byte("boom: missing module\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Mark running as a native proc with a non-nil cmd that already exited,
	// so isSameProcessRunning flips to false mid-wait.
	cmd := execCommandFn("sh", "-c", "exit 0")
	if runtime.GOOS != "windows" {
		_ = cmd.Start()
		_ = cmd.Wait()
	}
	p := &process{name: "nat", app: app, runtimeKind: RuntimeNode, port: port, workDir: workDir, stopCh: make(chan struct{})}
	m.mu.Lock()
	m.procs["nat"] = p
	m.mu.Unlock()

	// Spawn the wait; flip cmd to nil so the not-running branch fires with a
	// native runtime, exercising the log-tail attachment.
	done := make(chan error, 1)
	go func() { done <- m.WaitListening("nat", 3*time.Second) }()
	// Make it appear running first, then stop.
	m.mu.Lock()
	p.cmd = cmd // non-nil but Process may be set; we then clear it
	m.mu.Unlock()
	time.Sleep(80 * time.Millisecond)
	m.mu.Lock()
	p.cmd = nil
	m.mu.Unlock()

	err := <-done
	if err == nil || !strings.Contains(err.Error(), "process exited before binding") {
		t.Fatalf("WaitListening native exit = %v", err)
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error should include log tail, got: %v", err)
	}
}

func TestWaitListeningAdoptConflictError(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(filepath.Join(dir, "apps.d"))
	store.DataRoot = filepath.Join(dir, "data")
	m := NewManager(store, nil)
	workDir := filepath.Join(store.DataRoot, "conf")

	app := &App{Name: "conf", Runtime: RuntimeNode, Port: 41100, WorkDir: workDir}
	p := &process{name: "conf", app: app, runtimeKind: RuntimeDocker, dockerID: "alive", port: 41100, workDir: workDir, stopCh: make(chan struct{})}
	// Another app already owns the discovered port → adopt returns conflict.
	other := &process{name: "other", app: &App{Name: "other", Port: 42200}, port: 42200, stopCh: make(chan struct{})}
	m.mu.Lock()
	m.procs["conf"] = p
	m.procs["other"] = other
	m.mu.Unlock()

	portFile := runtimePortFile(workDir, "conf")
	if err := os.MkdirAll(filepath.Dir(portFile), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(portFile, []byte("42200\n"), 0644); err != nil {
		t.Fatal(err)
	}

	err := m.WaitListening("conf", 2*time.Second)
	if err == nil || !strings.Contains(err.Error(), "already owned") {
		t.Fatalf("WaitListening adopt-conflict = %v", err)
	}
}

// ---------------------------------------------------------------------------
// exposedPortsForProcess — duplicate extra port skipped
// ---------------------------------------------------------------------------

func TestExposedPortsForProcessDedup(t *testing.T) {
	p := &process{port: 3000, app: &App{Port: 3000, Ports: []int{3000, 5173, 5173, 0}}}
	got := exposedPortsForProcess(p)
	if len(got) != 2 || got[0] != 3000 || got[1] != 5173 {
		t.Fatalf("exposedPorts dedup = %v, want [3000 5173]", got)
	}
	if exposedPortsForProcess(nil) != nil {
		t.Fatal("nil process should yield nil")
	}
}

// ---------------------------------------------------------------------------
// ensureStartPortAvailable — recheck-under-lock free path + logger warn
// ---------------------------------------------------------------------------

func TestEnsureStartPortAvailableRecheckFree(t *testing.T) {
	restoreHooks(t)
	dir := t.TempDir()
	m := NewManager(NewStore(dir), quietLog())
	p := &process{name: "recheck", runtimeKind: RuntimeNode, port: 3001, workDir: dir, stopCh: make(chan struct{})}
	m.mu.Lock()
	m.procs["recheck"] = p
	m.mu.Unlock()

	// First isPortFreeFn call (outside lock) says occupied; the recheck under
	// the write lock says free → early-return without reassigning (line 462).
	var n int
	isPortFreeFn = func(int) bool {
		n++
		return n > 1 // first call false, rest true
	}
	if err := m.ensureStartPortAvailable(p); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if p.port != 3001 {
		t.Fatalf("recheck-free path should keep port, got %d", p.port)
	}
	if n < 2 {
		t.Fatalf("expected an under-lock recheck, isPortFree called %d times", n)
	}
}

func TestEnsureStartPortAvailableReassignsWithLogger(t *testing.T) {
	restoreHooks(t)
	dir := t.TempDir()
	m := NewManager(NewStore(dir), quietLog())
	app := &App{Name: "warn", Runtime: RuntimeNode, Port: 3001, WorkDir: dir}
	if err := m.Store().Save(app); err != nil {
		t.Fatal(err)
	}
	p := &process{name: "warn", app: app, runtimeKind: RuntimeNode, port: 3001, workDir: dir, stopCh: make(chan struct{})}
	m.mu.Lock()
	m.procs["warn"] = p
	m.mu.Unlock()

	// Port always occupied; allocate accepts a higher candidate. Reassignment
	// + warn-log branch (lines 466-482).
	isPortFreeFn = func(p int) bool { return p > 3001 }
	if err := m.ensureStartPortAvailable(p); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if p.port == 3001 {
		t.Fatalf("port should have been reassigned away from 3001")
	}
}

// ---------------------------------------------------------------------------
// store.go — additional branches
// ---------------------------------------------------------------------------

func TestStoreLoadSkipsInvalidSchemaAndSorts(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	// Out-of-order valid apps to exercise the insertion sort swap.
	for _, n := range []string{"zzz", "aaa", "mmm"} {
		os.WriteFile(filepath.Join(dir, n+".yaml"),
			[]byte("name: "+n+"\nruntime: custom\ncommand: ./r\n"), 0600)
	}
	// Valid filename/name match but schema-invalid (missing runtime) → skip.
	os.WriteFile(filepath.Join(dir, "schemabad.yaml"), []byte("name: schemabad\n"), 0600)

	apps, skipped, err := s.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(skipped) != 1 {
		t.Fatalf("expected 1 schema skip, got %d: %v", len(skipped), skipped)
	}
	if len(apps) != 3 || apps[0].Name != "aaa" || apps[2].Name != "zzz" {
		t.Fatalf("apps not sorted: %v", appNames(apps))
	}
}

func appNames(apps []*App) []string {
	out := make([]string, len(apps))
	for i, a := range apps {
		out[i] = a.Name
	}
	return out
}

func TestStoreGetReadError(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	// Make <name>.yaml a directory so ReadFile fails with a non-NotExist err.
	os.Mkdir(filepath.Join(dir, "isdir.yaml"), 0755)
	if _, err := s.Get("isdir"); err == nil {
		t.Fatal("Get on a directory path should error")
	}
}

func TestStoreDeleteNonEmptyDirError(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	// Make <name>.yaml a non-empty directory so os.Remove fails (not NotExist).
	d := filepath.Join(dir, "busy.yaml")
	os.Mkdir(d, 0755)
	os.WriteFile(filepath.Join(d, "child"), []byte("x"), 0644)
	if err := s.Delete("busy"); err == nil {
		t.Fatal("Delete on non-empty dir should error")
	}
}

// ---------------------------------------------------------------------------
// scaffold.go — filesystem error branches
// ---------------------------------------------------------------------------

func TestScaffoldDemoMkdirError(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "afile")
	os.WriteFile(f, []byte("x"), 0644)
	// WorkDir under a file → MkdirAll fails.
	app := &App{Name: "s", Runtime: RuntimeNode, WorkDir: filepath.Join(f, "sub")}
	if _, err := ScaffoldDemo(app); err == nil {
		t.Fatal("scaffold should fail when workdir cannot be created")
	}
}

func TestScaffoldDemoReadDirError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod-based unreadable dir does not block root")
	}
	dir := t.TempDir()
	wd := filepath.Join(dir, "locked")
	if err := os.MkdirAll(wd, 0755); err != nil {
		t.Fatal(err)
	}
	// Make the workdir unreadable so MkdirAll (idempotent, dir exists) passes
	// but the subsequent ReadDir fails.
	if err := os.Chmod(wd, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(wd, 0o755) })
	app := &App{Name: "s", Runtime: RuntimeNode, WorkDir: wd}
	if _, err := ScaffoldDemo(app); err == nil {
		t.Fatal("scaffold should fail when workdir cannot be read")
	}
}

func TestStatsNativeRunningSelf(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh")
	}
	dir := t.TempDir()
	m := NewManager(NewStore(dir), nil)
	if err := m.Register(&App{Name: "live", Runtime: RuntimeCustom, Command: "sleep 2", WorkDir: dir}); err != nil {
		t.Fatal(err)
	}
	if err := m.Start("live"); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer m.Stop("live")
	s := m.Stats("live")
	if s == nil || !s.Running || s.PID == 0 {
		t.Fatalf("native stats should report running pid: %#v", s)
	}
}

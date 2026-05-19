package apps

import (
	"fmt"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestDetectHintCoversRuntimes(t *testing.T) {
	for _, rt := range []string{"node", "python", "ruby", "go", "custom"} {
		hint := detectHint(rt)
		if hint == "" {
			t.Errorf("detectHint(%q) returned empty", rt)
		}
	}
	if h := detectHint("nonsense"); !strings.Contains(h, "command") {
		t.Errorf("unknown runtime hint should mention command, got %q", h)
	}
}

func TestStartNativeReportsImmediateExit(t *testing.T) {
	// Windows uses cmd /C which has different exit semantics; the
	// 500ms probe is OS-agnostic but the failing-command shorthand
	// below is /bin/sh specific. Skip on Windows.
	if runtime.GOOS == "windows" {
		t.Skip("liveness probe test is unix-only")
	}

	dir := t.TempDir()
	store := NewStore(dir)
	mgr := NewManager(store, nil)

	// Register an app that will exit immediately with non-zero. The
	// liveness probe should catch this and return a descriptive error
	// instead of "started successfully".
	app := &App{
		Name:    "fail-fast",
		Runtime: RuntimeCustom,
		Command: "false", // sh exits non-zero immediately
		WorkDir: dir,
	}
	if err := mgr.Register(app); err != nil {
		t.Fatalf("register: %v", err)
	}

	err := mgr.Start("fail-fast")
	if err == nil {
		t.Fatal("Start should have errored on an immediately-exiting process")
	}
	if !strings.Contains(err.Error(), "exited within") {
		t.Errorf("error should mention liveness probe; got: %v", err)
	}
}

func TestStartNativeMissingCommandHasHint(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	mgr := NewManager(store, nil)

	// Node runtime with no command and an empty workdir → detection
	// fails, error should include the detect-hint listing the files
	// that would have been accepted.
	app := &App{
		Name:    "no-cmd",
		Runtime: RuntimeNode,
		WorkDir: dir,
	}
	if err := mgr.Register(app); err != nil {
		t.Fatalf("register: %v", err)
	}

	err := mgr.Start("no-cmd")
	if err == nil {
		t.Fatal("Start should have errored when no command and no entrypoint")
	}
	if !strings.Contains(err.Error(), "server.js") {
		t.Errorf("error should hint at expected files; got: %v", err)
	}
}

func TestTailLogFile(t *testing.T) {
	if got := tailLogFile("", 100); got != "" {
		t.Errorf("empty path should return empty, got %q", got)
	}
	if got := tailLogFile("/nonexistent/path/zzz", 100); got != "" {
		t.Errorf("missing file should return empty, got %q", got)
	}
}

func TestComputeBackoff(t *testing.T) {
	cases := []struct {
		restartCount int
		want         time.Duration
	}{
		{0, 2 * time.Second},
		{1, 2 * time.Second},
		{2, 4 * time.Second},
		{3, 8 * time.Second},
		{4, 16 * time.Second},
		{5, 32 * time.Second},
		// Eventually caps at crashloopMaxBackoff (5min)
		{20, crashloopMaxBackoff},
	}
	for _, c := range cases {
		got := computeBackoff(c.restartCount)
		if got != c.want {
			t.Errorf("computeBackoff(%d) = %v, want %v", c.restartCount, got, c.want)
		}
	}
}

func TestWaitListeningSuccess(t *testing.T) {
	// Stand up a TCP listener on an arbitrary free port, then register
	// an app that "owns" that port (we don't actually spawn it — we
	// just simulate the running state by manually setting cmd).
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	dir := t.TempDir()
	mgr := NewManager(NewStore(dir), nil)
	app := &App{
		Name:    "listening",
		Runtime: RuntimeNode,
		Command: "true",
		Port:    port,
		WorkDir: dir,
	}
	mgr.mu.Lock()
	mgr.procs[app.Name] = &process{
		name:        app.Name,
		app:         app,
		runtimeKind: app.Runtime,
		command:     app.Command,
		port:        port,
		workDir:     dir,
		stopCh:      make(chan struct{}),
		// Mark as running via a dummy non-nil cmd. We can't easily
		// fabricate an *exec.Cmd with a live Process, so use the
		// docker code path: set dockerID so isRunning returns true.
	}
	// Hack: pretend it's a docker app to skip the cmd.Process check.
	mgr.procs[app.Name].runtimeKind = RuntimeDocker
	mgr.procs[app.Name].dockerID = "fake-id"
	mgr.mu.Unlock()

	if err := mgr.WaitListening("listening", 2*time.Second); err != nil {
		t.Errorf("WaitListening should have succeeded against an open listener: %v", err)
	}
}

func TestWaitListeningTimeout(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(NewStore(dir), nil)
	// Pick a port that's almost certainly free and nobody is listening on.
	freePort := 0
	{
		l, _ := net.Listen("tcp", "127.0.0.1:0")
		freePort = l.Addr().(*net.TCPAddr).Port
		l.Close() // immediately close so the port is unused
	}

	app := &App{
		Name:    "not-listening",
		Runtime: RuntimeNode,
		Port:    freePort,
		WorkDir: dir,
	}
	mgr.mu.Lock()
	mgr.procs[app.Name] = &process{
		name:        app.Name,
		app:         app,
		runtimeKind: RuntimeDocker, // skip cmd.Process check
		dockerID:    "fake-id",
		port:        freePort,
		workDir:     dir,
		stopCh:      make(chan struct{}),
	}
	mgr.mu.Unlock()

	err := mgr.WaitListening("not-listening", 500*time.Millisecond)
	if err == nil {
		t.Fatal("WaitListening should have timed out")
	}
	if !strings.Contains(err.Error(), "not listening") {
		t.Errorf("error should mention not listening; got: %v", err)
	}
}

func TestWaitListeningSkipsCustomRuntime(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(NewStore(dir), nil)
	app := &App{
		Name:    "batch-worker",
		Runtime: RuntimeCustom,
		Command: "true",
		Port:    65530, // unlikely to be bound
		WorkDir: dir,
	}
	mgr.mu.Lock()
	mgr.procs[app.Name] = &process{
		name:        app.Name,
		app:         app,
		runtimeKind: RuntimeCustom,
		port:        65530,
		workDir:     dir,
		stopCh:      make(chan struct{}),
	}
	mgr.mu.Unlock()

	if err := mgr.WaitListening("batch-worker", 100*time.Millisecond); err != nil {
		t.Errorf("custom runtime should skip the listening probe: %v", err)
	}
}

func TestEnsureStartPortAvailableReassignsOccupiedPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	occupied := ln.Addr().(*net.TCPAddr).Port

	dir := t.TempDir()
	mgr := NewManager(NewStore(dir), nil)
	app := &App{
		Name:    "port-hop",
		Runtime: RuntimeNode,
		Command: "node index.js",
		Port:    occupied,
		WorkDir: dir,
	}
	p := &process{
		name:        app.Name,
		app:         app,
		runtimeKind: app.Runtime,
		command:     app.Command,
		port:        occupied,
		workDir:     dir,
		stopCh:      make(chan struct{}),
	}
	mgr.mu.Lock()
	mgr.procs[app.Name] = p
	mgr.mu.Unlock()

	if err := mgr.ensureStartPortAvailable(p); err != nil {
		t.Fatalf("ensureStartPortAvailable: %v", err)
	}
	if p.port == occupied {
		t.Fatalf("port should have been reassigned away from occupied %d", occupied)
	}
	if !isPortFreeFn(p.port) {
		t.Fatalf("replacement port %d should be free", p.port)
	}
	saved, err := mgr.Store().Get(app.Name)
	if err != nil {
		t.Fatalf("load saved app: %v", err)
	}
	if saved == nil || saved.Port != p.port {
		t.Fatalf("saved port = %v, want %d", saved, p.port)
	}
}

func TestMonitorNativeUsesStartStopChannelSnapshot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh")
	}

	dir := t.TempDir()
	mgr := NewManager(NewStore(dir), nil)
	oldStopCh := make(chan struct{})
	close(oldStopCh)
	newStopCh := make(chan struct{})

	cmd := execCommandFn("sh", "-c", "exit 1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start test cmd: %v", err)
	}
	p := &process{
		name:        "stop-snapshot",
		app:         &App{Name: "stop-snapshot", Runtime: RuntimeCustom, Command: "false", WorkDir: dir},
		runtimeKind: RuntimeCustom,
		command:     "false",
		workDir:     dir,
		autoRestart: true,
		cmd:         cmd,
		stopCh:      newStopCh,
		startedAt:   time.Now(),
	}

	mgr.monitorNative(p, cmd, nil, oldStopCh)
	if p.cmd != nil {
		t.Fatal("monitor should clear cmd after observing the closed start-time stop channel")
	}
	if p.restartCount != 0 {
		t.Fatalf("monitor should not count a stopped process as a crash, got %d", p.restartCount)
	}
}

func TestStartAllStartsEnabledLoadedAppsOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh")
	}

	dir := t.TempDir()
	store := NewStore(dir)
	if err := store.Save(&App{
		Name:    "enabled",
		Runtime: RuntimeCustom,
		Command: "sleep 2",
		WorkDir: dir,
	}); err != nil {
		t.Fatalf("save enabled app: %v", err)
	}
	if err := store.Save(&App{
		Name:     "disabled",
		Runtime:  RuntimeCustom,
		Command:  "sleep 2",
		WorkDir:  dir,
		Disabled: true,
	}); err != nil {
		t.Fatalf("save disabled app: %v", err)
	}

	mgr := NewManager(store, nil)
	if _, _, err := mgr.LoadAll(); err != nil {
		t.Fatalf("load all: %v", err)
	}
	mgr.StartAll()
	defer mgr.StopAll()

	enabled := mgr.Get("enabled")
	if enabled == nil || !enabled.Running {
		t.Fatalf("enabled app should be running after StartAll, got %#v", enabled)
	}
	disabled := mgr.Get("disabled")
	if disabled == nil || disabled.Running {
		t.Fatalf("disabled app should not be running after StartAll, got %#v", disabled)
	}
}

// silence the unused import linter for fmt when not all branches use it.
var _ = fmt.Sprintf

func TestStatsForUnknownApp(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(NewStore(dir), nil)
	if got := mgr.Stats("nobody"); got != nil {
		t.Errorf("Stats for unregistered app should be nil, got %+v", got)
	}
}

func TestStatsForStoppedApp(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(NewStore(dir), nil)
	app := &App{Name: "stopped", Runtime: RuntimeCustom, Command: "true"}
	if err := mgr.Register(app); err != nil {
		t.Fatal(err)
	}
	s := mgr.Stats("stopped")
	if s == nil {
		t.Fatal("Stats should return a struct for a registered-but-stopped app")
	}
	if s.Running {
		t.Errorf("Running should be false for a stopped app, got true")
	}
	if s.Name != "stopped" {
		t.Errorf("Name = %q, want %q", s.Name, "stopped")
	}
}

func TestStartResetsCrashloopState(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("relies on /bin/sh")
	}

	dir := t.TempDir()
	store := NewStore(dir)
	mgr := NewManager(store, nil)

	app := &App{
		Name:    "cl-test",
		Runtime: RuntimeCustom,
		Command: "sleep 1",
		WorkDir: dir,
	}
	if err := mgr.Register(app); err != nil {
		t.Fatal(err)
	}

	// Simulate prior crashloop state.
	mgr.mu.Lock()
	p := mgr.procs["cl-test"]
	p.restartCount = 8
	p.crashloopGave = true
	mgr.mu.Unlock()

	// Start is supposed to reset both fields. We don't care about the
	// process actually running for this test — we care about the
	// pre-spawn state reset.
	_ = mgr.Start("cl-test")
	defer mgr.Stop("cl-test")

	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	if p.restartCount != 0 {
		t.Errorf("Start should have reset restartCount, got %d", p.restartCount)
	}
	if p.crashloopGave {
		t.Error("Start should have cleared crashloopGave")
	}
}

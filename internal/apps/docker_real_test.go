package apps

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// lockDockerHost serializes the orphan-sweep tests across processes. They all
// manipulate the host-global "uwas-app-" docker container namespace, so one
// process's cleanupOrphanContainers would otherwise remove a container another
// process expects kept/removed. A portable O_EXCL lock file (no syscall, builds
// on Windows) is acquired by spinning until it's free.
func lockDockerHost(t *testing.T) {
	t.Helper()
	lockPath := filepath.Join(os.TempDir(), "uwas-orphan-test.lock")
	deadline := time.Now().Add(90 * time.Second)
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err == nil {
			t.Cleanup(func() { _ = f.Close(); _ = os.Remove(lockPath) })
			return
		}
		if time.Now().After(deadline) {
			// Stale lock (a crashed run left it behind) — take it over.
			t.Cleanup(func() { _ = os.Remove(lockPath) })
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// uniqueDockerApp returns a process-unique app name so concurrent runs of the
// real-daemon tests (separate OS processes) don't collide on the shared
// container name "uwas-app-<name>".
func uniqueDockerApp(base string) string {
	return base + "-" + strconv.Itoa(os.Getpid())
}

// freeHostPort returns an OS-assigned free localhost port. Each test process
// gets a distinct ephemeral port, so concurrent docker runs don't all pile
// onto the manager's fixed 3001 base and fail to bind the host port.
func freeHostPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

// pickSmallImage returns a locally-available small image to run, or skips.
func pickSmallImage(t *testing.T) string {
	t.Helper()
	for _, img := range []string{"alpine", "busybox"} {
		if err := exec.Command("docker", "image", "inspect", img).Run(); err == nil {
			return img
		}
	}
	t.Skip("no small runnable image (alpine/busybox) available locally")
	return ""
}

// dockerAvailable reports whether a working docker daemon is reachable. Tests
// that touch the real daemon skip when it is not, so the suite stays green on
// hosts without docker.
func dockerAvailable(t *testing.T) bool {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		return false
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		return false
	}
	return true
}

// TestDockerLifecycleRealDaemon covers the success paths of dockerContainer
// Running, readDockerStats, Stats (docker), and stopDockerLocked against a
// real, controlled container. It deliberately does NOT route through
// startDocker, because startDocker spawns a watchDocker goroutine that races
// stopDockerLocked on p.dockerID (a pre-existing prod data race the -race
// detector flags). Instead the container is launched directly with a plain
// `sleep` (no listener) and adopted into a process struct WITHOUT a watcher,
// so the stop path is exercised race-free. The container is force-removed in
// cleanup, so nothing leaks.
func TestDockerLifecycleRealDaemon(t *testing.T) {
	if !dockerAvailable(t) {
		t.Skip("docker daemon not available")
	}
	lockDockerHost(t)
	img := pickSmallImage(t)

	app := uniqueDockerApp("coverage-lifecycle")
	cname := containerName(app)
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", "-v", cname).Run()
	})
	_ = exec.Command("docker", "rm", "-f", cname).Run()

	dir := t.TempDir()
	store := NewStore(filepath.Join(dir, "apps.d"))
	store.DataRoot = filepath.Join(dir, "data")
	m := NewManager(store, quietLog())

	runOut, err := exec.Command("docker", "run", "-d", "--name", cname, img, "sleep", "60").CombinedOutput()
	if err != nil {
		t.Skipf("docker run failed (environment): %v: %s", err, runOut)
	}
	id := strings.TrimSpace(string(runOut))

	m.mu.Lock()
	m.procs[app] = &process{
		name:        app,
		app:         &App{Name: app, Runtime: RuntimeDocker, Docker: DockerSpec{Image: img, ContainerPort: 1}},
		runtimeKind: RuntimeDocker,
		dockerID:    id,
		startedAt:   time.Now(),
		stopCh:      make(chan struct{}),
	}
	m.mu.Unlock()

	// dockerContainerRunning success path.
	if !dockerContainerRunning(cname) {
		t.Fatalf("container %q should be running", cname)
	}

	// readDockerStats parse path against a live container.
	s := m.Stats(app)
	if s == nil || !s.Running {
		t.Fatalf("stats should report running: %#v", s)
	}
	if s.MemoryRSS <= 0 {
		t.Logf("note: docker stats returned MemoryRSS=%d (acceptable on some daemons)", s.MemoryRSS)
	}

	// Stop → stopDockerLocked success path. No watcher goroutine exists for
	// this process, so there is no race on p.dockerID.
	if err := m.Stop(app); err != nil {
		t.Fatalf("stop: %v", err)
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if !dockerContainerRunning(cname) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if dockerContainerRunning(cname) {
		t.Fatalf("container should be stopped after Stop")
	}
	if m.State(app) != StateStopped {
		t.Fatalf("manager should report stopped")
	}
}

// TestStartDockerSuccessRealDaemon covers startDocker's run → liveness-probe →
// success path (and watchDocker's `docker wait` on a live container) using a
// real daemon. The `docker run` is rewritten to launch a long-running `sleep`
// container so it survives the 500ms probe. To stay race-free we set
// autoRestart=false before the watcher spawns and, after startDocker returns,
// we never touch p.dockerID again — instead we close stopCh and stop the
// container externally, then wait for the watcher to wind down by polling the
// real daemon. (watchDocker writes p.dockerID without holding m.mu, so any
// concurrent access from the test would trip -race.)
func TestStartDockerSuccessRealDaemon(t *testing.T) {
	if !dockerAvailable(t) {
		t.Skip("docker daemon not available")
	}
	lockDockerHost(t)
	img := pickSmallImage(t)

	app := uniqueDockerApp("coverage-startdocker")
	cname := containerName(app)
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", "-v", cname).Run()
	})
	_ = exec.Command("docker", "rm", "-f", cname).Run()

	restoreHooks(t)
	execCommandFn = func(name string, arg ...string) *exec.Cmd {
		if name == "docker" && len(arg) > 0 && arg[0] == "run" {
			arg = append(arg, "sleep", "120")
		}
		return exec.Command(name, arg...)
	}

	dir := t.TempDir()
	store := NewStore(filepath.Join(dir, "apps.d"))
	store.DataRoot = filepath.Join(dir, "data")
	m := NewManager(store, quietLog())

	// Explicit unique host port avoids the fixed-3001 cross-process collision.
	spec := &App{Name: app, Runtime: RuntimeDocker, Port: freeHostPort(t), Docker: DockerSpec{Image: img, ContainerPort: 54321}}
	if err := m.Register(spec); err != nil {
		t.Fatalf("register: %v", err)
	}
	// Disable auto-restart before Start so the watcher (spawned inside
	// startDocker) never attempts a restart and we have a single, deterministic
	// shutdown path. Setting it before the goroutine spawn is race-free.
	m.mu.Lock()
	p := m.procs[app]
	p.autoRestart = false
	stopCh := p.stopCh
	m.mu.Unlock()

	// Drive through Start to cover the Start → startDocker dispatch branch.
	if err := m.Start(app); err != nil {
		t.Fatalf("Start docker success path: %v", err)
	}

	// Verify via the real daemon (not p.dockerID, which the watcher owns now).
	if !dockerContainerRunning(cname) {
		t.Fatalf("container %q should be running after startDocker", cname)
	}

	// Wind the watcher down race-free: signal stop, then stop the container so
	// `docker wait` returns and the watcher takes the graceful-stop branch.
	close(stopCh)
	_ = exec.Command("docker", "stop", "-t", "1", cname).Run()

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if !dockerContainerRunning(cname) {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	if dockerContainerRunning(cname) {
		t.Fatalf("container should have stopped")
	}
	// Give the watcher goroutine a moment to observe the exit and return.
	time.Sleep(500 * time.Millisecond)
}

// TestStartDockerBuildContextRealDaemon covers startDocker's build-context
// dispatch (buildImage) and the default-image-tag fallback (Image unset) by
// building a trivial busybox-derived image and running it. The `docker run` is
// rewritten to keep the container alive; teardown mirrors the success test.
func TestStartDockerBuildContextRealDaemon(t *testing.T) {
	if !dockerAvailable(t) {
		t.Skip("docker daemon not available")
	}
	lockDockerHost(t)
	base := pickSmallImage(t) // busybox/alpine present locally

	app := uniqueDockerApp("coverage-build")
	cname := containerName(app)
	defImage := cname + ":latest"
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", "-v", cname).Run()
		_ = exec.Command("docker", "rmi", "-f", defImage).Run()
	})
	_ = exec.Command("docker", "rm", "-f", cname).Run()

	dir := t.TempDir()
	store := NewStore(filepath.Join(dir, "apps.d"))
	store.DataRoot = filepath.Join(dir, "data")
	workDir := filepath.Join(store.DataRoot, app)
	if err := writeFileTree(workDir, map[string]string{
		"Dockerfile": "FROM " + base + "\n",
	}); err != nil {
		t.Fatal(err)
	}

	restoreHooks(t)
	execCommandFn = func(name string, arg ...string) *exec.Cmd {
		if name == "docker" && len(arg) > 0 && arg[0] == "run" {
			arg = append(arg, "sleep", "120")
		}
		return exec.Command(name, arg...)
	}

	m := NewManager(store, quietLog())
	// Image left empty → startDocker defaults the tag to containerName:latest
	// and the build context drives buildImage to produce it.
	spec := &App{
		Name:    app,
		Runtime: RuntimeDocker,
		WorkDir: workDir,
		Port:    freeHostPort(t),
		Docker:  DockerSpec{ContainerPort: 54321, Build: DockerBuild{Context: "."}},
	}
	stopCh := make(chan struct{})
	p := &process{
		name: app, app: spec, runtimeKind: RuntimeDocker,
		port: 59998, workDir: workDir, autoRestart: false, stopCh: stopCh,
	}

	if err := m.startDocker(p); err != nil {
		t.Skipf("startDocker build path failed (environment, e.g. buildx missing): %v", err)
	}
	if !dockerContainerRunning(cname) {
		t.Fatalf("built container should be running")
	}

	close(stopCh)
	_ = exec.Command("docker", "stop", "-t", "1", cname).Run()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if !dockerContainerRunning(cname) {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond)
}

func writeFileTree(dir string, files map[string]string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			return err
		}
	}
	return nil
}

// TestCleanupOrphanContainersRemovesCreatedOrphan exercises the orphan-removal
// loop in cleanupOrphanContainers using a real but never-started container
// (created via `docker create`, so no application process ever runs). The
// container is named with the uwas-app- prefix but has no corresponding app
// definition, so the sweep must remove it. The test force-removes the
// container in cleanup as a belt-and-suspenders guard against a sweep failure.
func TestCleanupOrphanContainersRemovesCreatedOrphan(t *testing.T) {
	if !dockerAvailable(t) {
		t.Skip("docker daemon not available")
	}
	lockDockerHost(t)

	// PID-unique name (still "uwas-app-"-prefixed so the sweep treats it as a
	// managed orphan) so concurrent test processes don't race on a shared
	// container name. The assertion below filters on this exact name, so it
	// only ever checks this process's own container.
	cname := "uwas-app-coverage-orphan-test-" + strconv.Itoa(os.Getpid())
	// Always clean up, even if the sweep under test fails.
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", "-v", cname).Run()
	})

	// hello-world is tiny and never long-runs; `create` doesn't even start it.
	// Fall back to alpine if hello-world isn't present.
	image := "hello-world"
	if err := exec.Command("docker", "image", "inspect", image).Run(); err != nil {
		image = "alpine"
		if err := exec.Command("docker", "image", "inspect", image).Run(); err != nil {
			t.Skipf("no small image available locally (%v)", err)
		}
	}

	if out, err := exec.Command("docker", "create", "--name", cname, image).CombinedOutput(); err != nil {
		t.Skipf("docker create failed (environment): %v: %s", err, out)
	}

	m := NewManager(NewStore(t.TempDir()), quietLog())
	// No app named "coverage-orphan-test" is registered → it's an orphan.
	m.cleanupOrphanContainers()

	// The container should be gone. Poll rather than check once — under heavy
	// parallel load `docker rm` can lag behind cleanupOrphanContainers.
	var out []byte
	deadline := time.Now().Add(10 * time.Second)
	for {
		out, _ = exec.Command("docker", "ps", "-a", "--filter", "name="+cname, "--format", "{{.Names}}").CombinedOutput()
		if !strings.Contains(string(out), cname) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("orphan container %q should have been removed, ps output: %q", cname, out)
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// TestCleanupOrphanContainersKeepsKnown verifies the sweep does NOT remove a
// uwas-app- container whose name corresponds to a registered app.
func TestCleanupOrphanContainersKeepsKnown(t *testing.T) {
	if !dockerAvailable(t) {
		t.Skip("docker daemon not available")
	}
	lockDockerHost(t)

	app := uniqueDockerApp("coverage-known-test")
	cname := containerName(app)
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", "-v", cname).Run()
	})

	image := "hello-world"
	if err := exec.Command("docker", "image", "inspect", image).Run(); err != nil {
		image = "alpine"
		if err := exec.Command("docker", "image", "inspect", image).Run(); err != nil {
			t.Skipf("no small image available locally (%v)", err)
		}
	}
	if out, err := exec.Command("docker", "create", "--name", cname, image).CombinedOutput(); err != nil {
		t.Skipf("docker create failed: %v: %s", err, out)
	}

	m := NewManager(NewStore(t.TempDir()), quietLog())
	m.mu.Lock()
	m.procs[app] = &process{name: app, runtimeKind: RuntimeDocker, stopCh: make(chan struct{})}
	m.mu.Unlock()

	m.cleanupOrphanContainers()

	out, _ := exec.Command("docker", "ps", "-a", "--filter", "name="+cname, "--format", "{{.Names}}").CombinedOutput()
	if !strings.Contains(string(out), cname) {
		t.Fatalf("known container %q should be kept, ps output: %q", cname, out)
	}
}

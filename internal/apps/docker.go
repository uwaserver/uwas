package apps

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// dockerProbeTimeout caps best-effort docker CLI probes so a wedged
// daemon (Docker Desktop paused / starting) can't hang LoadAll forever.
// The orphan sweep and liveness check are fine with "couldn't reach
// docker in 3s, skip" — that's the same outcome as docker not being
// installed at all.
const dockerProbeTimeout = 3 * time.Second

// containerName is the deterministic name we give docker so an operator
// can `docker logs uwas-app-<name>` without consulting our records. The
// prefix matters: it lets the operator distinguish UWAS-managed
// containers from anything else on the host.
func containerName(appName string) string {
	return "uwas-app-" + appName
}

// startDocker is the docker-runtime equivalent of startNative. Optional
// build step (when DockerBuild.Context is set), then `docker run -d`,
// then a `docker wait` watcher goroutine for auto-restart parity with
// the native path.
//
// Caller does NOT hold m.mu — startDocker is reached from Start() after
// the read-lock has been released, which is safe because the process
// struct fields it mutates (dockerID, startedAt) are only inspected by
// methods that themselves acquire the lock.
func (m *Manager) startDocker(p *process) error {
	if p.app == nil {
		return fmt.Errorf("apps: %s: docker process has no app definition", p.name)
	}

	// Build first if a build context is configured. We tag with the
	// configured Image name, falling back to "uwas-app-<name>:latest"
	// if the operator didn't set one — keeps things working without
	// requiring a registry choice up front.
	image := p.app.Docker.Image
	if image == "" {
		image = containerName(p.name) + ":latest"
	}
	if p.app.Docker.Build.Context != "" {
		if err := m.buildImage(p, image); err != nil {
			return err
		}
	}

	cname := containerName(p.name)
	// Best-effort prior cleanup. The container should already be gone
	// thanks to --rm on the previous run, but an abrupt daemon kill
	// (OOM, panic, sigkill) can leave a leftover named container that
	// would make this `docker run` fail with "container name already
	// in use". Force-removing it is harmless when nothing's there.
	_ = exec.Command("docker", "rm", "-f", cname).Run()

	containerPort := p.app.Docker.ContainerPort
	if containerPort == 0 {
		// Validate() blocks save without ContainerPort but a hand-
		// edited file could slip through; bail with a clear message
		// rather than silently mapping 0.
		return fmt.Errorf("apps: %s: docker.container_port is required", p.name)
	}

	args := []string{
		"run", "-d",
		"--name", cname,
		"--rm",
		"-p", fmt.Sprintf("127.0.0.1:%d:%d", p.port, containerPort),
		"-e", fmt.Sprintf("PORT=%d", containerPort),
	}
	for k, v := range p.env {
		args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
	}
	for _, v := range p.app.Docker.Volumes {
		args = append(args, "-v", v)
	}
	if p.app.Docker.ExtraArgs != nil {
		args = append(args, p.app.Docker.ExtraArgs...)
	}
	args = append(args, image)

	cmd := execCommandFn("docker", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("apps: %s: docker run failed: %w (stderr: %s)",
			p.name, err, strings.TrimSpace(stderr.String()))
	}

	// `docker run -d` prints the container ID on stdout. Trim and stash.
	p.dockerID = strings.TrimSpace(stdout.String())
	p.startedAt = time.Now()

	if m.logger != nil {
		m.logger.Info("apps: docker container started",
			"app", p.name, "container", cname, "image", image, "host_port", p.port, "container_port", containerPort)
	}

	// Spawn watcher that triggers auto-restart on container exit.
	if m.logger != nil {
		m.logger.SafeGo("apps.docker."+p.name, func() {
			m.watchDocker(p)
		})
	} else {
		go m.watchDocker(p)
	}

	// Post-launch liveness probe (same contract as startNative). A
	// container can fail very fast — bad entrypoint, missing image
	// layer, OOM at boot, port already mapped — and `docker run -d`
	// will still report success because the container WAS created
	// even if it exited immediately. Probe via `docker inspect` so
	// the create call surfaces the real outcome along with the last
	// 4KB of container logs.
	time.Sleep(500 * time.Millisecond)
	if !dockerContainerRunning(cname) {
		// Pull whatever was logged before the container died. `docker
		// logs` is the canonical source here — our own log file only
		// captures runtime output for native apps, not docker.
		logsCmd := exec.Command("docker", "logs", "--tail", "50", cname)
		var logsOut bytes.Buffer
		logsCmd.Stdout = &logsOut
		logsCmd.Stderr = &logsOut
		_ = logsCmd.Run()
		tail := strings.TrimSpace(logsOut.String())
		if tail == "" {
			tail = "(no container output — check build log)"
		}
		p.dockerID = ""
		return fmt.Errorf("apps: %s: docker container exited within 500ms of start. Last log:\n%s",
			p.name, tail)
	}
	return nil
}

// dockerContainerRunning reports whether the named container is still
// alive on the host. Best-effort: a docker CLI failure (daemon down,
// docker not installed) returns false, which is the safe answer for
// the liveness probe — better to surface a "container died" error
// pointing the operator at the docker daemon than to pretend the
// container is up.
func dockerContainerRunning(name string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), dockerProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "inspect", "-f", "{{.State.Running}}", name)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return false
	}
	return strings.TrimSpace(out.String()) == "true"
}

// cleanupOrphanContainers stops + removes any `uwas-app-<name>`
// container on the host whose name doesn't correspond to a currently
// registered standalone app. The failure mode this addresses: uwas is
// killed hard (OOM, sigkill, host reboot) while a docker app is
// running, then on restart the operator deletes that app from
// dashboard. The container survives because nothing called `docker
// stop` for it — it's still bound to the host port and the next
// `docker run --name uwas-app-<name>` fails with "container name
// already in use".
//
// Called by LoadAll after the in-memory app set is populated.
// Best-effort: docker CLI failures are logged and ignored.
func (m *Manager) cleanupOrphanContainers() {
	ctx, cancel := context.WithTimeout(context.Background(), dockerProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "ps", "-a", "--filter", "name=uwas-app-",
		"--format", "{{.Names}}")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		// Docker not installed, daemon down, or probe timed out — fine,
		// nothing to do. Orphan sweep is opportunistic; the next LoadAll
		// will retry.
		return
	}

	known := map[string]struct{}{}
	m.mu.RLock()
	for name := range m.procs {
		known[containerName(name)] = struct{}{}
	}
	m.mu.RUnlock()

	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		cname := strings.TrimSpace(line)
		if cname == "" || !strings.HasPrefix(cname, "uwas-app-") {
			continue
		}
		if _, isOurs := known[cname]; isOurs {
			continue
		}
		if m.logger != nil {
			m.logger.Warn("apps: removing orphan docker container",
				"container", cname,
				"reason", "no corresponding app definition in /etc/uwas/apps.d/")
		}
		// -f to force-stop a running orphan; -v to drop its anonymous volumes.
		rmCtx, rmCancel := context.WithTimeout(context.Background(), dockerProbeTimeout)
		_ = exec.CommandContext(rmCtx, "docker", "rm", "-f", "-v", cname).Run()
		rmCancel()
	}
}

// buildImage runs `docker buildx build` against the configured context,
// tagging the output as `image`. BuildKit is the path going forward —
// `docker build` (the legacy builder) is being phased out upstream and
// docker desktop / engine 23+ defaults to buildx anyway.
//
// Writes stdout/stderr to <workdir>/../logs/<name>-build.log so the
// operator can inspect a failed build instead of staring at a generic
// "build failed" toast in the dashboard.
func (m *Manager) buildImage(p *process, image string) error {
	bld := p.app.Docker.Build
	ctxDir := bld.Context
	// Relative contexts are anchored to the app's workdir so an
	// operator writing `context: .` gets the obvious thing.
	if !filepath.IsAbs(ctxDir) {
		ctxDir = filepath.Join(p.workDir, ctxDir)
	}

	args := []string{"buildx", "build", "--tag", image, "--load"}
	if bld.Dockerfile != "" {
		args = append(args, "--file", filepath.Join(ctxDir, bld.Dockerfile))
	}
	for k, v := range bld.Args {
		args = append(args, "--build-arg", fmt.Sprintf("%s=%s", k, v))
	}
	if bld.Target != "" {
		args = append(args, "--target", bld.Target)
	}
	args = append(args, ctxDir)

	logDir := filepath.Join(filepath.Dir(p.workDir), "logs")
	_ = osMkdirAllFn(logDir, 0755)
	logPath := filepath.Join(logDir, p.name+"-build.log")
	logFile, _ := osOpenFileFn(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if logFile != nil {
		defer logFile.Close()
		fmt.Fprintf(logFile, "\n=== %s buildx build %s ===\n", time.Now().Format(time.RFC3339), image)
	}

	cmd := execCommandFn("docker", args...)
	cmd.Dir = ctxDir
	if logFile != nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}

	if m.logger != nil {
		m.logger.Info("apps: docker build starting",
			"app", p.name, "image", image, "context", ctxDir, "log", logPath)
	}

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("apps: %s: docker build failed (see %s): %w", p.name, logPath, err)
	}

	if m.logger != nil {
		m.logger.Info("apps: docker build complete", "app", p.name, "image", image)
	}
	return nil
}

// watchDocker blocks on `docker wait <id>` and triggers auto-restart
// when the container exits, unless stopCh has been signaled. Mirrors
// the native monitor goroutine's contract.
func (m *Manager) watchDocker(p *process) {
	defer func() {
		if r := recover(); r != nil && m.logger != nil {
			m.logger.Error("apps: docker watcher panic", "app", p.name, "panic", r)
		}
	}()

	if p.dockerID == "" {
		return
	}

	// Snapshot the stopCh we're observing. Stop swaps in a fresh
	// channel; without this snapshot we'd race against that swap.
	stopCh := p.stopCh
	id := p.dockerID

	waitCmd := exec.Command("docker", "wait", id)
	var stderr bytes.Buffer
	waitCmd.Stderr = &stderr
	waitErr := waitCmd.Run()

	// `docker wait` returns immediately after the container exits, or
	// errors out if the container is unknown (e.g. we --rm'd it via
	// our own stop path). Treat both as "container is no longer
	// running" and let stopCh / autoRestart decide next steps.

	select {
	case <-stopCh:
		// Graceful stop — don't restart.
		p.dockerID = ""
		return
	default:
	}

	p.dockerID = ""

	if waitErr != nil && m.logger != nil {
		m.logger.Warn("apps: docker container exited",
			"app", p.name, "error", waitErr, "stderr", strings.TrimSpace(stderr.String()))
	} else if m.logger != nil {
		m.logger.Info("apps: docker container exited", "app", p.name)
	}

	if !p.autoRestart || p.crashloopGave {
		return
	}

	// Same crashloop logic as monitorNative. Docker containers that
	// exit immediately (bad image, missing volume) would otherwise
	// `docker run` 30 times a minute and trash the daemon.
	now := time.Now()
	uptime := now.Sub(p.startedAt)
	if uptime < crashloopHealthyWindow {
		p.restartCount++
		p.lastCrashAt = now
	} else {
		p.restartCount = 0
	}

	if p.restartCount >= crashloopMaxRestarts {
		p.crashloopGave = true
		if m.logger != nil {
			m.logger.Error("apps: giving up docker auto-restart after crashloop",
				"app", p.name, "consecutive_crashes", p.restartCount,
				"hint", "check the build log and the image's entrypoint, then click Start")
		}
		return
	}

	delay := computeBackoff(p.restartCount)
	if m.logger != nil && p.restartCount > 1 {
		m.logger.Warn("apps: backing off docker auto-restart",
			"app", p.name, "consecutive_crashes", p.restartCount, "delay", delay)
	}

	backoff := time.NewTimer(delay)
	select {
	case <-stopCh:
		backoff.Stop()
		return
	case <-backoff.C:
	}
	select {
	case <-stopCh:
		return
	default:
	}
	if err := m.startDocker(p); err != nil && m.logger != nil {
		m.logger.Error("apps: docker auto-restart failed", "app", p.name, "error", err)
	}
}

// stopDockerLocked tears down the container. Caller MUST hold
// m.mu.Lock(). The --rm flag on `docker run` means the container
// removes itself on exit, so `docker stop` is sufficient — we don't
// need a follow-up `docker rm`. Best-effort: if stop fails (e.g.
// container already gone) we still clear dockerID so subsequent
// state checks report stopped.
func (m *Manager) stopDockerLocked(p *process) error {
	if p.dockerID == "" {
		return nil
	}
	cname := containerName(p.name)
	id := p.dockerID
	p.dockerID = ""

	// Use the name (stable) rather than the id (could already be
	// gone from a daemon-side gc) — both work but the name is what
	// the operator sees in `docker ps`.
	cmd := exec.Command("docker", "stop", cname)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if m.logger != nil {
			m.logger.Warn("apps: docker stop failed",
				"app", p.name, "id", id, "error", err, "stderr", strings.TrimSpace(stderr.String()))
		}
		// Don't return the error — the container may have already
		// exited and we want Stop() to be idempotent.
	} else if m.logger != nil {
		m.logger.Info("apps: docker container stopped", "app", p.name, "container", cname)
	}
	return nil
}

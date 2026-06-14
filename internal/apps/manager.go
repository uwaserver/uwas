package apps

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/uwaserver/uwas/internal/logger"
)

// Testable hooks — overridden in tests.
var (
	execCommandFn = exec.Command
	osMkdirAllFn  = os.MkdirAll
	osOpenFileFn  = os.OpenFile
	osStatFn      = os.Stat
)

// isPortFreeFn checks whether 127.0.0.1:port can be bound. Replaceable
// in tests.
var isPortFreeFn = func(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// process is the in-memory runtime state for one supervised app. It is
// distinct from the App definition so the on-disk YAML stays a pure
// declarative artifact — the process struct owns transient state
// (PID, startedAt, stopCh) that should never round-trip to disk.
type process struct {
	name        string
	app         *App // immutable snapshot at register time
	runtimeKind Runtime
	command     string // resolved command for native runtimes
	port        int    // resolved listen port
	workDir     string
	env         map[string]string
	autoRestart bool

	cmd       *exec.Cmd // native runtimes
	dockerID  string    // docker container id (short, returned by `docker run -d`)
	startedAt time.Time
	stopCh    chan struct{}

	// Crashloop tracking: exponential backoff escalates with each
	// quick-succession crash so a permanently broken app doesn't
	// hammer the host at 2s intervals forever. The window resets
	// when an app stays up long enough to be considered "healthy"
	// (see crashloopHealthyWindow below).
	restartCount  int
	lastCrashAt   time.Time
	crashloopGave bool // true after we give up auto-restart entirely
}

// crashloopHealthyWindow is the uptime threshold beyond which a
// previously-crashing app is considered recovered. If a process stays
// up longer than this between exits, restartCount resets to 0 and the
// 2-second base backoff comes back.
const crashloopHealthyWindow = 30 * time.Second

// crashloopMaxRestarts is the consecutive-crash count after which the
// supervisor gives up auto-restart and leaves the app stopped. A perma-
// broken config (typo in command, missing dependency, port forever in
// use) would otherwise restart-spam the box.
const crashloopMaxRestarts = 10

// crashloopMaxBackoff caps the exponential backoff so an app that
// briefly crashed many times doesn't get stuck with hours of waiting
// once the underlying problem is fixed.
const crashloopMaxBackoff = 5 * time.Minute

// computeBackoff returns the next restart delay using exponential
// growth (2s, 4s, 8s, 16s, ...) capped at crashloopMaxBackoff. Caller
// owns the bookkeeping that increments restartCount.
func computeBackoff(restartCount int) time.Duration {
	if restartCount <= 0 {
		return 2 * time.Second
	}
	d := 2 * time.Second
	for i := 0; i < restartCount-1 && d < crashloopMaxBackoff; i++ {
		d *= 2
	}
	if d > crashloopMaxBackoff {
		d = crashloopMaxBackoff
	}
	return d
}

// Instance is the externally-visible runtime view of an app, returned
// to API callers. It blends the App definition with live process
// state.
type Instance struct {
	Name      string            `json:"name"`
	Runtime   Runtime           `json:"runtime"`
	Command   string            `json:"command,omitempty"`
	Port      int               `json:"port"`
	WorkDir   string            `json:"work_dir,omitempty"`
	PID       int               `json:"pid,omitempty"`
	Running   bool              `json:"running"`
	Disabled  bool              `json:"disabled,omitempty"`
	Uptime    string            `json:"uptime,omitempty"`
	StartedAt *time.Time        `json:"started_at,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	// DockerImage is set when Runtime == RuntimeDocker; reported back
	// so the dashboard can show what's actually running.
	DockerImage string `json:"docker_image,omitempty"`

	// CrashloopGaveUp is true when the supervisor stopped attempting
	// auto-restarts because the app exited too many times in quick
	// succession. The dashboard renders this distinctly from a
	// user-initiated Stop — "supervisor backed off, fix it and click
	// Start" vs "you stopped this, click Start to restore".
	CrashloopGaveUp bool `json:"crashloop_gave_up,omitempty"`

	// RestartCount is the consecutive-crash counter; non-zero means
	// the app has been crash-looping recently even if it's now back
	// up. Lets the dashboard show "unstable" warnings.
	RestartCount int `json:"restart_count,omitempty"`
}

// State enumerates the lifecycle phases of an app, used so a reverse
// proxy can render a meaningful error instead of a generic 502 when
// the upstream is misconfigured.
type State int

const (
	// StateNotRegistered — no app file exists with this name.
	StateNotRegistered State = iota
	// StateStopped — the app is registered but not running (crashed,
	// manually stopped, disabled, or never started).
	StateStopped
	// StateRunning — process is up; ListenAddr is valid.
	StateRunning
)

// Manager supervises one or more standalone apps, persisting their
// definitions through a Store. It is the v0.6.0 successor to the
// domain-keyed appmanager.Manager.
//
// Lifecycle:
//
//	m := apps.NewManager(store, log)
//	m.LoadAll()        // reads /etc/uwas/apps.d, registers everything
//	m.StartAll()       // launches enabled apps
//	... operator API calls Register/Unregister/Start/Stop ...
//	m.StopAll()        // shutdown
type Manager struct {
	mu       sync.RWMutex
	store    *Store
	procs    map[string]*process // name → in-memory state
	nextPort int
	logger   *logger.Logger
}

// NewManager constructs a manager backed by the given store. Pass a
// nil store to use one rooted at DefaultDir.
func NewManager(store *Store, log *logger.Logger) *Manager {
	if store == nil {
		store = NewStore("")
	}
	return &Manager{
		store:    store,
		procs:    make(map[string]*process),
		nextPort: 3001, // above common dev ports
		logger:   log,
	}
}

// Store returns the underlying persistence layer so callers (e.g. API
// handlers) can List/Get/Save without going through the supervisor for
// pure CRUD that doesn't need lifecycle effects.
func (m *Manager) Store() *Store { return m.store }

// LoadAll reads every app file from the store and registers them in
// memory. Already-registered apps are refreshed (re-pointed at the new
// on-disk snapshot) but NOT auto-restarted — callers that want to
// pick up command/port edits on a running app must call Restart
// explicitly after Save.
//
// Returned slice contains any per-file load errors (corrupt YAML,
// invalid schema, name mismatch). A non-nil error in the third return
// is a fatal-to-the-call problem (e.g. apps.d unreadable).
func (m *Manager) LoadAll() ([]*App, []error, error) {
	apps, skipErrs, err := m.store.Load()
	if err != nil {
		return nil, skipErrs, err
	}

	m.mu.Lock()
	for _, a := range apps {
		if _, exists := m.procs[a.Name]; exists {
			// Existing process: refresh the immutable snapshot only.
			// Port/command/etc. of a RUNNING app are not hot-reloaded
			// because doing so would silently desync the in-memory
			// listen port from the actual process. Operator must
			// Restart to pick up.
			m.procs[a.Name].app = a
			continue
		}
		m.registerLocked(a)
	}
	m.mu.Unlock()

	// Orphan-container sweep runs AFTER the lock is released:
	// cleanupOrphanContainers reacquires m.mu.RLock() to enumerate
	// names, and docker CLI calls can stall for seconds. Holding the
	// write lock through that would block every API reader.
	m.cleanupOrphanContainers()

	return apps, skipErrs, nil
}

// Register adds or replaces an app in the manager and persists it.
// Stops the existing process if any (operator must explicitly Start
// after Register completes — semantics match systemctl enable, not
// start).
func (m *Manager) Register(a *App) error {
	if a == nil {
		return fmt.Errorf("apps: nil app")
	}
	if err := a.Validate(); err != nil {
		return err
	}

	// Persist first so a crash mid-register doesn't leave an in-memory
	// app with no on-disk record.
	if err := m.store.Save(a); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if existing, ok := m.procs[a.Name]; ok {
		// Stop the old process before swapping. Use the package's stop
		// machinery so monitor goroutine winds down cleanly.
		m.stopLocked(existing)
		delete(m.procs, a.Name)
	}
	m.registerLocked(a)
	return nil
}

// registerLocked is the in-memory half of Register. Caller MUST hold
// m.mu.Lock().
func (m *Manager) registerLocked(a *App) {
	port := a.Port

	// Auto-assign if unset, or if the requested port collides with
	// another managed app. Host-level conflicts are checked again at
	// Start time so stopped apps recover from orphaned children or
	// external processes that grabbed their old port.
	if port > 0 {
		for _, other := range m.procs {
			if other.name != a.Name && other.port == port {
				if m.logger != nil {
					m.logger.Warn("apps: requested port already used by another managed app, auto-assigning",
						"app", a.Name, "requested", port, "conflict", other.name)
				}
				port = 0
				break
			}
		}
	}
	if port == 0 {
		port = m.allocateFreePortLocked()
		a.Port = port
		// Persist the resolved port so the next boot reuses it instead
		// of re-allocating. Best-effort — failure to write back just
		// means the next start picks fresh.
		_ = m.store.Save(a)
	}

	cmd := a.Command
	if a.Runtime != RuntimeDocker && cmd == "" {
		cmd = detectCommand(string(a.Runtime), a.WorkDir)
	}

	// PM2-style default: auto-restart unless explicitly disabled.
	autoRestart := a.AutoRestart || !a.Disabled

	m.procs[a.Name] = &process{
		name:        a.Name,
		app:         a,
		runtimeKind: a.Runtime,
		command:     cmd,
		port:        port,
		workDir:     a.WorkDir,
		env:         a.Env,
		autoRestart: autoRestart,
		stopCh:      make(chan struct{}),
	}

	if m.logger != nil {
		m.logger.Info("apps: registered",
			"app", a.Name, "runtime", a.Runtime, "port", port, "command", cmd, "workdir", a.WorkDir)
	}
}

// allocateFreePortLocked walks forward from nextPort, skipping
// anything already claimed by another managed app or bound on
// 127.0.0.1. Caller MUST hold m.mu.Lock().
func (m *Manager) allocateFreePortLocked() int {
	const maxAttempts = 1000
	port := m.nextPort
	for i := 0; i < maxAttempts; i++ {
		taken := false
		for _, other := range m.procs {
			if other.port == port {
				taken = true
				break
			}
		}
		if !taken && isPortFreeFn(port) {
			m.nextPort = port + 1
			return port
		}
		port++
	}
	m.nextPort = port + 1
	return port
}

// Unregister stops the app and removes its YAML file. Idempotent.
func (m *Manager) Unregister(name string) error {
	m.mu.Lock()
	if p, ok := m.procs[name]; ok {
		m.stopLocked(p)
		delete(m.procs, name)
	}
	m.mu.Unlock()
	return m.store.Delete(name)
}

// Start launches the app's process / container. Returns an error if
// the app isn't registered, is disabled, or fails to spawn.
//
// An explicit Start (operator action) clears the crashloop bookkeeping
// — the operator has presumably fixed whatever caused the previous
// failures, so the supervisor should give the app a fresh budget of
// retry slots instead of inheriting the "we gave up" state.
func (m *Manager) Start(name string) error {
	m.mu.Lock()
	p, ok := m.procs[name]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("apps: %s not registered", name)
	}
	if p.app != nil && p.app.Disabled {
		m.mu.Unlock()
		return fmt.Errorf("apps: %s is disabled — clear the disabled flag first", name)
	}
	if m.isRunning(p) {
		m.mu.Unlock()
		return fmt.Errorf("apps: %s already running", name)
	}

	// Reset crashloop state — explicit Start means "operator has
	// addressed the problem". If the app still crashes, the counter
	// will climb again from zero.
	p.restartCount = 0
	p.crashloopGave = false
	m.mu.Unlock()

	if p.runtimeKind != RuntimeCustom {
		if err := m.ensureStartPortAvailable(p); err != nil {
			return err
		}
	}

	if p.runtimeKind == RuntimeDocker {
		return m.startDocker(p)
	}
	return m.startNative(p)
}

// Stop terminates the app's process. Closes stopCh so the monitor
// goroutine doesn't auto-restart.
func (m *Manager) Stop(name string) error {
	m.mu.Lock()
	p, ok := m.procs[name]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("apps: %s not registered", name)
	}
	return m.stop(p)
}

// stop is the public-facing wrapper around stopLocked that grabs the
// lock for itself.
func (m *Manager) stop(p *process) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stopLocked(p)
}

// stopLocked is the actual kill path. Caller MUST hold m.mu.Lock().
// Returns nil if the process is already down.
//
// Native runtimes get a SIGTERM → 3-second grace → SIGKILL ladder so
// node/python/ruby/go apps with shutdown handlers (DB flush, queue
// drain, in-flight request completion) get a chance to clean up
// before the kernel takes them out. Pre-hardening we went straight
// to SIGKILL on every Stop, which was fine for hello-world demos
// but lost data for real workloads.
//
// Docker containers stay on `docker stop` which itself does
// SIGTERM-then-SIGKILL with the container's configured stop timeout.
func (m *Manager) stopLocked(p *process) error {
	// Signal monitor to skip auto-restart, then reset stopCh for the
	// next Start.
	select {
	case <-p.stopCh:
		// already closed
	default:
		close(p.stopCh)
	}
	p.stopCh = make(chan struct{})

	if p.runtimeKind == RuntimeDocker {
		return m.stopDockerLocked(p)
	}
	if p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	if err := gracefulKill(p.cmd, p.name); err != nil {
		return fmt.Errorf("apps: kill %s: %w", p.name, err)
	}
	p.cmd = nil
	if m.logger != nil {
		m.logger.Info("apps: stopped", "app", p.name)
	}
	return nil
}

func (m *Manager) ensureStartPortAvailable(p *process) error {
	if p.port <= 0 {
		return nil
	}
	if isPortFreeFn(p.port) {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Re-check under the write lock in case a concurrent start/stop
	// changed the world while we were waiting.
	if isPortFreeFn(p.port) {
		return nil
	}

	oldPort := p.port
	newPort := m.allocateFreePortLocked()
	p.port = newPort
	if p.app != nil {
		p.app.Port = newPort
		if err := m.store.Save(p.app); err != nil {
			p.port = oldPort
			p.app.Port = oldPort
			return fmt.Errorf("apps: %s: port %d is already in use and saving replacement port %d failed: %w",
				p.name, oldPort, newPort, err)
		}
	}

	if m.logger != nil {
		m.logger.Warn("apps: port already in use, auto-assigned replacement",
			"app", p.name, "old_port", oldPort, "new_port", newPort)
	}
	return nil
}

// Restart is Stop + brief settle + Start. The settle interval matches
// the legacy appmanager so behavior stays familiar.
func (m *Manager) Restart(name string) error {
	_ = m.Stop(name)
	time.Sleep(500 * time.Millisecond)
	return m.Start(name)
}

// StartAll launches every non-disabled registered app. Called once on
// daemon boot after LoadAll. Errors are logged but don't abort the
// loop — one bad app shouldn't keep the others down.
func (m *Manager) StartAll() {
	m.mu.RLock()
	names := make([]string, 0, len(m.procs))
	for n, p := range m.procs {
		if p.app != nil && p.app.Disabled {
			continue
		}
		if m.isRunning(p) {
			continue
		}
		names = append(names, n)
	}
	m.mu.RUnlock()

	for _, n := range names {
		if err := m.Start(n); err != nil && m.logger != nil {
			m.logger.Error("apps: start failed", "app", n, "error", err)
		}
	}
}

// StopAll stops every running app. Best-effort.
func (m *Manager) StopAll() {
	m.mu.RLock()
	names := make([]string, 0, len(m.procs))
	for n := range m.procs {
		names = append(names, n)
	}
	m.mu.RUnlock()

	for _, n := range names {
		_ = m.Stop(n)
	}
}

// Instances returns a snapshot of every registered app's runtime view.
func (m *Manager) Instances() []Instance {
	m.mu.RLock()
	procs := make([]*process, 0, len(m.procs))
	for _, p := range m.procs {
		procs = append(procs, p)
	}
	m.mu.RUnlock()

	out := make([]Instance, 0, len(procs))
	for _, p := range procs {
		out = append(out, m.instanceFromProcess(p))
	}
	return out
}

// Get returns the runtime view of a single app, or nil if unregistered.
func (m *Manager) Get(name string) *Instance {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.procs[name]
	if !ok {
		return nil
	}
	i := m.instanceFromProcess(p)
	return &i
}

func (m *Manager) instanceFromProcess(p *process) Instance {
	inst := Instance{
		Name:            p.name,
		Runtime:         p.runtimeKind,
		Command:         p.command,
		Port:            p.port,
		WorkDir:         p.workDir,
		Env:             p.env,
		CrashloopGaveUp: p.crashloopGave,
		RestartCount:    p.restartCount,
	}
	if p.app != nil {
		inst.Disabled = p.app.Disabled
		if p.runtimeKind == RuntimeDocker {
			inst.DockerImage = p.app.Docker.Image
		}
	}
	if p.runtimeKind == RuntimeDocker {
		if p.dockerID != "" {
			inst.Running = true
			t := p.startedAt
			inst.StartedAt = &t
			inst.Uptime = time.Since(p.startedAt).Truncate(time.Second).String()
		}
		return inst
	}
	if p.cmd != nil && p.cmd.Process != nil {
		inst.Running = true
		inst.PID = p.cmd.Process.Pid
		t := p.startedAt
		inst.StartedAt = &t
		inst.Uptime = time.Since(p.startedAt).Truncate(time.Second).String()
	}
	return inst
}

// isRunning reports whether the process is live. Caller must hold m.mu.
func (m *Manager) isRunning(p *process) bool {
	if p.runtimeKind == RuntimeDocker {
		return p.dockerID != ""
	}
	return p.cmd != nil && p.cmd.Process != nil
}

// ListenAddr returns the 127.0.0.1:port a domain's reverse proxy
// should target when forwarding to the named app. Empty when the app
// is unregistered OR registered-but-stopped — the proxy uses the
// empty return as its "tell the user the app is down" signal.
func (m *Manager) ListenAddr(name string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.procs[name]
	if !ok {
		return ""
	}
	if !m.isRunning(p) {
		return ""
	}
	return fmt.Sprintf("127.0.0.1:%d", p.port)
}

// Stats describes resource usage for one supervised app. Fields are
// zero when the app is stopped, when the platform doesn't expose the
// data (CPU% on non-Linux), or when reading procfs fails.
type Stats struct {
	Name      string  `json:"name"`
	PID       int     `json:"pid,omitempty"`
	Running   bool    `json:"running"`
	CPUPct    float64 `json:"cpu_percent"`
	MemoryRSS int64   `json:"memory_rss"` // bytes resident
	MemoryVMS int64   `json:"memory_vms"` // bytes virtual
	Uptime    string  `json:"uptime,omitempty"`
}

// Stats returns the runtime resource usage for the named app. Native
// runtimes get CPU%/RSS from /proc on Linux; docker runtimes get the
// same from `docker stats --no-stream`. On unsupported platforms or
// missing data, fields are zero — the caller handles "no signal"
// gracefully rather than getting an error.
func (m *Manager) Stats(name string) *Stats {
	m.mu.RLock()
	p, ok := m.procs[name]
	if !ok {
		m.mu.RUnlock()
		return nil
	}

	s := &Stats{Name: name}
	if !m.isRunning(p) {
		m.mu.RUnlock()
		return s
	}
	s.Running = true
	s.Uptime = time.Since(p.startedAt).Truncate(time.Second).String()

	if p.runtimeKind == RuntimeDocker {
		dockerID := p.dockerID
		m.mu.RUnlock()
		s.CPUPct, s.MemoryRSS = readDockerStats(dockerID)
		return s
	}

	pid := 0
	if p.cmd != nil && p.cmd.Process != nil {
		pid = p.cmd.Process.Pid
	}
	m.mu.RUnlock()
	if pid != 0 {
		s.PID = pid
		s.CPUPct, s.MemoryRSS, s.MemoryVMS = readProcStats(pid)
	}
	return s
}

// WaitListening blocks until 127.0.0.1:port accepts a TCP connection
// for the named app, OR the timeout elapses, OR the process exits.
// Returns nil only when the connect succeeds — the post-start health
// signal the deploy flow uses to upgrade "started" to "actually
// reachable".
//
// Skipped for the `custom` runtime: operator-defined workloads
// (cron-like batch jobs, queue workers, anything not serving HTTP)
// have no obligation to listen on a port and would always fail this
// probe. Custom runtimes are the operator's explicit "I know what
// I'm doing" escape hatch.
func (m *Manager) WaitListening(name string, timeout time.Duration) error {
	m.mu.RLock()
	p, ok := m.procs[name]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("apps: %s not registered", name)
	}
	if p.runtimeKind == RuntimeCustom {
		// Don't gate "deploy ok" on a port for custom runtimes.
		return nil
	}
	if p.port <= 0 {
		return fmt.Errorf("apps: %s has no port assigned", name)
	}

	addr := fmt.Sprintf("127.0.0.1:%d", p.port)
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		// If the process died while we were waiting, bail with a
		// useful error rather than burning the full timeout. The
		// monitor goroutine sets p.cmd = nil (native) or p.dockerID
		// = "" (docker) on exit.
		if !m.isRunning(p) {
			tail := ""
			if p.runtimeKind != RuntimeDocker {
				logPath := filepath.Join(filepath.Dir(p.workDir), "logs", p.name+".log")
				tail = tailLogFile(logPath, 2048)
			}
			if tail == "" {
				tail = "(no log output captured)"
			}
			return fmt.Errorf("apps: %s: process exited before binding to port %d. Log tail:\n%s",
				p.name, p.port, tail)
		}

		conn, err := net.DialTimeout("tcp", addr, 250*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	return fmt.Errorf("apps: %s: still not listening on %s after %s — app may be binding to a different interface (use 127.0.0.1 or 0.0.0.0), or startup is slower than the probe window",
		name, addr, timeout)
}

// State reports the lifecycle phase. Mirrors appmanager.State to keep
// the proxy diagnostic code uniform across both managers during the
// deprecation window.
func (m *Manager) State(name string) State {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.procs[name]
	if !ok {
		return StateNotRegistered
	}
	if !m.isRunning(p) {
		return StateStopped
	}
	return StateRunning
}

// startNative spawns a native runtime via /bin/sh -c (or cmd /C on
// Windows). Mirrors the appmanager flow: ${PORT} substitution, PORT/
// HOST/NODE_ENV env, log file under <workdir>/../logs/<name>.log,
// monitor goroutine for auto-restart, plus a 500ms post-launch
// liveness probe that surfaces immediate crashes synchronously with
// the last 4KB of log output. Without that probe an app that exits
// during boot (missing dependency, port collision inside the process,
// thrown unhandled exception) is reported as "started ok" and only
// shows up later via polling — the deploy UX described as "zero
// errors" depends on the create call seeing the real outcome.
func (m *Manager) startNative(p *process) error {
	if p.command == "" {
		return fmt.Errorf("apps: %s: no start command set for runtime %q and nothing recognizable in workdir %s — %s",
			p.name, p.runtimeKind, p.workDir, detectHint(string(p.runtimeKind)))
	}
	cmdStr := strings.ReplaceAll(p.command, "${PORT}", fmt.Sprintf("%d", p.port))
	if err := validateShellCommand(cmdStr); err != nil {
		return fmt.Errorf("apps: %s: invalid command: %w", p.name, err)
	}

	// Ensure workdir exists — auto-assigned paths under DataRoot may
	// not have been created yet when an operator registers an app via
	// API before pushing source.
	_ = osMkdirAllFn(p.workDir, 0755)

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = execCommandFn("cmd", "/C", cmdStr)
	} else {
		cmd = execCommandFn("sh", "-c", cmdStr)
	}
	configureProcessGroup(cmd)
	cmd.Dir = p.workDir

	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env,
		fmt.Sprintf("PORT=%d", p.port),
		"HOST=0.0.0.0",
		"NODE_ENV=production",
	)
	for k, v := range p.env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	logDir := filepath.Join(filepath.Dir(p.workDir), "logs")
	_ = osMkdirAllFn(logDir, 0755)
	logFile, _ := osOpenFileFn(filepath.Join(logDir, p.name+".log"),
		os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if logFile != nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}

	if err := cmd.Start(); err != nil {
		if logFile != nil {
			_ = logFile.Close()
		}
		return fmt.Errorf("apps: %s: start: %w", p.name, err)
	}

	m.mu.Lock()
	p.cmd = cmd
	p.startedAt = time.Now()
	stopCh := p.stopCh
	m.mu.Unlock()

	if m.logger != nil {
		m.logger.Info("apps: started", "app", p.name, "pid", cmd.Process.Pid, "port", p.port)
	}

	// Snapshot the cmd pointer for the post-launch liveness probe
	// below. The monitor goroutine sets p.cmd = nil when the process
	// exits, so a mismatched pointer after the wait window tells us
	// the process died — without us having to compete with the monitor
	// for cmd.Wait().
	origCmd := cmd
	logPath := ""
	if logFile != nil {
		logPath = logFile.Name()
	}

	if m.logger != nil {
		m.logger.SafeGo("apps.monitor."+p.name, func() {
			m.monitorNative(p, origCmd, logFile, stopCh)
		})
	} else {
		go m.monitorNative(p, origCmd, logFile, stopCh)
	}

	// 500ms post-launch probe. Long enough for the shell to exec the
	// real binary and for trivial startup errors to manifest; short
	// enough that a healthy app's start path doesn't feel laggy. If
	// the original cmd pointer is gone, the monitor observed an early
	// exit — surface it as a failed start with the log tail attached.
	time.Sleep(500 * time.Millisecond)
	m.mu.RLock()
	exited := p.cmd != origCmd
	m.mu.RUnlock()
	if exited {
		tail := tailLogFile(logPath, 4096)
		if tail == "" {
			tail = "(no log output)"
		}
		return fmt.Errorf("apps: %s: process exited within 500ms of start — startup error. Log tail:\n%s",
			p.name, tail)
	}
	return nil
}

// tailLogFile reads up to lastN bytes from the END of a log file.
// Used by the post-launch liveness probe to attach a concrete reason
// to "started but immediately died" errors. Returns empty string on
// any error so callers can fall back to a generic message.
func tailLogFile(path string, lastN int) string {
	if path == "" || lastN <= 0 {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if len(data) > lastN {
		data = data[len(data)-lastN:]
	}
	return strings.TrimSpace(string(data))
}

// monitorNative waits on the process and triggers auto-restart on
// crash unless stopCh fires first.
func (m *Manager) monitorNative(p *process, cmd *exec.Cmd, logFile *os.File, stopCh <-chan struct{}) {
	defer func() {
		if r := recover(); r != nil && m.logger != nil {
			m.logger.Error("apps: monitor panic", "app", p.name, "panic", r)
		}
	}()

	if cmd == nil {
		return
	}
	waitErr := cmd.Wait()
	if logFile != nil {
		_ = logFile.Close()
	}

	select {
	case <-stopCh:
		m.mu.Lock()
		if p.cmd == cmd {
			p.cmd = nil
		}
		m.mu.Unlock()
		return
	default:
	}

	m.mu.Lock()
	if p.cmd == cmd {
		p.cmd = nil
	}
	autoRestart := p.autoRestart
	crashloopGave := p.crashloopGave

	if waitErr != nil && m.logger != nil {
		m.logger.Warn("apps: process exited", "app", p.name, "error", waitErr)
	}

	if !autoRestart || crashloopGave {
		m.mu.Unlock()
		return
	}

	// Crashloop bookkeeping: an exit within crashloopHealthyWindow of
	// the previous start means we're crash-looping; escalate the
	// backoff and increment the counter. A longer uptime means the
	// app recovered, so reset.
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
		restartCount := p.restartCount
		m.mu.Unlock()
		if m.logger != nil {
			m.logger.Error("apps: giving up auto-restart after crashloop",
				"app", p.name, "consecutive_crashes", restartCount,
				"hint", "fix the underlying issue and click Start in the dashboard")
		}
		return
	}

	delay := computeBackoff(p.restartCount)
	restartCount := p.restartCount
	m.mu.Unlock()
	if m.logger != nil && restartCount > 1 {
		m.logger.Warn("apps: backing off auto-restart",
			"app", p.name, "consecutive_crashes", restartCount, "delay", delay)
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
	if err := m.startNative(p); err != nil && m.logger != nil {
		m.logger.Error("apps: auto-restart failed", "app", p.name, "error", err)
	}
}

// detectHint returns a human-readable description of what files
// detectCommand would have accepted for the given runtime. Used in
// error messages so an operator who creates a node app on an empty
// workdir sees "expected one of: server.js, index.js, app.js, or
// package.json" instead of a generic "no command" error.
func detectHint(runtimeName string) string {
	switch runtimeName {
	case "node":
		return "expected one of: server.js, index.js, app.js, or package.json in workdir"
	case "python":
		return "expected one of: manage.py, app.py, main.py, wsgi.py, or requirements.txt in workdir"
	case "ruby":
		return "expected config.ru in workdir (Rack app)"
	case "go":
		return "expected ./main binary in workdir"
	case "custom":
		return "set the command field explicitly — custom runtime has no auto-detection"
	default:
		return "set the command field explicitly, or set runtime to node/python/ruby/go for auto-detection"
	}
}

// detectCommand mirrors appmanager.detectCommand — same heuristics so
// migrating from the legacy supervisor doesn't change behavior. node
// prefers `node <entry>` over `npm start` (PORT propagation reliability).
func detectCommand(runtimeName, workDir string) string {
	switch runtimeName {
	case "node":
		if _, err := osStatFn(filepath.Join(workDir, "package.json")); err == nil {
			if cmd := detectNodePackageCommand(workDir); cmd != "" {
				return cmd
			}
		}
		for _, f := range []string{"server.js", "index.js", "app.js"} {
			if _, err := osStatFn(filepath.Join(workDir, f)); err == nil {
				return "node " + f
			}
		}
	case "python":
		if _, err := osStatFn(filepath.Join(workDir, "manage.py")); err == nil {
			return "python manage.py runserver 0.0.0.0:${PORT}"
		}
		for _, f := range []string{"app.py", "main.py", "wsgi.py"} {
			if _, err := osStatFn(filepath.Join(workDir, f)); err == nil {
				return "python " + f
			}
		}
		if _, err := osStatFn(filepath.Join(workDir, "requirements.txt")); err == nil {
			return "gunicorn app:app -b 0.0.0.0:${PORT}"
		}
	case "ruby":
		if _, err := osStatFn(filepath.Join(workDir, "config.ru")); err == nil {
			return "bundle exec puma -p ${PORT}"
		}
	case "go":
		return "./main"
	}
	return ""
}

func detectNodePackageCommand(workDir string) string {
	data, err := os.ReadFile(filepath.Join(workDir, "package.json"))
	if err != nil {
		return ""
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return ""
	}
	if _, ok := pkg.Scripts["start"]; ok {
		return "npm start"
	}
	if _, ok := pkg.Scripts["preview"]; ok {
		return "npm run preview -- --host 0.0.0.0 --port ${PORT}"
	}
	return ""
}

// validateShellCommand rejects metacharacters that could chain commands
// or redirect i/o — the same allowlist used in the legacy appmanager.
// Note: ${PORT} expansion is done by string replace BEFORE this check,
// so the resulting cmdStr is plain characters.
func validateShellCommand(command string) error {
	if strings.ContainsAny(command, "\x00\n\r") {
		return fmt.Errorf("command contains forbidden control characters")
	}
	for _, f := range []string{"$(", "`", "|", ">", "<", ";", "&&", "||"} {
		if strings.Contains(command, f) {
			return fmt.Errorf("command contains forbidden shell metacharacter: %q", f)
		}
	}
	return nil
}

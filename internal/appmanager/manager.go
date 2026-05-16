// Package appmanager manages non-PHP application processes (Node.js, Python, Ruby, Go, etc.).
// Each domain with type "app" gets a managed subprocess that is monitored and auto-restarted on crash.
package appmanager

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/rlimit"
)

// Testable hooks — can be overridden in tests.
var (
	execCommandFn = exec.Command
	osMkdirAllFn  = os.MkdirAll
	osOpenFileFn  = os.OpenFile
	osStatFn      = os.Stat
)

// AppInstance describes a running application process.
type AppInstance struct {
	Domain    string            `json:"domain"`
	Runtime   string            `json:"runtime"` // "node", "python", "ruby", "go", "custom"
	Command   string            `json:"command"` // actual command being run
	Port      int               `json:"port"`    // port the app listens on
	PID       int               `json:"pid"`     // OS process ID (0 if not running)
	Running   bool              `json:"running"`
	Uptime    string            `json:"uptime,omitempty"` // human-readable uptime
	StartedAt *time.Time        `json:"started_at,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
}

type appProcess struct {
	domain      string
	runtime     string
	command     string
	port        int
	workDir     string
	env         map[string]string
	autoRestart bool
	cmd         *exec.Cmd
	startedAt   time.Time
	stopCh      chan struct{}
	cgroupPath  string // cgroup path for resource limits (Linux)
}

// Manager manages application processes for domains.
type Manager struct {
	mu       sync.RWMutex
	apps     map[string]*appProcess // domain → process
	nextPort int
	logger   *logger.Logger
}

// New creates a new app manager.
func New(log *logger.Logger) *Manager {
	return &Manager{
		apps:     make(map[string]*appProcess),
		nextPort: 3001, // start above common dev ports
		logger:   log,
	}
}

// detectCommand infers the start command from project files if not explicitly set.
func detectCommand(runtimeName, workDir string) string {
	switch runtimeName {
	case "node":
		// Prefer direct `node <file>` over `npm start`:
		//   1. Avoids requiring npm to be installed
		//   2. PORT and other env vars propagate cleanly (npm's run-script
		//      wrapper has historically dropped or mangled them on some
		//      versions), so the appmanager-assigned port reliably reaches
		//      the app
		for _, f := range []string{"server.js", "index.js", "app.js"} {
			if _, err := osStatFn(filepath.Join(workDir, f)); err == nil {
				return "node " + f
			}
		}
		// Last resort — only if no entry-point file was found
		if _, err := osStatFn(filepath.Join(workDir, "package.json")); err == nil {
			return "npm start"
		}
	case "python":
		// Check for common WSGI/ASGI patterns
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

// Register adds an app domain to the manager (does not start it).
func (m *Manager) Register(domain string, appCfg config.AppConfig, webRoot string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.apps[domain]; exists {
		return fmt.Errorf("domain %s already registered", domain)
	}

	rt := appCfg.Runtime
	if rt == "" {
		rt = "custom"
	}

	workDir := appCfg.WorkDir
	if workDir == "" {
		workDir = webRoot
	}

	cmd := appCfg.Command
	if cmd == "" {
		cmd = detectCommand(rt, workDir)
	}
	if cmd == "" {
		return fmt.Errorf("no command configured and could not detect for runtime %q in %s", rt, workDir)
	}

	port := appCfg.Port
	if port == 0 {
		port = m.nextPort
		m.nextPort++
	}

	// PM2-like default: auto-restart on crash unless the operator has
	// explicitly stopped the app (Disabled flag). Pre-v0.5.2 the AutoRestart
	// bool defaulted to false (Go zero value) even though the field comment
	// claimed "default true" — which meant a fresh process that crashed once
	// stayed dead and the operator had to find the Apps page to click
	// Restart. That's the opposite of what a process supervisor should do.
	autoRestart := appCfg.AutoRestart || !appCfg.Disabled

	m.apps[domain] = &appProcess{
		domain:      domain,
		runtime:     rt,
		command:     cmd,
		port:        port,
		workDir:     workDir,
		env:         appCfg.Env,
		autoRestart: autoRestart,
		stopCh:      make(chan struct{}),
	}

	if m.logger != nil {
		m.logger.Info("app registered", "domain", domain, "runtime", rt, "port", port, "command", cmd)
	}
	return nil
}

// SetCgroupPath sets the cgroup path for resource-limited process assignment.
func (m *Manager) SetCgroupPath(domain, path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if app, ok := m.apps[domain]; ok {
		app.cgroupPath = path
	}
}

// Start launches the application process for a domain.
func (m *Manager) Start(domain string) error {
	m.mu.RLock()
	app, exists := m.apps[domain]
	m.mu.RUnlock()
	if !exists {
		return fmt.Errorf("domain %s not registered", domain)
	}
	if app.cmd != nil && app.cmd.Process != nil {
		return fmt.Errorf("domain %s already running (PID %d)", domain, app.cmd.Process.Pid)
	}

	return m.startProcess(app)
}

func (m *Manager) startProcess(app *appProcess) error {
	// Expand ${PORT} in command
	cmdStr := strings.ReplaceAll(app.command, "${PORT}", fmt.Sprintf("%d", app.port))

	if err := validateShellCommand(cmdStr); err != nil {
		return fmt.Errorf("invalid app command: %w", err)
	}

	// Build exec.Cmd — use shell for complex commands
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = execCommandFn("cmd", "/C", cmdStr)
	} else {
		cmd = execCommandFn("sh", "-c", cmdStr)
	}

	cmd.Dir = app.workDir

	// Build environment
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, fmt.Sprintf("PORT=%d", app.port))
	cmd.Env = append(cmd.Env, "HOST=0.0.0.0")
	cmd.Env = append(cmd.Env, "NODE_ENV=production")
	for k, v := range app.env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	// Capture stdout/stderr to a log file
	logDir := filepath.Join(filepath.Dir(app.workDir), "logs")
	osMkdirAllFn(logDir, 0755)
	logFile, err := osOpenFileFn(filepath.Join(logDir, "app.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err == nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}

	if err := cmd.Start(); err != nil {
		if logFile != nil {
			logFile.Close()
		}
		return fmt.Errorf("failed to start %s for %s: %w", app.runtime, app.domain, err)
	}

	app.cmd = cmd
	now := time.Now()
	app.startedAt = now

	// Apply resource limits via cgroups (Linux only, best-effort)
	if app.cgroupPath != "" {
		rlimit.AssignPID(app.cgroupPath, cmd.Process.Pid)
	}

	if m.logger != nil {
		m.logger.Info("app started", "domain", app.domain, "pid", cmd.Process.Pid, "port", app.port)
	}

	// Monitor process in background — auto-restart on crash
	if m.logger != nil {
		m.logger.SafeGo("app.monitor."+app.domain, func() {
			m.monitorProcess(app, logFile)
		})
	} else {
		go m.monitorProcess(app, logFile)
	}

	return nil
}

func (m *Manager) monitorProcess(app *appProcess, logFile *os.File) {
	defer func() {
		if r := recover(); r != nil {
			if m.logger != nil {
				m.logger.Error("app monitor panic recovered", "domain", app.domain, "panic", r)
			}
		}
	}()

	if app.cmd == nil {
		return
	}
	waitErr := app.cmd.Wait()
	if logFile != nil {
		logFile.Close()
	}

	// Check if stop was requested
	select {
	case <-app.stopCh:
		// Graceful stop — don't restart
		app.cmd = nil
		return
	default:
	}

	app.cmd = nil

	if waitErr != nil && m.logger != nil {
		m.logger.Warn("app process exited", "domain", app.domain, "error", waitErr)
	}

	// Auto-restart after backoff (re-check stopCh to avoid zombie restart)
	if app.autoRestart {
		backoff := time.NewTimer(2 * time.Second)
		select {
		case <-app.stopCh:
			backoff.Stop()
			return // stopped during backoff
		case <-backoff.C:
		}
		// Final check before restart
		select {
		case <-app.stopCh:
			return
		default:
		}
		if err := m.startProcess(app); err != nil {
			if m.logger != nil {
				m.logger.Error("app auto-restart failed", "domain", app.domain, "error", err)
			}
		}
	}
}

// Stop terminates the application process for a domain.
func (m *Manager) Stop(domain string) error {
	m.mu.Lock()
	app, exists := m.apps[domain]
	if !exists {
		m.mu.Unlock()
		return fmt.Errorf("domain %s not registered", domain)
	}
	// Close stopCh to prevent auto-restart, even if cmd is nil (monitor may be in backoff)
	close(app.stopCh)
	app.stopCh = make(chan struct{}) // reset for next start

	// Snapshot cmd under lock to avoid race with monitorProcess setting cmd=nil
	cmd := app.cmd
	if cmd == nil || cmd.Process == nil {
		m.mu.Unlock()
		return fmt.Errorf("domain %s is not running", domain)
	}
	m.mu.Unlock()

	if err := cmd.Process.Kill(); err != nil {
		return fmt.Errorf("failed to stop %s: %w", domain, err)
	}

	if m.logger != nil {
		m.logger.Info("app stopped", "domain", domain)
	}
	return nil
}

// Restart stops and restarts the application.
func (m *Manager) Restart(domain string) error {
	_ = m.Stop(domain) // ignore error if not running
	time.Sleep(500 * time.Millisecond)
	return m.Start(domain)
}

// Unregister removes a domain's app and stops it if running.
func (m *Manager) Unregister(domain string) {
	_ = m.Stop(domain)
	m.mu.Lock()
	delete(m.apps, domain)
	m.mu.Unlock()
}

// StopAll stops all running app processes.
func (m *Manager) StopAll() {
	m.mu.RLock()
	domains := make([]string, 0, len(m.apps))
	for d := range m.apps {
		domains = append(domains, d)
	}
	m.mu.RUnlock()

	for _, d := range domains {
		_ = m.Stop(d)
	}
}

// Instances returns all registered app instances.
func (m *Manager) Instances() []AppInstance {
	// Take a snapshot of current apps under lock, then release lock before processing.
	m.mu.RLock()
	apps := make([]*appProcess, 0, len(m.apps))
	for _, app := range m.apps {
		apps = append(apps, app)
	}
	m.mu.RUnlock()

	result := make([]AppInstance, 0, len(apps))
	for _, app := range apps {
		inst := AppInstance{
			Domain:  app.domain,
			Runtime: app.runtime,
			Command: app.command,
			Port:    app.port,
			Env:     app.env,
		}
		if app.cmd != nil && app.cmd.Process != nil {
			inst.Running = true
			inst.PID = app.cmd.Process.Pid
			t := app.startedAt
			inst.StartedAt = &t
			uptime := time.Since(app.startedAt).Truncate(time.Second)
			inst.Uptime = uptime.String()
		}
		result = append(result, inst)
	}
	return result
}

// Get returns info for a single domain, or nil.
func (m *Manager) Get(domain string) *AppInstance {
	m.mu.RLock()
	app, exists := m.apps[domain]
	m.mu.RUnlock()
	if !exists {
		return nil
	}

	inst := &AppInstance{
		Domain:  app.domain,
		Runtime: app.runtime,
		Command: app.command,
		Port:    app.port,
		Env:     app.env,
	}
	if app.cmd != nil && app.cmd.Process != nil {
		inst.Running = true
		inst.PID = app.cmd.Process.Pid
		t := app.startedAt
		inst.StartedAt = &t
		uptime := time.Since(app.startedAt).Truncate(time.Second)
		inst.Uptime = uptime.String()
	}
	return inst
}

// AppStats holds resource usage for a running app process.
type AppStats struct {
	Domain    string  `json:"domain"`
	PID       int     `json:"pid"`
	Running   bool    `json:"running"`
	CPUPct    float64 `json:"cpu_percent"` // percentage of one core
	MemoryRSS int64   `json:"memory_rss"`  // resident set size in bytes
	MemoryVMS int64   `json:"memory_vms"`  // virtual memory size in bytes
	Uptime    string  `json:"uptime,omitempty"`
}

// Stats returns resource usage for a single app's process.
// On Linux it reads from /proc/[pid]/stat and /proc/[pid]/statm.
// On other platforms it returns zeroes for CPU/memory.
func (m *Manager) Stats(domain string) *AppStats {
	m.mu.RLock()
	app, exists := m.apps[domain]
	m.mu.RUnlock()
	if !exists {
		return nil
	}

	s := &AppStats{Domain: domain}
	if app.cmd == nil || app.cmd.Process == nil {
		return s
	}

	s.Running = true
	s.PID = app.cmd.Process.Pid
	uptime := time.Since(app.startedAt).Truncate(time.Second)
	s.Uptime = uptime.String()

	// Read process stats (Linux only — best effort)
	s.CPUPct, s.MemoryRSS, s.MemoryVMS = readProcessStats(app.cmd.Process.Pid)

	return s
}

// ListenAddr returns the address the app for this domain listens on. Empty
// when the domain has no app registered OR the app is registered but its
// process is not currently running. The "registered but stopped" case
// previously returned the address anyway, which made the proxy attempt a
// connection to localhost:PORT and surface a generic 502 (connection
// refused) with no hint that the app itself was the problem — see
// handleAppProxy for the operator-facing diagnostic that depends on this
// distinction.
func (m *Manager) ListenAddr(domain string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	app, exists := m.apps[domain]
	if !exists {
		return ""
	}
	if app.cmd == nil || app.cmd.Process == nil {
		return ""
	}
	return fmt.Sprintf("127.0.0.1:%d", app.port)
}

// AppState describes whether an app domain is wired up and running. Used
// by handleAppProxy to render a meaningful error body instead of a generic
// 502 when the upstream side of the proxy is misconfigured.
type AppState int

const (
	// AppStateNotRegistered means the domain has no app entry — operator
	// likely added a `type: app` domain but never called the deploy flow.
	AppStateNotRegistered AppState = iota
	// AppStateStopped means the app exists but its process is not running
	// (crashed, manually stopped, or never started).
	AppStateStopped
	// AppStateRunning means the process is currently up and the address
	// returned by ListenAddr is valid.
	AppStateRunning
)

// State reports the lifecycle phase of the app for the given domain so a
// caller can distinguish "not configured" from "configured but down" when
// surfacing errors.
func (m *Manager) State(domain string) AppState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	app, exists := m.apps[domain]
	if !exists {
		return AppStateNotRegistered
	}
	if app.cmd == nil || app.cmd.Process == nil {
		return AppStateStopped
	}
	return AppStateRunning
}

// validateShellCommand rejects commands with dangerous shell metacharacters.
func validateShellCommand(command string) error {
	if strings.ContainsAny(command, "\x00\n\r") {
		return fmt.Errorf("command contains forbidden control characters")
	}
	forbidden := []string{"$(", "`", "|", ">", "<", ";", "&&", "||"}
	for _, f := range forbidden {
		if strings.Contains(command, f) {
			return fmt.Errorf("command contains forbidden shell metacharacter: %q", f)
		}
	}
	return nil
}

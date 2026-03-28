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

// AppInstance describes a running application process.
type AppInstance struct {
	Domain    string            `json:"domain"`
	Runtime   string            `json:"runtime"`    // "node", "python", "ruby", "go", "custom"
	Command   string            `json:"command"`    // actual command being run
	Port      int               `json:"port"`       // port the app listens on
	PID       int               `json:"pid"`        // OS process ID (0 if not running)
	Running   bool              `json:"running"`
	Uptime    string            `json:"uptime,omitempty"` // human-readable uptime
	StartedAt *time.Time        `json:"started_at,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
}

type appProcess struct {
	domain    string
	runtime   string
	command   string
	port      int
	workDir   string
	env       map[string]string
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
func detectCommand(runtime, workDir string) string {
	switch runtime {
	case "node":
		// Check for package.json start script
		if _, err := os.Stat(filepath.Join(workDir, "package.json")); err == nil {
			return "npm start"
		}
		// Check for common entry points
		for _, f := range []string{"server.js", "index.js", "app.js"} {
			if _, err := os.Stat(filepath.Join(workDir, f)); err == nil {
				return "node " + f
			}
		}
	case "python":
		// Check for common WSGI/ASGI patterns
		if _, err := os.Stat(filepath.Join(workDir, "manage.py")); err == nil {
			return "python manage.py runserver 0.0.0.0:${PORT}"
		}
		for _, f := range []string{"app.py", "main.py", "wsgi.py"} {
			if _, err := os.Stat(filepath.Join(workDir, f)); err == nil {
				return "python " + f
			}
		}
		if _, err := os.Stat(filepath.Join(workDir, "requirements.txt")); err == nil {
			return "gunicorn app:app -b 0.0.0.0:${PORT}"
		}
	case "ruby":
		if _, err := os.Stat(filepath.Join(workDir, "config.ru")); err == nil {
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

	autoRestart := appCfg.AutoRestart
	// Default to true if not explicitly set (zero value is false, but we want true default)
	// Config should set auto_restart: false to disable
	if appCfg.Command != "" || appCfg.Runtime != "" {
		// Only default to true if app is actually configured
		autoRestart = true
	}
	if !appCfg.AutoRestart && appCfg.Command != "" {
		autoRestart = appCfg.AutoRestart
	}

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

	// Build exec.Cmd — use shell for complex commands
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/C", cmdStr)
	} else {
		cmd = exec.Command("sh", "-c", cmdStr)
	}

	cmd.Dir = app.workDir

	// Build environment
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, fmt.Sprintf("PORT=%d", app.port))
	cmd.Env = append(cmd.Env, fmt.Sprintf("HOST=0.0.0.0"))
	cmd.Env = append(cmd.Env, fmt.Sprintf("NODE_ENV=production"))
	for k, v := range app.env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	// Capture stdout/stderr to a log file
	logDir := filepath.Join(filepath.Dir(app.workDir), "logs")
	os.MkdirAll(logDir, 0755)
	logFile, err := os.OpenFile(filepath.Join(logDir, "app.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
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
		select {
		case <-app.stopCh:
			return // stopped during backoff
		case <-time.After(2 * time.Second):
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
	// Snapshot cmd under lock to avoid race with monitorProcess setting cmd=nil
	cmd := app.cmd
	if cmd == nil || cmd.Process == nil {
		m.mu.Unlock()
		return fmt.Errorf("domain %s is not running", domain)
	}

	close(app.stopCh)
	app.stopCh = make(chan struct{}) // reset for next start
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
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]AppInstance, 0, len(m.apps))
	for _, app := range m.apps {
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
	CPUPct    float64 `json:"cpu_percent"`  // percentage of one core
	MemoryRSS int64   `json:"memory_rss"`   // resident set size in bytes
	MemoryVMS int64   `json:"memory_vms"`   // virtual memory size in bytes
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

// ListenAddr returns the address the app for this domain listens on.
func (m *Manager) ListenAddr(domain string) string {
	m.mu.RLock()
	app, exists := m.apps[domain]
	m.mu.RUnlock()
	if !exists {
		return ""
	}
	return fmt.Sprintf("127.0.0.1:%d", app.port)
}

package phpmanager

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/uwaserver/uwas/internal/logger"
)

// Testable hooks for OS operations. Overridden in tests.
var (
	// osStat wraps os.Stat for testability.
	osStat = os.Stat
	// netDialTimeout wraps net.DialTimeout for testability.
	netDialTimeout = func(network, address string, timeout time.Duration) (net.Conn, error) {
		return net.DialTimeout(network, address, timeout)
	}
	// osMkdirAll wraps os.MkdirAll for testability.
	osMkdirAllHook = os.MkdirAll
	// osWriteFileHook wraps os.WriteFile for testability.
	osWriteFileHook = os.WriteFile
	// osCreateTemp wraps os.CreateTemp for testability.
	osCreateTempHook = os.CreateTemp
)

// PHPInstall describes a single detected PHP installation.
type PHPInstall struct {
	Version    string   `json:"version"`     // e.g. "8.4.19"
	Binary     string   `json:"binary"`      // path to php-cgi or php-fpm
	ConfigFile string   `json:"config_file"` // php.ini path
	Extensions []string `json:"extensions"`  // enabled extensions
	SAPI       string   `json:"sapi"`        // "cgi-fcgi" or "fpm-fcgi"
	Disabled   bool     `json:"disabled"`    // user disabled this version
}

// PHPConfig holds commonly tuned php.ini directives.
type PHPConfig struct {
	MemoryLimit      string `json:"memory_limit"`
	MaxExecutionTime string `json:"max_execution_time"`
	UploadMaxSize    string `json:"upload_max_filesize"`
	PostMaxSize      string `json:"post_max_size"`
	DisplayErrors    string `json:"display_errors"`
	ErrorReporting   string `json:"error_reporting"`
	OPcacheEnabled   string `json:"opcache.enable"`
	Timezone         string `json:"date.timezone"`
}

// processInfo tracks a running PHP-CGI subprocess.
type processInfo struct {
	cmd        *exec.Cmd
	listenAddr string
}

// DomainPHP describes a per-domain PHP-CGI instance.
type DomainPHP struct {
	Domain          string            `json:"domain"`
	Version         string            `json:"version"`     // "8.4" or "8.4.19"
	ListenAddr      string            `json:"listen_addr"` // auto-assigned "127.0.0.1:9001"
	Running         bool              `json:"running"`
	PID             int               `json:"pid"`
	ConfigOverrides map[string]string `json:"config_overrides"` // per-domain php.ini overrides
}

// DomainChangeFunc is called when a domain's PHP configuration changes.
// It receives the domain name and the new FPM address.
type DomainChangeFunc func(domain, fpmAddr string)

// domainInstance holds the internal state for a per-domain PHP assignment.
type domainInstance struct {
	domain          string
	version         string
	listenAddr      string
	webRoot         string // domain document root for open_basedir
	configOverrides map[string]string
	proc            *processInfo
	tmpINI          string // path to temp ini file, cleaned up on stop
	// crash-restart tracking: prevent a permanently-broken PHP binary from
	// looping start→crash→start every 500ms forever.
	restartCount int
	lastRestart  time.Time
	// stopGen is bumped by StopDomain/StopAll. The crash monitor captures it
	// before its restart backoff and re-checks after, so a stop issued while
	// the monitor is sleeping in backoff cancels the pending auto-restart
	// instead of resurrecting a process the operator just stopped.
	stopGen int
}

// Manager detects and manages PHP installations and subprocesses.
type Manager struct {
	installations []PHPInstall
	mu            sync.RWMutex
	processes     sync.Map // version string → *processInfo
	logger        *logger.Logger

	// Per-domain PHP instances.
	domainMu       sync.RWMutex
	domainMap      map[string]*domainInstance // domain → instance
	nextPort       int                        // next auto-assigned port
	onDomainChange DomainChangeFunc           // called when a domain PHP starts
	onCrash        func(domain string)        // called when PHP crashes and auto-restarts

	// execCommand is the function used to create exec.Cmd objects.
	// It defaults to exec.Command and can be overridden for testing.
	execCommand func(name string, arg ...string) *exec.Cmd
}

// New creates a new PHP Manager.
func New(log *logger.Logger) *Manager {
	return &Manager{
		logger:      log,
		execCommand: exec.Command,
		domainMap:   make(map[string]*domainInstance),
		nextPort:    9001,
	}
}

// SetDomainChangeFunc sets a callback invoked when a domain PHP instance
// starts and the running config should be updated with the new FPM address.
func (m *Manager) SetDomainChangeFunc(fn DomainChangeFunc) {
	m.domainMu.Lock()
	defer m.domainMu.Unlock()
	m.onDomainChange = fn
}

// SetOnCrash sets a callback that fires when a PHP process crashes and auto-restarts.
func (m *Manager) SetOnCrash(fn func(domain string)) {
	m.domainMu.Lock()
	defer m.domainMu.Unlock()
	m.onCrash = fn
}

// StopAll stops all running PHP-CGI subprocesses (both global and per-domain).
// Called during server shutdown.
func (m *Manager) StopAll() {
	m.processes.Range(func(key, val any) bool {
		version, ok := key.(string)
		if !ok {
			m.processes.Delete(key)
			return true
		}
		info, ok := val.(*processInfo)
		if !ok || info == nil || info.cmd == nil || info.cmd.Process == nil {
			m.logger.Warn("stale PHP-CGI process entry removed", "version", version)
		} else if err := info.cmd.Process.Kill(); err != nil {
			m.logger.Warn("failed to stop PHP-CGI", "version", version, "error", err)
		} else {
			m.logger.Info("stopped PHP-CGI", "version", version)
		}
		m.processes.Delete(key)
		return true
	})

	// Stop all per-domain instances.
	m.domainMu.Lock()
	for domain, inst := range m.domainMap {
		// Cancel any pending crash-backoff restart (see StopDomain).
		inst.stopGen++
		if inst.proc != nil {
			if inst.proc.cmd != nil && inst.proc.cmd.Process != nil {
				if err := inst.proc.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
					m.logger.Warn("failed to stop domain PHP-CGI", "domain", domain, "error", err)
				} else {
					m.logger.Info("stopped domain PHP-CGI", "domain", domain)
				}
			}
			inst.proc = nil
		}
		if inst.tmpINI != "" {
			os.Remove(inst.tmpINI)
			inst.tmpINI = ""
		}
	}
	m.domainMu.Unlock()
}

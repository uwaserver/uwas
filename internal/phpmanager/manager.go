package phpmanager

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
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

// AssignDomain assigns a PHP version to a domain.
// AssignDomainWithRoot assigns PHP to a domain with a known web root for open_basedir.
func (m *Manager) AssignDomainWithRoot(domain, version, webRoot string) (*DomainPHP, error) {
	dp, err := m.AssignDomain(domain, version)
	if err != nil {
		return dp, err
	}
	m.domainMu.Lock()
	if inst, ok := m.domainMap[domain]; ok {
		inst.webRoot = webRoot
	}
	m.domainMu.Unlock()
	return dp, nil
}

// RegisterExistingDomain registers a domain that already has a working PHP
// address (from config file). This ensures it appears in GetDomainInstances().
// If overrides is non-nil, they are loaded as the initial per-domain config.
func (m *Manager) RegisterExistingDomain(domain, version, listenAddr, webRoot string, overrides map[string]string) {
	m.domainMu.Lock()
	defer m.domainMu.Unlock()
	if _, exists := m.domainMap[domain]; exists {
		return // already registered
	}
	co := make(map[string]string, len(overrides))
	for k, v := range overrides {
		co[k] = v
	}
	inst := &domainInstance{
		domain:          domain,
		version:         version,
		listenAddr:      listenAddr,
		webRoot:         webRoot,
		configOverrides: co,
	}
	// For system php-fpm sockets, set sentinel proc so Running reports true.
	if strings.HasPrefix(listenAddr, "unix:") || strings.HasPrefix(listenAddr, "/") {
		inst.proc = &processInfo{listenAddr: listenAddr}
	}
	m.domainMap[domain] = inst
}

// AssignDomain assigns a PHP version to a domain.
// Priority: 1) system php-fpm socket 2) running shared php-fpm 3) per-domain port
func (m *Manager) AssignDomain(domain, version string) (*DomainPHP, error) {
	if domain == "" {
		return nil, fmt.Errorf("domain is required")
	}
	if version == "" {
		return nil, fmt.Errorf("version is required")
	}

	// Verify the version is installed.
	if _, ok := m.findInstall(version); !ok {
		return nil, fmt.Errorf("PHP %s not found", version)
	}

	m.domainMu.Lock()
	defer m.domainMu.Unlock()

	if _, exists := m.domainMap[domain]; exists {
		return nil, fmt.Errorf("domain %s already has a PHP assignment", domain)
	}

	// Try system php-fpm socket first (best performance, shared workers)
	addr := m.detectSystemFPMSocket(version)
	if addr == "" {
		// Fallback to per-domain TCP port (for php-cgi)
		addr = fmt.Sprintf("127.0.0.1:%d", m.nextPort)
		m.nextPort++
	}

	inst := &domainInstance{
		domain:          domain,
		version:         version,
		listenAddr:      addr,
		configOverrides: make(map[string]string),
	}
	m.domainMap[domain] = inst

	m.logger.Info("assigned PHP to domain", "domain", domain, "version", version, "listen", addr)

	return m.domainPHPFromInstance(inst), nil
}

// detectSystemFPMSocket checks if system php-fpm is running and returns its socket path.
func (m *Manager) detectSystemFPMSocket(version string) string {
	// Extract major.minor from version (e.g., "8.3.6" → "8.3")
	parts := strings.SplitN(version, ".", 3)
	shortVer := version
	if len(parts) >= 2 {
		shortVer = parts[0] + "." + parts[1]
	}

	// Check common php-fpm socket paths
	socketPaths := []string{
		fmt.Sprintf("/run/php/php%s-fpm.sock", shortVer),
		fmt.Sprintf("/var/run/php/php%s-fpm.sock", shortVer),
		"/run/php/php-fpm.sock",
		"/var/run/php-fpm.sock",
		fmt.Sprintf("/tmp/php%s-fpm.sock", shortVer),
	}

	for _, sock := range socketPaths {
		if _, err := osStat(sock); err == nil {
			// Socket exists — verify it's actually listening
			conn, err := netDialTimeout("unix", sock, 2*time.Second)
			if err == nil {
				conn.Close()
				m.logger.Info("detected system php-fpm socket", "version", shortVer, "socket", sock)
				return "unix:" + sock
			}
		}
	}

	return ""
}

// UnassignDomain removes a PHP assignment for a domain. Stops the process
// if it is running.
func (m *Manager) UnassignDomain(domain string) {
	m.domainMu.Lock()
	inst, exists := m.domainMap[domain]
	if !exists {
		m.domainMu.Unlock()
		return
	}
	delete(m.domainMap, domain)
	m.domainMu.Unlock()

	// Stop the process if running.
	if inst.proc != nil && inst.proc.cmd != nil && inst.proc.cmd.Process != nil {
		_ = inst.proc.cmd.Process.Kill()
	}
	// Clean up temp ini file.
	if inst.tmpINI != "" {
		os.Remove(inst.tmpINI)
	}

	m.logger.Info("unassigned PHP from domain", "domain", domain)
}

// StartDomain starts the PHP process for the given domain.
// If using a system php-fpm socket, no process is started (already running).
func (m *Manager) StartDomain(domain string) error {
	m.domainMu.Lock()
	inst, exists := m.domainMap[domain]
	if !exists {
		m.domainMu.Unlock()
		return fmt.Errorf("domain %s has no PHP assignment", domain)
	}
	if inst.proc != nil {
		m.domainMu.Unlock()
		return fmt.Errorf("PHP for domain %s is already running", domain)
	}

	// If using system php-fpm socket, no process to start — mark as running
	if strings.HasPrefix(inst.listenAddr, "unix:") || strings.HasPrefix(inst.listenAddr, "/") {
		inst.proc = &processInfo{listenAddr: inst.listenAddr} // no cmd — system managed
		m.domainMu.Unlock()
		m.logger.Info("using system php-fpm", "domain", domain, "socket", inst.listenAddr)
		return nil
	}

	phpInst, ok := m.findInstall(inst.version)
	if !ok {
		m.domainMu.Unlock()
		return fmt.Errorf("PHP %s not found", inst.version)
	}
	if phpInst.SAPI != "cgi-fcgi" && phpInst.SAPI != "fpm-fcgi" {
		m.domainMu.Unlock()
		return fmt.Errorf("PHP %s binary %s is %s, not cgi-fcgi — install php-cgi or php-fpm", inst.version, phpInst.Binary, phpInst.SAPI)
	}

	// Build command args.
	args := []string{"-b", inst.listenAddr}

	// Create per-domain temp ini if there are overrides or a base config.
	tmpINI, err := m.buildDomainINI(domain, phpInst, inst.configOverrides)
	if err != nil {
		m.domainMu.Unlock()
		return fmt.Errorf("build domain ini: %w", err)
	}
	if tmpINI != "" {
		args = append([]string{"-c", tmpINI}, args...)
		inst.tmpINI = tmpINI
	}

	cmd := m.execCommand(phpInst.Binary, args...)
	// Spawn worker children for parallel request handling
	cmd.Env = append(os.Environ(),
		"PHP_FCGI_CHILDREN=8",
		"PHP_FCGI_MAX_REQUESTS=500",
	)
	cmd.Stdout = m.logger.Writer(4) // slog.LevelInfo
	cmd.Stderr = m.logger.Writer(8) // slog.LevelError

	if err := cmd.Start(); err != nil {
		m.domainMu.Unlock()
		if tmpINI != "" {
			os.Remove(tmpINI)
		}
		return fmt.Errorf("start php-cgi for domain %s: %w", domain, err)
	}

	inst.proc = &processInfo{
		cmd:        cmd,
		listenAddr: inst.listenAddr,
	}

	// Capture callback before releasing lock.
	changeFn := m.onDomainChange
	listenAddr := inst.listenAddr
	m.domainMu.Unlock()

	m.logger.Info("started PHP-CGI for domain", "domain", domain, "version", inst.version,
		"listen", listenAddr, "pid", cmd.Process.Pid)

	// Notify domain change so the running config is updated.
	if changeFn != nil {
		changeFn(domain, listenAddr)
	}

	// Reap the process in the background and auto-restart on crash.
	m.logger.SafeGo("php.monitor."+domain, func() {
		waitErr := cmd.Wait()
		const (
			maxRapidRestarts = 5
			restartWindow    = 60 * time.Second
			baseBackoff      = 500 * time.Millisecond
			maxBackoff       = 30 * time.Second
		)
		m.domainMu.Lock()
		di, stillAssigned := m.domainMap[domain]
		shouldRestart := stillAssigned && di.proc != nil && di.proc.cmd == cmd
		var backoff time.Duration
		var restartGen int
		giveUp := false
		if shouldRestart {
			restartGen = di.stopGen
			di.proc = nil
			if di.tmpINI != "" {
				os.Remove(di.tmpINI)
				di.tmpINI = ""
			}
			// Reset the counter if the last restart was long enough ago, so a
			// stable process that crashes once weeks later isn't penalized.
			now := time.Now()
			if now.Sub(di.lastRestart) > restartWindow {
				di.restartCount = 0
			}
			di.restartCount++
			di.lastRestart = now
			if di.restartCount > maxRapidRestarts {
				giveUp = true
			} else {
				// Exponential backoff: 0.5s, 1s, 2s, 4s, 8s (capped).
				backoff = baseBackoff << (di.restartCount - 1)
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
		}
		m.domainMu.Unlock()

		if waitErr != nil {
			m.logger.Warn("PHP-CGI for domain exited", "domain", domain, "error", waitErr)
		} else {
			m.logger.Info("PHP-CGI for domain exited normally", "domain", domain)
		}

		// Auto-restart only when this goroutine still owns the active process
		// (prevents restart after intentional StopDomain/StopAll).
		if shouldRestart {
			if waitErr != nil && m.onCrash != nil {
				m.onCrash(domain)
			}
			if giveUp {
				m.logger.Error("PHP-CGI crash-looping, giving up auto-restart",
					"domain", domain, "restarts", maxRapidRestarts)
				return
			}
			time.Sleep(backoff)
			// A StopDomain/StopAll during the backoff sleep bumps stopGen (or
			// removes the assignment). Re-check before restarting so an
			// operator's stop isn't undone by the pending auto-restart.
			m.domainMu.Lock()
			cur, ok := m.domainMap[domain]
			canceled := !ok || cur != di || cur.stopGen != restartGen
			m.domainMu.Unlock()
			if canceled {
				m.logger.Info("PHP-CGI auto-restart canceled by stop", "domain", domain)
				return
			}
			if err := m.StartDomain(domain); err != nil {
				m.logger.Error("PHP-CGI auto-restart failed", "domain", domain, "error", err)
			} else {
				m.logger.Info("PHP-CGI auto-restarted", "domain", domain)
			}
		}
	})

	return nil
}

// StopDomain stops the PHP-CGI process for the given domain.
func (m *Manager) StopDomain(domain string) error {
	m.domainMu.Lock()
	inst, exists := m.domainMap[domain]
	if !exists {
		m.domainMu.Unlock()
		return fmt.Errorf("domain %s has no PHP assignment", domain)
	}
	// Record the stop intent before the proc==nil early return: during a crash
	// backoff inst.proc is already nil, but the monitor is sleeping and about
	// to restart — bumping stopGen cancels that pending restart.
	inst.stopGen++
	if inst.proc == nil {
		m.domainMu.Unlock()
		return fmt.Errorf("PHP for domain %s is not running", domain)
	}

	proc := inst.proc
	inst.proc = nil
	tmpINI := inst.tmpINI
	inst.tmpINI = ""
	m.domainMu.Unlock()

	// System FPM socket: no process to kill (managed by systemd)
	if proc.cmd == nil || proc.cmd.Process == nil {
		m.logger.Info("detached from system php-fpm", "domain", domain)
		return nil
	}
	if err := proc.cmd.Process.Kill(); err != nil {
		return fmt.Errorf("kill php-cgi for domain %s: %w", domain, err)
	}

	if tmpINI != "" {
		os.Remove(tmpINI)
	}

	m.logger.Info("stopped PHP-CGI for domain", "domain", domain)
	return nil
}

// RestartDomain gracefully restarts the per-domain PHP process so that
// updated config overrides take effect.
func (m *Manager) RestartDomain(domain string) error {
	m.domainMu.RLock()
	inst, exists := m.domainMap[domain]
	if !exists {
		m.domainMu.RUnlock()
		return fmt.Errorf("domain %s has no PHP assignment", domain)
	}
	if inst.proc == nil {
		m.domainMu.RUnlock()
		return fmt.Errorf("PHP for domain %s is not running", domain)
	}
	m.domainMu.RUnlock()

	if err := m.StopDomain(domain); err != nil {
		return fmt.Errorf("restart domain: stop failed: %w", err)
	}
	if err := m.StartDomain(domain); err != nil {
		return fmt.Errorf("restart domain: start failed: %w", err)
	}
	m.logger.Info("restarted PHP for domain", "domain", domain)
	return nil
}

// Blocked PHP directives — these cannot be overridden per-domain for security.
var blockedPHPDirectives = map[string]bool{
	"open_basedir":                    true, // managed by UWAS (chroot)
	"disable_functions":               true, // security critical
	"disable_classes":                 true,
	"allow_url_include":               true, // RCE risk
	"allow_url_fopen":                 true, // SSRF risk when changed carelessly
	"safe_mode":                       true, // deprecated but dangerous
	"enable_dl":                       true, // load arbitrary extensions
	"suhosin.executor.func.blacklist": true,
	"auto_prepend_file":               true, // code injection
	"auto_append_file":                true,
	"sendmail_path":                   true, // command injection
	"mail.force_extra_parameters":     true,
	"extension_dir":                   true,
	"extension":                       true,
	"zend_extension":                  true,
	"doc_root":                        true, // path override
	"user_dir":                        true,
	"cgi.force_redirect":              true,
	"cgi.redirect_status_env":         true,
}

// SetDomainConfig sets a per-domain php.ini override with security validation.
func (m *Manager) SetDomainConfig(domain, key, value string) error {
	if key == "" {
		return fmt.Errorf("key is required")
	}

	// Security: block dangerous directives
	if blockedPHPDirectives[key] {
		return fmt.Errorf("directive %q is blocked for security — can only be set in global php.ini", key)
	}

	m.domainMu.Lock()
	defer m.domainMu.Unlock()

	inst, exists := m.domainMap[domain]
	if !exists {
		return fmt.Errorf("domain %s has no PHP assignment", domain)
	}

	inst.configOverrides[key] = value
	m.logger.Info("set domain PHP config", "domain", domain, "key", key, "value", value)
	return nil
}

// GetDomainConfig returns the per-domain php.ini overrides.
func (m *Manager) GetDomainConfig(domain string) map[string]string {
	m.domainMu.RLock()
	defer m.domainMu.RUnlock()

	inst, exists := m.domainMap[domain]
	if !exists {
		return nil
	}

	out := make(map[string]string, len(inst.configOverrides))
	for k, v := range inst.configOverrides {
		out[k] = v
	}
	return out
}

// RunningAddrForDomain returns the listen address of the running PHP-FPM
// instance assigned to the given domain, or "" when no such instance is
// running. Single map probe, used by the request hot path instead of
// scanning all instances on every PHP request (was P6).
func (m *Manager) RunningAddrForDomain(domain string) string {
	m.domainMu.RLock()
	defer m.domainMu.RUnlock()
	inst, ok := m.domainMap[domain]
	if !ok {
		return ""
	}
	// Same liveness rules as domainPHPFromInstance.
	if inst.proc != nil {
		if inst.proc.cmd != nil && inst.proc.cmd.Process != nil {
			return inst.listenAddr
		}
		if strings.HasPrefix(inst.listenAddr, "unix:") || strings.HasPrefix(inst.listenAddr, "/") {
			return inst.listenAddr
		}
		return ""
	}
	// System-managed (no proc) on a unix socket / abs path: still serving.
	if strings.HasPrefix(inst.listenAddr, "unix:") || strings.HasPrefix(inst.listenAddr, "/") {
		return inst.listenAddr
	}
	return ""
}

// GetDomainInstances returns all per-domain PHP assignments.
func (m *Manager) GetDomainInstances() []DomainPHP {
	m.domainMu.RLock()
	defer m.domainMu.RUnlock()

	result := make([]DomainPHP, 0, len(m.domainMap))
	for _, inst := range m.domainMap {
		result = append(result, *m.domainPHPFromInstance(inst))
	}

	// Sort by domain for deterministic output.
	sort.Slice(result, func(i, j int) bool {
		return result[i].Domain < result[j].Domain
	})

	return result
}

// AutoStartAll starts PHP-CGI processes for all assigned domains.
// It is intended to be called on server startup.
func (m *Manager) AutoStartAll() error {
	m.domainMu.RLock()
	domains := make([]string, 0, len(m.domainMap))
	for domain := range m.domainMap {
		domains = append(domains, domain)
	}
	m.domainMu.RUnlock()

	var errs []string
	for _, domain := range domains {
		if err := m.StartDomain(domain); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", domain, err))
			m.logger.Warn("failed to auto-start PHP for domain", "domain", domain, "error", err)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("auto-start errors: %s", strings.Join(errs, "; "))
	}
	return nil
}

// buildDomainINI creates a temporary php.ini that includes the base config
// and adds per-domain overrides. Returns the path to the temp file, or ""
// if no customization is needed.
func (m *Manager) buildDomainINI(domain string, inst PHPInstall, overrides map[string]string) (string, error) {
	if inst.ConfigFile == "" && len(overrides) == 0 {
		return "", nil
	}

	tmpFile, err := osCreateTempHook("", fmt.Sprintf("uwas-php-%s-*.ini", domain))
	if err != nil {
		return "", err
	}

	var lines []string

	// Include base php.ini if available.
	if inst.ConfigFile != "" {
		// On Windows, use forward slashes or escape backslashes in the include path.
		cfgPath := strings.ReplaceAll(inst.ConfigFile, `\`, `/`)
		lines = append(lines, fmt.Sprintf("; Per-domain PHP config for %s", domain))
		lines = append(lines, fmt.Sprintf("; Base config: %s", cfgPath))
		lines = append(lines, "")

		// Read base config and include its contents directly, because
		// PHP's include directive is not available in all builds.
		baseData, readErr := os.ReadFile(inst.ConfigFile)
		if readErr == nil {
			lines = append(lines, string(baseData))
			lines = append(lines, "")
		}
	}

	// Security: enforce open_basedir per domain (chroot PHP to web root + tmp).
	// NOTE: Caller must hold domainMu (or not depend on lock) since buildDomainINI
	// is invoked from StartDomain which already holds domainMu.Lock().
	domainInst := m.domainMap[domain]
	if domainInst != nil {
		// Security: UWAS enforced — domain isolation
		lines = append(lines, "; Security: UWAS enforced — domain isolation")
		// Note: curl_multi_exec is needed by WordPress HTTP API.
		// proc_open is needed by WordPress/Composer for updates.
		// parse_ini_file is needed by some plugins for reading configs.
		lines = append(lines, "disable_functions = exec,passthru,shell_exec,system,popen,pcntl_exec")
		lines = append(lines, "allow_url_include = Off")
		lines = append(lines, "expose_php = Off")
		lines = append(lines, "display_errors = On")
		lines = append(lines, "error_reporting = E_ALL")
		lines = append(lines, "log_errors = On")
		// UWAS handles compression — disable PHP's to prevent double-compress
		lines = append(lines, "zlib.output_compression = Off")
		lines = append(lines, "output_handler = ")

		// open_basedir: restrict PHP to domain's web root + tmp
		if domainInst.webRoot != "" {
			// Per-domain tmp directory for session/upload isolation
			domainTmp := filepath.Join(domainInst.webRoot, ".tmp")
			os.MkdirAll(domainTmp, 0770)
			// open_basedir paths:
			// - domain web root (site files)
			// - domain .tmp (sessions, uploads, temp)
			// - /usr/share/php, /usr/share/pear (PHP libraries)
			// - /etc/ssl/certs, /usr/share/ca-certificates (HTTPS/curl)
			// - /usr/share/zoneinfo (timezone data)
			// - /dev/urandom (random for sessions/tokens)
			lines = append(lines, fmt.Sprintf("open_basedir = %s:%s:/usr/share/php:/usr/share/pear:/etc/php:/etc/ssl/certs:/usr/share/ca-certificates:/usr/share/zoneinfo:/usr/lib/php:/dev/urandom:/tmp", domainInst.webRoot, domainTmp))
			lines = append(lines, fmt.Sprintf("upload_tmp_dir = %s", domainTmp))
			lines = append(lines, fmt.Sprintf("session.save_path = %s", domainTmp))
			lines = append(lines, fmt.Sprintf("sys_temp_dir = %s", domainTmp))
		}
		lines = append(lines, "")

		// Performance: sane defaults (overridable via per-domain config)
		lines = append(lines, "; Performance: UWAS defaults")
		lines = append(lines, "realpath_cache_size = 4096K")
		lines = append(lines, "realpath_cache_ttl = 600")
		lines = append(lines, "opcache.enable = 1")
		lines = append(lines, "opcache.memory_consumption = 128")
		lines = append(lines, "opcache.max_accelerated_files = 10000")
		lines = append(lines, "opcache.revalidate_freq = 2")
		lines = append(lines, "opcache.validate_timestamps = 1")
		lines = append(lines, "")
	}

	// Add overrides.
	if len(overrides) > 0 {
		lines = append(lines, "; Per-domain overrides")
		// Sort keys for deterministic output.
		keys := make([]string, 0, len(overrides))
		for k := range overrides {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			lines = append(lines, fmt.Sprintf("%s = %s", k, overrides[k]))
		}
	}

	content := strings.Join(lines, "\n") + "\n"
	if _, err := tmpFile.WriteString(content); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return "", err
	}
	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpFile.Name())
		return "", err
	}

	return tmpFile.Name(), nil
}

// domainPHPFromInstance converts internal state to the public DomainPHP type.
func (m *Manager) domainPHPFromInstance(inst *domainInstance) *DomainPHP {
	dp := &DomainPHP{
		Domain:          inst.domain,
		Version:         inst.version,
		ListenAddr:      inst.listenAddr,
		ConfigOverrides: make(map[string]string, len(inst.configOverrides)),
	}
	for k, v := range inst.configOverrides {
		dp.ConfigOverrides[k] = v
	}
	if inst.proc != nil {
		if inst.proc.cmd != nil && inst.proc.cmd.Process != nil {
			dp.Running = true
			dp.PID = inst.proc.cmd.Process.Pid
		} else if strings.HasPrefix(inst.listenAddr, "unix:") || strings.HasPrefix(inst.listenAddr, "/") {
			dp.Running = true
			dp.PID = -1 // system-managed
		}
	} else if strings.HasPrefix(inst.listenAddr, "unix:") || strings.HasPrefix(inst.listenAddr, "/") {
		// System php-fpm registered without proc — still running via system service
		dp.Running = true
		dp.PID = -1
	}
	return dp
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
				if err := inst.proc.cmd.Process.Kill(); err != nil {
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

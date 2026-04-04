package phpmanager

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
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
		m.domainMu.Lock()
		di, stillAssigned := m.domainMap[domain]
		shouldRestart := stillAssigned && di.proc != nil && di.proc.cmd == cmd
		if shouldRestart {
			di.proc = nil
			if di.tmpINI != "" {
				os.Remove(di.tmpINI)
				di.tmpINI = ""
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
			time.Sleep(500 * time.Millisecond) // brief backoff
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

// Allowed per-domain directives — only these can be set.
var allowedDomainDirectives = []string{
	"memory_limit", "max_execution_time", "max_input_time",
	"upload_max_filesize", "post_max_size", "max_file_uploads", "max_input_vars",
	"display_errors", "error_reporting", "log_errors",
	"date.timezone", "session.gc_maxlifetime", "session.cookie_secure",
	"opcache.enable", "opcache.memory_consumption", "opcache.max_accelerated_files",
	"short_open_tag", "output_buffering", "default_charset",
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

// AllowedDomainDirectives returns the list of directives that can be set per-domain.
func AllowedDomainDirectives() []string {
	return allowedDomainDirectives
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

// Installations returns a copy of the detected PHP installations.
func (m *Manager) Installations() []PHPInstall {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]PHPInstall, len(m.installations))
	copy(out, m.installations)
	return out
}

// candidatePathsFunc can be overridden in tests to inject custom search paths.
var candidatePathsFunc = candidatePaths

// Detect scans the system for PHP binaries and populates the installations
// list. It looks in OS-specific common locations.
func (m *Manager) Detect() error {
	patterns := candidatePathsFunc()

	var found []PHPInstall
	seen := make(map[string]bool) // resolved real path → already added

	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		for _, bin := range matches {
			abs, err := filepath.Abs(bin)
			if err != nil {
				continue
			}

			// Resolve symlinks to deduplicate identical binaries.
			real, err := filepath.EvalSymlinks(abs)
			if err != nil {
				real = abs
			}
			if seen[real] {
				continue
			}
			seen[real] = true

			install, err := m.probe(abs)
			if err != nil {
				m.logger.Debug("skipping PHP binary", "path", abs, "error", err)
				continue
			}
			found = append(found, install)
			m.logger.Info("detected PHP", "version", install.Version, "sapi", install.SAPI, "binary", install.Binary)
		}
	}

	m.mu.Lock()
	m.installations = found
	m.mu.Unlock()

	m.logger.Info("PHP detection complete", "count", len(found))
	return nil
}

// runtimeGOOS is the OS identifier used by candidatePaths. Overridden in tests.
var runtimeGOOS = runtime.GOOS

// candidatePaths returns glob patterns for common PHP binary locations.
func candidatePaths() []string {
	switch runtimeGOOS {
	case "linux":
		return []string{
			"/usr/bin/php-cgi*",
			"/usr/bin/php-fpm*",
			"/usr/sbin/php-fpm*",
			"/usr/bin/php[0-9]*",
			"/usr/lib/cgi-bin/php*",
			"/usr/local/bin/php-cgi*",
			"/usr/local/sbin/php-fpm*",
		}
	case "darwin":
		return []string{
			"/opt/homebrew/bin/php*",
			"/usr/local/bin/php*",
		}
	case "windows":
		home, _ := os.UserHomeDir()
		paths := []string{
			"C:/php/php-cgi.exe",
			"C:/php*/php-cgi.exe",
			"C:/laragon/bin/php/php*/php-cgi.exe",
		}
		if home != "" {
			paths = append(paths, filepath.Join(home, ".config/herd/bin/php*/php-cgi.exe"))
		}
		return paths
	default:
		return []string{"/usr/bin/php-cgi*", "/usr/local/bin/php*"}
	}
}

// probe runs a PHP binary and extracts version, SAPI, config path, and extensions.
func (m *Manager) probe(binary string) (PHPInstall, error) {
	install := PHPInstall{Binary: binary}

	// Get version
	version, err := m.runPHP(binary, "-v")
	if err != nil {
		return install, fmt.Errorf("version check: %w", err)
	}
	install.Version = parseVersion(version)
	if install.Version == "" {
		return install, fmt.Errorf("could not parse version from: %s", version)
	}

	// Get SAPI
	install.SAPI = parseSAPI(version)

	// Get config file path
	info, err := m.runPHP(binary, "-i")
	if err == nil {
		install.ConfigFile = parseConfigPath(info)
	}
	// If no config file found, try common locations and create if needed
	if install.ConfigFile == "" {
		install.ConfigFile = findOrCreatePHPConfig(install.Version, info)
	}

	// Get extensions
	modules, err := m.runPHP(binary, "-m")
	if err == nil {
		install.Extensions = parseExtensions(modules)
	}

	return install, nil
}

// runPHP executes a PHP binary with given arguments and returns stdout.
func (m *Manager) runPHP(binary string, args ...string) (string, error) {
	cmd := m.execCommand(binary, args...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

var versionRegex = regexp.MustCompile(`PHP\s+(\d+\.\d+\.\d+)`)

// parseVersion extracts the version number from `php -v` output.
func parseVersion(output string) string {
	m := versionRegex.FindStringSubmatch(output)
	if len(m) >= 2 {
		return m[1]
	}
	return ""
}

// parseSAPI extracts the SAPI type from `php -v` output.
// findOrCreatePHPConfig locates or creates a php.ini for the given version.
// PHP installations sometimes have no loaded config file (Loaded Configuration File => (none)).
// We check the scan directory from php -i output, then common paths, and create one if needed.
func findOrCreatePHPConfig(version, phpInfo string) string {
	// Extract short version: "8.3.6" → "8.3"
	short := version
	if parts := strings.SplitN(version, ".", 3); len(parts) >= 2 {
		short = parts[0] + "." + parts[1]
	}

	// 1. Check "Scan this dir for additional .ini files" from php -i
	for _, line := range strings.Split(phpInfo, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Scan this dir") {
			if parts := strings.SplitN(line, "=>", 2); len(parts) == 2 {
				dir := strings.TrimSpace(parts[1])
				if dir != "(none)" && dir != "" {
					// The scan dir parent usually has php.ini
					parent := filepath.Dir(dir)
					candidate := filepath.Join(parent, "php.ini")
					if _, err := osStat(candidate); err == nil {
						return candidate
					}
				}
			}
		}
	}

	// 2. Try common paths
	candidates := []string{
		fmt.Sprintf("/etc/php/%s/cgi/php.ini", short),
		fmt.Sprintf("/etc/php/%s/fpm/php.ini", short),
		fmt.Sprintf("/etc/php/%s/cli/php.ini", short),
		fmt.Sprintf("/etc/php/%s/php.ini", short),
		"/etc/php.ini",
		"/usr/local/etc/php.ini",
	}
	for _, c := range candidates {
		if _, err := osStat(c); err == nil {
			return c
		}
	}

	// 3. Create a minimal php.ini in the expected location
	iniDir := fmt.Sprintf("/etc/php/%s/cgi", short)
	osMkdirAllHook(iniDir, 0755)
	iniPath := filepath.Join(iniDir, "php.ini")
	content := "; PHP configuration managed by UWAS\n; Edit via dashboard or uwas php config\n\n[PHP]\n"
	if err := osWriteFileHook(iniPath, []byte(content), 0644); err != nil {
		// Fallback: try cli path
		iniDir = fmt.Sprintf("/etc/php/%s/cli", short)
		iniPath = filepath.Join(iniDir, "php.ini")
		if _, err := osStat(iniPath); err == nil {
			return iniPath
		}
		return "" // give up
	}
	return iniPath
}

func parseSAPI(output string) string {
	lower := strings.ToLower(output)
	if strings.Contains(lower, "fpm-fcgi") {
		return "fpm-fcgi"
	}
	if strings.Contains(lower, "cgi-fcgi") || strings.Contains(lower, "cgi") {
		return "cgi-fcgi"
	}
	return "cli"
}

// parseConfigPath extracts the loaded php.ini path from `php -i` output.
func parseConfigPath(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Loaded Configuration File") {
			parts := strings.SplitN(line, "=>", 2)
			if len(parts) == 2 {
				p := strings.TrimSpace(parts[1])
				if p != "(none)" && p != "" {
					return p
				}
			}
		}
	}
	return ""
}

// parseExtensions extracts extension names from `php -m` output.
func parseExtensions(output string) []string {
	var exts []string
	inSection := false
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "[PHP Modules]" {
			inSection = true
			continue
		}
		if line == "[Zend Modules]" {
			break
		}
		if inSection && line != "" {
			exts = append(exts, line)
		}
	}
	return exts
}

// findInstall looks up an installation by version string. The version can be
// a prefix match (e.g. "8.4" matches "8.4.19").
func (m *Manager) findInstall(version string) (PHPInstall, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var fallback PHPInstall
	hasFallback := false

	for _, inst := range m.installations {
		if inst.Version == version || strings.HasPrefix(inst.Version, version) {
			// Prefer cgi-fcgi or fpm-fcgi over cli.
			if inst.SAPI == "cgi-fcgi" || inst.SAPI == "fpm-fcgi" {
				return inst, true
			}
			if !hasFallback {
				fallback = inst
				hasFallback = true
			}
		}
	}
	return fallback, hasFallback
}

// findInstallPtr returns a pointer to the installation in the slice so that
// mutations (e.g. populating ConfigFile) persist in the cache.
func (m *Manager) findInstallPtr(version string) (*PHPInstall, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var fallback *PHPInstall

	for i := range m.installations {
		inst := &m.installations[i]
		if inst.Version == version || strings.HasPrefix(inst.Version, version) {
			if inst.SAPI == "cgi-fcgi" || inst.SAPI == "fpm-fcgi" {
				return inst, true
			}
			if fallback == nil {
				fallback = inst
			}
		}
	}
	if fallback != nil {
		return fallback, true
	}
	return nil, false
}

// GetConfig reads key php.ini settings for the given PHP version.
func (m *Manager) GetConfig(version string) (PHPConfig, error) {
	inst, ok := m.findInstall(version)
	if !ok {
		return PHPConfig{}, fmt.Errorf("PHP %s not found", version)
	}

	if inst.ConfigFile == "" {
		// No config file — return sensible defaults
		return PHPConfig{
			MemoryLimit:      "128M",
			MaxExecutionTime: "30",
			PostMaxSize:      "8M",
			UploadMaxSize:    "2M",
			DisplayErrors:    "Off",
			ErrorReporting:   "E_ALL & ~E_DEPRECATED & ~E_STRICT",
			OPcacheEnabled:   "1",
			Timezone:         "UTC",
		}, nil
	}

	return parseINIConfig(inst.ConfigFile)
}

// parseINIConfig reads a php.ini file and extracts key settings.
func parseINIConfig(path string) (PHPConfig, error) {
	f, err := os.Open(path)
	if err != nil {
		return PHPConfig{}, fmt.Errorf("open php.ini: %w", err)
	}
	defer f.Close()

	cfg := PHPConfig{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])

		switch key {
		case "memory_limit":
			cfg.MemoryLimit = val
		case "max_execution_time":
			cfg.MaxExecutionTime = val
		case "upload_max_filesize":
			cfg.UploadMaxSize = val
		case "post_max_size":
			cfg.PostMaxSize = val
		case "display_errors":
			cfg.DisplayErrors = val
		case "error_reporting":
			cfg.ErrorReporting = val
		case "opcache.enable":
			cfg.OPcacheEnabled = val
		case "date.timezone":
			cfg.Timezone = val
		}
	}

	return cfg, scanner.Err()
}

// GetConfigRaw returns the raw php.ini file content.
func (m *Manager) GetConfigRaw(version string) (string, error) {
	inst, ok := m.findInstall(version)
	if !ok {
		return "", fmt.Errorf("PHP %s not found", version)
	}
	if inst.ConfigFile == "" {
		return "; No php.ini found for PHP " + version + "\n; Use the form below to create one, or install php.ini:\n;   sudo apt install php" + version + "-common\n\n[PHP]\nmemory_limit = 128M\nupload_max_filesize = 64M\npost_max_size = 64M\nmax_execution_time = 300\n", nil
	}
	data, err := os.ReadFile(inst.ConfigFile)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// SetConfigRaw writes the entire php.ini file content.
func (m *Manager) SetConfigRaw(version, content string) error {
	inst, ok := m.findInstallPtr(version)
	if !ok {
		return fmt.Errorf("PHP %s not found", version)
	}
	if inst.ConfigFile == "" {
		// Create config file via findOrCreatePHPConfig
		info, _ := m.runPHP(inst.Binary, "-i")
		inst.ConfigFile = findOrCreatePHPConfig(inst.Version, info)
		if inst.ConfigFile == "" {
			return fmt.Errorf("cannot create php.ini for PHP %s", version)
		}
	}
	return os.WriteFile(inst.ConfigFile, []byte(content), 0644)
}

// SetConfig updates a single php.ini directive for the given PHP version.
// It rewrites the ini file in place.
func (m *Manager) SetConfig(version, key, value string) error {
	inst, ok := m.findInstallPtr(version)
	if !ok {
		return fmt.Errorf("PHP %s not found", version)
	}
	if inst.ConfigFile == "" {
		info, _ := m.runPHP(inst.Binary, "-i")
		inst.ConfigFile = findOrCreatePHPConfig(inst.Version, info)
		if inst.ConfigFile == "" {
			return fmt.Errorf("cannot create php.ini for PHP %s", version)
		}
	}

	return updateINI(inst.ConfigFile, key, value)
}

// updateINI rewrites a php.ini file, setting key = value. If the key exists
// (even commented out) the line is replaced; otherwise it is appended.
func updateINI(path, key, value string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	lines := strings.Split(string(data), "\n")
	found := false
	newLine := key + " = " + value
	prefix := key + " "
	prefixEq := key + "="
	commentPrefix := ";" + key

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, prefix) || strings.HasPrefix(trimmed, prefixEq) || strings.HasPrefix(trimmed, commentPrefix) {
			lines[i] = newLine
			found = true
			break
		}
	}
	if !found {
		lines = append(lines, newLine)
	}

	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0644)
}

// GetExtensions returns the list of enabled extensions for the given version.
func (m *Manager) GetExtensions(version string) ([]string, error) {
	inst, ok := m.findInstall(version)
	if !ok {
		return nil, fmt.Errorf("PHP %s not found", version)
	}
	return inst.Extensions, nil
}

// StartFPM starts a php-cgi process listening on the given address (e.g. "127.0.0.1:9000").
// It uses `php-cgi -b <addr>` as a lightweight FastCGI server.
func (m *Manager) StartFPM(version, listenAddr string) error {
	if _, loaded := m.processes.Load(version); loaded {
		return fmt.Errorf("PHP %s is already running", version)
	}

	inst, ok := m.findInstall(version)
	if !ok {
		return fmt.Errorf("PHP %s not found", version)
	}
	if inst.SAPI != "cgi-fcgi" && inst.SAPI != "fpm-fcgi" {
		return fmt.Errorf("PHP %s binary %s is %s, not cgi-fcgi — install php-cgi or php-fpm", version, inst.Binary, inst.SAPI)
	}

	// If php-fpm binary exists, prefer it (proper process manager)
	fpmBinary := strings.Replace(inst.Binary, "php-cgi", "php-fpm", 1)
	if inst.SAPI == "fpm-fcgi" || fileExists(fpmBinary) {
		return m.startFPMDaemon(version, fpmBinary, listenAddr)
	}

	cmd := m.execCommand(inst.Binary, "-b", listenAddr)
	// PHP_FCGI_CHILDREN: spawn N worker children for parallel requests.
	// Without this, php-cgi handles only 1 request at a time!
	cmd.Env = append(os.Environ(),
		"PHP_FCGI_CHILDREN=8",
		"PHP_FCGI_MAX_REQUESTS=500",
	)
	cmd.Stdout = m.logger.Writer(4) // slog.LevelInfo
	cmd.Stderr = m.logger.Writer(8) // slog.LevelError

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start php-cgi: %w", err)
	}

	m.processes.Store(version, &processInfo{
		cmd:        cmd,
		listenAddr: listenAddr,
	})

	m.logger.Info("started PHP-CGI", "version", version, "listen", listenAddr, "pid", cmd.Process.Pid)

	// Reap the process in the background.
	go func() {
		err := cmd.Wait()
		m.processes.Delete(version)
		if err != nil {
			m.logger.Warn("PHP-CGI exited", "version", version, "error", err)
		} else {
			m.logger.Info("PHP-CGI exited", "version", version)
		}
	}()

	return nil
}

// startFPMDaemon starts php-fpm as a proper daemon with worker pool.
func (m *Manager) startFPMDaemon(version, binary, listenAddr string) error {
	// Generate a minimal php-fpm config for this listen address
	confDir := filepath.Join(os.TempDir(), "uwas-fpm")
	osMkdirAllHook(confDir, 0755)
	confPath := filepath.Join(confDir, fmt.Sprintf("php%s-fpm.conf", strings.ReplaceAll(version, ".", "")))

	conf := fmt.Sprintf(`[global]
pid = %s/php%s-fpm.pid
error_log = /dev/stderr
daemonize = no

[www]
listen = %s
pm = dynamic
pm.max_children = 10
pm.start_servers = 4
pm.min_spare_servers = 2
pm.max_spare_servers = 6
pm.max_requests = 500
`, confDir, strings.ReplaceAll(version, ".", ""), listenAddr)

	if err := osWriteFileHook(confPath, []byte(conf), 0644); err != nil {
		return fmt.Errorf("write fpm config: %w", err)
	}

	cmd := m.execCommand(binary, "--fpm-config", confPath, "--nodaemonize")
	cmd.Stdout = m.logger.Writer(4)
	cmd.Stderr = m.logger.Writer(8)

	if err := cmd.Start(); err != nil {
		// Fallback: try as php-cgi
		return fmt.Errorf("start php-fpm: %w", err)
	}

	m.processes.Store(version, &processInfo{
		cmd:        cmd,
		listenAddr: listenAddr,
	})

	m.logger.Info("started PHP-FPM", "version", version, "listen", listenAddr, "pid", cmd.Process.Pid, "workers", 10)

	go func() {
		err := cmd.Wait()
		m.processes.Delete(version)
		if err != nil {
			m.logger.Warn("PHP-FPM exited", "version", version, "error", err)
		}
	}()

	return nil
}

func fileExists(path string) bool {
	_, err := osStat(path)
	return err == nil
}

// StopFPM stops the PHP-CGI process for the given version.
func (m *Manager) StopFPM(version string) error {
	val, ok := m.processes.Load(version)
	if !ok {
		return fmt.Errorf("PHP %s is not running", version)
	}

	info, ok := val.(*processInfo)
	if !ok || info == nil || info.cmd == nil || info.cmd.Process == nil {
		m.processes.Delete(version)
		m.logger.Warn("stale PHP-CGI process entry removed", "version", version)
		return nil
	}
	if err := info.cmd.Process.Kill(); err != nil {
		return fmt.Errorf("kill php-cgi: %w", err)
	}

	m.processes.Delete(version)
	m.logger.Info("stopped PHP-CGI", "version", version)
	return nil
}

// RestartFPM gracefully restarts the PHP process for the given version.
// It stops the running process and starts a new one with the same listen address,
// ensuring that updated php.ini settings take effect.
func (m *Manager) RestartFPM(version string) error {
	val, ok := m.processes.Load(version)
	if !ok {
		return fmt.Errorf("PHP %s is not running", version)
	}
	info, ok := val.(*processInfo)
	if !ok || info == nil {
		m.processes.Delete(version)
		return fmt.Errorf("PHP %s has stale process entry", version)
	}
	listenAddr := info.listenAddr

	if err := m.StopFPM(version); err != nil {
		return fmt.Errorf("restart: stop failed: %w", err)
	}
	if err := m.StartFPM(version, listenAddr); err != nil {
		return fmt.Errorf("restart: start failed: %w", err)
	}
	m.logger.Info("restarted PHP", "version", version, "listen", listenAddr)
	return nil
}

// PHPStatus represents the status of a single PHP installation.
type PHPStatus struct {
	PHPInstall
	Running       bool     `json:"running"`
	ListenAddr    string   `json:"listen_addr,omitempty"`
	SocketPath    string   `json:"socket_path,omitempty"`
	PID           int      `json:"pid,omitempty"`
	SystemManaged bool     `json:"system_managed,omitempty"`
	DomainCount   int      `json:"domain_count"`      // number of domains using this version
	Domains       []string `json:"domains,omitempty"` // domain names using this version
}

// Status returns the status of all detected PHP installations, including
// whether each one has a running subprocess or system php-fpm socket.
func (m *Manager) Status() []PHPStatus {
	m.mu.RLock()
	installs := make([]PHPInstall, len(m.installations))
	copy(installs, m.installations)
	m.mu.RUnlock()

	var statuses []PHPStatus
	for _, inst := range installs {
		// Skip CLI binaries — they can't serve FastCGI.
		if inst.SAPI == "cli" {
			continue
		}
		st := PHPStatus{PHPInstall: inst}

		// Check for UWAS-managed process
		if val, ok := m.processes.Load(inst.Version); ok {
			info := val.(*processInfo)
			// Verify the process is actually running
			if info.cmd != nil && info.cmd.Process != nil && m.isProcessRunning(info.cmd.Process.Pid) {
				st.Running = true
				st.ListenAddr = info.listenAddr
				st.PID = info.cmd.Process.Pid
			} else {
				// Process is dead, clean up
				m.processes.Delete(inst.Version)
			}
		}

		// Check for system php-fpm socket (only for FPM SAPI)
		if strings.Contains(inst.SAPI, "fpm") {
			sock := m.detectSystemFPMSocket(inst.Version)
			if sock != "" {
				st.SocketPath = sock
				st.Running = true
				st.SystemManaged = true
				if st.ListenAddr == "" {
					st.ListenAddr = sock
				}
			}
		}

		// Count domains using this PHP version (match by short version e.g. "8.3")
		short := inst.Version
		if parts := strings.SplitN(inst.Version, ".", 3); len(parts) >= 2 {
			short = parts[0] + "." + parts[1]
		}
		m.domainMu.RLock()
		for domain, di := range m.domainMap {
			diShort := di.version
			if parts := strings.SplitN(di.version, ".", 3); len(parts) >= 2 {
				diShort = parts[0] + "." + parts[1]
			}
			if diShort == short {
				st.DomainCount++
				st.Domains = append(st.Domains, domain)
			}
		}
		m.domainMu.RUnlock()

		statuses = append(statuses, st)
	}
	return statuses
}

// isProcessRunning checks if a process with the given PID is actually running.
func (m *Manager) isProcessRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	// Try to find the process - this works on both Unix and Windows
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, signal 0 checks if process exists without sending a real signal
	// On Windows, this may not work, so we just rely on FindProcess success
	if runtime.GOOS != "windows" {
		return p.Signal(syscall.Signal(0)) == nil
	}
	// On Windows, FindProcess succeeding is enough indication the process exists
	return true
}

// EnableVersion enables all binaries of a PHP version for use.
func (m *Manager) EnableVersion(version string) {
	short := shortVersion(version)
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.installations {
		if shortVersion(m.installations[i].Version) == short {
			m.installations[i].Disabled = false
		}
	}
}

// DisableVersion disables all binaries of a PHP version.
// Returns error if any domains are still using this version.
func (m *Manager) DisableVersion(version string) error {
	short := shortVersion(version)

	// Check if any domains are assigned to this version
	m.domainMu.RLock()
	var attached []string
	for domain, di := range m.domainMap {
		if shortVersion(di.version) == short {
			attached = append(attached, domain)
		}
	}
	m.domainMu.RUnlock()

	if len(attached) > 0 {
		return fmt.Errorf("cannot disable PHP %s — %d domain(s) attached: %s", version, len(attached), strings.Join(attached, ", "))
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.installations {
		if shortVersion(m.installations[i].Version) == short {
			m.installations[i].Disabled = true
		}
	}
	return nil
}

// shortVersion extracts "8.3" from "8.3.30".
func shortVersion(v string) string {
	parts := strings.SplitN(v, ".", 3)
	if len(parts) >= 2 {
		return parts[0] + "." + parts[1]
	}
	return v
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

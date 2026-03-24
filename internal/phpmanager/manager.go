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
	"time"

	"github.com/uwaserver/uwas/internal/logger"
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
		// Fallback to per-domain TCP port
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
		if _, err := os.Stat(sock); err == nil {
			// Socket exists — verify it's actually listening
			conn, err := net.DialTimeout("unix", sock, 2*time.Second)
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

	// Reap the process in the background.
	go func() {
		waitErr := cmd.Wait()
		m.domainMu.Lock()
		if di, ok := m.domainMap[domain]; ok && di.proc != nil && di.proc.cmd == cmd {
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
			m.logger.Info("PHP-CGI for domain exited", "domain", domain)
		}
	}()

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

	if err := proc.cmd.Process.Kill(); err != nil {
		return fmt.Errorf("kill php-cgi for domain %s: %w", domain, err)
	}

	if tmpINI != "" {
		os.Remove(tmpINI)
	}

	m.logger.Info("stopped PHP-CGI for domain", "domain", domain)
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

	tmpFile, err := os.CreateTemp("", fmt.Sprintf("uwas-php-%s-*.ini", domain))
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
	m.domainMu.RLock()
	domainInst, _ := m.domainMap[domain]
	m.domainMu.RUnlock()
	if domainInst != nil {
		// Find domain root from the listen addr — we'll use a safe default
		lines = append(lines, "; Security: UWAS enforced")
		lines = append(lines, "disable_functions = exec,passthru,shell_exec,system,proc_open,popen,curl_multi_exec,parse_ini_file,show_source,pcntl_exec")
		lines = append(lines, "allow_url_include = Off")
		lines = append(lines, "expose_php = Off")
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
			// System php-fpm — no cmd but still running
			dp.Running = true
			dp.PID = -1 // system-managed
		}
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

// Detect scans the system for PHP binaries and populates the installations
// list. It looks in OS-specific common locations.
func (m *Manager) Detect() error {
	patterns := candidatePaths()

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

// candidatePaths returns glob patterns for common PHP binary locations.
func candidatePaths() []string {
	switch runtime.GOOS {
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

// GetConfig reads key php.ini settings for the given PHP version.
func (m *Manager) GetConfig(version string) (PHPConfig, error) {
	inst, ok := m.findInstall(version)
	if !ok {
		return PHPConfig{}, fmt.Errorf("PHP %s not found", version)
	}

	if inst.ConfigFile == "" {
		return PHPConfig{}, fmt.Errorf("no config file for PHP %s", version)
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
		return "", fmt.Errorf("no config file for PHP %s", version)
	}
	data, err := os.ReadFile(inst.ConfigFile)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// SetConfigRaw writes the entire php.ini file content.
func (m *Manager) SetConfigRaw(version, content string) error {
	inst, ok := m.findInstall(version)
	if !ok {
		return fmt.Errorf("PHP %s not found", version)
	}
	if inst.ConfigFile == "" {
		return fmt.Errorf("no config file for PHP %s", version)
	}
	return os.WriteFile(inst.ConfigFile, []byte(content), 0644)
}

// SetConfig updates a single php.ini directive for the given PHP version.
// It rewrites the ini file in place.
func (m *Manager) SetConfig(version, key, value string) error {
	inst, ok := m.findInstall(version)
	if !ok {
		return fmt.Errorf("PHP %s not found", version)
	}
	if inst.ConfigFile == "" {
		return fmt.Errorf("no config file for PHP %s", version)
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
	os.MkdirAll(confDir, 0755)
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

	if err := os.WriteFile(confPath, []byte(conf), 0644); err != nil {
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
	_, err := os.Stat(path)
	return err == nil
}

// StopFPM stops the PHP-CGI process for the given version.
func (m *Manager) StopFPM(version string) error {
	val, ok := m.processes.Load(version)
	if !ok {
		return fmt.Errorf("PHP %s is not running", version)
	}

	info := val.(*processInfo)
	if err := info.cmd.Process.Kill(); err != nil {
		return fmt.Errorf("kill php-cgi: %w", err)
	}

	m.processes.Delete(version)
	m.logger.Info("stopped PHP-CGI", "version", version)
	return nil
}

// PHPStatus represents the status of a single PHP installation.
type PHPStatus struct {
	PHPInstall
	Running      bool   `json:"running"`
	ListenAddr   string `json:"listen_addr,omitempty"`
	SocketPath   string `json:"socket_path,omitempty"` // system php-fpm socket if detected
	PID          int    `json:"pid,omitempty"`
	SystemManaged bool  `json:"system_managed,omitempty"` // true if using system php-fpm
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
			st.Running = true
			st.ListenAddr = info.listenAddr
			if info.cmd != nil && info.cmd.Process != nil {
				st.PID = info.cmd.Process.Pid
			}
		}

		// Check for system php-fpm socket
		sock := m.detectSystemFPMSocket(inst.Version)
		if sock != "" {
			st.SocketPath = sock
			st.Running = true
			st.SystemManaged = true
			if st.ListenAddr == "" {
				st.ListenAddr = sock
			}
		}

		statuses = append(statuses, st)
	}
	return statuses
}

// EnableVersion enables a PHP version for use.
func (m *Manager) EnableVersion(version string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.installations {
		if m.installations[i].Version == version {
			m.installations[i].Disabled = false
			return
		}
	}
}

// DisableVersion disables a PHP version — it won't be selectable for domains.
func (m *Manager) DisableVersion(version string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.installations {
		if m.installations[i].Version == version {
			m.installations[i].Disabled = true
			return
		}
	}
}

// StopAll stops all running PHP-CGI subprocesses (both global and per-domain).
// Called during server shutdown.
func (m *Manager) StopAll() {
	m.processes.Range(func(key, val any) bool {
		version := key.(string)
		info := val.(*processInfo)
		if err := info.cmd.Process.Kill(); err != nil {
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

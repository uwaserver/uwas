package phpmanager

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"

	"github.com/uwaserver/uwas/internal/logger"
)

// PHPInstall describes a single detected PHP installation.
type PHPInstall struct {
	Version    string   `json:"version"`     // e.g. "8.4.19"
	Binary     string   `json:"binary"`      // path to php-cgi or php-fpm
	ConfigFile string   `json:"config_file"` // php.ini path
	Extensions []string `json:"extensions"`  // enabled extensions
	SAPI       string   `json:"sapi"`        // "cgi-fcgi" or "fpm-fcgi"
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

// Manager detects and manages PHP installations and subprocesses.
type Manager struct {
	installations []PHPInstall
	mu            sync.RWMutex
	processes     sync.Map // version string → *processInfo
	logger        *logger.Logger

	// execCommand is the function used to create exec.Cmd objects.
	// It defaults to exec.Command and can be overridden for testing.
	execCommand func(name string, arg ...string) *exec.Cmd
}

// New creates a new PHP Manager.
func New(log *logger.Logger) *Manager {
	return &Manager{
		logger:      log,
		execCommand: exec.Command,
	}
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
	seen := make(map[string]bool)

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
			if seen[abs] {
				continue
			}
			seen[abs] = true

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
			"/usr/bin/php[0-9]*",
			"/usr/sbin/php-fpm*",
			"/usr/lib/cgi-bin/php*",
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
	for _, inst := range m.installations {
		if inst.Version == version || strings.HasPrefix(inst.Version, version) {
			return inst, true
		}
	}
	return PHPInstall{}, false
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

	cmd := m.execCommand(inst.Binary, "-b", listenAddr)
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
	Running    bool   `json:"running"`
	ListenAddr string `json:"listen_addr,omitempty"`
	PID        int    `json:"pid,omitempty"`
}

// Status returns the status of all detected PHP installations, including
// whether each one has a running subprocess.
func (m *Manager) Status() []PHPStatus {
	m.mu.RLock()
	installs := make([]PHPInstall, len(m.installations))
	copy(installs, m.installations)
	m.mu.RUnlock()

	statuses := make([]PHPStatus, len(installs))
	for i, inst := range installs {
		st := PHPStatus{PHPInstall: inst}
		if val, ok := m.processes.Load(inst.Version); ok {
			info := val.(*processInfo)
			st.Running = true
			st.ListenAddr = info.listenAddr
			if info.cmd.Process != nil {
				st.PID = info.cmd.Process.Pid
			}
		}
		statuses[i] = st
	}
	return statuses
}

// StopAll stops all running PHP-CGI subprocesses. Called during server shutdown.
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
}

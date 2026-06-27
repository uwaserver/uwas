// PHP installation detection and version selection. Split out of
// manager.go per refactor.md A14: discovers PHP binaries by walking
// OS-specific candidate paths, probes each candidate for version /
// SAPI / config file / extensions, and exposes Enable/DisableVersion
// + the in-memory installations slice that the rest of the package
// consults.
//
// Stays in the same package so manager.go can use these helpers
// without an import cycle; logically these are "discovery time"
// operations as opposed to lifecycle/runtime ones in fpm.go.
package phpmanager

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

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

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return "", err
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()

	select {
	case err := <-done:
		if err != nil {
			if msg := strings.TrimSpace(stderr.String()); msg != "" {
				return "", fmt.Errorf("%w: %s", err, msg)
			}
			return "", err
		}
	case <-timer.C:
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		select {
		case <-done:
		case <-time.After(500 * time.Millisecond):
		}
		return "", fmt.Errorf("php probe timed out after 3s")
	}

	return stdout.String(), nil
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

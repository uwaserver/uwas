// Per-version PHP INI configuration. Split out of manager.go per
// refactor.md A14. Owns reading php.ini for a detected version
// (GetConfig, GetConfigRaw), writing it back (SetConfig, SetConfigRaw,
// updateINI), and the small INI-parser the rest of the package uses
// to populate PHPConfig from disk.
package phpmanager

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// iniMu serializes php.ini read-modify-write cycles: two concurrent SetConfig
// calls for different keys otherwise lose one update (F8510).
var iniMu sync.Mutex

// writeINIAtomic replaces the php.ini at path through a temp file in the same
// directory and a rename, keeping the file's mode and following a symlinked
// php.ini to its target, so FPM never reads a truncated file.
func writeINIAtomic(path string, data []byte) error {
	target := path
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		target = resolved
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(target); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), filepath.Base(target)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	fail := func(err error) error {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Chmod(mode); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, target); err != nil {
		os.Remove(name)
		return err
	}
	return nil
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
	scanner.Buffer(make([]byte, 4096), 1<<20) // 4KB initial, 1MB max token size
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
	path, err := m.configFileFor(version)
	if err != nil {
		return err
	}
	iniMu.Lock()
	defer iniMu.Unlock()
	return writeINIAtomic(path, []byte(content))
}

// configFileFor returns the php.ini path for version, creating the file and
// recording it on the installation when none was detected. The record is
// written under m.mu: the old code assigned through a pointer taken from
// findInstallPtr after the lock was released, racing every reader of the
// installation list (F29100).
func (m *Manager) configFileFor(version string) (string, error) {
	inst, ok := m.findInstall(version)
	if !ok {
		return "", fmt.Errorf("PHP %s not found", version)
	}
	if inst.ConfigFile != "" {
		return inst.ConfigFile, nil
	}
	info, _ := m.runPHP(inst.Binary, "-i")
	path := findOrCreatePHPConfig(inst.Version, info)
	if path == "" {
		return "", fmt.Errorf("cannot create php.ini for PHP %s", version)
	}
	m.mu.Lock()
	for i := range m.installations {
		cur := &m.installations[i]
		if cur.Version == inst.Version && cur.Binary == inst.Binary && cur.SAPI == inst.SAPI && cur.ConfigFile == "" {
			cur.ConfigFile = path
		}
	}
	m.mu.Unlock()
	return path, nil
}

// SetConfig updates a single php.ini directive for the given PHP version.
// It rewrites the ini file in place.
func (m *Manager) SetConfig(version, key, value string) error {
	path, err := m.configFileFor(version)
	if err != nil {
		return err
	}
	return updateINI(path, key, value)
}

// afterINIRead, when set (tests only), runs between updateINI's read and write.
var afterINIRead func()

// updateINI rewrites a php.ini file, setting key = value. If the key exists
// (even commented out) the line is replaced; otherwise it is appended.
func updateINI(path, key, value string) error {
	iniMu.Lock()
	defer iniMu.Unlock()
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	if afterINIRead != nil {
		afterINIRead()
	}
	lines := strings.Split(string(data), "\n")
	newLine := key + " = " + value

	// PHP honours the last active assignment, so replace that one. A
	// commented-out line is only a fallback: replacing it while an active
	// line exists left the effective value unchanged.
	idx, commented := -1, -1
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if !iniLineSetsKey(t, key) {
			continue
		}
		if strings.HasPrefix(t, ";") {
			if commented < 0 {
				commented = i
			}
		} else {
			idx = i
		}
	}
	if idx < 0 {
		idx = commented
	}
	if idx >= 0 {
		lines[idx] = newLine
	} else {
		lines = append(lines, newLine)
	}

	return writeINIAtomic(path, []byte(strings.Join(lines, "\n")))
}

// iniLineSetsKey reports whether a trimmed php.ini line assigns key (whether
// active or commented out). It requires the key to be followed by an "="
// (allowing whitespace) so setting "opcache.enable" does not clobber an
// unrelated ";opcache.enable_cli = 1" line — the old prefix match did.
func iniLineSetsKey(trimmed, key string) bool {
	rest := strings.TrimSpace(strings.TrimPrefix(trimmed, ";"))
	if !strings.HasPrefix(rest, key) {
		return false
	}
	after := strings.TrimLeft(rest[len(key):], " \t")
	return strings.HasPrefix(after, "=")
}

// GetExtensions returns the list of enabled extensions for the given version.
func (m *Manager) GetExtensions(version string) ([]string, error) {
	inst, ok := m.findInstall(version)
	if !ok {
		return nil, fmt.Errorf("PHP %s not found", version)
	}
	return inst.Extensions, nil
}

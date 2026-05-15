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
	"strings"
)

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

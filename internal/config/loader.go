package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

var envPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

// Load reads the main config file, then loads any included files and
// per-domain configs from domains_dir.
//
// Directory structure:
//
//	/etc/uwas/
//	├── uwas.yaml              ← main config (global + optional inline domains)
//	├── domains.d/             ← per-domain YAML files (auto-loaded)
//	│   ├── example.com.yaml
//	│   ├── blog.example.com.yaml
//	│   └── api.example.com.yaml
//	└── includes/              ← extra config fragments
//	    └── shared-security.yaml
//
// Config merging:
//   - Global settings come from the main file only
//   - Domains from main file, include files, and domains_dir are all merged
//   - domains_dir files can contain a single domain (no "domains:" wrapper needed)
func Load(path string) (*Config, error) {
	baseDir := filepath.Dir(path)

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	// Strip UTF-8 BOM if present (common with Windows editors like Notepad)
	data = stripBOM(data)

	expanded := expandEnvVars(string(data))

	cfg := &Config{}
	if err := yaml.Unmarshal([]byte(expanded), cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	cfg.Warnings = append(cfg.Warnings, unknownKeys(filepath.Base(path), []byte(expanded), &Config{})...)

	// Process include patterns
	for _, pattern := range cfg.Include {
		if !filepath.IsAbs(pattern) {
			pattern = filepath.Join(baseDir, pattern)
		}
		includeDomains, ws, err := loadGlobWarn(pattern)
		if err != nil {
			return nil, fmt.Errorf("include %q: %w", pattern, err)
		}
		cfg.Domains = append(cfg.Domains, includeDomains...)
		cfg.Warnings = append(cfg.Warnings, ws...)
	}

	// Process domains_dir
	if cfg.DomainsDir != "" {
		dir := cfg.DomainsDir
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(baseDir, dir)
		}
		dirDomains, ws, err := loadDomainsDirWarn(dir)
		if err != nil {
			return nil, fmt.Errorf("domains_dir %q: %w", dir, err)
		}
		cfg.Domains = append(cfg.Domains, dirDomains...)
		cfg.Warnings = append(cfg.Warnings, ws...)
	}

	// Auto-detect domains.d/ next to config file
	autoDir := filepath.Join(baseDir, "domains.d")
	if cfg.DomainsDir == "" {
		if info, err := os.Stat(autoDir); err == nil && info.IsDir() {
			dirDomains, ws, err := loadDomainsDirWarn(autoDir)
			if err != nil {
				return nil, fmt.Errorf("auto domains.d: %w", err)
			}
			cfg.Domains = append(cfg.Domains, dirDomains...)
			cfg.Warnings = append(cfg.Warnings, ws...)
		}
	}

	applyDefaults(cfg)

	if err := Validate(cfg); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	return cfg, nil
}

// loadGlob loads domains from files matching a glob pattern.
// Each file can contain either:
//   - A full config with "domains:" list
//   - A single domain object (no wrapper)
func loadGlob(pattern string) ([]Domain, error) {
	domains, _, err := loadGlobWarn(pattern)
	return domains, err
}

// loadGlobWarn is loadGlob that also returns what the files contained that
// the schema does not know (see Config.Warnings).
func loadGlobWarn(pattern string) ([]Domain, []string, error) {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, nil, err
	}

	sort.Strings(matches) // deterministic order

	var all []Domain
	var warnings []string
	for _, path := range matches {
		domains, ws, err := loadDomainFileWarn(path)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
		}
		all = append(all, domains...)
		warnings = append(warnings, ws...)
	}
	return all, warnings, nil
}

// loadDomainsDir loads all .yaml/.yml files from a directory.
func loadDomainsDir(dir string) ([]Domain, error) {
	domains, _, err := loadDomainsDirWarn(dir)
	return domains, err
}

// loadDomainsDirWarn is loadDomainsDir that also returns warnings.
func loadDomainsDirWarn(dir string) ([]Domain, []string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}

	var all []Domain
	var warnings []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			continue
		}
		// Editor lock and swap artefacts (Emacs ".#name.yaml", a dangling
		// symlink) are not domain files (F56302).
		if strings.HasPrefix(name, ".") {
			continue
		}

		path := filepath.Join(dir, name)
		domains, ws, err := loadDomainFileWarn(path)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", name, err)
		}
		all = append(all, domains...)
		warnings = append(warnings, ws...)
	}
	return all, warnings, nil
}

// loadDomainFile loads domains from a single YAML file.
// Supports two formats:
//
// Format 1 — Single domain (recommended for domains.d/):
//
//	host: example.com
//	root: /var/www/example
//	type: static
//	ssl:
//	  mode: auto
//
// Format 2 — Domain list (for include files):
//
//	domains:
//	  - host: example.com
//	    ...
//	  - host: other.com
//	    ...
func loadDomainFile(path string) ([]Domain, error) {
	domains, _, err := loadDomainFileWarn(path)
	return domains, err
}

// LoadDomainFile loads the domains one domains.d file defines, with the same
// rules Load applies (environment expansion, single-domain or domains-list
// format). The admin API uses it to find out which file a host came from.
func LoadDomainFile(path string) ([]Domain, error) { return loadDomainFile(path) }

// loadDomainFileWarn is loadDomainFile that also returns what the file
// contained that the loader could not act on: keys the schema does not have,
// and a file that names no host and so contributes no domain.
func loadDomainFileWarn(path string) ([]Domain, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read: %w", err)
	}
	data = stripBOM(data)

	expanded := expandEnvVars(string(data))
	name := filepath.Base(path)

	// Try Format 2 first: { domains: [...] }
	var wrapper struct {
		Domains []Domain `yaml:"domains"`
	}
	if err := yaml.Unmarshal([]byte(expanded), &wrapper); err != nil {
		return nil, nil, fmt.Errorf("parse: %w", err)
	}

	// Try Format 1: single domain object
	var domain Domain
	if err := yaml.Unmarshal([]byte(expanded), &domain); err != nil {
		return nil, nil, fmt.Errorf("parse: %w", err)
	}

	if len(wrapper.Domains) > 0 {
		// A file is one format or the other. Mixing them used to load only the
		// list and silently drop the top-level domain, so what was loaded was
		// not what a single-domain reader (e.g. the raw editor) had checked.
		if domain.Host != "" {
			return nil, nil, fmt.Errorf("file defines both a top-level host and a domains list")
		}
		type listFile struct {
			Domains []Domain `yaml:"domains"`
		}
		return wrapper.Domains, unknownKeys(name, []byte(expanded), &listFile{}), nil
	}

	if domain.Host == "" {
		// Empty or invalid file, skipped. A file with content but no host is
		// usually a misspelt "host" key, and the whole domain vanishes with it.
		var top map[string]interface{}
		if yaml.Unmarshal([]byte(expanded), &top) == nil && len(top) > 0 {
			return nil, []string{name + ": no host (and no domains list): file skipped, no domain loaded from it"}, nil
		}
		return nil, nil, nil
	}

	return []Domain{domain}, unknownKeys(name, []byte(expanded), &Domain{}), nil
}

// unknownKeys decodes data into out strictly and returns one line per key the
// schema does not have. The ordinary decode ignores such keys, so a misspelt
// setting ("ip_whitlist") is dropped without an error and the protection it
// was meant to switch on is silently absent (F13504). Any other decode
// failure was already reported by that ordinary decode and is not repeated.
func unknownKeys(source string, data []byte, out interface{}) []string {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	err := dec.Decode(out)
	if err == nil {
		return nil
	}
	var te *yaml.TypeError
	if !errors.As(err, &te) {
		return nil
	}
	var warnings []string
	for _, e := range te.Errors {
		if strings.Contains(e, "not found in type") {
			warnings = append(warnings, source+": unknown key ignored: "+e)
		}
	}
	return warnings
}

func expandEnvVars(s string) string {
	return envPattern.ReplaceAllStringFunc(s, func(match string) string {
		varName := match[2 : len(match)-1]

		if name, fallback, ok := strings.Cut(varName, ":-"); ok {
			// `:-` (POSIX/compose) falls back when the variable is unset or empty.
			if val, found := os.LookupEnv(name); found && val != "" {
				return val
			}
			return fallback
		}

		if val, ok := os.LookupEnv(varName); ok {
			return val
		}
		return match
	})
}

// stripBOM removes a UTF-8 Byte Order Mark (EF BB BF) from the beginning of data.
// Windows editors (Notepad, VS Code with BOM) prepend this; Go's YAML parser rejects it.
func stripBOM(data []byte) []byte {
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		return data[3:]
	}
	return data
}

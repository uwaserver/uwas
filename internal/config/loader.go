package config

import (
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

	// Process include patterns
	for _, pattern := range cfg.Include {
		if !filepath.IsAbs(pattern) {
			pattern = filepath.Join(baseDir, pattern)
		}
		includeDomains, err := loadGlob(pattern)
		if err != nil {
			return nil, fmt.Errorf("include %q: %w", pattern, err)
		}
		cfg.Domains = append(cfg.Domains, includeDomains...)
	}

	// Process domains_dir
	if cfg.DomainsDir != "" {
		dir := cfg.DomainsDir
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(baseDir, dir)
		}
		dirDomains, err := loadDomainsDir(dir)
		if err != nil {
			return nil, fmt.Errorf("domains_dir %q: %w", dir, err)
		}
		cfg.Domains = append(cfg.Domains, dirDomains...)
	}

	// Auto-detect domains.d/ next to config file
	autoDir := filepath.Join(baseDir, "domains.d")
	if cfg.DomainsDir == "" {
		if info, err := os.Stat(autoDir); err == nil && info.IsDir() {
			dirDomains, err := loadDomainsDir(autoDir)
			if err != nil {
				return nil, fmt.Errorf("auto domains.d: %w", err)
			}
			cfg.Domains = append(cfg.Domains, dirDomains...)
		}
	}

	applyDefaults(cfg)

	if err := validate(cfg); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	return cfg, nil
}

// loadGlob loads domains from files matching a glob pattern.
// Each file can contain either:
//   - A full config with "domains:" list
//   - A single domain object (no wrapper)
func loadGlob(pattern string) ([]Domain, error) {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}

	sort.Strings(matches) // deterministic order

	var all []Domain
	for _, path := range matches {
		domains, err := loadDomainFile(path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
		}
		all = append(all, domains...)
	}
	return all, nil
}

// loadDomainsDir loads all .yaml/.yml files from a directory.
func loadDomainsDir(dir string) ([]Domain, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var all []Domain
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			continue
		}

		path := filepath.Join(dir, name)
		domains, err := loadDomainFile(path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		all = append(all, domains...)
	}
	return all, nil
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
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	data = stripBOM(data)

	expanded := expandEnvVars(string(data))

	// Try Format 2 first: { domains: [...] }
	var wrapper struct {
		Domains []Domain `yaml:"domains"`
	}
	if err := yaml.Unmarshal([]byte(expanded), &wrapper); err == nil && len(wrapper.Domains) > 0 {
		return wrapper.Domains, nil
	}

	// Try Format 1: single domain object
	var domain Domain
	if err := yaml.Unmarshal([]byte(expanded), &domain); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}

	if domain.Host == "" {
		return nil, nil // empty or invalid file, skip
	}

	return []Domain{domain}, nil
}

func expandEnvVars(s string) string {
	return envPattern.ReplaceAllStringFunc(s, func(match string) string {
		varName := match[2 : len(match)-1]

		if name, fallback, ok := strings.Cut(varName, ":-"); ok {
			if val, found := os.LookupEnv(name); found {
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

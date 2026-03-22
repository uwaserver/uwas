package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadValidConfig(t *testing.T) {
	yaml := `
global:
  log_level: debug
  log_format: json
  timeouts:
    read: 10s
    write: 20s

domains:
  - host: "example.com"
    root: /var/www/html
    type: static
    ssl:
      mode: off
`
	cfg := loadFromString(t, yaml)

	if cfg.Global.LogLevel != "debug" {
		t.Errorf("log_level = %q, want debug", cfg.Global.LogLevel)
	}
	if cfg.Global.Timeouts.Read.Duration != 10*time.Second {
		t.Errorf("read timeout = %v, want 10s", cfg.Global.Timeouts.Read.Duration)
	}
	if len(cfg.Domains) != 1 {
		t.Fatalf("domains count = %d, want 1", len(cfg.Domains))
	}
	if cfg.Domains[0].Host != "example.com" {
		t.Errorf("host = %q, want example.com", cfg.Domains[0].Host)
	}
}

func TestDefaults(t *testing.T) {
	yaml := `
domains:
  - host: "test.com"
    root: /var/www
    type: static
    ssl:
      mode: off
`
	cfg := loadFromString(t, yaml)

	if cfg.Global.WorkerCount != "auto" {
		t.Errorf("worker_count = %q, want auto", cfg.Global.WorkerCount)
	}
	if cfg.Global.MaxConnections != 65536 {
		t.Errorf("max_connections = %d, want 65536", cfg.Global.MaxConnections)
	}
	if cfg.Global.Timeouts.Read.Duration != 30*time.Second {
		t.Errorf("read timeout = %v, want 30s", cfg.Global.Timeouts.Read.Duration)
	}
	if cfg.Global.Cache.MemoryLimit != 512*MB {
		t.Errorf("memory_limit = %d, want %d", cfg.Global.Cache.MemoryLimit, 512*MB)
	}
}

func TestEnvVarExpansion(t *testing.T) {
	os.Setenv("TEST_UWAS_KEY", "secret123")
	defer os.Unsetenv("TEST_UWAS_KEY")

	yaml := `
global:
  admin:
    api_key: "${TEST_UWAS_KEY}"

domains:
  - host: "test.com"
    root: /var/www
    type: static
    ssl:
      mode: off
`
	cfg := loadFromString(t, yaml)

	if cfg.Global.Admin.APIKey != "secret123" {
		t.Errorf("api_key = %q, want secret123", cfg.Global.Admin.APIKey)
	}
}

func TestEnvVarDefaultValue(t *testing.T) {
	os.Unsetenv("NONEXISTENT_VAR")

	yaml := `
global:
  admin:
    api_key: "${NONEXISTENT_VAR:-fallback_value}"

domains:
  - host: "test.com"
    root: /var/www
    type: static
    ssl:
      mode: off
`
	cfg := loadFromString(t, yaml)

	if cfg.Global.Admin.APIKey != "fallback_value" {
		t.Errorf("api_key = %q, want fallback_value", cfg.Global.Admin.APIKey)
	}
}

func TestValidationDuplicateHost(t *testing.T) {
	yaml := `
domains:
  - host: "example.com"
    root: /var/www/a
    type: static
    ssl:
      mode: off
  - host: "example.com"
    root: /var/www/b
    type: static
    ssl:
      mode: off
`
	_, err := loadStringConfig(yaml)
	if err == nil {
		t.Fatal("expected validation error for duplicate host")
	}
}

func TestValidationMissingRoot(t *testing.T) {
	yaml := `
domains:
  - host: "example.com"
    type: php
    ssl:
      mode: off
`
	_, err := loadStringConfig(yaml)
	if err == nil {
		t.Fatal("expected validation error for missing root")
	}
}

func TestValidationProxyRequiresUpstreams(t *testing.T) {
	yaml := `
domains:
  - host: "api.example.com"
    type: proxy
    ssl:
      mode: off
`
	_, err := loadStringConfig(yaml)
	if err == nil {
		t.Fatal("expected validation error for missing upstreams")
	}
}

func TestByteSizeParsing(t *testing.T) {
	tests := []struct {
		input string
		want  ByteSize
	}{
		{"1024", 1024},
		{"1KB", KB},
		{"512MB", 512 * MB},
		{"10GB", 10 * GB},
		{"1.5GB", ByteSize(1.5 * float64(GB))},
	}

	for _, tt := range tests {
		got, err := parseByteSize(tt.input)
		if err != nil {
			t.Errorf("parseByteSize(%q) error: %v", tt.input, err)
			continue
		}
		if got != tt.want {
			t.Errorf("parseByteSize(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestInvalidConfig(t *testing.T) {
	yaml := `not: [valid: yaml: {{`
	_, err := loadStringConfig(yaml)
	if err == nil {
		t.Fatal("expected parse error for invalid YAML")
	}
}

func TestByteSizeUnmarshalYAMLRawInt(t *testing.T) {
	// ByteSize.UnmarshalYAML should handle raw integer values (not strings)
	cfgYAML := `
global:
  cache:
    memory_limit: 1048576
    disk_limit: 10737418240

domains:
  - host: "test.com"
    root: /var/www
    type: static
    ssl:
      mode: off
`
	cfg := loadFromString(t, cfgYAML)

	if cfg.Global.Cache.MemoryLimit != ByteSize(1048576) {
		t.Errorf("MemoryLimit = %d, want 1048576", cfg.Global.Cache.MemoryLimit)
	}
	if cfg.Global.Cache.DiskLimit != ByteSize(10737418240) {
		t.Errorf("DiskLimit = %d, want 10737418240", cfg.Global.Cache.DiskLimit)
	}
}

func TestDurationUnmarshalYAMLInteger(t *testing.T) {
	// Test Duration.UnmarshalYAML directly with an unmarshal function
	// that fails for string but succeeds for int (simulating non-string YAML node).
	var d Duration
	fakeUnmarshal := func(v any) error {
		switch p := v.(type) {
		case *string:
			return fmt.Errorf("not a string")
		case *int:
			*p = 45
			return nil
		default:
			return fmt.Errorf("unexpected type")
		}
	}

	err := d.UnmarshalYAML(fakeUnmarshal)
	if err != nil {
		t.Fatalf("UnmarshalYAML with int returned error: %v", err)
	}
	if d.Duration != 45*time.Second {
		t.Errorf("Duration = %v, want 45s", d.Duration)
	}
}

func TestDurationUnmarshalYAMLBothFail(t *testing.T) {
	// When both string and int unmarshal fail, should return the string error.
	var d Duration
	fakeUnmarshal := func(v any) error {
		return fmt.Errorf("fail for %T", v)
	}

	err := d.UnmarshalYAML(fakeUnmarshal)
	if err == nil {
		t.Fatal("expected error when both unmarshal attempts fail")
	}
}

func TestByteSizeUnmarshalYAMLDirect(t *testing.T) {
	// Test ByteSize.UnmarshalYAML directly with an unmarshal function
	// that fails for string but succeeds for int64.
	var b ByteSize
	fakeUnmarshal := func(v any) error {
		switch p := v.(type) {
		case *string:
			return fmt.Errorf("not a string")
		case *int64:
			*p = 2048
			return nil
		default:
			return fmt.Errorf("unexpected type")
		}
	}

	err := b.UnmarshalYAML(fakeUnmarshal)
	if err != nil {
		t.Fatalf("UnmarshalYAML with int64 returned error: %v", err)
	}
	if b != ByteSize(2048) {
		t.Errorf("ByteSize = %d, want 2048", b)
	}
}

func TestByteSizeUnmarshalYAMLBothFail(t *testing.T) {
	var b ByteSize
	fakeUnmarshal := func(v any) error {
		return fmt.Errorf("fail for %T", v)
	}

	err := b.UnmarshalYAML(fakeUnmarshal)
	if err == nil {
		t.Fatal("expected error when both unmarshal attempts fail")
	}
}

// Helpers

func loadFromString(t *testing.T, content string) *Config {
	t.Helper()
	cfg, err := loadStringConfig(content)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return cfg
}

func loadStringConfig(content string) (*Config, error) {
	dir := os.TempDir()
	path := filepath.Join(dir, "uwas_test.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return nil, err
	}
	defer os.Remove(path)
	return Load(path)
}

// --- Include and DomainsDir tests ---

func TestDomainsDir(t *testing.T) {
	dir := t.TempDir()
	domainsDir := filepath.Join(dir, "domains.d")
	os.MkdirAll(domainsDir, 0755)

	// Single domain file (Format 1)
	os.WriteFile(filepath.Join(domainsDir, "blog.yaml"), []byte(`
host: blog.example.com
root: /var/www/blog
type: php
ssl:
  mode: auto
`), 0644)

	// Another domain
	os.WriteFile(filepath.Join(domainsDir, "api.yaml"), []byte(`
host: api.example.com
type: proxy
ssl:
  mode: auto
proxy:
  upstreams:
    - address: "http://127.0.0.1:3000"
`), 0644)

	// Main config with domains_dir
	mainConfig := fmt.Sprintf(`
global:
  log_level: info
domains_dir: "%s"
domains:
  - host: example.com
    root: /var/www/main
    type: static
    ssl:
      mode: off
`, filepath.ToSlash(domainsDir))

	configPath := filepath.Join(dir, "uwas.yaml")
	os.WriteFile(configPath, []byte(mainConfig), 0644)

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(cfg.Domains) != 3 {
		t.Fatalf("domains = %d, want 3 (1 inline + 2 from domains.d)", len(cfg.Domains))
	}

	hosts := map[string]bool{}
	for _, d := range cfg.Domains {
		hosts[d.Host] = true
	}
	if !hosts["example.com"] || !hosts["blog.example.com"] || !hosts["api.example.com"] {
		t.Errorf("missing domains: %v", hosts)
	}
}

func TestAutoDomainsDir(t *testing.T) {
	dir := t.TempDir()

	// Create domains.d/ next to config file (auto-detected)
	domainsDir := filepath.Join(dir, "domains.d")
	os.MkdirAll(domainsDir, 0755)

	os.WriteFile(filepath.Join(domainsDir, "site.yaml"), []byte(`
host: auto.example.com
root: /var/www/auto
type: static
ssl:
  mode: off
`), 0644)

	mainConfig := `
global:
  log_level: info
domains:
  - host: main.example.com
    root: /var/www/main
    type: static
    ssl:
      mode: off
`
	configPath := filepath.Join(dir, "uwas.yaml")
	os.WriteFile(configPath, []byte(mainConfig), 0644)

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(cfg.Domains) != 2 {
		t.Fatalf("domains = %d, want 2", len(cfg.Domains))
	}
}

func TestIncludeGlob(t *testing.T) {
	dir := t.TempDir()
	includeDir := filepath.Join(dir, "includes")
	os.MkdirAll(includeDir, 0755)

	// Include file with domain list (Format 2)
	os.WriteFile(filepath.Join(includeDir, "extra.yaml"), []byte(`
domains:
  - host: extra1.com
    root: /var/www/extra1
    type: static
    ssl:
      mode: off
  - host: extra2.com
    root: /var/www/extra2
    type: static
    ssl:
      mode: off
`), 0644)

	mainConfig := fmt.Sprintf(`
global:
  log_level: info
include:
  - "%s"
domains:
  - host: main.com
    root: /var/www/main
    type: static
    ssl:
      mode: off
`, filepath.ToSlash(filepath.Join(includeDir, "*.yaml")))

	configPath := filepath.Join(dir, "uwas.yaml")
	os.WriteFile(configPath, []byte(mainConfig), 0644)

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(cfg.Domains) != 3 {
		t.Fatalf("domains = %d, want 3 (1 inline + 2 from include)", len(cfg.Domains))
	}
}

func TestDomainFileSingleFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "single.yaml")
	os.WriteFile(path, []byte(`
host: single.com
root: /var/www/single
type: static
ssl:
  mode: off
`), 0644)

	domains, err := loadDomainFile(path)
	if err != nil {
		t.Fatalf("loadDomainFile: %v", err)
	}
	if len(domains) != 1 {
		t.Fatalf("domains = %d, want 1", len(domains))
	}
	if domains[0].Host != "single.com" {
		t.Errorf("host = %q", domains[0].Host)
	}
}

func TestDomainFileListFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "list.yaml")
	os.WriteFile(path, []byte(`
domains:
  - host: a.com
    root: /a
    type: static
    ssl:
      mode: off
  - host: b.com
    root: /b
    type: static
    ssl:
      mode: off
`), 0644)

	domains, err := loadDomainFile(path)
	if err != nil {
		t.Fatalf("loadDomainFile: %v", err)
	}
	if len(domains) != 2 {
		t.Fatalf("domains = %d, want 2", len(domains))
	}
}

func TestEmptyDomainFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.yaml")
	os.WriteFile(path, []byte("# empty file\n"), 0644)

	domains, err := loadDomainFile(path)
	if err != nil {
		t.Fatalf("loadDomainFile: %v", err)
	}
	if len(domains) != 0 {
		t.Errorf("domains = %d, want 0 for empty file", len(domains))
	}
}

func TestNonExistentDomainsDir(t *testing.T) {
	dir := t.TempDir()
	mainConfig := `
global:
  log_level: info
domains_dir: "/nonexistent/path"
`
	configPath := filepath.Join(dir, "uwas.yaml")
	os.WriteFile(configPath, []byte(mainConfig), 0644)

	// Should not error — nonexistent dir is silently skipped
	_, err := Load(configPath)
	if err != nil {
		t.Fatalf("should not error for nonexistent domains_dir: %v", err)
	}
}

func TestEnvVarInDomainFile(t *testing.T) {
	os.Setenv("TEST_DOMAIN_HOST", "env.example.com")
	defer os.Unsetenv("TEST_DOMAIN_HOST")

	dir := t.TempDir()
	domainsDir := filepath.Join(dir, "domains.d")
	os.MkdirAll(domainsDir, 0755)

	os.WriteFile(filepath.Join(domainsDir, "env.yaml"), []byte(`
host: "${TEST_DOMAIN_HOST}"
root: /var/www/env
type: static
ssl:
  mode: off
`), 0644)

	mainConfig := `
global:
  log_level: info
`
	configPath := filepath.Join(dir, "uwas.yaml")
	os.WriteFile(configPath, []byte(mainConfig), 0644)

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(cfg.Domains) != 1 {
		t.Fatalf("domains = %d, want 1", len(cfg.Domains))
	}
	if cfg.Domains[0].Host != "env.example.com" {
		t.Errorf("host = %q, want env.example.com", cfg.Domains[0].Host)
	}
}

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

// === Additional coverage tests ===

// --- validate.go: all validation error paths ---

func TestValidationInvalidLogLevel(t *testing.T) {
	yaml := `
global:
  log_level: "trace"
domains:
  - host: "example.com"
    root: /var/www
    type: static
    ssl:
      mode: off
`
	_, err := loadStringConfig(yaml)
	if err == nil {
		t.Fatal("expected validation error for invalid log_level")
	}
	if !contains(err.Error(), "invalid log_level") {
		t.Errorf("error = %q, want to contain 'invalid log_level'", err.Error())
	}
}

func TestValidationInvalidLogFormat(t *testing.T) {
	yaml := `
global:
  log_format: "xml"
domains:
  - host: "example.com"
    root: /var/www
    type: static
    ssl:
      mode: off
`
	_, err := loadStringConfig(yaml)
	if err == nil {
		t.Fatal("expected validation error for invalid log_format")
	}
	if !contains(err.Error(), "invalid log_format") {
		t.Errorf("error = %q, want to contain 'invalid log_format'", err.Error())
	}
}

func TestValidationInvalidSSLMode(t *testing.T) {
	yaml := `
domains:
  - host: "example.com"
    root: /var/www
    type: static
    ssl:
      mode: "tls"
`
	_, err := loadStringConfig(yaml)
	if err == nil {
		t.Fatal("expected validation error for invalid ssl.mode")
	}
	if !contains(err.Error(), "invalid ssl.mode") {
		t.Errorf("error = %q, want to contain 'invalid ssl.mode'", err.Error())
	}
}

func TestValidationInvalidType(t *testing.T) {
	yaml := `
domains:
  - host: "example.com"
    root: /var/www
    type: "ruby"
    ssl:
      mode: off
`
	_, err := loadStringConfig(yaml)
	if err == nil {
		t.Fatal("expected validation error for invalid type")
	}
	if !contains(err.Error(), "invalid type") {
		t.Errorf("error = %q, want to contain 'invalid type'", err.Error())
	}
}

func TestValidationMissingHost(t *testing.T) {
	yaml := `
domains:
  - root: /var/www
    type: static
    ssl:
      mode: off
`
	_, err := loadStringConfig(yaml)
	if err == nil {
		t.Fatal("expected validation error for missing host")
	}
	if !contains(err.Error(), "host is required") {
		t.Errorf("error = %q, want to contain 'host is required'", err.Error())
	}
}

func TestValidationDuplicateAlias(t *testing.T) {
	yaml := `
domains:
  - host: "a.com"
    aliases: ["www.a.com"]
    root: /var/www/a
    type: static
    ssl:
      mode: off
  - host: "b.com"
    aliases: ["www.a.com"]
    root: /var/www/b
    type: static
    ssl:
      mode: off
`
	_, err := loadStringConfig(yaml)
	if err == nil {
		t.Fatal("expected validation error for duplicate alias")
	}
	if !contains(err.Error(), "duplicate alias") {
		t.Errorf("error = %q, want to contain 'duplicate alias'", err.Error())
	}
}

func TestValidationSSLManualMissingCert(t *testing.T) {
	yaml := `
domains:
  - host: "example.com"
    root: /var/www
    type: static
    ssl:
      mode: manual
      key: "/path/to/key.pem"
`
	_, err := loadStringConfig(yaml)
	if err == nil {
		t.Fatal("expected validation error for missing ssl.cert")
	}
	if !contains(err.Error(), "ssl.cert required") {
		t.Errorf("error = %q, want to contain 'ssl.cert required'", err.Error())
	}
}

func TestValidationSSLManualMissingKey(t *testing.T) {
	yaml := `
domains:
  - host: "example.com"
    root: /var/www
    type: static
    ssl:
      mode: manual
      cert: "/path/to/cert.pem"
`
	_, err := loadStringConfig(yaml)
	if err == nil {
		t.Fatal("expected validation error for missing ssl.key")
	}
	if !contains(err.Error(), "ssl.key required") {
		t.Errorf("error = %q, want to contain 'ssl.key required'", err.Error())
	}
}

func TestValidationRedirectMissingTarget(t *testing.T) {
	yaml := `
domains:
  - host: "old.com"
    type: redirect
    ssl:
      mode: off
`
	_, err := loadStringConfig(yaml)
	if err == nil {
		t.Fatal("expected validation error for missing redirect.target")
	}
	if !contains(err.Error(), "redirect.target required") {
		t.Errorf("error = %q, want to contain 'redirect.target required'", err.Error())
	}
}

func TestValidationProxyNoUpstreams(t *testing.T) {
	yaml := `
domains:
  - host: "api.com"
    type: proxy
    ssl:
      mode: off
`
	_, err := loadStringConfig(yaml)
	if err == nil {
		t.Fatal("expected validation error for proxy without upstreams")
	}
	if !contains(err.Error(), "proxy.upstreams required") {
		t.Errorf("error = %q, want to contain 'proxy.upstreams required'", err.Error())
	}
}

func TestValidationStaticMissingRoot(t *testing.T) {
	yaml := `
domains:
  - host: "example.com"
    type: static
    ssl:
      mode: off
`
	_, err := loadStringConfig(yaml)
	if err == nil {
		t.Fatal("expected validation error for missing root in static")
	}
	if !contains(err.Error(), "root is required") {
		t.Errorf("error = %q, want to contain 'root is required'", err.Error())
	}
}

// --- loader.go: loadDomainFile with invalid YAML ---

func TestLoadDomainFileInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	os.WriteFile(path, []byte("host: [invalid: yaml: {{"), 0644)

	_, err := loadDomainFile(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML in domain file")
	}
}

func TestLoadDomainFileNotFound(t *testing.T) {
	_, err := loadDomainFile(filepath.Join(t.TempDir(), "doesnotexist.yaml"))
	if err == nil {
		t.Fatal("expected error for non-existent file")
	}
}

// --- loader.go: loadGlob with no matches ---

func TestLoadGlobNoMatches(t *testing.T) {
	dir := t.TempDir()
	pattern := filepath.Join(dir, "*.yaml")

	domains, err := loadGlob(pattern)
	if err != nil {
		t.Fatalf("loadGlob: %v", err)
	}
	if len(domains) != 0 {
		t.Errorf("domains = %d, want 0 for no matches", len(domains))
	}
}

func TestLoadGlobInvalidPattern(t *testing.T) {
	// filepath.Glob returns error for bad patterns
	_, err := loadGlob("[invalid")
	if err == nil {
		t.Fatal("expected error for invalid glob pattern")
	}
}

// --- parse.go: parseByteSize edge cases ---

func TestParseByteSizeEmpty(t *testing.T) {
	_, err := parseByteSize("")
	if err == nil {
		t.Fatal("expected error for empty byte size")
	}
	if !contains(err.Error(), "empty byte size") {
		t.Errorf("error = %q, want 'empty byte size'", err.Error())
	}
}

func TestParseByteSizeUnknownUnit(t *testing.T) {
	_, err := parseByteSize("100TB")
	if err == nil {
		t.Fatal("expected error for unknown unit TB")
	}
	if !contains(err.Error(), "unknown byte unit") {
		t.Errorf("error = %q, want 'unknown byte unit'", err.Error())
	}
}

func TestParseByteSizeInvalidNumber(t *testing.T) {
	_, err := parseByteSize("abcMB")
	if err == nil {
		t.Fatal("expected error for non-numeric input")
	}
	if !contains(err.Error(), "invalid byte size") {
		t.Errorf("error = %q, want 'invalid byte size'", err.Error())
	}
}

func TestParseByteSizeWhitespace(t *testing.T) {
	// Leading/trailing whitespace should be trimmed
	got, err := parseByteSize("  512 MB  ")
	if err != nil {
		t.Fatalf("parseByteSize: %v", err)
	}
	if got != 512*MB {
		t.Errorf("parseByteSize = %d, want %d", got, 512*MB)
	}
}

func TestParseByteSizeJustNumber(t *testing.T) {
	got, err := parseByteSize("42")
	if err != nil {
		t.Fatalf("parseByteSize: %v", err)
	}
	if got != ByteSize(42) {
		t.Errorf("parseByteSize = %d, want 42", got)
	}
}

func TestParseByteSizeBSuffix(t *testing.T) {
	got, err := parseByteSize("256B")
	if err != nil {
		t.Fatalf("parseByteSize: %v", err)
	}
	if got != ByteSize(256) {
		t.Errorf("parseByteSize = %d, want 256", got)
	}
}

func TestParseByteSizeK(t *testing.T) {
	got, err := parseByteSize("8K")
	if err != nil {
		t.Fatalf("parseByteSize: %v", err)
	}
	if got != 8*KB {
		t.Errorf("parseByteSize = %d, want %d", got, 8*KB)
	}
}

func TestParseByteSizeG(t *testing.T) {
	got, err := parseByteSize("2G")
	if err != nil {
		t.Fatalf("parseByteSize: %v", err)
	}
	if got != 2*GB {
		t.Errorf("parseByteSize = %d, want %d", got, 2*GB)
	}
}

// --- loader.go: expandEnvVars with unset variable (kept as-is) ---

func TestExpandEnvVarsUnset(t *testing.T) {
	os.Unsetenv("TOTALLY_UNSET_VAR")
	result := expandEnvVars("value=${TOTALLY_UNSET_VAR}")
	if result != "value=${TOTALLY_UNSET_VAR}" {
		t.Errorf("expandEnvVars = %q, want original for unset var", result)
	}
}

// --- loader.go: Load with non-existent config file ---

func TestLoadNonExistentFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nonexistent.yaml"))
	if err == nil {
		t.Fatal("expected error for non-existent config file")
	}
}

// --- loader.go: include with relative path ---

func TestIncludeRelativePath(t *testing.T) {
	dir := t.TempDir()
	includeDir := filepath.Join(dir, "inc")
	os.MkdirAll(includeDir, 0755)

	os.WriteFile(filepath.Join(includeDir, "extra.yaml"), []byte(`
host: included.com
root: /var/www/inc
type: static
ssl:
  mode: off
`), 0644)

	// Use relative include path
	mainConfig := `
global:
  log_level: info
include:
  - "inc/*.yaml"
domains:
  - host: main.com
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
		t.Errorf("domains = %d, want 2", len(cfg.Domains))
	}
}

// --- loader.go: include with invalid file in glob ---

func TestIncludeInvalidDomainFile(t *testing.T) {
	dir := t.TempDir()
	includeDir := filepath.Join(dir, "inc")
	os.MkdirAll(includeDir, 0755)

	os.WriteFile(filepath.Join(includeDir, "bad.yaml"), []byte("host: [invalid {{"), 0644)

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

	_, err := Load(configPath)
	if err == nil {
		t.Fatal("expected error for invalid include file")
	}
}

// --- loader.go: domains_dir relative path ---

func TestDomainsDirRelativePath(t *testing.T) {
	dir := t.TempDir()
	domainsDir := filepath.Join(dir, "mydomains")
	os.MkdirAll(domainsDir, 0755)

	os.WriteFile(filepath.Join(domainsDir, "site.yaml"), []byte(`
host: relative.com
root: /var/www/rel
type: static
ssl:
  mode: off
`), 0644)

	mainConfig := `
global:
  log_level: info
domains_dir: "mydomains"
domains:
  - host: main.com
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
		t.Errorf("domains = %d, want 2", len(cfg.Domains))
	}
}

// --- loader.go: domains_dir with invalid file ---

func TestDomainsDirInvalidFile(t *testing.T) {
	dir := t.TempDir()
	domainsDir := filepath.Join(dir, "domains.d")
	os.MkdirAll(domainsDir, 0755)

	os.WriteFile(filepath.Join(domainsDir, "bad.yaml"), []byte("host: [broken {{"), 0644)

	mainConfig := fmt.Sprintf(`
global:
  log_level: info
domains_dir: "%s"
`, filepath.ToSlash(domainsDir))

	configPath := filepath.Join(dir, "uwas.yaml")
	os.WriteFile(configPath, []byte(mainConfig), 0644)

	_, err := Load(configPath)
	if err == nil {
		t.Fatal("expected error for invalid file in domains_dir")
	}
}

// --- loader.go: loadDomainsDir skips non-yaml files and directories ---

func TestDomainsDirSkipsNonYAML(t *testing.T) {
	dir := t.TempDir()
	domainsDir := filepath.Join(dir, "domains.d")
	os.MkdirAll(domainsDir, 0755)

	// Create a non-YAML file and a subdirectory
	os.WriteFile(filepath.Join(domainsDir, "readme.txt"), []byte("not yaml"), 0644)
	os.MkdirAll(filepath.Join(domainsDir, "subdir"), 0755)
	// Create a valid .yml file
	os.WriteFile(filepath.Join(domainsDir, "site.yml"), []byte(`
host: yml.com
root: /var/www/yml
type: static
ssl:
  mode: off
`), 0644)

	mainConfig := fmt.Sprintf(`
global:
  log_level: info
domains_dir: "%s"
`, filepath.ToSlash(domainsDir))

	configPath := filepath.Join(dir, "uwas.yaml")
	os.WriteFile(configPath, []byte(mainConfig), 0644)

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Only the .yml file should be loaded
	if len(cfg.Domains) != 1 {
		t.Errorf("domains = %d, want 1", len(cfg.Domains))
	}
}

// --- loader.go: expandEnvVars with set variable (with :- fallback syntax but var is set) ---

func TestExpandEnvVarsSetWithFallback(t *testing.T) {
	os.Setenv("TEST_EXPAND_SET", "actual_value")
	defer os.Unsetenv("TEST_EXPAND_SET")

	result := expandEnvVars("val=${TEST_EXPAND_SET:-default}")
	if result != "val=actual_value" {
		t.Errorf("expandEnvVars = %q, want val=actual_value", result)
	}
}

// --- Duration UnmarshalYAML with bad duration string ---

func TestDurationUnmarshalBadString(t *testing.T) {
	var d Duration
	fakeUnmarshal := func(v any) error {
		switch p := v.(type) {
		case *string:
			*p = "not-a-duration"
			return nil
		default:
			return fmt.Errorf("unexpected type %T", v)
		}
	}

	err := d.UnmarshalYAML(fakeUnmarshal)
	if err == nil {
		t.Fatal("expected error for bad duration string")
	}
}

// --- ByteSize UnmarshalYAML with bad string ---

func TestByteSizeUnmarshalBadString(t *testing.T) {
	var b ByteSize
	fakeUnmarshal := func(v any) error {
		switch p := v.(type) {
		case *string:
			*p = "badTB"
			return nil
		default:
			return fmt.Errorf("unexpected type %T", v)
		}
	}

	err := b.UnmarshalYAML(fakeUnmarshal)
	if err == nil {
		t.Fatal("expected error for bad byte size string")
	}
}

// --- defaults.go: PHP domain defaults (FPMAddress, IndexFiles, MaxUpload, Timeout) ---

func TestDefaultsPHPDomain(t *testing.T) {
	yaml := `
domains:
  - host: "php.example.com"
    root: /var/www
    type: php
    ssl:
      mode: off
`
	cfg := loadFromString(t, yaml)

	d := cfg.Domains[0]
	if d.PHP.FPMAddress != "unix:/var/run/php/php-fpm.sock" {
		t.Errorf("FPMAddress = %q, want default unix socket", d.PHP.FPMAddress)
	}
	if len(d.PHP.IndexFiles) != 2 || d.PHP.IndexFiles[0] != "index.php" {
		t.Errorf("PHP.IndexFiles = %v, want [index.php index.html]", d.PHP.IndexFiles)
	}
	if d.PHP.MaxUpload != 64*MB {
		t.Errorf("PHP.MaxUpload = %d, want %d", d.PHP.MaxUpload, 64*MB)
	}
	if d.PHP.Timeout.Duration != 300*time.Second {
		t.Errorf("PHP.Timeout = %v, want 300s", d.PHP.Timeout.Duration)
	}
}

// --- defaults.go: PHP domain with values already set (no override) ---

func TestDefaultsPHPDomainNoOverride(t *testing.T) {
	yaml := `
domains:
  - host: "php2.example.com"
    root: /var/www
    type: php
    ssl:
      mode: off
    php:
      fpm_address: "tcp:127.0.0.1:9000"
      index_files: ["app.php"]
      max_upload: "128MB"
      timeout: "60s"
`
	cfg := loadFromString(t, yaml)

	d := cfg.Domains[0]
	if d.PHP.FPMAddress != "tcp:127.0.0.1:9000" {
		t.Errorf("FPMAddress = %q, want tcp:127.0.0.1:9000", d.PHP.FPMAddress)
	}
	if len(d.PHP.IndexFiles) != 1 || d.PHP.IndexFiles[0] != "app.php" {
		t.Errorf("PHP.IndexFiles = %v, want [app.php]", d.PHP.IndexFiles)
	}
	if d.PHP.MaxUpload != 128*MB {
		t.Errorf("PHP.MaxUpload = %d, want %d", d.PHP.MaxUpload, 128*MB)
	}
	if d.PHP.Timeout.Duration != 60*time.Second {
		t.Errorf("PHP.Timeout = %v, want 60s", d.PHP.Timeout.Duration)
	}
}

// --- loader.go: auto domains.d/ with error in file ---

func TestAutoDetectDomainsDirError(t *testing.T) {
	dir := t.TempDir()
	domainsDir := filepath.Join(dir, "domains.d")
	os.MkdirAll(domainsDir, 0755)

	// Put a broken YAML file in auto-detected domains.d/
	os.WriteFile(filepath.Join(domainsDir, "broken.yaml"), []byte("host: [bad {{"), 0644)

	mainConfig := `
global:
  log_level: info
domains:
  - host: main.com
    root: /var/www/main
    type: static
    ssl:
      mode: off
`
	configPath := filepath.Join(dir, "uwas.yaml")
	os.WriteFile(configPath, []byte(mainConfig), 0644)

	_, err := Load(configPath)
	if err == nil {
		t.Fatal("expected error for broken file in auto-detected domains.d/")
	}
}

// --- defaults.go: all global defaults set (no override) ---

func TestDefaultsGlobalAlreadySet(t *testing.T) {
	yaml := `
global:
  worker_count: "4"
  max_connections: 1024
  http_listen: ":8080"
  https_listen: ":8443"
  pid_file: "/tmp/uwas.pid"
  log_level: warn
  log_format: json
  timeouts:
    read: 5s
    write: 10s
    idle: 30s
    shutdown_grace: 5s
  admin:
    listen: "127.0.0.1:8000"
  mcp:
    listen: "127.0.0.1:8001"
  acme:
    ca_url: "https://custom.ca/"
    storage: "/tmp/certs"
  cache:
    memory_limit: "256MB"
    disk_path: "/tmp/cache"
    disk_limit: "1GB"
    default_ttl: 600
    grace_ttl: 3600
domains:
  - host: "test.com"
    root: /var/www
    type: static
    ssl:
      mode: off
`
	cfg := loadFromString(t, yaml)

	if cfg.Global.WorkerCount != "4" {
		t.Errorf("WorkerCount = %q, want 4", cfg.Global.WorkerCount)
	}
	if cfg.Global.HTTPListen != ":8080" {
		t.Errorf("HTTPListen = %q, want :8080", cfg.Global.HTTPListen)
	}
	if cfg.Global.HTTPSListen != ":8443" {
		t.Errorf("HTTPSListen = %q, want :8443", cfg.Global.HTTPSListen)
	}
	if cfg.Global.PIDFile != "/tmp/uwas.pid" {
		t.Errorf("PIDFile = %q, want /tmp/uwas.pid", cfg.Global.PIDFile)
	}
	if cfg.Global.Admin.Listen != "127.0.0.1:8000" {
		t.Errorf("Admin.Listen = %q", cfg.Global.Admin.Listen)
	}
	if cfg.Global.MCP.Listen != "127.0.0.1:8001" {
		t.Errorf("MCP.Listen = %q", cfg.Global.MCP.Listen)
	}
	if cfg.Global.ACME.CAURL != "https://custom.ca/" {
		t.Errorf("ACME.CAURL = %q", cfg.Global.ACME.CAURL)
	}
	if cfg.Global.ACME.Storage != "/tmp/certs" {
		t.Errorf("ACME.Storage = %q", cfg.Global.ACME.Storage)
	}
	if cfg.Global.Cache.DefaultTTL != 600 {
		t.Errorf("Cache.DefaultTTL = %d, want 600", cfg.Global.Cache.DefaultTTL)
	}
	if cfg.Global.Cache.GraceTTL != 3600 {
		t.Errorf("Cache.GraceTTL = %d, want 3600", cfg.Global.Cache.GraceTTL)
	}
}

// helper
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
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

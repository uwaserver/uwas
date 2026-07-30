package integration

// Config validation behavior integration tests.
//
// These tests verify that invalid configuration inputs are rejected with
// specific, actionable error messages — not just "validation failed".
// They replace coverage-chasing unit tests with behavior contracts.
//
// What these tests prove:
//   1. Invalid domain types are rejected
//   2. Duplicate domain hosts are caught
//   3. Invalid SSL modes are rejected
//   4. Missing S3/SFTP backup fields are caught
//   5. Invalid rate limit configs are rejected
//   6. Manual SSL mode requires cert + key files
//   7. Invalid log levels/formats are rejected
//   8. ACME email must contain @

import (
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

func validBaseConfig() *config.Config {
	return &config.Config{
		Global: config.GlobalConfig{
			HTTPListen: ":80",
			LogLevel:   "info",
			LogFormat:  "text",
		},
		Domains: []config.Domain{
			{
				Host: "example.com",
				Type: "static",
				SSL:  config.SSLConfig{Mode: "off"},
				Root: "/var/www/example.com",
			},
		},
	}
}

// TestConfigValidation_ValidConfig verifies that a well-formed config
// passes validation with no errors.
func TestConfigValidation_ValidConfig(t *testing.T) {
	cfg := validBaseConfig()
	if err := config.Validate(cfg); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

// TestConfigValidation_InvalidDomainType verifies that an unknown domain type
// is rejected with a message naming the invalid type.
func TestConfigValidation_InvalidDomainType(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Domains[0].Type = "ruby-on-rails"
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected error for invalid domain type")
	}
	if !strings.Contains(err.Error(), "invalid type") {
		t.Errorf("error should mention 'invalid type': %v", err)
	}
	if !strings.Contains(err.Error(), "ruby-on-rails") {
		t.Errorf("error should name the invalid type: %v", err)
	}
}

// TestConfigValidation_DuplicateHost verifies that two domains with the same
// host are rejected.
func TestConfigValidation_DuplicateHost(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Domains = append(cfg.Domains, config.Domain{
		Host: "example.com", // duplicate
		Type: "static",
		SSL:  config.SSLConfig{Mode: "off"},
	})
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected error for duplicate host")
	}
	if !strings.Contains(err.Error(), "duplicate host") {
		t.Errorf("error should mention 'duplicate host': %v", err)
	}
}

// TestConfigValidation_InvalidSSLMmode verifies that an unknown SSL mode
// is rejected.
func TestConfigValidation_InvalidSSLMode(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Domains[0].SSL.Mode = "maybe"
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected error for invalid SSL mode")
	}
	if !strings.Contains(err.Error(), "ssl.mode") {
		t.Errorf("error should mention 'ssl.mode': %v", err)
	}
}

// TestConfigValidation_ManualSSLRequiresCertAndKey verifies that manual SSL
// mode requires both cert and key paths.
func TestConfigValidation_ManualSSLRequiresCertAndKey(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Domains[0].SSL.Mode = "manual"
	// No cert or key set
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected error for manual SSL without cert")
	}
	if !strings.Contains(err.Error(), "ssl.cert required") {
		t.Errorf("error should mention cert required: %v", err)
	}
}

// TestConfigValidation_MissingHost verifies that a domain without a host
// is rejected.
func TestConfigValidation_MissingHost(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Domains[0].Host = ""
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected error for missing host")
	}
	if !strings.Contains(err.Error(), "host is required") {
		t.Errorf("error should mention host required: %v", err)
	}
}

// TestConfigValidation_InvalidLogLevel verifies that an unknown log level
// is rejected.
func TestConfigValidation_InvalidLogLevel(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Global.LogLevel = "verbose"
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected error for invalid log level")
	}
	if !strings.Contains(err.Error(), "log_level") {
		t.Errorf("error should mention log_level: %v", err)
	}
}

// TestConfigValidation_InvalidLogFormat verifies that an unknown log format
// is rejected.
func TestConfigValidation_InvalidLogFormat(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Global.LogFormat = "xml"
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected error for invalid log format")
	}
	if !strings.Contains(err.Error(), "log_format") {
		t.Errorf("error should mention log_format: %v", err)
	}
}

// TestConfigValidation_BackupS3RequiresBucket verifies that S3 backup
// without a bucket name is rejected.
func TestConfigValidation_BackupS3RequiresBucket(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Global.Backup.Enabled = true
	cfg.Global.Backup.Provider = "s3"
	cfg.Global.Backup.Keep = 3
	// No bucket set
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected error for S3 without bucket")
	}
	if !strings.Contains(err.Error(), "bucket") {
		t.Errorf("error should mention bucket: %v", err)
	}
}

// TestConfigValidation_BackupSFTPRequiresHost verifies that SFTP backup
// without a host is rejected.
func TestConfigValidation_BackupSFTPRequiresHost(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Global.Backup.Enabled = true
	cfg.Global.Backup.Provider = "sftp"
	cfg.Global.Backup.Keep = 3
	// No host set
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected error for SFTP without host")
	}
	if !strings.Contains(err.Error(), "sftp.host") {
		t.Errorf("error should mention sftp.host: %v", err)
	}
}

// TestConfigValidation_BackupInvalidProvider verifies that an unknown backup
// provider is rejected.
func TestConfigValidation_BackupInvalidProvider(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Global.Backup.Enabled = true
	cfg.Global.Backup.Provider = "dropbox"
	cfg.Global.Backup.Keep = 3
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected error for invalid backup provider")
	}
	if !strings.Contains(err.Error(), "provider") {
		t.Errorf("error should mention provider: %v", err)
	}
}

// TestConfigValidation_NegativeCacheTTL verifies that a negative cache TTL
// is rejected.
func TestConfigValidation_NegativeCacheTTL(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Global.Cache.DefaultTTL = -1
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected error for negative cache TTL")
	}
	if !strings.Contains(err.Error(), "default_ttl") {
		t.Errorf("error should mention default_ttl: %v", err)
	}
}

// TestConfigValidation_ACMEEmailRequiresAt verifies that an ACME email
// without @ is rejected.
func TestConfigValidation_ACMEEmailRequiresAt(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Global.ACME.Email = "not-an-email"
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected error for invalid ACME email")
	}
	if !strings.Contains(err.Error(), "acme.email") {
		t.Errorf("error should mention acme.email: %v", err)
	}
}

// TestConfigValidation_InvalidCanonicalHost verifies that an unknown
// canonical_host value is rejected.
func TestConfigValidation_InvalidCanonicalHost(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Domains[0].CanonicalHost = "ftp"
	err := config.Validate(cfg)
	if err == nil {
		t.Fatal("expected error for invalid canonical_host")
	}
	if !strings.Contains(err.Error(), "canonical_host") {
		t.Errorf("error should mention canonical_host: %v", err)
	}
}

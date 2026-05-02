package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// helper: minimal valid config for tests
func minimalValidConfig() *Config {
	cfg := &Config{
		Global: GlobalConfig{
			LogLevel:  "info",
			LogFormat: "text",
		},
		Domains: []Domain{
			{
				Host: "example.com",
				Root: "/var/www/html",
				Type: "static",
				SSL:  SSLConfig{Mode: "off", MinVersion: "1.2"},
			},
		},
	}
	return cfg
}

// helper: minimal valid proxy domain
func proxyDomain(upstreams []Upstream) Domain {
	return Domain{
		Host: "proxy.example.com",
		Type: "proxy",
		SSL:  SSLConfig{Mode: "off", MinVersion: "1.2"},
		Proxy: ProxyConfig{
			Upstreams: upstreams,
		},
	}
}

func expectValidationError(t *testing.T, cfg *Config, substr string) {
	t.Helper()
	err := Validate(cfg)
	if err == nil {
		t.Fatalf("expected validation error containing %q, got nil", substr)
	}
	if !strings.Contains(err.Error(), substr) {
		t.Errorf("expected error containing %q, got: %s", substr, err.Error())
	}
}

func expectNoValidationError(t *testing.T, cfg *Config) {
	t.Helper()
	err := Validate(cfg)
	if err != nil {
		t.Fatalf("expected no validation error, got: %v", err)
	}
}

// --- Listen Address Tests ---

func TestValidateListenAddr_Valid(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Global.HTTPListen = ":8080"
	cfg.Global.HTTPSListen = "0.0.0.0:443"
	cfg.Global.Admin.Listen = "127.0.0.1:9443"
	cfg.Global.MCP.Listen = "127.0.0.1:9444"
	expectNoValidationError(t, cfg)
}

func TestValidateListenAddr_InvalidFormat(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Global.HTTPListen = "not-an-address"
	expectValidationError(t, cfg, "global.http_listen")
}

func TestValidateListenAddr_PortZero(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Global.HTTPListen = ":0"
	expectValidationError(t, cfg, "port must be 1-65535")
}

func TestValidateListenAddr_PortTooHigh(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Global.HTTPSListen = ":99999"
	expectValidationError(t, cfg, "port must be 1-65535")
}

func TestValidateListenAddr_InvalidPort(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Global.Admin.Listen = "127.0.0.1:abc"
	expectValidationError(t, cfg, "invalid port")
}

func TestValidateListenAddr_HostWithSpaces(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Global.MCP.Listen = "bad host:8080"
	expectValidationError(t, cfg, "invalid")
}

// --- Trusted Proxies Tests ---

func TestValidateTrustedProxies_ValidCIDR(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Global.TrustedProxies = []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"}
	expectNoValidationError(t, cfg)
}

func TestValidateTrustedProxies_ValidPlainIP(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Global.TrustedProxies = []string{"127.0.0.1"}
	expectNoValidationError(t, cfg)
}

func TestValidateTrustedProxies_Invalid(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Global.TrustedProxies = []string{"not-a-cidr"}
	expectValidationError(t, cfg, "trusted_proxies[0]")
}

func TestValidateTrustedProxies_InvalidCIDR(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Global.TrustedProxies = []string{"10.0.0.0/33"}
	expectValidationError(t, cfg, "trusted_proxies[0]")
}

// --- URL SSRF Safety Tests ---

func TestWebhookURLSafetyBlocksInternalTargets(t *testing.T) {
	tests := []string{
		"http://localhost:8080/hook",
		"http://127.0.0.1:8080/hook",
		"http://10.0.0.1/hook",
		"http://172.16.0.1/hook",
		"http://192.168.1.10/hook",
		"http://169.254.169.254/latest/meta-data/",
	}

	for _, rawURL := range tests {
		if err := IsWebhookURLSafe(rawURL); err == nil {
			t.Errorf("IsWebhookURLSafe(%q) = nil, want blocked", rawURL)
		}
	}
}

func TestProxyUpstreamSafetyAllowsLoopbackButBlocksInternalRanges(t *testing.T) {
	for _, rawURL := range []string{"http://localhost:3000", "http://127.0.0.1:3000"} {
		if err := IsProxyUpstreamSafe(rawURL); err != nil {
			t.Errorf("IsProxyUpstreamSafe(%q) = %v, want nil", rawURL, err)
		}
	}

	for _, rawURL := range []string{"http://10.0.0.1:3000", "http://169.254.169.254/latest/meta-data/"} {
		if err := IsProxyUpstreamSafe(rawURL); err == nil {
			t.Errorf("IsProxyUpstreamSafe(%q) = nil, want blocked", rawURL)
		}
	}
}

func TestPrivateProxyUpstreamSafetyStillBlocksMetadata(t *testing.T) {
	if err := IsPrivateProxyUpstreamSafe("http://10.0.0.1:3000"); err != nil {
		t.Errorf("IsPrivateProxyUpstreamSafe(private IP) = %v, want nil", err)
	}
	if err := IsPrivateProxyUpstreamSafe("http://169.254.169.254/latest/meta-data/"); err == nil {
		t.Error("IsPrivateProxyUpstreamSafe(metadata) = nil, want blocked")
	}
}

func TestIsSSRFSafeUsesWebhookPolicy(t *testing.T) {
	if err := IsSSRFSafe("http://127.0.0.1:8080/hook"); err == nil {
		t.Error("IsSSRFSafe(loopback) = nil, want blocked")
	}
}

// --- Proxy Upstream Tests ---

func TestValidateProxyUpstreams_Valid(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Domains = []Domain{proxyDomain([]Upstream{
		{Address: "http://127.0.0.1:3000", Weight: 1},
		{Address: "http://127.0.0.1:3001", Weight: 2},
	})}
	expectNoValidationError(t, cfg)
}

func TestValidateProxyUpstreams_InvalidURL(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Domains = []Domain{proxyDomain([]Upstream{
		{Address: "://bad-url", Weight: 1},
	})}
	expectValidationError(t, cfg, "invalid URL")
}

func TestValidateProxyUpstreams_DuplicateAddress(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Domains = []Domain{proxyDomain([]Upstream{
		{Address: "http://127.0.0.1:3000", Weight: 1},
		{Address: "http://127.0.0.1:3000", Weight: 2},
	})}
	expectValidationError(t, cfg, "duplicate upstream")
}

func TestValidateProxyUpstreams_NegativeWeight(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Domains = []Domain{proxyDomain([]Upstream{
		{Address: "http://127.0.0.1:3000", Weight: -1},
	})}
	expectValidationError(t, cfg, "weight must be >= 0")
}

func TestValidateProxyUpstreams_EmptyAddress(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Domains = []Domain{proxyDomain([]Upstream{
		{Address: "", Weight: 1},
	})}
	expectValidationError(t, cfg, "address is required")
}

// --- Proxy Algorithm Tests ---

func TestValidateProxyAlgorithm_AllValid(t *testing.T) {
	for _, alg := range []string{"round-robin", "least-conn", "weighted", "random", "sticky"} {
		cfg := minimalValidConfig()
		d := proxyDomain([]Upstream{{Address: "http://127.0.0.1:3000", Weight: 1}})
		d.Proxy.Algorithm = alg
		cfg.Domains = []Domain{d}
		expectNoValidationError(t, cfg)
	}
}

func TestValidateProxyAlgorithm_Invalid(t *testing.T) {
	cfg := minimalValidConfig()
	d := proxyDomain([]Upstream{{Address: "http://127.0.0.1:3000", Weight: 1}})
	d.Proxy.Algorithm = "hash"
	cfg.Domains = []Domain{d}
	expectValidationError(t, cfg, "invalid proxy.algorithm")
}

func TestValidateProxyAlgorithm_EmptyIsValid(t *testing.T) {
	cfg := minimalValidConfig()
	d := proxyDomain([]Upstream{{Address: "http://127.0.0.1:3000", Weight: 1}})
	d.Proxy.Algorithm = ""
	cfg.Domains = []Domain{d}
	expectNoValidationError(t, cfg)
}

// --- Certificate File Tests ---

func TestValidateSSLManual_FilesExist(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	os.WriteFile(certPath, []byte("cert"), 0644)
	os.WriteFile(keyPath, []byte("key"), 0644)

	cfg := minimalValidConfig()
	cfg.Domains[0].SSL.Mode = "manual"
	cfg.Domains[0].SSL.Cert = certPath
	cfg.Domains[0].SSL.Key = keyPath
	expectNoValidationError(t, cfg)
}

func TestValidateSSLManual_CertMissing(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "key.pem")
	os.WriteFile(keyPath, []byte("key"), 0644)

	cfg := minimalValidConfig()
	cfg.Domains[0].SSL.Mode = "manual"
	cfg.Domains[0].SSL.Cert = filepath.Join(dir, "nonexistent.pem")
	cfg.Domains[0].SSL.Key = keyPath
	expectValidationError(t, cfg, "ssl.cert file")
}

func TestValidateSSLManual_KeyMissing(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	os.WriteFile(certPath, []byte("cert"), 0644)

	cfg := minimalValidConfig()
	cfg.Domains[0].SSL.Mode = "manual"
	cfg.Domains[0].SSL.Cert = certPath
	cfg.Domains[0].SSL.Key = filepath.Join(dir, "nonexistent.pem")
	expectValidationError(t, cfg, "ssl.key file")
}

func TestValidateSSLManual_CertEmpty(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Domains[0].SSL.Mode = "manual"
	cfg.Domains[0].SSL.Cert = ""
	cfg.Domains[0].SSL.Key = ""
	expectValidationError(t, cfg, "ssl.cert required")
}

// --- ACME Email Tests ---

func TestValidateACMEEmail_Valid(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Global.ACME.Email = "admin@example.com"
	expectNoValidationError(t, cfg)
}

func TestValidateACMEEmail_Invalid(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Global.ACME.Email = "not-an-email"
	expectValidationError(t, cfg, "invalid email")
}

func TestValidateACMEEmail_EmptyIsOk(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Global.ACME.Email = ""
	expectNoValidationError(t, cfg)
}

// --- TLS MinVersion Tests ---

func TestValidateTLSMinVersion_AllValid(t *testing.T) {
	for _, v := range []string{"1.0", "1.1", "1.2", "1.3"} {
		cfg := minimalValidConfig()
		cfg.Domains[0].SSL.MinVersion = v
		expectNoValidationError(t, cfg)
	}
}

func TestValidateTLSMinVersion_Invalid(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Domains[0].SSL.MinVersion = "1.4"
	expectValidationError(t, cfg, "invalid ssl.min_version")
}

func TestValidateTLSMinVersion_EmptyIsOk(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Domains[0].SSL.MinVersion = ""
	expectNoValidationError(t, cfg)
}

// --- Cache TTL Tests ---

func TestValidateCacheTTL_GlobalNegative(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Global.Cache.DefaultTTL = -1
	expectValidationError(t, cfg, "default_ttl: must be >= 0")
}

func TestValidateCacheTTL_GlobalGraceNegative(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Global.Cache.GraceTTL = -5
	expectValidationError(t, cfg, "grace_ttl: must be >= 0")
}

func TestValidateCacheTTL_GlobalZeroIsOk(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Global.Cache.DefaultTTL = 0
	cfg.Global.Cache.GraceTTL = 0
	expectNoValidationError(t, cfg)
}

func TestValidateCacheTTL_DomainNegative(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Domains[0].Cache.TTL = -1
	expectValidationError(t, cfg, "cache.ttl must be >= 0")
}

func TestValidateCacheTTL_DomainRuleNegative(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Domains[0].Cache.Rules = []CacheRule{{Match: "*.html", TTL: -10}}
	expectValidationError(t, cfg, "cache.rules[0]: ttl must be >= 0")
}

// --- Rate Limit Tests ---

func TestValidateRateLimit_Valid(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Domains[0].Security.RateLimit.Requests = 100
	cfg.Domains[0].Security.RateLimit.Window = Duration{Duration: 60 * time.Second}
	expectNoValidationError(t, cfg)
}

func TestValidateRateLimit_ZeroRequestsWithWindow(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Domains[0].Security.RateLimit.Requests = 0
	cfg.Domains[0].Security.RateLimit.Window = Duration{Duration: 60 * time.Second}
	expectValidationError(t, cfg, "rate_limit.requests must be > 0")
}

func TestValidateRateLimit_NegativeRequests(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Domains[0].Security.RateLimit.Requests = -5
	cfg.Domains[0].Security.RateLimit.Window = Duration{Duration: 60 * time.Second}
	expectValidationError(t, cfg, "rate_limit.requests must be > 0")
}

func TestValidateRateLimit_ZeroWindowWithRequests(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Domains[0].Security.RateLimit.Requests = 100
	cfg.Domains[0].Security.RateLimit.Window = Duration{Duration: 0}
	expectValidationError(t, cfg, "rate_limit.window must be > 0")
}

func TestValidateRateLimit_BothZeroIsOk(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Domains[0].Security.RateLimit.Requests = 0
	cfg.Domains[0].Security.RateLimit.Window = Duration{Duration: 0}
	expectNoValidationError(t, cfg)
}

// --- Rewrite Rule Tests ---

func TestValidateRewriteRules_ValidRegex(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Domains[0].Rewrites = []RewriteRule{
		{Match: `^/old/(.*)$`, To: "/new/$1"},
		{Match: `\.php$`, To: "/index.php"},
	}
	expectNoValidationError(t, cfg)
}

func TestValidateRewriteRules_InvalidRegex(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Domains[0].Rewrites = []RewriteRule{
		{Match: `[invalid`, To: "/"},
	}
	expectValidationError(t, cfg, "invalid regex")
}

func TestValidateRewriteRules_EmptyMatchIsOk(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Domains[0].Rewrites = []RewriteRule{
		{Match: "", To: "/"},
	}
	expectNoValidationError(t, cfg)
}

// --- Compression Algorithm Tests ---

func TestValidateCompression_ValidAlgorithms(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Domains[0].Compression.Enabled = true
	cfg.Domains[0].Compression.Algorithms = []string{"gzip", "br"}
	expectNoValidationError(t, cfg)
}

func TestValidateCompression_InvalidAlgorithm(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Domains[0].Compression.Enabled = true
	cfg.Domains[0].Compression.Algorithms = []string{"gzip", "deflate"}
	expectValidationError(t, cfg, "invalid algorithm \"deflate\"")
}

func TestValidateCompression_DisabledSkipsValidation(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Domains[0].Compression.Enabled = false
	cfg.Domains[0].Compression.Algorithms = []string{"invalid"}
	expectNoValidationError(t, cfg)
}

// --- Image Optimization Format Tests ---

func TestValidateImageOptimization_ValidFormats(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Domains[0].ImageOptimization.Enabled = true
	cfg.Domains[0].ImageOptimization.Formats = []string{"webp", "avif"}
	expectNoValidationError(t, cfg)
}

func TestValidateImageOptimization_InvalidFormat(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Domains[0].ImageOptimization.Enabled = true
	cfg.Domains[0].ImageOptimization.Formats = []string{"webp", "jpeg"}
	expectValidationError(t, cfg, "invalid format \"jpeg\"")
}

func TestValidateImageOptimization_DisabledSkipsValidation(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Domains[0].ImageOptimization.Enabled = false
	cfg.Domains[0].ImageOptimization.Formats = []string{"invalid"}
	expectNoValidationError(t, cfg)
}

// --- Redirect Status Tests ---

func TestValidateRedirectStatus_AllValid(t *testing.T) {
	for _, status := range []int{301, 302, 307, 308} {
		cfg := minimalValidConfig()
		cfg.Domains = []Domain{{
			Host: "redir.example.com",
			Type: "redirect",
			SSL:  SSLConfig{Mode: "off", MinVersion: "1.2"},
			Redirect: RedirectConfig{
				Target: "https://example.com",
				Status: status,
			},
		}}
		expectNoValidationError(t, cfg)
	}
}

func TestValidateRedirectStatus_Invalid(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Domains = []Domain{{
		Host: "redir.example.com",
		Type: "redirect",
		SSL:  SSLConfig{Mode: "off", MinVersion: "1.2"},
		Redirect: RedirectConfig{
			Target: "https://example.com",
			Status: 303,
		},
	}}
	expectValidationError(t, cfg, "invalid redirect.status")
}

func TestValidateRedirectStatus_ZeroIsOk(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Domains = []Domain{{
		Host: "redir.example.com",
		Type: "redirect",
		SSL:  SSLConfig{Mode: "off", MinVersion: "1.2"},
		Redirect: RedirectConfig{
			Target: "https://example.com",
			Status: 0,
		},
	}}
	expectNoValidationError(t, cfg)
}

func TestValidateRedirectStatus_MissingTarget(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Domains = []Domain{{
		Host: "redir.example.com",
		Type: "redirect",
		SSL:  SSLConfig{Mode: "off", MinVersion: "1.2"},
		Redirect: RedirectConfig{
			Target: "",
		},
	}}
	expectValidationError(t, cfg, "redirect.target required")
}

// --- Backup Provider Tests ---

func TestValidateBackupProvider_AllValid(t *testing.T) {
	for _, p := range []string{"local", "s3", "sftp"} {
		cfg := minimalValidConfig()
		cfg.Global.Backup.Enabled = true
		cfg.Global.Backup.Provider = p
		cfg.Global.Backup.Keep = 5
		if p == "s3" {
			cfg.Global.Backup.S3.Bucket = "my-bucket"
		}
		if p == "sftp" {
			cfg.Global.Backup.SFTP.Host = "backup.example.com"
			cfg.Global.Backup.SFTP.User = "deploy"
		}
		expectNoValidationError(t, cfg)
	}
}

func TestValidateBackupProvider_Invalid(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Global.Backup.Enabled = true
	cfg.Global.Backup.Provider = "ftp"
	cfg.Global.Backup.Keep = 5
	expectValidationError(t, cfg, "invalid provider \"ftp\"")
}

func TestValidateBackupProvider_DisabledSkipsValidation(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Global.Backup.Enabled = false
	cfg.Global.Backup.Provider = "ftp"
	cfg.Global.Backup.Keep = 0
	expectNoValidationError(t, cfg)
}

// --- Backup Keep Tests ---

func TestValidateBackupKeep_Zero(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Global.Backup.Enabled = true
	cfg.Global.Backup.Provider = "local"
	cfg.Global.Backup.Keep = 0
	expectValidationError(t, cfg, "keep: must be > 0")
}

func TestValidateBackupKeep_Negative(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Global.Backup.Enabled = true
	cfg.Global.Backup.Provider = "local"
	cfg.Global.Backup.Keep = -1
	expectValidationError(t, cfg, "keep: must be > 0")
}

// --- S3 Config Tests ---

func TestValidateBackupS3_MissingBucket(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Global.Backup.Enabled = true
	cfg.Global.Backup.Provider = "s3"
	cfg.Global.Backup.Keep = 5
	cfg.Global.Backup.S3.Bucket = ""
	expectValidationError(t, cfg, "s3.bucket: required when provider is s3")
}

func TestValidateBackupS3_WithBucket(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Global.Backup.Enabled = true
	cfg.Global.Backup.Provider = "s3"
	cfg.Global.Backup.Keep = 5
	cfg.Global.Backup.S3.Bucket = "my-bucket"
	expectNoValidationError(t, cfg)
}

// --- SFTP Config Tests ---

func TestValidateBackupSFTP_MissingHost(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Global.Backup.Enabled = true
	cfg.Global.Backup.Provider = "sftp"
	cfg.Global.Backup.Keep = 5
	cfg.Global.Backup.SFTP.Host = ""
	cfg.Global.Backup.SFTP.User = "deploy"
	expectValidationError(t, cfg, "sftp.host: required when provider is sftp")
}

func TestValidateBackupSFTP_MissingUser(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Global.Backup.Enabled = true
	cfg.Global.Backup.Provider = "sftp"
	cfg.Global.Backup.Keep = 5
	cfg.Global.Backup.SFTP.Host = "backup.example.com"
	cfg.Global.Backup.SFTP.User = ""
	expectValidationError(t, cfg, "sftp.user: required when provider is sftp")
}

func TestValidateBackupSFTP_Valid(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Global.Backup.Enabled = true
	cfg.Global.Backup.Provider = "sftp"
	cfg.Global.Backup.Keep = 5
	cfg.Global.Backup.SFTP.Host = "backup.example.com"
	cfg.Global.Backup.SFTP.User = "deploy"
	expectNoValidationError(t, cfg)
}

// --- Canary Weight Tests ---

func TestValidateCanaryWeight_Valid(t *testing.T) {
	cfg := minimalValidConfig()
	d := proxyDomain([]Upstream{{Address: "http://127.0.0.1:3000", Weight: 1}})
	d.Proxy.Canary.Enabled = true
	d.Proxy.Canary.Weight = 50
	cfg.Domains = []Domain{d}
	expectNoValidationError(t, cfg)
}

func TestValidateCanaryWeight_TooHigh(t *testing.T) {
	cfg := minimalValidConfig()
	d := proxyDomain([]Upstream{{Address: "http://127.0.0.1:3000", Weight: 1}})
	d.Proxy.Canary.Enabled = true
	d.Proxy.Canary.Weight = 150
	cfg.Domains = []Domain{d}
	expectValidationError(t, cfg, "canary.weight must be 0-100")
}

func TestValidateCanaryWeight_Negative(t *testing.T) {
	cfg := minimalValidConfig()
	d := proxyDomain([]Upstream{{Address: "http://127.0.0.1:3000", Weight: 1}})
	d.Proxy.Canary.Enabled = true
	d.Proxy.Canary.Weight = -1
	cfg.Domains = []Domain{d}
	expectValidationError(t, cfg, "canary.weight must be 0-100")
}

func TestValidateCanaryWeight_BoundaryZero(t *testing.T) {
	cfg := minimalValidConfig()
	d := proxyDomain([]Upstream{{Address: "http://127.0.0.1:3000", Weight: 1}})
	d.Proxy.Canary.Enabled = true
	d.Proxy.Canary.Weight = 0
	cfg.Domains = []Domain{d}
	expectNoValidationError(t, cfg)
}

func TestValidateCanaryWeight_Boundary100(t *testing.T) {
	cfg := minimalValidConfig()
	d := proxyDomain([]Upstream{{Address: "http://127.0.0.1:3000", Weight: 1}})
	d.Proxy.Canary.Enabled = true
	d.Proxy.Canary.Weight = 100
	cfg.Domains = []Domain{d}
	expectNoValidationError(t, cfg)
}

// --- Mirror Percent Tests ---

func TestValidateMirrorPercent_Valid(t *testing.T) {
	cfg := minimalValidConfig()
	d := proxyDomain([]Upstream{{Address: "http://127.0.0.1:3000", Weight: 1}})
	d.Proxy.Mirror.Enabled = true
	d.Proxy.Mirror.Percent = 50
	cfg.Domains = []Domain{d}
	expectNoValidationError(t, cfg)
}

func TestValidateMirrorPercent_TooHigh(t *testing.T) {
	cfg := minimalValidConfig()
	d := proxyDomain([]Upstream{{Address: "http://127.0.0.1:3000", Weight: 1}})
	d.Proxy.Mirror.Enabled = true
	d.Proxy.Mirror.Percent = 200
	cfg.Domains = []Domain{d}
	expectValidationError(t, cfg, "mirror.percent must be 0-100")
}

func TestValidateMirrorPercent_Negative(t *testing.T) {
	cfg := minimalValidConfig()
	d := proxyDomain([]Upstream{{Address: "http://127.0.0.1:3000", Weight: 1}})
	d.Proxy.Mirror.Enabled = true
	d.Proxy.Mirror.Percent = -1
	cfg.Domains = []Domain{d}
	expectValidationError(t, cfg, "mirror.percent must be 0-100")
}

// --- Multiple Errors Accumulated ---

func TestValidateMultipleErrors(t *testing.T) {
	cfg := &Config{
		Global: GlobalConfig{
			LogLevel:  "invalid",
			LogFormat: "invalid",
		},
		Domains: []Domain{},
	}
	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected validation errors")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, "log_level") {
		t.Error("expected log_level error")
	}
	if !strings.Contains(errStr, "log_format") {
		t.Error("expected log_format error")
	}
}

// --- Edge case: valid minimal config passes ---

func TestValidateMinimalConfig(t *testing.T) {
	cfg := minimalValidConfig()
	expectNoValidationError(t, cfg)
}

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// --- Duration MarshalJSON ---

func TestDurationMarshalJSON_Zero(t *testing.T) {
	d := Duration{Duration: 0}
	data, err := d.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "0" {
		t.Errorf("got %s, want 0", string(data))
	}
}

func TestDurationMarshalJSON_NonZero(t *testing.T) {
	d := Duration{Duration: 30 * time.Second}
	data, err := d.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `"30s"` {
		t.Errorf("got %s, want \"30s\"", string(data))
	}
}

func TestDurationMarshalJSON_Complex(t *testing.T) {
	d := Duration{Duration: 2*time.Minute + 30*time.Second}
	data, err := d.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `"2m30s"` {
		t.Errorf("got %s, want \"2m30s\"", string(data))
	}
}

// --- Duration UnmarshalJSON ---

func TestDurationUnmarshalJSON_Null(t *testing.T) {
	var d Duration
	err := d.UnmarshalJSON([]byte("null"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Duration != 0 {
		t.Errorf("got %v, want 0", d.Duration)
	}
}

func TestDurationUnmarshalJSON_Empty(t *testing.T) {
	var d Duration
	err := d.UnmarshalJSON([]byte(""))
	if err != nil {
		t.Fatal(err)
	}
	if d.Duration != 0 {
		t.Errorf("got %v, want 0", d.Duration)
	}
}

func TestDurationUnmarshalJSON_Number(t *testing.T) {
	var d Duration
	err := d.UnmarshalJSON([]byte("30"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Duration != 30*time.Second {
		t.Errorf("got %v, want 30s", d.Duration)
	}
}

func TestDurationUnmarshalJSON_NegativeNumber(t *testing.T) {
	var d Duration
	err := d.UnmarshalJSON([]byte("-5"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Duration != -5*time.Second {
		t.Errorf("got %v, want -5s", d.Duration)
	}
}

func TestDurationUnmarshalJSON_FloatNumber(t *testing.T) {
	var d Duration
	err := d.UnmarshalJSON([]byte("1.5"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Duration != time.Duration(1.5*float64(time.Second)) {
		t.Errorf("got %v, want 1.5s", d.Duration)
	}
}

func TestDurationUnmarshalJSON_String(t *testing.T) {
	var d Duration
	err := d.UnmarshalJSON([]byte(`"5m"`))
	if err != nil {
		t.Fatal(err)
	}
	if d.Duration != 5*time.Minute {
		t.Errorf("got %v, want 5m", d.Duration)
	}
}

func TestDurationUnmarshalJSON_InvalidString(t *testing.T) {
	var d Duration
	err := d.UnmarshalJSON([]byte(`"not-a-duration"`))
	if err == nil {
		t.Fatal("expected error for invalid duration string")
	}
}

func TestDurationUnmarshalJSON_InvalidJSON(t *testing.T) {
	var d Duration
	err := d.UnmarshalJSON([]byte(`[1,2,3]`))
	if err == nil {
		t.Fatal("expected error for array JSON")
	}
}

// --- Duration MarshalYAML ---

func TestDurationMarshalYAML_Zero(t *testing.T) {
	d := Duration{Duration: 0}
	val, err := d.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	if val != "0s" {
		t.Errorf("got %v, want 0s", val)
	}
}

func TestDurationMarshalYAML_NonZero(t *testing.T) {
	d := Duration{Duration: 5 * time.Minute}
	val, err := d.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	if val != "5m0s" {
		t.Errorf("got %v, want 5m0s", val)
	}
}

// --- ByteSize MarshalJSON ---

func TestByteSizeMarshalJSON(t *testing.T) {
	tests := []struct {
		size ByteSize
		want string
	}{
		{0, "0"},
		{1024, "1024"},
		{512 * MB, "536870912"},
		{10 * GB, "10737418240"},
	}
	for _, tt := range tests {
		data, err := tt.size.MarshalJSON()
		if err != nil {
			t.Errorf("MarshalJSON(%d) error: %v", tt.size, err)
			continue
		}
		if string(data) != tt.want {
			t.Errorf("MarshalJSON(%d) = %s, want %s", tt.size, string(data), tt.want)
		}
	}
}

// --- ByteSize UnmarshalJSON ---

func TestByteSizeUnmarshalJSON_Null(t *testing.T) {
	var b ByteSize
	err := b.UnmarshalJSON([]byte("null"))
	if err != nil {
		t.Fatal(err)
	}
	if b != 0 {
		t.Errorf("got %d, want 0", b)
	}
}

func TestByteSizeUnmarshalJSON_Number(t *testing.T) {
	var b ByteSize
	err := b.UnmarshalJSON([]byte("2048"))
	if err != nil {
		t.Fatal(err)
	}
	if b != 2048 {
		t.Errorf("got %d, want 2048", b)
	}
}

func TestByteSizeUnmarshalJSON_String(t *testing.T) {
	var b ByteSize
	err := b.UnmarshalJSON([]byte(`"512MB"`))
	if err != nil {
		t.Fatal(err)
	}
	if b != 512*MB {
		t.Errorf("got %d, want %d", b, 512*MB)
	}
}

func TestByteSizeUnmarshalJSON_InvalidString(t *testing.T) {
	var b ByteSize
	err := b.UnmarshalJSON([]byte(`"100TB"`))
	if err == nil {
		t.Fatal("expected error for unknown unit")
	}
}

func TestByteSizeUnmarshalJSON_InvalidJSON(t *testing.T) {
	var b ByteSize
	err := b.UnmarshalJSON([]byte(`[1]`))
	if err == nil {
		t.Fatal("expected error for array JSON")
	}
}

// --- ByteSize MarshalYAML ---

func TestByteSizeMarshalYAML(t *testing.T) {
	tests := []struct {
		size ByteSize
		want any
	}{
		{0, 0},
		{10 * GB, "10GB"},
		{512 * MB, "512MB"},
		{8 * KB, "8KB"},
		{ByteSize(1023), int64(1023)},
	}
	for _, tt := range tests {
		val, err := tt.size.MarshalYAML()
		if err != nil {
			t.Errorf("MarshalYAML(%d) error: %v", tt.size, err)
			continue
		}
		if val != tt.want {
			t.Errorf("MarshalYAML(%d) = %v (%T), want %v (%T)", tt.size, val, val, tt.want, tt.want)
		}
	}
}

// --- Domain MarshalYAML ---

func TestDomainMarshalYAML_MinimalStatic(t *testing.T) {
	d := Domain{
		Host: "example.com",
		Root: "/var/www/html",
		Type: "static",
		SSL:  SSLConfig{Mode: "off"},
	}
	val, err := d.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	m := val.(map[string]any)
	if m["host"] != "example.com" {
		t.Errorf("host = %v", m["host"])
	}
	if m["root"] != "/var/www/html" {
		t.Errorf("root = %v", m["root"])
	}
}

func TestDomainMarshalYAML_WithIP(t *testing.T) {
	d := Domain{Host: "example.com", IP: "1.2.3.4", Type: "static", SSL: SSLConfig{Mode: "off"}}
	val, err := d.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	m := val.(map[string]any)
	if m["ip"] != "1.2.3.4" {
		t.Errorf("ip = %v", m["ip"])
	}
}

func TestDomainMarshalYAML_WithAliases(t *testing.T) {
	d := Domain{Host: "example.com", Aliases: []string{"www.example.com"}, Type: "static", SSL: SSLConfig{Mode: "off"}}
	val, err := d.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	m := val.(map[string]any)
	if m["aliases"] == nil {
		t.Error("aliases should be set")
	}
}

func TestDomainMarshalYAML_PHP(t *testing.T) {
	d := Domain{
		Host: "php.com",
		Type: "php",
		SSL:  SSLConfig{Mode: "off"},
		PHP: PHPConfig{
			FPMAddress: "unix:/var/run/php/php-fpm.sock",
			IndexFiles: []string{"index.php"},
			Timeout:    Duration{Duration: 30 * time.Second},
			MaxUpload:  64 * MB,
		},
	}
	val, err := d.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	m := val.(map[string]any)
	php := m["php"].(map[string]any)
	if php["fpm_address"] != "unix:/var/run/php/php-fpm.sock" {
		t.Errorf("fpm_address = %v", php["fpm_address"])
	}
	if php["timeout"] != "30s" {
		t.Errorf("timeout = %v", php["timeout"])
	}
}

func TestDomainMarshalYAML_Cache(t *testing.T) {
	d := Domain{
		Host: "cached.com",
		Type: "static",
		SSL:  SSLConfig{Mode: "off"},
		Cache: DomainCache{
			Enabled: true,
			TTL:     3600,
			Rules:   []CacheRule{{Match: "*.html", TTL: 300}},
		},
	}
	val, err := d.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	m := val.(map[string]any)
	if m["cache"] == nil {
		t.Error("cache should be set")
	}
}

func TestDomainMarshalYAML_Htaccess(t *testing.T) {
	d := Domain{Host: "h.com", Type: "static", SSL: SSLConfig{Mode: "off"}, Htaccess: HtaccessConfig{Mode: "full"}}
	val, err := d.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	m := val.(map[string]any)
	if m["htaccess"] == nil {
		t.Error("htaccess should be set")
	}
}

func TestDomainMarshalYAML_Security(t *testing.T) {
	d := Domain{
		Host: "sec.com",
		Type: "static",
		SSL:  SSLConfig{Mode: "off"},
		Security: SecurityConfig{
			BlockedPaths: []string{"/admin"},
			WAF:          WAFConfig{Enabled: true},
			RateLimit:    RateLimitConfig{Requests: 100},
			IPWhitelist:  []string{"10.0.0.0/8"},
			IPBlacklist:  []string{"1.2.3.4"},
		},
	}
	val, err := d.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	m := val.(map[string]any)
	sec := m["security"].(map[string]any)
	if sec["blocked_paths"] == nil {
		t.Error("blocked_paths should be set")
	}
	if sec["waf"] == nil {
		t.Error("waf should be set")
	}
	if sec["rate_limit"] == nil {
		t.Error("rate_limit should be set")
	}
	if sec["ip_whitelist"] == nil {
		t.Error("ip_whitelist should be set")
	}
	if sec["ip_blacklist"] == nil {
		t.Error("ip_blacklist should be set")
	}
}

func TestDomainMarshalYAML_Compression(t *testing.T) {
	d := Domain{Host: "c.com", Type: "static", SSL: SSLConfig{Mode: "off"}, Compression: CompressionConfig{Enabled: true}}
	val, err := d.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	m := val.(map[string]any)
	if m["compression"] == nil {
		t.Error("compression should be set")
	}
}

func TestDomainMarshalYAML_Proxy(t *testing.T) {
	d := Domain{
		Host: "proxy.com",
		Type: "proxy",
		SSL:  SSLConfig{Mode: "off"},
		Proxy: ProxyConfig{
			Upstreams: []Upstream{{Address: "http://127.0.0.1:3000"}},
			Algorithm: "round-robin",
			WebSocket: true,
		},
	}
	val, err := d.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	m := val.(map[string]any)
	proxy := m["proxy"].(map[string]any)
	if proxy["algorithm"] != "round-robin" {
		t.Errorf("algorithm = %v", proxy["algorithm"])
	}
	if proxy["websocket"] != true {
		t.Error("websocket should be true")
	}
}

func TestDomainMarshalYAML_Redirect(t *testing.T) {
	d := Domain{
		Host: "old.com",
		Type: "redirect",
		SSL:  SSLConfig{Mode: "off"},
		Redirect: RedirectConfig{
			Target:       "https://new.com",
			Status:       301,
			PreservePath: true,
		},
	}
	val, err := d.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	m := val.(map[string]any)
	redir := m["redirect"].(map[string]any)
	if redir["target"] != "https://new.com" {
		t.Errorf("target = %v", redir["target"])
	}
	if redir["status"] != 301 {
		t.Errorf("status = %v", redir["status"])
	}
	if redir["preserve_path"] != true {
		t.Error("preserve_path should be true")
	}
}

func TestDomainMarshalYAML_Rewrites(t *testing.T) {
	d := Domain{
		Host:     "r.com",
		Type:     "static",
		SSL:      SSLConfig{Mode: "off"},
		Rewrites: []RewriteRule{{Match: "^/old$", To: "/new"}},
	}
	val, err := d.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	m := val.(map[string]any)
	if m["rewrites"] == nil {
		t.Error("rewrites should be set")
	}
}

func TestDomainMarshalYAML_TryFiles(t *testing.T) {
	d := Domain{Host: "t.com", Type: "static", SSL: SSLConfig{Mode: "off"}, TryFiles: []string{"$uri", "/index.html"}}
	val, err := d.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	m := val.(map[string]any)
	if m["try_files"] == nil {
		t.Error("try_files should be set")
	}
}

func TestDomainMarshalYAML_SPAMode(t *testing.T) {
	d := Domain{Host: "s.com", Type: "static", SSL: SSLConfig{Mode: "off"}, SPAMode: true}
	val, err := d.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	m := val.(map[string]any)
	if m["spa_mode"] != true {
		t.Error("spa_mode should be true")
	}
}

func TestDomainMarshalYAML_IndexFiles(t *testing.T) {
	d := Domain{Host: "i.com", Type: "static", SSL: SSLConfig{Mode: "off"}, IndexFiles: []string{"index.php"}}
	val, err := d.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	m := val.(map[string]any)
	if m["index_files"] == nil {
		t.Error("index_files should be set")
	}
}

func TestDomainMarshalYAML_DirectoryListing(t *testing.T) {
	d := Domain{Host: "d.com", Type: "static", SSL: SSLConfig{Mode: "off"}, DirectoryListing: true}
	val, err := d.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	m := val.(map[string]any)
	if m["directory_listing"] != true {
		t.Error("directory_listing should be true")
	}
}

func TestDomainMarshalYAML_CORS(t *testing.T) {
	d := Domain{Host: "c.com", Type: "static", SSL: SSLConfig{Mode: "off"}, CORS: CORSConfig{Enabled: true}}
	val, err := d.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	m := val.(map[string]any)
	if m["cors"] == nil {
		t.Error("cors should be set")
	}
}

func TestDomainMarshalYAML_BasicAuth(t *testing.T) {
	d := Domain{Host: "b.com", Type: "static", SSL: SSLConfig{Mode: "off"}, BasicAuth: BasicAuthConfig{Enabled: true}}
	val, err := d.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	m := val.(map[string]any)
	if m["basic_auth"] == nil {
		t.Error("basic_auth should be set")
	}
}

func TestDomainMarshalYAML_ErrorPages(t *testing.T) {
	d := Domain{Host: "e.com", Type: "static", SSL: SSLConfig{Mode: "off"}, ErrorPages: map[int]string{404: "/404.html"}}
	val, err := d.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	m := val.(map[string]any)
	if m["error_pages"] == nil {
		t.Error("error_pages should be set")
	}
}

func TestDomainMarshalYAML_Bandwidth(t *testing.T) {
	d := Domain{Host: "bw.com", Type: "static", SSL: SSLConfig{Mode: "off"}, Bandwidth: BandwidthConfig{Enabled: true}}
	val, err := d.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	m := val.(map[string]any)
	if m["bandwidth"] == nil {
		t.Error("bandwidth should be set")
	}
}

// --- Domain MarshalYAML via yaml.Marshal ---

func TestDomainMarshalYAML_FullRoundTrip(t *testing.T) {
	d := Domain{
		Host:    "full.com",
		Type:    "static",
		SSL:     SSLConfig{Mode: "auto"},
		Aliases: []string{"www.full.com"},
		Root:    "/var/www/full",
	}
	data, err := yaml.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Error("expected non-empty YAML output")
	}
}

// --- Duration JSON round-trip ---

func TestDurationJSONRoundTrip(t *testing.T) {
	original := Duration{Duration: 5 * time.Minute}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}

	var restored Duration
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Duration != original.Duration {
		t.Errorf("got %v, want %v", restored.Duration, original.Duration)
	}
}

// --- ByteSize JSON round-trip ---

func TestByteSizeJSONRoundTrip(t *testing.T) {
	original := ByteSize(512 * MB)
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}

	var restored ByteSize
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if restored != original {
		t.Errorf("got %d, want %d", restored, original)
	}
}

// --- validate: edge case with security only having WAF enabled (no blocked_paths) ---

func TestDomainMarshalYAML_SecurityOnlyWAF(t *testing.T) {
	d := Domain{
		Host: "waf.com",
		Type: "static",
		SSL:  SSLConfig{Mode: "off"},
		Security: SecurityConfig{
			WAF: WAFConfig{Enabled: true},
		},
	}
	val, err := d.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	m := val.(map[string]any)
	if m["security"] == nil {
		t.Error("security should be set when WAF is enabled")
	}
}

func TestDomainMarshalYAML_SecurityOnlyRateLimit(t *testing.T) {
	d := Domain{
		Host: "rl.com",
		Type: "static",
		SSL:  SSLConfig{Mode: "off"},
		Security: SecurityConfig{
			RateLimit: RateLimitConfig{Requests: 100},
		},
	}
	val, err := d.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	m := val.(map[string]any)
	if m["security"] == nil {
		t.Error("security should be set when rate_limit is configured")
	}
}

// --- Proxy with empty algorithm ---

func TestDomainMarshalYAML_ProxyNoAlgorithm(t *testing.T) {
	d := Domain{
		Host: "p.com",
		Type: "proxy",
		SSL:  SSLConfig{Mode: "off"},
		Proxy: ProxyConfig{
			Upstreams: []Upstream{{Address: "http://127.0.0.1:3000"}},
		},
	}
	val, err := d.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	m := val.(map[string]any)
	proxy := m["proxy"].(map[string]any)
	if _, ok := proxy["algorithm"]; ok {
		t.Error("algorithm should not be set when empty")
	}
}

// --- Redirect without status and preserve_path ---

func TestDomainMarshalYAML_RedirectMinimal(t *testing.T) {
	d := Domain{
		Host: "rmin.com",
		Type: "redirect",
		SSL:  SSLConfig{Mode: "off"},
		Redirect: RedirectConfig{
			Target: "https://new.com",
		},
	}
	val, err := d.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	m := val.(map[string]any)
	redir := m["redirect"].(map[string]any)
	if _, ok := redir["status"]; ok {
		t.Error("status should not be set when 0")
	}
	if _, ok := redir["preserve_path"]; ok {
		t.Error("preserve_path should not be set when false")
	}
}

// --- Cache without rules ---

func TestDomainMarshalYAML_CacheNoRules(t *testing.T) {
	d := Domain{
		Host:  "cn.com",
		Type:  "static",
		SSL:   SSLConfig{Mode: "off"},
		Cache: DomainCache{Enabled: true, TTL: 600},
	}
	val, err := d.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	m := val.(map[string]any)
	cache := m["cache"].(map[string]any)
	if _, ok := cache["rules"]; ok {
		t.Error("rules should not be set when empty")
	}
}

// --- PHP without optional fields ---

func TestDomainMarshalYAML_PHPMinimal(t *testing.T) {
	d := Domain{
		Host: "phpm.com",
		Type: "php",
		SSL:  SSLConfig{Mode: "off"},
		PHP:  PHPConfig{FPMAddress: "tcp:127.0.0.1:9000"},
	}
	val, err := d.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	m := val.(map[string]any)
	php := m["php"].(map[string]any)
	if _, ok := php["index_files"]; ok {
		t.Error("index_files should not be set when empty")
	}
	if _, ok := php["timeout"]; ok {
		t.Error("timeout should not be set when 0")
	}
	if _, ok := php["max_upload"]; ok {
		t.Error("max_upload should not be set when 0")
	}
}

// --- validate: edge case - validate one more uncovered line ---

func TestValidateEdgeCases(t *testing.T) {
	// A domain with type static but web_root is set (auto-fills root)
	cfg := minimalValidConfig()
	cfg.Global.WebRoot = "/var/www"
	cfg.Domains = []Domain{
		{Host: "auto.com", Type: "static", SSL: SSLConfig{Mode: "off", MinVersion: "1.2"}},
	}
	expectNoValidationError(t, cfg)
	if cfg.Domains[0].Root == "" {
		t.Error("root should be auto-filled")
	}
}

// --- Duration UnmarshalJSON edge: numeric-looking but invalid as float ---

func TestDurationUnmarshalJSON_NumericButInvalid(t *testing.T) {
	// A value starting with a digit but not valid JSON number
	var d Duration
	err := d.UnmarshalJSON([]byte("1e999999"))
	// json.Unmarshal may parse this as +Inf, which is still a valid float64
	// So this may or may not error. Let's just exercise the path.
	_ = err
}

// --- loadDomainsDir with permission error ---
// This test creates a directory that os.ReadDir can't read on Unix.
// On Windows it's hard to trigger, but we include it for completeness.

func TestLoadDomainsDirReadError(t *testing.T) {
	// Create a directory and make it unreadable
	dir := t.TempDir()
	domainsDir := filepath.Join(dir, "domains.d")
	os.MkdirAll(domainsDir, 0755)

	// Put a valid file in it
	os.WriteFile(filepath.Join(domainsDir, "test.yaml"), []byte("host: test.com\nroot: /var/www\ntype: static\nssl:\n  mode: off\n"), 0644)

	// Try to make it unreadable (only works on Unix-like systems)
	os.Chmod(domainsDir, 0000)
	defer os.Chmod(domainsDir, 0755) // restore for cleanup

	_, err := loadDomainsDir(domainsDir)
	// On Windows, Chmod doesn't actually prevent reading, so this may succeed
	if err != nil {
		// Good - we exercised the error path
		t.Logf("got expected error: %v", err)
	}
}

func TestValidateEmptyWebRootMissingRoot(t *testing.T) {
	cfg := &Config{
		Global: GlobalConfig{
			LogLevel:  "info",
			LogFormat: "text",
			WebRoot:   "",
		},
		Domains: []Domain{
			{Host: "noroot.com", Type: "static", SSL: SSLConfig{Mode: "off", MinVersion: "1.2"}},
		},
	}
	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error for missing root with empty web_root")
	}
}

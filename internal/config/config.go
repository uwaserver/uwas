package config

import (
	"encoding/json"
	"strings"
	"time"
)

type Config struct {
	Global    GlobalConfig `yaml:"global"`
	Domains   []Domain     `yaml:"domains"`
	Include   []string     `yaml:"include"`    // glob patterns: ["domains.d/*.yaml"]
	DomainsDir string     `yaml:"domains_dir"` // directory of per-domain YAML files
}

type GlobalConfig struct {
	WorkerCount    string         `yaml:"worker_count"`
	MaxConnections int            `yaml:"max_connections"`
	HTTPListen     string         `yaml:"http_listen"`
	HTTPSListen    string         `yaml:"https_listen"`
	HTTP3Enabled   bool           `yaml:"http3"`
	PIDFile        string         `yaml:"pid_file"`
	WebRoot        string         `yaml:"web_root"`
	LogLevel       string         `yaml:"log_level"`
	LogFormat      string         `yaml:"log_format"`
	TrustedProxies []string       `yaml:"trusted_proxies"`
	Timeouts       TimeoutConfig  `yaml:"timeouts"`
	Admin          AdminConfig    `yaml:"admin"`
	MCP            MCPConfig      `yaml:"mcp"`
	ACME           ACMEConfig     `yaml:"acme"`
	Cache          CacheConfig    `yaml:"cache"`
	Alerting       AlertingConfig `yaml:"alerting"`
	Backup         BackupConfig   `yaml:"backup"`
}

type BackupConfig struct {
	Enabled  bool             `yaml:"enabled"`
	Provider string           `yaml:"provider"` // local | s3 | sftp
	Schedule string           `yaml:"schedule"` // duration string e.g. "24h"
	Keep     int              `yaml:"keep"`      // keep last N backups
	Local    BackupLocalConfig `yaml:"local"`
	S3       BackupS3Config   `yaml:"s3"`
	SFTP     BackupSFTPConfig `yaml:"sftp"`
}

type BackupLocalConfig struct {
	Path string `yaml:"path"`
}

type BackupS3Config struct {
	Endpoint  string `yaml:"endpoint"`
	Bucket    string `yaml:"bucket"`
	AccessKey string `yaml:"access_key"`
	SecretKey string `yaml:"secret_key"`
	Region    string `yaml:"region"`
}

type BackupSFTPConfig struct {
	Host       string `yaml:"host"`
	Port       int    `yaml:"port"`
	User       string `yaml:"user"`
	KeyFile    string `yaml:"key_file"`
	Password   string `yaml:"password"`
	RemotePath string `yaml:"remote_path"`
}

type TimeoutConfig struct {
	Read          Duration `yaml:"read"`
	ReadHeader    Duration `yaml:"read_header"`
	Write         Duration `yaml:"write"`
	Idle          Duration `yaml:"idle"`
	ShutdownGrace Duration `yaml:"shutdown_grace"`
	MaxHeaderBytes int     `yaml:"max_header_bytes"`
}

type AdminConfig struct {
	Listen  string `yaml:"listen"`
	Enabled bool   `yaml:"enabled"`
	APIKey  string `yaml:"api_key"`
}

type MCPConfig struct {
	Enabled bool   `yaml:"enabled"`
	Listen  string `yaml:"listen"`
}

type ACMEConfig struct {
	Email          string            `yaml:"email"`
	CAURL          string            `yaml:"ca_url"`
	Storage        string            `yaml:"storage"`
	DNSProvider    string            `yaml:"dns_provider"`
	DNSCredentials map[string]string `yaml:"dns_credentials"`
	OnDemand       bool              `yaml:"on_demand"`
	OnDemandAsk    string            `yaml:"on_demand_ask"`
}

type CacheConfig struct {
	Enabled               bool     `yaml:"enabled"`
	MemoryLimit           ByteSize `yaml:"memory_limit"`
	DiskPath              string   `yaml:"disk_path"`
	DiskLimit             ByteSize `yaml:"disk_limit"`
	DefaultTTL            int      `yaml:"default_ttl"`
	GraceTTL              int      `yaml:"grace_ttl"`
	StaleWhileRevalidate  bool     `yaml:"stale_while_revalidate"`
	PurgeKey              string   `yaml:"purge_key"`
}

type AlertingConfig struct {
	Enabled    bool   `yaml:"enabled"`
	WebhookURL string `yaml:"webhook_url"`
}

type MirrorConfig struct {
	Enabled bool   `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Backend string `yaml:"backend,omitempty" json:"backend,omitempty"`
	Percent int    `yaml:"percent,omitempty" json:"percent,omitempty"`
}

type Domain struct {
	Host        string           `yaml:"host" json:"host"`
	Aliases     []string         `yaml:"aliases,omitempty" json:"aliases,omitempty"`
	Root        string           `yaml:"root,omitempty" json:"root,omitempty"`
	Type        string           `yaml:"type" json:"type"`
	SSL         SSLConfig        `yaml:"ssl" json:"ssl"`
	PHP         PHPConfig        `yaml:"php,omitempty" json:"php,omitempty"`
	Cache       DomainCache      `yaml:"cache,omitempty" json:"cache,omitempty"`
	Rewrites    []RewriteRule    `yaml:"rewrites,omitempty" json:"rewrites,omitempty"`
	Htaccess    HtaccessConfig   `yaml:"htaccess,omitempty" json:"htaccess,omitempty"`
	Security    SecurityConfig   `yaml:"security,omitempty" json:"security,omitempty"`
	Headers     HeadersConfig    `yaml:"headers,omitempty" json:"headers,omitempty"`
	Compression CompressionConfig `yaml:"compression,omitempty" json:"compression,omitempty"`
	AccessLog   AccessLogConfig  `yaml:"access_log,omitempty" json:"access_log,omitempty"`
	ErrorPages  map[int]string   `yaml:"error_pages,omitempty" json:"error_pages,omitempty"`
	Proxy       ProxyConfig      `yaml:"proxy,omitempty" json:"proxy,omitempty"`
	Redirect    RedirectConfig   `yaml:"redirect,omitempty" json:"redirect,omitempty"`
	TryFiles          []string                `yaml:"try_files,omitempty" json:"try_files,omitempty"`
	SPAMode           bool                    `yaml:"spa_mode,omitempty" json:"spa_mode,omitempty"`
	IndexFiles        []string                `yaml:"index_files,omitempty" json:"index_files,omitempty"`
	DirectoryListing  bool                    `yaml:"directory_listing,omitempty" json:"directory_listing,omitempty"`
	ImageOptimization ImageOptimizationConfig `yaml:"image_optimization,omitempty" json:"image_optimization,omitempty"`
	CORS              CORSConfig              `yaml:"cors,omitempty" json:"cors,omitempty"`
	BasicAuth         BasicAuthConfig         `yaml:"basic_auth,omitempty" json:"basic_auth,omitempty"`
}

// MarshalYAML produces clean YAML by omitting zero-value nested structs.
func (d Domain) MarshalYAML() (any, error) {
	m := map[string]any{
		"host": d.Host,
		"type": d.Type,
		"ssl":  map[string]string{"mode": d.SSL.Mode},
	}
	if d.Root != "" {
		m["root"] = d.Root
	}
	if len(d.Aliases) > 0 {
		m["aliases"] = d.Aliases
	}
	if d.PHP.FPMAddress != "" {
		php := map[string]any{"fpm_address": d.PHP.FPMAddress}
		if len(d.PHP.IndexFiles) > 0 {
			php["index_files"] = d.PHP.IndexFiles
		}
		if d.PHP.Timeout.Duration > 0 {
			php["timeout"] = d.PHP.Timeout.Duration.String()
		}
		if d.PHP.MaxUpload > 0 {
			php["max_upload"] = int64(d.PHP.MaxUpload)
		}
		m["php"] = php
	}
	if d.Cache.Enabled {
		cache := map[string]any{"enabled": true, "ttl": d.Cache.TTL}
		if len(d.Cache.Rules) > 0 {
			cache["rules"] = d.Cache.Rules
		}
		m["cache"] = cache
	}
	if d.Htaccess.Mode != "" {
		m["htaccess"] = map[string]string{"mode": d.Htaccess.Mode}
	}
	if len(d.Security.BlockedPaths) > 0 || d.Security.WAF.Enabled || d.Security.RateLimit.Requests > 0 {
		sec := map[string]any{}
		if len(d.Security.BlockedPaths) > 0 {
			sec["blocked_paths"] = d.Security.BlockedPaths
		}
		if d.Security.WAF.Enabled {
			sec["waf"] = map[string]any{"enabled": true}
		}
		if d.Security.RateLimit.Requests > 0 {
			sec["rate_limit"] = d.Security.RateLimit
		}
		if len(d.Security.IPWhitelist) > 0 {
			sec["ip_whitelist"] = d.Security.IPWhitelist
		}
		if len(d.Security.IPBlacklist) > 0 {
			sec["ip_blacklist"] = d.Security.IPBlacklist
		}
		m["security"] = sec
	}
	if d.Compression.Enabled {
		m["compression"] = d.Compression
	}
	if len(d.Proxy.Upstreams) > 0 {
		proxy := map[string]any{"upstreams": d.Proxy.Upstreams}
		if d.Proxy.Algorithm != "" {
			proxy["algorithm"] = d.Proxy.Algorithm
		}
		if d.Proxy.WebSocket {
			proxy["websocket"] = true
		}
		m["proxy"] = proxy
	}
	if d.Redirect.Target != "" {
		redir := map[string]any{"target": d.Redirect.Target}
		if d.Redirect.Status > 0 {
			redir["status"] = d.Redirect.Status
		}
		if d.Redirect.PreservePath {
			redir["preserve_path"] = true
		}
		m["redirect"] = redir
	}
	if len(d.Rewrites) > 0 {
		m["rewrites"] = d.Rewrites
	}
	if len(d.TryFiles) > 0 {
		m["try_files"] = d.TryFiles
	}
	if d.SPAMode {
		m["spa_mode"] = true
	}
	if len(d.IndexFiles) > 0 {
		m["index_files"] = d.IndexFiles
	}
	if d.DirectoryListing {
		m["directory_listing"] = true
	}
	if d.CORS.Enabled {
		m["cors"] = d.CORS
	}
	if d.BasicAuth.Enabled {
		m["basic_auth"] = d.BasicAuth
	}
	if len(d.ErrorPages) > 0 {
		m["error_pages"] = d.ErrorPages
	}
	return m, nil
}

type CORSConfig struct {
	Enabled          bool     `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	AllowedOrigins   []string `yaml:"allowed_origins,omitempty" json:"allowed_origins,omitempty"`
	AllowedMethods   []string `yaml:"allowed_methods,omitempty" json:"allowed_methods,omitempty"`
	AllowedHeaders   []string `yaml:"allowed_headers,omitempty" json:"allowed_headers,omitempty"`
	AllowCredentials bool     `yaml:"allow_credentials,omitempty" json:"allow_credentials,omitempty"`
	MaxAge           int      `yaml:"max_age,omitempty" json:"max_age,omitempty"`
}

type BasicAuthConfig struct {
	Enabled bool              `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Users   map[string]string `yaml:"users,omitempty" json:"users,omitempty"`
	Realm   string            `yaml:"realm,omitempty" json:"realm,omitempty"`
}

type ImageOptimizationConfig struct {
	Enabled bool     `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Formats []string `yaml:"formats,omitempty" json:"formats,omitempty"`
}

type SSLConfig struct {
	Mode       string `yaml:"mode,omitempty" json:"mode,omitempty"`
	Cert       string `yaml:"cert,omitempty" json:"cert,omitempty"`
	Key        string `yaml:"key,omitempty" json:"key,omitempty"`
	MinVersion string `yaml:"min_version,omitempty" json:"min_version,omitempty"`
}

type PHPConfig struct {
	FPMAddress string            `yaml:"fpm_address,omitempty" json:"fpm_address,omitempty"`
	IndexFiles []string          `yaml:"index_files,omitempty" json:"index_files,omitempty"`
	MaxUpload  ByteSize          `yaml:"max_upload,omitempty" json:"max_upload,omitempty"`
	Timeout    Duration          `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	Env        map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
}

type DomainCache struct {
	Enabled bool             `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	TTL     int              `yaml:"ttl,omitempty" json:"ttl,omitempty"`
	Rules   []CacheRule      `yaml:"rules,omitempty" json:"rules,omitempty"`
	Tags    []string         `yaml:"tags,omitempty" json:"tags,omitempty"`
	ESI     bool             `yaml:"esi,omitempty" json:"esi,omitempty"`
}

type CacheRule struct {
	Match  string `yaml:"match,omitempty" json:"match,omitempty"`
	TTL    int    `yaml:"ttl,omitempty" json:"ttl,omitempty"`
	Bypass bool   `yaml:"bypass,omitempty" json:"bypass,omitempty"`
}

type RewriteRule struct {
	Match      string   `yaml:"match,omitempty" json:"match,omitempty"`
	To         string   `yaml:"to,omitempty" json:"to,omitempty"`
	Status     int      `yaml:"status,omitempty" json:"status,omitempty"`
	Conditions []string `yaml:"conditions,omitempty" json:"conditions,omitempty"`
	Flags      []string `yaml:"flags,omitempty" json:"flags,omitempty"`
}

type HtaccessConfig struct {
	Mode string `yaml:"mode,omitempty" json:"mode,omitempty"`
}

type SecurityConfig struct {
	BlockedPaths      []string           `yaml:"blocked_paths,omitempty" json:"blocked_paths,omitempty"`
	HotlinkProtection HotlinkConfig      `yaml:"hotlink_protection,omitempty" json:"hotlink_protection,omitempty"`
	RateLimit         RateLimitConfig    `yaml:"rate_limit,omitempty" json:"rate_limit,omitempty"`
	WAF               WAFConfig          `yaml:"waf,omitempty" json:"waf,omitempty"`
	IPWhitelist       []string           `yaml:"ip_whitelist,omitempty" json:"ip_whitelist,omitempty"`
	IPBlacklist       []string           `yaml:"ip_blacklist,omitempty" json:"ip_blacklist,omitempty"`
}

type HotlinkConfig struct {
	Enabled         bool     `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	AllowedReferers []string `yaml:"allowed_referers,omitempty" json:"allowed_referers,omitempty"`
	Extensions      []string `yaml:"extensions,omitempty" json:"extensions,omitempty"`
}

type RateLimitConfig struct {
	Requests int      `yaml:"requests,omitempty" json:"requests,omitempty"`
	Window   Duration `yaml:"window,omitempty" json:"window,omitempty"`
	By       string   `yaml:"by,omitempty" json:"by,omitempty"`
}

type WAFConfig struct {
	Enabled bool     `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Rules   []string `yaml:"rules,omitempty" json:"rules,omitempty"`
}

type HeadersConfig struct {
	Add            map[string]string `yaml:"add,omitempty" json:"add,omitempty"`
	Remove         []string          `yaml:"remove,omitempty" json:"remove,omitempty"`
	RequestAdd     map[string]string `yaml:"request_add,omitempty" json:"request_add,omitempty"`
	RequestRemove  []string          `yaml:"request_remove,omitempty" json:"request_remove,omitempty"`
	ResponseAdd    map[string]string `yaml:"response_add,omitempty" json:"response_add,omitempty"`
	ResponseRemove []string          `yaml:"response_remove,omitempty" json:"response_remove,omitempty"`
}

type CompressionConfig struct {
	Enabled    bool     `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Algorithms []string `yaml:"algorithms,omitempty" json:"algorithms,omitempty"`
	MinSize    int      `yaml:"min_size,omitempty" json:"min_size,omitempty"`
	Types      []string `yaml:"types,omitempty" json:"types,omitempty"`
}

type AccessLogConfig struct {
	Path       string          `yaml:"path,omitempty" json:"path,omitempty"`
	Format     string          `yaml:"format,omitempty" json:"format,omitempty"`
	BufferSize int             `yaml:"buffer_size,omitempty" json:"buffer_size,omitempty"`
	Rotate     RotateConfig    `yaml:"rotate,omitempty" json:"rotate,omitempty"`
}

type RotateConfig struct {
	MaxSize    ByteSize `yaml:"max_size,omitempty" json:"max_size,omitempty"`
	MaxAge     Duration `yaml:"max_age,omitempty" json:"max_age,omitempty"`
	MaxBackups int      `yaml:"max_backups,omitempty" json:"max_backups,omitempty"`
}

type ProxyConfig struct {
	Upstreams      []Upstream         `yaml:"upstreams,omitempty" json:"upstreams,omitempty"`
	Algorithm      string             `yaml:"algorithm,omitempty" json:"algorithm,omitempty"`
	HealthCheck    HealthCheckConfig  `yaml:"health_check,omitempty" json:"health_check,omitempty"`
	Sticky         StickyConfig       `yaml:"sticky,omitempty" json:"sticky,omitempty"`
	CircuitBreaker CircuitConfig      `yaml:"circuit_breaker,omitempty" json:"circuit_breaker,omitempty"`
	WebSocket      bool               `yaml:"websocket,omitempty" json:"websocket,omitempty"`
	Timeouts       ProxyTimeouts      `yaml:"timeouts,omitempty" json:"timeouts,omitempty"`
	MaxRetries     int                `yaml:"max_retries,omitempty" json:"max_retries,omitempty"`
	Canary         CanaryConfig       `yaml:"canary,omitempty" json:"canary,omitempty"`
	Mirror         MirrorConfig       `yaml:"mirror,omitempty" json:"mirror,omitempty"`
}

type CanaryConfig struct {
	Enabled   bool       `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Weight    int        `yaml:"weight,omitempty" json:"weight,omitempty"`
	Upstreams []Upstream `yaml:"upstreams,omitempty" json:"upstreams,omitempty"`
	Cookie    string     `yaml:"cookie,omitempty" json:"cookie,omitempty"`
}

type Upstream struct {
	Address string `yaml:"address,omitempty" json:"address,omitempty"`
	Weight  int    `yaml:"weight,omitempty" json:"weight,omitempty"`
}

type HealthCheckConfig struct {
	Path      string   `yaml:"path,omitempty" json:"path,omitempty"`
	Interval  Duration `yaml:"interval,omitempty" json:"interval,omitempty"`
	Timeout   Duration `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	Threshold int      `yaml:"threshold,omitempty" json:"threshold,omitempty"`
	Rise      int      `yaml:"rise,omitempty" json:"rise,omitempty"`
}

type StickyConfig struct {
	Type       string `yaml:"type,omitempty" json:"type,omitempty"`
	CookieName string `yaml:"cookie_name,omitempty" json:"cookie_name,omitempty"`
	TTL        int    `yaml:"ttl,omitempty" json:"ttl,omitempty"`
}

type CircuitConfig struct {
	Threshold int      `yaml:"threshold,omitempty" json:"threshold,omitempty"`
	Timeout   Duration `yaml:"timeout,omitempty" json:"timeout,omitempty"`
}

type ProxyTimeouts struct {
	Connect Duration `yaml:"connect,omitempty" json:"connect,omitempty"`
	Read    Duration `yaml:"read,omitempty" json:"read,omitempty"`
	Write   Duration `yaml:"write,omitempty" json:"write,omitempty"`
}

type RedirectConfig struct {
	Target       string `yaml:"target,omitempty" json:"target,omitempty"`
	Status       int    `yaml:"status,omitempty" json:"status,omitempty"`
	PreservePath bool   `yaml:"preserve_path,omitempty" json:"preserve_path,omitempty"`
}

// Duration wraps time.Duration for YAML/JSON unmarshaling of strings like "30s", "5m"
// or plain numbers (interpreted as seconds).
type Duration struct {
	time.Duration
}

func (d Duration) MarshalJSON() ([]byte, error) {
	if d.Duration == 0 {
		return []byte("0"), nil
	}
	return []byte(`"` + d.Duration.String() + `"`), nil
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	// Try number (seconds)
	s := strings.TrimSpace(string(b))
	if s == "null" || s == "" {
		return nil
	}
	if s[0] >= '0' && s[0] <= '9' || s[0] == '-' {
		var secs float64
		if err := json.Unmarshal(b, &secs); err != nil {
			return err
		}
		d.Duration = time.Duration(secs * float64(time.Second))
		return nil
	}
	// Try string like "30s"
	var str string
	if err := json.Unmarshal(b, &str); err != nil {
		return err
	}
	dur, err := time.ParseDuration(str)
	if err != nil {
		return err
	}
	d.Duration = dur
	return nil
}

func (d *Duration) UnmarshalYAML(unmarshal func(any) error) error {
	var s string
	if err := unmarshal(&s); err != nil {
		var secs int
		if err2 := unmarshal(&secs); err2 != nil {
			return err
		}
		d.Duration = time.Duration(secs) * time.Second
		return nil
	}
	dur, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	d.Duration = dur
	return nil
}

// ByteSize represents a size in bytes, parsed from strings like "512MB", "10GB".
type ByteSize int64

func (b ByteSize) MarshalJSON() ([]byte, error) {
	return json.Marshal(int64(b))
}

func (b *ByteSize) UnmarshalJSON(data []byte) error {
	s := strings.TrimSpace(string(data))
	if s == "null" {
		return nil
	}
	// Try number
	var n int64
	if err := json.Unmarshal(data, &n); err == nil {
		*b = ByteSize(n)
		return nil
	}
	// Try string like "512MB"
	var str string
	if err := json.Unmarshal(data, &str); err != nil {
		return err
	}
	size, err := parseByteSize(str)
	if err != nil {
		return err
	}
	*b = size
	return nil
}

const (
	KB ByteSize = 1024
	MB ByteSize = 1024 * KB
	GB ByteSize = 1024 * MB
)

func (b *ByteSize) UnmarshalYAML(unmarshal func(any) error) error {
	var s string
	if err := unmarshal(&s); err != nil {
		var n int64
		if err2 := unmarshal(&n); err2 != nil {
			return err
		}
		*b = ByteSize(n)
		return nil
	}
	size, err := parseByteSize(s)
	if err != nil {
		return err
	}
	*b = size
	return nil
}

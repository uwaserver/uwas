package config

import "time"

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
	Enabled bool   `yaml:"enabled" json:"enabled"`
	Backend string `yaml:"backend" json:"backend"`
	Percent int    `yaml:"percent" json:"percent"`
}

type Domain struct {
	Host        string           `yaml:"host" json:"host"`
	Aliases     []string         `yaml:"aliases" json:"aliases"`
	Root        string           `yaml:"root" json:"root"`
	Type        string           `yaml:"type" json:"type"`
	SSL         SSLConfig        `yaml:"ssl" json:"ssl"`
	PHP         PHPConfig        `yaml:"php" json:"php"`
	Cache       DomainCache      `yaml:"cache" json:"cache"`
	Rewrites    []RewriteRule    `yaml:"rewrites" json:"rewrites"`
	Htaccess    HtaccessConfig   `yaml:"htaccess" json:"htaccess"`
	Security    SecurityConfig   `yaml:"security" json:"security"`
	Headers     HeadersConfig    `yaml:"headers" json:"headers"`
	Compression CompressionConfig `yaml:"compression" json:"compression"`
	AccessLog   AccessLogConfig  `yaml:"access_log" json:"access_log"`
	ErrorPages  map[int]string   `yaml:"error_pages" json:"error_pages"`
	Proxy       ProxyConfig      `yaml:"proxy" json:"proxy"`
	Redirect    RedirectConfig   `yaml:"redirect" json:"redirect"`
	TryFiles          []string                `yaml:"try_files" json:"try_files"`
	SPAMode           bool                    `yaml:"spa_mode" json:"spa_mode"`
	IndexFiles        []string                `yaml:"index_files" json:"index_files"`
	DirectoryListing  bool                    `yaml:"directory_listing" json:"directory_listing"`
	ImageOptimization ImageOptimizationConfig `yaml:"image_optimization" json:"image_optimization"`
	CORS              CORSConfig              `yaml:"cors" json:"cors"`
	BasicAuth         BasicAuthConfig         `yaml:"basic_auth" json:"basic_auth"`
}

type CORSConfig struct {
	Enabled          bool     `yaml:"enabled" json:"enabled"`
	AllowedOrigins   []string `yaml:"allowed_origins" json:"allowed_origins"`
	AllowedMethods   []string `yaml:"allowed_methods" json:"allowed_methods"`
	AllowedHeaders   []string `yaml:"allowed_headers" json:"allowed_headers"`
	AllowCredentials bool     `yaml:"allow_credentials" json:"allow_credentials"`
	MaxAge           int      `yaml:"max_age" json:"max_age"`
}

type BasicAuthConfig struct {
	Enabled bool              `yaml:"enabled" json:"enabled"`
	Users   map[string]string `yaml:"users" json:"users"`
	Realm   string            `yaml:"realm" json:"realm"`
}

type ImageOptimizationConfig struct {
	Enabled bool     `yaml:"enabled" json:"enabled"`
	Formats []string `yaml:"formats" json:"formats"`
}

type SSLConfig struct {
	Mode       string `yaml:"mode" json:"mode"`
	Cert       string `yaml:"cert" json:"cert"`
	Key        string `yaml:"key" json:"key"`
	MinVersion string `yaml:"min_version" json:"min_version"`
}

type PHPConfig struct {
	FPMAddress string            `yaml:"fpm_address" json:"fpm_address"`
	IndexFiles []string          `yaml:"index_files" json:"index_files"`
	MaxUpload  ByteSize          `yaml:"max_upload" json:"max_upload"`
	Timeout    Duration          `yaml:"timeout" json:"timeout"`
	Env        map[string]string `yaml:"env" json:"env"`
}

type DomainCache struct {
	Enabled bool             `yaml:"enabled" json:"enabled"`
	TTL     int              `yaml:"ttl" json:"ttl"`
	Rules   []CacheRule      `yaml:"rules" json:"rules"`
	Tags    []string         `yaml:"tags" json:"tags"`
	ESI     bool             `yaml:"esi" json:"esi"`
}

type CacheRule struct {
	Match  string `yaml:"match" json:"match"`
	TTL    int    `yaml:"ttl" json:"ttl"`
	Bypass bool   `yaml:"bypass" json:"bypass"`
}

type RewriteRule struct {
	Match      string   `yaml:"match" json:"match"`
	To         string   `yaml:"to" json:"to"`
	Status     int      `yaml:"status" json:"status"`
	Conditions []string `yaml:"conditions" json:"conditions"`
	Flags      []string `yaml:"flags" json:"flags"`
}

type HtaccessConfig struct {
	Mode string `yaml:"mode" json:"mode"`
}

type SecurityConfig struct {
	BlockedPaths      []string           `yaml:"blocked_paths" json:"blocked_paths"`
	HotlinkProtection HotlinkConfig      `yaml:"hotlink_protection" json:"hotlink_protection"`
	RateLimit         RateLimitConfig    `yaml:"rate_limit" json:"rate_limit"`
	WAF               WAFConfig          `yaml:"waf" json:"waf"`
	IPWhitelist       []string           `yaml:"ip_whitelist" json:"ip_whitelist"`
	IPBlacklist       []string           `yaml:"ip_blacklist" json:"ip_blacklist"`
}

type HotlinkConfig struct {
	Enabled         bool     `yaml:"enabled" json:"enabled"`
	AllowedReferers []string `yaml:"allowed_referers" json:"allowed_referers"`
	Extensions      []string `yaml:"extensions" json:"extensions"`
}

type RateLimitConfig struct {
	Requests int      `yaml:"requests" json:"requests"`
	Window   Duration `yaml:"window" json:"window"`
	By       string   `yaml:"by" json:"by"`
}

type WAFConfig struct {
	Enabled bool     `yaml:"enabled" json:"enabled"`
	Rules   []string `yaml:"rules" json:"rules"`
}

type HeadersConfig struct {
	Add            map[string]string `yaml:"add" json:"add"`
	Remove         []string          `yaml:"remove" json:"remove"`
	RequestAdd     map[string]string `yaml:"request_add" json:"request_add"`
	RequestRemove  []string          `yaml:"request_remove" json:"request_remove"`
	ResponseAdd    map[string]string `yaml:"response_add" json:"response_add"`
	ResponseRemove []string          `yaml:"response_remove" json:"response_remove"`
}

type CompressionConfig struct {
	Enabled    bool     `yaml:"enabled" json:"enabled"`
	Algorithms []string `yaml:"algorithms" json:"algorithms"`
	MinSize    int      `yaml:"min_size" json:"min_size"`
	Types      []string `yaml:"types" json:"types"`
}

type AccessLogConfig struct {
	Path       string          `yaml:"path" json:"path"`
	Format     string          `yaml:"format" json:"format"`
	BufferSize int             `yaml:"buffer_size" json:"buffer_size"`
	Rotate     RotateConfig    `yaml:"rotate" json:"rotate"`
}

type RotateConfig struct {
	MaxSize    ByteSize `yaml:"max_size" json:"max_size"`
	MaxAge     Duration `yaml:"max_age" json:"max_age"`
	MaxBackups int      `yaml:"max_backups" json:"max_backups"`
}

type ProxyConfig struct {
	Upstreams      []Upstream         `yaml:"upstreams" json:"upstreams"`
	Algorithm      string             `yaml:"algorithm" json:"algorithm"`
	HealthCheck    HealthCheckConfig  `yaml:"health_check" json:"health_check"`
	Sticky         StickyConfig       `yaml:"sticky" json:"sticky"`
	CircuitBreaker CircuitConfig      `yaml:"circuit_breaker" json:"circuit_breaker"`
	WebSocket      bool               `yaml:"websocket" json:"websocket"`
	Timeouts       ProxyTimeouts      `yaml:"timeouts" json:"timeouts"`
	MaxRetries     int                `yaml:"max_retries" json:"max_retries"`
	Canary         CanaryConfig       `yaml:"canary" json:"canary"`
	Mirror         MirrorConfig       `yaml:"mirror" json:"mirror"`
}

type CanaryConfig struct {
	Enabled   bool       `yaml:"enabled" json:"enabled"`
	Weight    int        `yaml:"weight" json:"weight"`
	Upstreams []Upstream `yaml:"upstreams" json:"upstreams"`
	Cookie    string     `yaml:"cookie" json:"cookie"`
}

type Upstream struct {
	Address string `yaml:"address" json:"address"`
	Weight  int    `yaml:"weight" json:"weight"`
}

type HealthCheckConfig struct {
	Path      string   `yaml:"path" json:"path"`
	Interval  Duration `yaml:"interval" json:"interval"`
	Timeout   Duration `yaml:"timeout" json:"timeout"`
	Threshold int      `yaml:"threshold" json:"threshold"`
	Rise      int      `yaml:"rise" json:"rise"`
}

type StickyConfig struct {
	Type       string `yaml:"type" json:"type"`
	CookieName string `yaml:"cookie_name" json:"cookie_name"`
	TTL        int    `yaml:"ttl" json:"ttl"`
}

type CircuitConfig struct {
	Threshold int      `yaml:"threshold" json:"threshold"`
	Timeout   Duration `yaml:"timeout" json:"timeout"`
}

type ProxyTimeouts struct {
	Connect Duration `yaml:"connect" json:"connect"`
	Read    Duration `yaml:"read" json:"read"`
	Write   Duration `yaml:"write" json:"write"`
}

type RedirectConfig struct {
	Target       string `yaml:"target" json:"target"`
	Status       int    `yaml:"status" json:"status"`
	PreservePath bool   `yaml:"preserve_path" json:"preserve_path"`
}

// Duration wraps time.Duration for YAML unmarshaling of strings like "30s", "5m".
type Duration struct {
	time.Duration
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

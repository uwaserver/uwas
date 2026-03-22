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
	PIDFile        string         `yaml:"pid_file"`
	LogLevel       string         `yaml:"log_level"`
	LogFormat      string         `yaml:"log_format"`
	TrustedProxies []string       `yaml:"trusted_proxies"`
	Timeouts       TimeoutConfig  `yaml:"timeouts"`
	Admin          AdminConfig    `yaml:"admin"`
	MCP            MCPConfig      `yaml:"mcp"`
	ACME           ACMEConfig     `yaml:"acme"`
	Cache          CacheConfig    `yaml:"cache"`
}

type TimeoutConfig struct {
	Read          Duration `yaml:"read"`
	Write         Duration `yaml:"write"`
	Idle          Duration `yaml:"idle"`
	ShutdownGrace Duration `yaml:"shutdown_grace"`
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

type Domain struct {
	Host        string           `yaml:"host"`
	Aliases     []string         `yaml:"aliases"`
	Root        string           `yaml:"root"`
	Type        string           `yaml:"type"`
	SSL         SSLConfig        `yaml:"ssl"`
	PHP         PHPConfig        `yaml:"php"`
	Cache       DomainCache      `yaml:"cache"`
	Rewrites    []RewriteRule    `yaml:"rewrites"`
	Htaccess    HtaccessConfig   `yaml:"htaccess"`
	Security    SecurityConfig   `yaml:"security"`
	Headers     HeadersConfig    `yaml:"headers"`
	Compression CompressionConfig `yaml:"compression"`
	AccessLog   AccessLogConfig  `yaml:"access_log"`
	ErrorPages  map[int]string   `yaml:"error_pages"`
	Proxy       ProxyConfig      `yaml:"proxy"`
	Redirect    RedirectConfig   `yaml:"redirect"`
	TryFiles          []string                `yaml:"try_files"`
	SPAMode           bool                    `yaml:"spa_mode"`
	IndexFiles        []string                `yaml:"index_files"`
	DirectoryListing  bool                    `yaml:"directory_listing"`
	ImageOptimization ImageOptimizationConfig `yaml:"image_optimization"`
}

type ImageOptimizationConfig struct {
	Enabled bool     `yaml:"enabled"`
	Formats []string `yaml:"formats"` // ["webp", "avif"]
}

type SSLConfig struct {
	Mode       string `yaml:"mode"`
	Cert       string `yaml:"cert"`
	Key        string `yaml:"key"`
	MinVersion string `yaml:"min_version"`
}

type PHPConfig struct {
	FPMAddress string            `yaml:"fpm_address"`
	IndexFiles []string          `yaml:"index_files"`
	MaxUpload  ByteSize          `yaml:"max_upload"`
	Timeout    Duration          `yaml:"timeout"`
	Env        map[string]string `yaml:"env"`
}

type DomainCache struct {
	Enabled bool             `yaml:"enabled"`
	TTL     int              `yaml:"ttl"`
	Rules   []CacheRule      `yaml:"rules"`
	Tags    []string         `yaml:"tags"`
	ESI     bool             `yaml:"esi"`
}

type CacheRule struct {
	Match  string `yaml:"match"`
	TTL    int    `yaml:"ttl"`
	Bypass bool   `yaml:"bypass"`
}

type RewriteRule struct {
	Match      string   `yaml:"match"`
	To         string   `yaml:"to"`
	Status     int      `yaml:"status"`
	Conditions []string `yaml:"conditions"`
	Flags      []string `yaml:"flags"`
}

type HtaccessConfig struct {
	Mode string `yaml:"mode"`
}

type SecurityConfig struct {
	BlockedPaths      []string           `yaml:"blocked_paths"`
	HotlinkProtection HotlinkConfig      `yaml:"hotlink_protection"`
	RateLimit         RateLimitConfig    `yaml:"rate_limit"`
	WAF               WAFConfig          `yaml:"waf"`
	IPWhitelist       []string           `yaml:"ip_whitelist"`
	IPBlacklist       []string           `yaml:"ip_blacklist"`
}

type HotlinkConfig struct {
	Enabled         bool     `yaml:"enabled"`
	AllowedReferers []string `yaml:"allowed_referers"`
	Extensions      []string `yaml:"extensions"`
}

type RateLimitConfig struct {
	Requests int      `yaml:"requests"`
	Window   Duration `yaml:"window"`
	By       string   `yaml:"by"`
}

type WAFConfig struct {
	Enabled bool     `yaml:"enabled"`
	Rules   []string `yaml:"rules"`
}

type HeadersConfig struct {
	Add            map[string]string `yaml:"add"`
	Remove         []string          `yaml:"remove"`
	RequestAdd     map[string]string `yaml:"request_add"`
	RequestRemove  []string          `yaml:"request_remove"`
	ResponseAdd    map[string]string `yaml:"response_add"`
	ResponseRemove []string          `yaml:"response_remove"`
}

type CompressionConfig struct {
	Enabled    bool     `yaml:"enabled"`
	Algorithms []string `yaml:"algorithms"`
	MinSize    int      `yaml:"min_size"`
	Types      []string `yaml:"types"`
}

type AccessLogConfig struct {
	Path       string          `yaml:"path"`
	Format     string          `yaml:"format"`
	BufferSize int             `yaml:"buffer_size"`
	Rotate     RotateConfig    `yaml:"rotate"`
}

type RotateConfig struct {
	MaxSize    ByteSize `yaml:"max_size"`
	MaxAge     Duration `yaml:"max_age"`
	MaxBackups int      `yaml:"max_backups"`
}

type ProxyConfig struct {
	Upstreams      []Upstream         `yaml:"upstreams"`
	Algorithm      string             `yaml:"algorithm"`
	HealthCheck    HealthCheckConfig  `yaml:"health_check"`
	Sticky         StickyConfig       `yaml:"sticky"`
	CircuitBreaker CircuitConfig      `yaml:"circuit_breaker"`
	WebSocket      bool               `yaml:"websocket"`
	Timeouts       ProxyTimeouts      `yaml:"timeouts"`
	MaxRetries     int                `yaml:"max_retries"`
	Canary         CanaryConfig       `yaml:"canary"`
}

type CanaryConfig struct {
	Enabled   bool       `yaml:"enabled"`
	Weight    int        `yaml:"weight"`
	Upstreams []Upstream `yaml:"upstreams"`
	Cookie    string     `yaml:"cookie"`
}

type Upstream struct {
	Address string `yaml:"address"`
	Weight  int    `yaml:"weight"`
}

type HealthCheckConfig struct {
	Path      string   `yaml:"path"`
	Interval  Duration `yaml:"interval"`
	Timeout   Duration `yaml:"timeout"`
	Threshold int      `yaml:"threshold"`
	Rise      int      `yaml:"rise"`
}

type StickyConfig struct {
	Type       string `yaml:"type"`
	CookieName string `yaml:"cookie_name"`
	TTL        int    `yaml:"ttl"`
}

type CircuitConfig struct {
	Threshold int      `yaml:"threshold"`
	Timeout   Duration `yaml:"timeout"`
}

type ProxyTimeouts struct {
	Connect Duration `yaml:"connect"`
	Read    Duration `yaml:"read"`
	Write   Duration `yaml:"write"`
}

type RedirectConfig struct {
	Target       string `yaml:"target"`
	Status       int    `yaml:"status"`
	PreservePath bool   `yaml:"preserve_path"`
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

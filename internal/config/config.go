package config

// Config is the root configuration object. Sub-feature struct types live in
// per-feature files in this package (see admin.go, acme.go, backup.go,
// cache.go, domain.go, proxy.go, security.go, types.go).
type Config struct {
	Global     GlobalConfig `yaml:"global"`
	Domains    []Domain     `yaml:"domains"`
	Include    []string     `yaml:"include"`     // glob patterns: ["domains.d/*.yaml"]
	DomainsDir string       `yaml:"domains_dir"` // directory of per-domain YAML files
}

// GlobalConfig is the server-wide configuration: listeners, log shape, ACME
// account, cache + backup defaults, admin API, alerting fan-out.
type GlobalConfig struct {
	WorkerCount    string           `yaml:"worker_count"`
	MaxConnections int              `yaml:"max_connections"`
	HTTPListen     string           `yaml:"http_listen"`
	HTTPSListen    string           `yaml:"https_listen"`
	SFTPListen     string           `yaml:"sftp_listen"` // e.g. ":2222", empty = disabled
	HTTP3Enabled   bool             `yaml:"http3"`
	PIDFile        string           `yaml:"pid_file"`
	WebRoot        string           `yaml:"web_root"`
	LogLevel       string           `yaml:"log_level"`
	LogFormat      string           `yaml:"log_format"`
	TrustedProxies []string         `yaml:"trusted_proxies"`
	Timeouts       TimeoutConfig    `yaml:"timeouts"`
	RateLimit      RateLimitConfig  `yaml:"rate_limit,omitempty"`
	Admin          AdminConfig      `yaml:"admin"`
	Audit          AuditConfig      `yaml:"audit"`
	MCP            MCPConfig        `yaml:"mcp"`
	ACME           ACMEConfig       `yaml:"acme"`
	Cache          CacheConfig      `yaml:"cache"`
	Alerting       AlertingConfig   `yaml:"alerting"`
	Backup         BackupConfig     `yaml:"backup"`
	Cloudflare     CloudflareConfig `yaml:"cloudflare,omitempty"`
	Webhooks       []WebhookConfig  `yaml:"webhooks"`
	Users          UsersConfig      `yaml:"users"`
	ProxyProtocol  bool             `yaml:"proxy_protocol"` // enable PROXY protocol v1/v2 on listeners
}

// CloudflareConfig stores origin-protection settings shared by all domains.
// Per-domain enforcement is controlled by Domain.Security.CloudflareOnly.
type CloudflareConfig struct {
	IPRanges   []string `yaml:"ip_ranges,omitempty" json:"ip_ranges,omitempty"`
	LastSynced string   `yaml:"last_synced,omitempty" json:"last_synced,omitempty"`
}

// TimeoutConfig holds HTTP server timeout knobs.
type TimeoutConfig struct {
	Read           Duration `yaml:"read"`
	ReadHeader     Duration `yaml:"read_header"`
	Write          Duration `yaml:"write"`
	Idle           Duration `yaml:"idle"`
	ShutdownGrace  Duration `yaml:"shutdown_grace"`
	MaxHeaderBytes int      `yaml:"max_header_bytes"`
}

// AlertingConfig fans out operational alerts to one or more channels.
type AlertingConfig struct {
	Enabled        bool   `yaml:"enabled"`
	WebhookURL     string `yaml:"webhook_url"`
	SlackURL       string `yaml:"slack_url"`
	TelegramToken  string `yaml:"telegram_token"`
	TelegramChatID string `yaml:"telegram_chat_id"`
	EmailSMTP      string `yaml:"email_smtp"`
	EmailFrom      string `yaml:"email_from"`
	EmailTo        string `yaml:"email_to"`
}

// WebhookConfig is a single registered outbound webhook subscription.
type WebhookConfig struct {
	URL     string            `yaml:"url" json:"url"`
	Events  []string          `yaml:"events" json:"events"`   // empty = all events
	Headers map[string]string `yaml:"headers" json:"headers"` // custom headers
	Secret  string            `yaml:"secret" json:"secret"`   // for HMAC signature
	Retry   int               `yaml:"retry" json:"retry"`     // max retries, default 3
	Timeout Duration          `yaml:"timeout" json:"timeout"` // default 30s
	Enabled bool              `yaml:"enabled" json:"enabled"`
}

// UsersConfig controls the multi-user RBAC subsystem.
type UsersConfig struct {
	Enabled      bool `yaml:"enabled"`        // Enable multi-user mode
	AllowResller bool `yaml:"allow_reseller"` // Allow reseller role
	SessionTTL   int  `yaml:"session_ttl"`    // Session TTL in hours (default 24)

	// AllowLegacyPlaintextAPIKey enables the v0.1 plaintext-comparison
	// fallback for users whose stored APIKey has not yet been rehashed
	// to APIKeyHash. Defaults to false from v0.5; legacy users must
	// rotate via RegenerateAPIKey before this flag can stay off.
	// Removed in a future release. Refs: refactor.md A16.
	AllowLegacyPlaintextAPIKey bool `yaml:"allow_legacy_plaintext_api_key"`
}

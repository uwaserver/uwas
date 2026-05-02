package config

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type Config struct {
	Global     GlobalConfig `yaml:"global"`
	Domains    []Domain     `yaml:"domains"`
	Include    []string     `yaml:"include"`     // glob patterns: ["domains.d/*.yaml"]
	DomainsDir string       `yaml:"domains_dir"` // directory of per-domain YAML files
}

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
	Admin          AdminConfig      `yaml:"admin"`
	Audit          AuditConfig      `yaml:"audit"`
	MCP            MCPConfig        `yaml:"mcp"`
	ACME           ACMEConfig       `yaml:"acme"`
	Cache          CacheConfig      `yaml:"cache"`
	Alerting       AlertingConfig   `yaml:"alerting"`
	Backup         BackupConfig     `yaml:"backup"`
	Webhooks       []WebhookConfig  `yaml:"webhooks"`
	Users          UsersConfig      `yaml:"users"`
	ProxyProtocol  bool             `yaml:"proxy_protocol"`    // enable PROXY protocol v1/v2 on listeners
}

type BackupConfig struct {
	Enabled      bool              `yaml:"enabled"`
	Provider     string            `yaml:"provider"`   // local | s3 | sftp
	Schedule     string            `yaml:"schedule"`   // duration string e.g. "24h" (fallback if Cron is empty)
	Cron         string            `yaml:"cron"`       // cron expression e.g. "0 2 * * *" (5-field, local timezone)
	Keep         int               `yaml:"keep"`       // keep last N backups
	MaxFileSize  int64             `yaml:"max_file_size"`  // max bytes per file (default 500MB)
	MaxTotalSize int64             `yaml:"max_total_size"` // max bytes total (default 10GB)
	Local        BackupLocalConfig `yaml:"local"`
	S3           BackupS3Config    `yaml:"s3"`
	SFTP         BackupSFTPConfig  `yaml:"sftp"`
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
	Read           Duration `yaml:"read"`
	ReadHeader     Duration `yaml:"read_header"`
	Write          Duration `yaml:"write"`
	Idle           Duration `yaml:"idle"`
	ShutdownGrace  Duration `yaml:"shutdown_grace"`
	MaxHeaderBytes int      `yaml:"max_header_bytes"`
}

type AdminConfig struct {
	Listen        string         `yaml:"listen"`
	Enabled       bool           `yaml:"enabled"`
	APIKey        string         `yaml:"api_key"`
	PinCode       string         `yaml:"pin_code,omitempty"`
	TOTPSecret    string         `yaml:"totp_secret,omitempty"`
	TLSCert       string         `yaml:"tls_cert,omitempty"`
	TLSKey        string         `yaml:"tls_key,omitempty"`
	RecoveryCodes []string       `yaml:"recovery_codes,omitempty"` // 2FA recovery codes
	Branding      BrandingConfig `yaml:"branding,omitempty"`
	OAuth         OAuthConfig    `yaml:"oauth,omitempty"`
}

// OAuthConfig enables OAuth2/SSO login (Google, GitHub).
type OAuthConfig struct {
	Enabled      bool   `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	GoogleID     string `yaml:"google_client_id,omitempty" json:"google_client_id,omitempty"`
	GoogleSecret string `yaml:"google_client_secret,omitempty" json:"google_client_secret,omitempty"`
	GitHubID     string `yaml:"github_client_id,omitempty" json:"github_client_id,omitempty"`
	GitHubSecret string `yaml:"github_client_secret,omitempty" json:"github_client_secret,omitempty"`
	AllowedEmails []string `yaml:"allowed_emails,omitempty" json:"allowed_emails,omitempty"` // restrict to specific emails
}

// BrandingConfig allows white-labeling the dashboard.
type BrandingConfig struct {
	Name      string `yaml:"name,omitempty" json:"name,omitempty"`           // e.g. "My Hosting Panel"
	LogoURL   string `yaml:"logo_url,omitempty" json:"logo_url,omitempty"`   // URL or data: URI
	FaviconURL string `yaml:"favicon_url,omitempty" json:"favicon_url,omitempty"`
	PrimaryColor string `yaml:"primary_color,omitempty" json:"primary_color,omitempty"` // hex color
	FooterText  string `yaml:"footer_text,omitempty" json:"footer_text,omitempty"`
}

// AuditConfig controls audit log behavior.
type AuditConfig struct {
	RecordIP   bool `yaml:"record_ip"`    // whether to record client IP addresses ( GDPR compliance)
}

// MCPConfig controls the built-in MCP server for AI agent access.
type MCPConfig struct {
	Enabled bool   `yaml:"enabled"`
	Listen  string `yaml:"listen"`
}

type ACMEConfig struct {
	Email             string            `yaml:"email"`
	CAURL             string            `yaml:"ca_url"`
	Storage           string            `yaml:"storage"`
	DNSProvider       string            `yaml:"dns_provider"`
	DNSCredentials    map[string]string `yaml:"dns_credentials"`
	OnDemand          bool              `yaml:"on_demand"`
	OnDemandAsk       string            `yaml:"on_demand_ask"`
	SelfSignedValidity Duration         `yaml:"self_signed_validity"` // validity period for self-signed fallback certs (default 24h)
}

type CacheConfig struct {
	Enabled              bool         `yaml:"enabled"`
	MemoryLimit          ByteSize     `yaml:"memory_limit"`
	DiskPath             string       `yaml:"disk_path"`
	DiskLimit            ByteSize     `yaml:"disk_limit"`
	DefaultTTL           int          `yaml:"default_ttl"`
	GraceTTL             int          `yaml:"grace_ttl"`
	StaleWhileRevalidate bool         `yaml:"stale_while_revalidate"`
	PurgeKey             string       `yaml:"purge_key"`
	VaryByQuery          bool         `yaml:"vary_by_query"`       // include query string in cache key
	VaryByHeaders        []string     `yaml:"vary_by_headers"`     // include specific request headers in cache key
	Redis                RedisConfig  `yaml:"redis"`               // L3 Redis cache
}

type RedisConfig struct {
	Enabled  bool     `yaml:"enabled"`
	Addr     string   `yaml:"addr"`      // "localhost:6379"
	Password string   `yaml:"password"`
	DB       int      `yaml:"db"`
	Prefix   string   `yaml:"prefix"`    // key prefix
}

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

type WebhookConfig struct {
	URL     string            `yaml:"url" json:"url"`
	Events  []string          `yaml:"events" json:"events"`   // empty = all events
	Headers map[string]string `yaml:"headers" json:"headers"` // custom headers
	Secret  string            `yaml:"secret" json:"secret"`   // for HMAC signature
	Retry   int               `yaml:"retry" json:"retry"`     // max retries, default 3
	Timeout Duration          `yaml:"timeout" json:"timeout"` // default 30s
	Enabled bool              `yaml:"enabled" json:"enabled"`
}

type UsersConfig struct {
	Enabled      bool `yaml:"enabled"`       // Enable multi-user mode
	AllowResller bool `yaml:"allow_reseller"` // Allow reseller role
	SessionTTL   int  `yaml:"session_ttl"`   // Session TTL in hours (default 24)
}

type MirrorConfig struct {
	Enabled     bool   `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Backend     string `yaml:"backend,omitempty" json:"backend,omitempty"`
	Percent     int    `yaml:"percent,omitempty" json:"percent,omitempty"`
	MaxBodyBytes int   `yaml:"max_body_bytes,omitempty" json:"max_body_bytes,omitempty"` // max body size for mirror requests (default 2MB)
}

type Domain struct {
	Host              string                  `yaml:"host" json:"host"`
	IP                string                  `yaml:"ip,omitempty" json:"ip,omitempty"` // dedicated IP for this domain
	Aliases           []string                `yaml:"aliases,omitempty" json:"aliases,omitempty"`
	Root              string                  `yaml:"root,omitempty" json:"root,omitempty"`
	Type              string                  `yaml:"type" json:"type"`
	SSL               SSLConfig               `yaml:"ssl" json:"ssl"`
	PHP               PHPConfig               `yaml:"php,omitempty" json:"php,omitempty"`
	App               AppConfig               `yaml:"app,omitempty" json:"app,omitempty"`
	Resources         ResourceLimits          `yaml:"resources,omitempty" json:"resources,omitempty"`
	Cache             DomainCache             `yaml:"cache,omitempty" json:"cache,omitempty"`
	Rewrites          []RewriteRule           `yaml:"rewrites,omitempty" json:"rewrites,omitempty"`
	Htaccess          HtaccessConfig          `yaml:"htaccess,omitempty" json:"htaccess,omitempty"`
	Security          SecurityConfig          `yaml:"security,omitempty" json:"security,omitempty"`
	Headers           HeadersConfig           `yaml:"headers,omitempty" json:"headers,omitempty"`
	Compression       CompressionConfig       `yaml:"compression,omitempty" json:"compression,omitempty"`
	AccessLog         AccessLogConfig         `yaml:"access_log,omitempty" json:"access_log,omitempty"`
	ErrorPages        map[int]string          `yaml:"error_pages,omitempty" json:"error_pages,omitempty"`
	Proxy             ProxyConfig             `yaml:"proxy,omitempty" json:"proxy,omitempty"`
	Redirect          RedirectConfig          `yaml:"redirect,omitempty" json:"redirect,omitempty"`
	TryFiles          []string                `yaml:"try_files,omitempty" json:"try_files,omitempty"`
	SPAMode           bool                    `yaml:"spa_mode,omitempty" json:"spa_mode,omitempty"`
	IndexFiles        []string                `yaml:"index_files,omitempty" json:"index_files,omitempty"`
	DirectoryListing  bool                    `yaml:"directory_listing,omitempty" json:"directory_listing,omitempty"`
	ImageOptimization ImageOptimizationConfig `yaml:"image_optimization,omitempty" json:"image_optimization,omitempty"`
	CORS              CORSConfig              `yaml:"cors,omitempty" json:"cors,omitempty"`
	BasicAuth         BasicAuthConfig         `yaml:"basic_auth,omitempty" json:"basic_auth,omitempty"`
	Bandwidth         BandwidthConfig         `yaml:"bandwidth,omitempty" json:"bandwidth,omitempty"`
	Maintenance       MaintenanceConfig       `yaml:"maintenance,omitempty" json:"maintenance,omitempty"`
	Locations         []LocationConfig        `yaml:"locations,omitempty" json:"locations,omitempty"`
	SecurityHeaders   SecurityHeadersConfig   `yaml:"security_headers,omitempty" json:"security_headers,omitempty"`
	InternalAliases   []string                `yaml:"internal_aliases,omitempty" json:"internal_aliases,omitempty"` // allowed path prefixes for X-Accel-Redirect/X-Sendfile
	WebhookSecret     string                  `yaml:"webhook_secret,omitempty" json:"webhook_secret,omitempty"`     // per-domain webhook secret (falls back to global API key)
}

// MaintenanceConfig enables a 503 maintenance page for the domain.
type MaintenanceConfig struct {
	Enabled    bool   `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Message    string `yaml:"message,omitempty" json:"message,omitempty"`      // custom HTML body
	RetryAfter int    `yaml:"retry_after,omitempty" json:"retry_after,omitempty"` // seconds, sent as Retry-After header
	AllowedIPs []string `yaml:"allowed_ips,omitempty" json:"allowed_ips,omitempty"` // bypass maintenance for these IPs
}

// LocationConfig defines per-path overrides (like Nginx location blocks).
// Supports sub-path routing: proxy, static root, redirect, or custom headers.
//
// Examples:
//
//	locations:
//	  - match: "/api/"
//	    proxy_pass: "http://127.0.0.1:4000"
//	  - match: "/blog/"
//	    root: "/var/www/blog"
//	  - match: "/old-path"
//	    redirect: "https://example.com/new-path"
//	    redirect_code: 301
//	  - match: "/assets/"
//	    cache_control: "public, max-age=31536000, immutable"
type LocationConfig struct {
	Match          string            `yaml:"match" json:"match"`                                       // path prefix or regex (prefix: "/api/", regex: "~\\.php$")
	ProxyPass      string            `yaml:"proxy_pass,omitempty" json:"proxy_pass,omitempty"`         // forward to upstream (e.g. "http://127.0.0.1:4000")
	Root           string            `yaml:"root,omitempty" json:"root,omitempty"`                     // serve static files from this directory
	Redirect       string            `yaml:"redirect,omitempty" json:"redirect,omitempty"`             // redirect to this URL
	RedirectCode   int               `yaml:"redirect_code,omitempty" json:"redirect_code,omitempty"`   // 301, 302, 307, 308
	StripPrefix    bool              `yaml:"strip_prefix,omitempty" json:"strip_prefix,omitempty"`     // strip the matched prefix before proxying
	Headers        map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`               // response headers to add
	CacheControl   string            `yaml:"cache_control,omitempty" json:"cache_control,omitempty"`   // Cache-Control header value
	RequestTimeout Duration          `yaml:"request_timeout,omitempty" json:"request_timeout,omitempty"` // per-path timeout
	RateLimit      *RateLimitConfig  `yaml:"rate_limit,omitempty" json:"rate_limit,omitempty"`         // per-path rate limit
	BasicAuth      *BasicAuthConfig  `yaml:"basic_auth,omitempty" json:"basic_auth,omitempty"`         // per-path basic auth
}

// SecurityHeadersConfig adds modern security headers per domain.
type SecurityHeadersConfig struct {
	ContentSecurityPolicy   string `yaml:"content_security_policy,omitempty" json:"content_security_policy,omitempty"`
	PermissionsPolicy       string `yaml:"permissions_policy,omitempty" json:"permissions_policy,omitempty"`
	CrossOriginEmbedder     string `yaml:"cross_origin_embedder_policy,omitempty" json:"cross_origin_embedder_policy,omitempty"`   // require-corp, unsafe-none
	CrossOriginOpener       string `yaml:"cross_origin_opener_policy,omitempty" json:"cross_origin_opener_policy,omitempty"`       // same-origin, same-origin-allow-popups, unsafe-none
	CrossOriginResource     string `yaml:"cross_origin_resource_policy,omitempty" json:"cross_origin_resource_policy,omitempty"`   // same-origin, same-site, cross-origin
	ReferrerPolicy          string `yaml:"referrer_policy,omitempty" json:"referrer_policy,omitempty"`                         // no-referrer, no-referrer-when-downgrade, same-origin, strict-origin-when-cross-origin, etc.
	StrictTransportSecurity string `yaml:"strict_transport_security,omitempty" json:"strict_transport_security,omitempty"`       // HSTS header, e.g. "max-age=31536000; includeSubDomains"
	XContentTypeOptions     string `yaml:"x_content_type_options,omitempty" json:"x_content_type_options,omitempty"`           // nosniff
	XSSProtection          string `yaml:"x_xss_protection,omitempty" json:"x_xss_protection,omitempty"`                     // 1; mode=block
}

// MarshalYAML produces clean YAML by omitting zero-value nested structs.
func (d Domain) MarshalYAML() (any, error) {
	m := map[string]any{
		"host": d.Host,
		"type": d.Type,
		"ssl":  map[string]string{"mode": d.SSL.Mode},
	}
	if d.IP != "" {
		m["ip"] = d.IP
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
	if d.Bandwidth.Enabled {
		m["bandwidth"] = d.Bandwidth
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
	ClientCA   string `yaml:"client_ca,omitempty" json:"client_ca,omitempty"`     // path to CA cert for mTLS
	ClientAuth string `yaml:"client_auth,omitempty" json:"client_auth,omitempty"` // "require", "request", "none"
}

type PHPConfig struct {
	FPMAddress      string            `yaml:"fpm_address,omitempty" json:"fpm_address,omitempty"`
	IndexFiles      []string          `yaml:"index_files,omitempty" json:"index_files,omitempty"`
	MaxUpload       ByteSize          `yaml:"max_upload,omitempty" json:"max_upload,omitempty"`
	Timeout         Duration          `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	Env             map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	ConfigOverrides map[string]string `yaml:"config_overrides,omitempty" json:"config_overrides,omitempty"` // per-domain php.ini overrides (memory_limit, etc.)
}

// AppConfig holds configuration for non-PHP application processes (Node.js, Python, etc.).
type AppConfig struct {
	Command    string            `yaml:"command,omitempty" json:"command,omitempty"`       // e.g. "npm start", "gunicorn app:app"
	Port       int               `yaml:"port,omitempty" json:"port,omitempty"`             // app listens on this port (auto-assigned if 0)
	Env        map[string]string `yaml:"env,omitempty" json:"env,omitempty"`               // environment variables
	WorkDir    string            `yaml:"work_dir,omitempty" json:"work_dir,omitempty"`     // working directory (defaults to domain root)
	Runtime    string            `yaml:"runtime,omitempty" json:"runtime,omitempty"`       // "node", "python", "ruby", "go", "custom"
	AutoRestart bool            `yaml:"auto_restart,omitempty" json:"auto_restart,omitempty"` // restart on crash (default true)
}

// ResourceLimits defines per-domain CPU/memory/PID limits (Linux cgroups v2).
type ResourceLimits struct {
	CPUPercent int `yaml:"cpu_percent,omitempty" json:"cpu_percent,omitempty"` // max CPU % (e.g. 50 = half a core)
	MemoryMB   int `yaml:"memory_mb,omitempty" json:"memory_mb,omitempty"`    // max memory in MB
	PIDMax     int `yaml:"pid_max,omitempty" json:"pid_max,omitempty"`        // max processes
}

type DomainCache struct {
	Enabled bool        `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	TTL     int         `yaml:"ttl,omitempty" json:"ttl,omitempty"`
	Rules   []CacheRule `yaml:"rules,omitempty" json:"rules,omitempty"`
	Tags    []string    `yaml:"tags,omitempty" json:"tags,omitempty"`
	ESI     bool        `yaml:"esi,omitempty" json:"esi,omitempty"`
}

type CacheRule struct {
	Match        string `yaml:"match,omitempty" json:"match,omitempty"`
	TTL          int    `yaml:"ttl,omitempty" json:"ttl,omitempty"`
	Bypass       bool   `yaml:"bypass,omitempty" json:"bypass,omitempty"`
	CacheControl string `yaml:"cache_control,omitempty" json:"cache_control,omitempty"` // Cache-Control header override
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
	BlockedPaths      []string        `yaml:"blocked_paths,omitempty" json:"blocked_paths,omitempty"`
	HotlinkProtection HotlinkConfig   `yaml:"hotlink_protection,omitempty" json:"hotlink_protection,omitempty"`
	RateLimit         RateLimitConfig `yaml:"rate_limit,omitempty" json:"rate_limit,omitempty"`
	WAF               WAFConfig       `yaml:"waf,omitempty" json:"waf,omitempty"`
	IPWhitelist       []string        `yaml:"ip_whitelist,omitempty" json:"ip_whitelist,omitempty"`
	IPBlacklist       []string        `yaml:"ip_blacklist,omitempty" json:"ip_blacklist,omitempty"`
	GeoBlockCountries []string        `yaml:"geo_block_countries,omitempty" json:"geo_block_countries,omitempty"` // 2-letter ISO codes
	GeoAllowCountries []string        `yaml:"geo_allow_countries,omitempty" json:"geo_allow_countries,omitempty"` // whitelist mode
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
	Enabled     bool     `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	BypassPaths []string `yaml:"bypass_paths,omitempty" json:"bypass_paths,omitempty"` // skip WAF for these path prefixes
	Rules       []string `yaml:"rules,omitempty" json:"rules,omitempty"`
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
	Path       string       `yaml:"path,omitempty" json:"path,omitempty"`
	Format     string       `yaml:"format,omitempty" json:"format,omitempty"`
	BufferSize int          `yaml:"buffer_size,omitempty" json:"buffer_size,omitempty"`
	Rotate     RotateConfig `yaml:"rotate,omitempty" json:"rotate,omitempty"`
}

type RotateConfig struct {
	MaxSize    ByteSize `yaml:"max_size,omitempty" json:"max_size,omitempty"`
	MaxAge     Duration `yaml:"max_age,omitempty" json:"max_age,omitempty"`
	MaxBackups int      `yaml:"max_backups,omitempty" json:"max_backups,omitempty"`
}

type ProxyConfig struct {
	Upstreams            []Upstream        `yaml:"upstreams,omitempty" json:"upstreams,omitempty"`
	Algorithm            string            `yaml:"algorithm,omitempty" json:"algorithm,omitempty"`
	HealthCheck          HealthCheckConfig `yaml:"health_check,omitempty" json:"health_check,omitempty"`
	Sticky               StickyConfig      `yaml:"sticky,omitempty" json:"sticky,omitempty"`
	CircuitBreaker       CircuitConfig     `yaml:"circuit_breaker,omitempty" json:"circuit_breaker,omitempty"`
	WebSocket            bool              `yaml:"websocket,omitempty" json:"websocket,omitempty"`
	GRPC                 bool              `yaml:"grpc,omitempty" json:"grpc,omitempty"`             // enable gRPC/h2c proxy
	Timeouts             ProxyTimeouts     `yaml:"timeouts,omitempty" json:"timeouts,omitempty"`
	MaxRetries           int               `yaml:"max_retries,omitempty" json:"max_retries,omitempty"`
	Canary               CanaryConfig      `yaml:"canary,omitempty" json:"canary,omitempty"`
	Mirror               MirrorConfig      `yaml:"mirror,omitempty" json:"mirror,omitempty"`
	BufferResponse       bool              `yaml:"buffer_response,omitempty" json:"buffer_response,omitempty"` // buffer entire upstream response
	AllowPrivateUpstreams bool             `yaml:"allow_private_upstreams,omitempty" json:"allow_private_upstreams,omitempty"` // allow private IP upstreams (default false for SSRF protection)
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

func (d Duration) MarshalYAML() (any, error) {
	if d.Duration == 0 {
		return "0s", nil
	}
	return d.Duration.String(), nil
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

func (b ByteSize) MarshalYAML() (any, error) {
	if b == 0 {
		return 0, nil
	}
	if b%GB == 0 {
		return fmt.Sprintf("%dGB", b/GB), nil
	}
	if b%MB == 0 {
		return fmt.Sprintf("%dMB", b/MB), nil
	}
	if b%KB == 0 {
		return fmt.Sprintf("%dKB", b/KB), nil
	}
	return int64(b), nil
}

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

// BandwidthConfig defines bandwidth limits for a domain.
type BandwidthConfig struct {
	Enabled      bool     `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	MonthlyLimit ByteSize `yaml:"monthly_limit,omitempty" json:"monthly_limit,omitempty"`
	DailyLimit   ByteSize `yaml:"daily_limit,omitempty" json:"daily_limit,omitempty"`
	Action       string   `yaml:"action,omitempty" json:"action,omitempty"` // block | throttle | alert
}


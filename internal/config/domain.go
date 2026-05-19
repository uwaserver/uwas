package config

import (
	"reflect"
	"strings"
)

// DomainType is the central dispatch axis: which handler family processes
// requests for this domain. Kept as an underlying string so YAML/JSON
// serialization is unchanged; Domain.Type stays a bare string for the same
// reason, and call sites can convert with DomainType(d.Type) when they want
// the enum semantics.
type DomainType string

const (
	DomainTypeStatic   DomainType = "static"
	DomainTypePHP      DomainType = "php"
	DomainTypeProxy    DomainType = "proxy"
	DomainTypeApp      DomainType = "app"
	DomainTypeRedirect DomainType = "redirect"
)

// IsValid reports whether t is one of the recognized domain types.
func (t DomainType) IsValid() bool {
	switch t {
	case DomainTypeStatic, DomainTypePHP, DomainTypeProxy, DomainTypeApp, DomainTypeRedirect:
		return true
	}
	return false
}

// Domain is a single virtual host. The Type field selects which feature
// block(s) are honored (php / proxy / app / static / redirect).
type Domain struct {
	Host              string                  `yaml:"host" json:"host"`
	CanonicalHost     string                  `yaml:"canonical_host,omitempty" json:"canonical_host,omitempty"` // "apex" or "www"; host stays apex, this selects the primary URL
	IP                string                  `yaml:"ip,omitempty" json:"ip,omitempty"`                         // dedicated IP for this domain
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
	Enabled    bool     `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Message    string   `yaml:"message,omitempty" json:"message,omitempty"`         // custom HTML body
	RetryAfter int      `yaml:"retry_after,omitempty" json:"retry_after,omitempty"` // seconds, sent as Retry-After header
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
	Match          string            `yaml:"match" json:"match"`                                         // path prefix or regex (prefix: "/api/", regex: "~\\.php$")
	ProxyPass      string            `yaml:"proxy_pass,omitempty" json:"proxy_pass,omitempty"`           // forward to upstream (e.g. "http://127.0.0.1:4000")
	Root           string            `yaml:"root,omitempty" json:"root,omitempty"`                       // serve static files from this directory
	Redirect       string            `yaml:"redirect,omitempty" json:"redirect,omitempty"`               // redirect to this URL
	RedirectCode   int               `yaml:"redirect_code,omitempty" json:"redirect_code,omitempty"`     // 301, 302, 307, 308
	StripPrefix    bool              `yaml:"strip_prefix,omitempty" json:"strip_prefix,omitempty"`       // strip the matched prefix before proxying
	Headers        map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`                 // response headers to add
	CacheControl   string            `yaml:"cache_control,omitempty" json:"cache_control,omitempty"`     // Cache-Control header value
	RequestTimeout Duration          `yaml:"request_timeout,omitempty" json:"request_timeout,omitempty"` // per-path timeout
	RateLimit      *RateLimitConfig  `yaml:"rate_limit,omitempty" json:"rate_limit,omitempty"`           // per-path rate limit
	BasicAuth      *BasicAuthConfig  `yaml:"basic_auth,omitempty" json:"basic_auth,omitempty"`           // per-path basic auth
}

// SecurityHeadersConfig adds modern security headers per domain.
type SecurityHeadersConfig struct {
	ContentSecurityPolicy   string `yaml:"content_security_policy,omitempty" json:"content_security_policy,omitempty"`
	PermissionsPolicy       string `yaml:"permissions_policy,omitempty" json:"permissions_policy,omitempty"`
	CrossOriginEmbedder     string `yaml:"cross_origin_embedder_policy,omitempty" json:"cross_origin_embedder_policy,omitempty"` // require-corp, unsafe-none
	CrossOriginOpener       string `yaml:"cross_origin_opener_policy,omitempty" json:"cross_origin_opener_policy,omitempty"`     // same-origin, same-origin-allow-popups, unsafe-none
	CrossOriginResource     string `yaml:"cross_origin_resource_policy,omitempty" json:"cross_origin_resource_policy,omitempty"` // same-origin, same-site, cross-origin
	ReferrerPolicy          string `yaml:"referrer_policy,omitempty" json:"referrer_policy,omitempty"`                           // no-referrer, no-referrer-when-downgrade, same-origin, strict-origin-when-cross-origin, etc.
	StrictTransportSecurity string `yaml:"strict_transport_security,omitempty" json:"strict_transport_security,omitempty"`       // HSTS header, e.g. "max-age=31536000; includeSubDomains"
	XContentTypeOptions     string `yaml:"x_content_type_options,omitempty" json:"x_content_type_options,omitempty"`             // nosniff
	XSSProtection           string `yaml:"x_xss_protection,omitempty" json:"x_xss_protection,omitempty"`                         // 1; mode=block
}

// SSLConfig is the per-domain TLS configuration.
type SSLConfig struct {
	Mode       string `yaml:"mode,omitempty" json:"mode,omitempty"`
	ForceSSL   bool   `yaml:"force_ssl,omitempty" json:"force_ssl,omitempty"`
	Cert       string `yaml:"cert,omitempty" json:"cert,omitempty"`
	Key        string `yaml:"key,omitempty" json:"key,omitempty"`
	MinVersion string `yaml:"min_version,omitempty" json:"min_version,omitempty"`
	ClientCA   string `yaml:"client_ca,omitempty" json:"client_ca,omitempty"`     // path to CA cert for mTLS
	ClientAuth string `yaml:"client_auth,omitempty" json:"client_auth,omitempty"` // "require", "request", "none"
}

// PHPConfig configures PHP-FPM dispatch for a domain.
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
	Command     string            `yaml:"command,omitempty" json:"command,omitempty"`           // e.g. "npm start", "gunicorn app:app"
	Port        int               `yaml:"port,omitempty" json:"port,omitempty"`                 // app listens on this port (auto-assigned if 0)
	Env         map[string]string `yaml:"env,omitempty" json:"env,omitempty"`                   // environment variables
	WorkDir     string            `yaml:"work_dir,omitempty" json:"work_dir,omitempty"`         // working directory (defaults to domain root)
	Runtime     string            `yaml:"runtime,omitempty" json:"runtime,omitempty"`           // "node", "python", "ruby", "go", "custom"
	AutoRestart bool              `yaml:"auto_restart,omitempty" json:"auto_restart,omitempty"` // restart on crash (default true)
	Disabled    bool              `yaml:"disabled,omitempty" json:"disabled,omitempty"`         // user explicitly stopped this app — don't auto-start on boot
}

// ResourceLimits defines per-domain CPU/memory/PID limits (Linux cgroups v2).
type ResourceLimits struct {
	CPUPercent int `yaml:"cpu_percent,omitempty" json:"cpu_percent,omitempty"` // max CPU % (e.g. 50 = half a core)
	MemoryMB   int `yaml:"memory_mb,omitempty" json:"memory_mb,omitempty"`     // max memory in MB
	PIDMax     int `yaml:"pid_max,omitempty" json:"pid_max,omitempty"`         // max processes
}

// RewriteRule is a single mod_rewrite-compatible rule.
type RewriteRule struct {
	Match      string   `yaml:"match,omitempty" json:"match,omitempty"`
	To         string   `yaml:"to,omitempty" json:"to,omitempty"`
	Status     int      `yaml:"status,omitempty" json:"status,omitempty"`
	Conditions []string `yaml:"conditions,omitempty" json:"conditions,omitempty"`
	Flags      []string `yaml:"flags,omitempty" json:"flags,omitempty"`
}

// HtaccessConfig controls per-domain .htaccess parsing.
type HtaccessConfig struct {
	Mode string `yaml:"mode,omitempty" json:"mode,omitempty"`
}

// HeadersConfig adds/removes request and response headers.
type HeadersConfig struct {
	Add            map[string]string `yaml:"add,omitempty" json:"add,omitempty"`
	Remove         []string          `yaml:"remove,omitempty" json:"remove,omitempty"`
	RequestAdd     map[string]string `yaml:"request_add,omitempty" json:"request_add,omitempty"`
	RequestRemove  []string          `yaml:"request_remove,omitempty" json:"request_remove,omitempty"`
	ResponseAdd    map[string]string `yaml:"response_add,omitempty" json:"response_add,omitempty"`
	ResponseRemove []string          `yaml:"response_remove,omitempty" json:"response_remove,omitempty"`
}

// CORSConfig is the per-domain CORS policy.
type CORSConfig struct {
	Enabled          bool     `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	AllowedOrigins   []string `yaml:"allowed_origins,omitempty" json:"allowed_origins,omitempty"`
	AllowedMethods   []string `yaml:"allowed_methods,omitempty" json:"allowed_methods,omitempty"`
	AllowedHeaders   []string `yaml:"allowed_headers,omitempty" json:"allowed_headers,omitempty"`
	AllowCredentials bool     `yaml:"allow_credentials,omitempty" json:"allow_credentials,omitempty"`
	MaxAge           int      `yaml:"max_age,omitempty" json:"max_age,omitempty"`
}

// BasicAuthConfig is the per-domain (or per-location) HTTP Basic Auth.
type BasicAuthConfig struct {
	Enabled bool              `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Users   map[string]string `yaml:"users,omitempty" json:"users,omitempty"`
	Realm   string            `yaml:"realm,omitempty" json:"realm,omitempty"`
}

// CompressionConfig controls per-domain response compression.
type CompressionConfig struct {
	Enabled    bool     `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Algorithms []string `yaml:"algorithms,omitempty" json:"algorithms,omitempty"`
	MinSize    int      `yaml:"min_size,omitempty" json:"min_size,omitempty"`
	Types      []string `yaml:"types,omitempty" json:"types,omitempty"`
}

// ImageOptimizationConfig enables on-the-fly WebP/AVIF.
type ImageOptimizationConfig struct {
	Enabled bool     `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Formats []string `yaml:"formats,omitempty" json:"formats,omitempty"`
}

// AccessLogConfig is the per-domain access-log file configuration.
type AccessLogConfig struct {
	Path       string       `yaml:"path,omitempty" json:"path,omitempty"`
	Format     string       `yaml:"format,omitempty" json:"format,omitempty"`
	BufferSize int          `yaml:"buffer_size,omitempty" json:"buffer_size,omitempty"`
	Rotate     RotateConfig `yaml:"rotate,omitempty" json:"rotate,omitempty"`
}

// RotateConfig controls access-log rotation.
type RotateConfig struct {
	MaxSize    ByteSize `yaml:"max_size,omitempty" json:"max_size,omitempty"`
	MaxAge     Duration `yaml:"max_age,omitempty" json:"max_age,omitempty"`
	MaxBackups int      `yaml:"max_backups,omitempty" json:"max_backups,omitempty"`
}

// BandwidthConfig defines bandwidth limits for a domain.
type BandwidthConfig struct {
	Enabled      bool     `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	MonthlyLimit ByteSize `yaml:"monthly_limit,omitempty" json:"monthly_limit,omitempty"`
	DailyLimit   ByteSize `yaml:"daily_limit,omitempty" json:"daily_limit,omitempty"`
	Action       string   `yaml:"action,omitempty" json:"action,omitempty"` // block | throttle | alert
}

// MarshalYAML produces clean YAML by omitting zero-value nested structs.
func (d Domain) MarshalYAML() (any, error) {
	return yamlMapFromStruct(reflect.ValueOf(d), map[string]bool{
		"host": true,
		"type": true,
		"ssl":  true,
	})
}

type yamlMarshaler interface {
	MarshalYAML() (any, error)
}

func yamlMapFromStruct(v reflect.Value, force map[string]bool) (map[string]any, error) {
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil, nil
		}
		v = v.Elem()
	}

	t := v.Type()
	out := make(map[string]any)
	for i := 0; i < v.NumField(); i++ {
		sf := t.Field(i)
		if sf.PkgPath != "" {
			continue
		}
		name, omitEmpty := yamlFieldName(sf)
		if name == "-" {
			continue
		}
		fv := v.Field(i)
		if omitEmpty && !force[name] && yamlEmpty(fv) {
			continue
		}
		value, err := yamlValue(fv)
		if err != nil {
			return nil, err
		}
		out[name] = value
	}
	return out, nil
}

func yamlFieldName(sf reflect.StructField) (string, bool) {
	tag := sf.Tag.Get("yaml")
	if tag == "" {
		return strings.ToLower(sf.Name), false
	}
	parts := strings.Split(tag, ",")
	name := parts[0]
	omitEmpty := false
	for _, opt := range parts[1:] {
		if opt == "omitempty" {
			omitEmpty = true
			break
		}
	}
	if name == "" {
		name = strings.ToLower(sf.Name)
	}
	return name, omitEmpty
}

func yamlValue(v reflect.Value) (any, error) {
	if !v.IsValid() {
		return nil, nil
	}
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil, nil
		}
		if v.CanInterface() {
			if m, ok := v.Interface().(yamlMarshaler); ok {
				return m.MarshalYAML()
			}
		}
		return yamlValue(v.Elem())
	}
	if v.CanInterface() {
		if m, ok := v.Interface().(yamlMarshaler); ok {
			return m.MarshalYAML()
		}
	}
	if v.CanAddr() {
		if m, ok := v.Addr().Interface().(yamlMarshaler); ok {
			return m.MarshalYAML()
		}
	}

	switch v.Kind() {
	case reflect.Struct:
		return yamlMapFromStruct(v, nil)
	case reflect.Slice, reflect.Array:
		items := make([]any, 0, v.Len())
		for i := 0; i < v.Len(); i++ {
			item, err := yamlValue(v.Index(i))
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		}
		return items, nil
	case reflect.Map:
		if v.IsNil() {
			return nil, nil
		}
		out := make(map[any]any, v.Len())
		iter := v.MapRange()
		for iter.Next() {
			val, err := yamlValue(iter.Value())
			if err != nil {
				return nil, err
			}
			out[iter.Key().Interface()] = val
		}
		return out, nil
	default:
		return v.Interface(), nil
	}
}

func yamlEmpty(v reflect.Value) bool {
	if !v.IsValid() {
		return true
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		return v.IsNil()
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Struct:
		return v.IsZero()
	}
	return false
}

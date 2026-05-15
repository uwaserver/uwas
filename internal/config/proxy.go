package config

// ProxyConfig is the per-domain reverse-proxy configuration.
type ProxyConfig struct {
	Upstreams             []Upstream        `yaml:"upstreams,omitempty" json:"upstreams,omitempty"`
	Algorithm             string            `yaml:"algorithm,omitempty" json:"algorithm,omitempty"`
	HealthCheck           HealthCheckConfig `yaml:"health_check,omitempty" json:"health_check,omitempty"`
	Sticky                StickyConfig      `yaml:"sticky,omitempty" json:"sticky,omitempty"`
	CircuitBreaker        CircuitConfig     `yaml:"circuit_breaker,omitempty" json:"circuit_breaker,omitempty"`
	WebSocket             bool              `yaml:"websocket,omitempty" json:"websocket,omitempty"`
	GRPC                  bool              `yaml:"grpc,omitempty" json:"grpc,omitempty"` // enable gRPC/h2c proxy
	Timeouts              ProxyTimeouts     `yaml:"timeouts,omitempty" json:"timeouts,omitempty"`
	MaxRetries            int               `yaml:"max_retries,omitempty" json:"max_retries,omitempty"`
	Canary                CanaryConfig      `yaml:"canary,omitempty" json:"canary,omitempty"`
	Mirror                MirrorConfig      `yaml:"mirror,omitempty" json:"mirror,omitempty"`
	BufferResponse        bool              `yaml:"buffer_response,omitempty" json:"buffer_response,omitempty"`               // buffer entire upstream response
	AllowPrivateUpstreams bool              `yaml:"allow_private_upstreams,omitempty" json:"allow_private_upstreams,omitempty"` // allow private IP upstreams (default false for SSRF protection)
}

// CanaryConfig is a side-traffic canary subset of upstreams.
type CanaryConfig struct {
	Enabled   bool       `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Weight    int        `yaml:"weight,omitempty" json:"weight,omitempty"`
	Upstreams []Upstream `yaml:"upstreams,omitempty" json:"upstreams,omitempty"`
	Cookie    string     `yaml:"cookie,omitempty" json:"cookie,omitempty"`
}

// MirrorConfig duplicates a percentage of live traffic to a shadow backend.
type MirrorConfig struct {
	Enabled      bool   `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Backend      string `yaml:"backend,omitempty" json:"backend,omitempty"`
	Percent      int    `yaml:"percent,omitempty" json:"percent,omitempty"`
	MaxBodyBytes int    `yaml:"max_body_bytes,omitempty" json:"max_body_bytes,omitempty"` // max body size for mirror requests (default 2MB)
}

// Upstream is a single backend address + load-balancing weight.
type Upstream struct {
	Address string `yaml:"address,omitempty" json:"address,omitempty"`
	Weight  int    `yaml:"weight,omitempty" json:"weight,omitempty"`
}

// HealthCheckConfig defines active upstream health probing.
type HealthCheckConfig struct {
	Path      string   `yaml:"path,omitempty" json:"path,omitempty"`
	Interval  Duration `yaml:"interval,omitempty" json:"interval,omitempty"`
	Timeout   Duration `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	Threshold int      `yaml:"threshold,omitempty" json:"threshold,omitempty"`
	Rise      int      `yaml:"rise,omitempty" json:"rise,omitempty"`
}

// StickyConfig pins a client to an upstream via cookie or IP hash.
type StickyConfig struct {
	Type       string `yaml:"type,omitempty" json:"type,omitempty"`
	CookieName string `yaml:"cookie_name,omitempty" json:"cookie_name,omitempty"`
	TTL        int    `yaml:"ttl,omitempty" json:"ttl,omitempty"`
}

// CircuitConfig controls the upstream circuit breaker.
type CircuitConfig struct {
	Threshold int      `yaml:"threshold,omitempty" json:"threshold,omitempty"`
	Timeout   Duration `yaml:"timeout,omitempty" json:"timeout,omitempty"`
}

// ProxyTimeouts is the per-phase upstream request budget.
type ProxyTimeouts struct {
	Connect Duration `yaml:"connect,omitempty" json:"connect,omitempty"`
	Read    Duration `yaml:"read,omitempty" json:"read,omitempty"`
	Write   Duration `yaml:"write,omitempty" json:"write,omitempty"`
}

// RedirectConfig is the per-domain redirect target.
type RedirectConfig struct {
	Target       string `yaml:"target,omitempty" json:"target,omitempty"`
	Status       int    `yaml:"status,omitempty" json:"status,omitempty"`
	PreservePath bool   `yaml:"preserve_path,omitempty" json:"preserve_path,omitempty"`
}

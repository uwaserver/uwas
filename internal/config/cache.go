package config

// CacheConfig configures the global HTTP cache (L1 memory + L2 disk + optional L3 Redis).
type CacheConfig struct {
	Enabled              bool     `yaml:"enabled"`
	MemoryLimit          ByteSize `yaml:"memory_limit"`
	DiskPath             string   `yaml:"disk_path"`
	DiskLimit            ByteSize `yaml:"disk_limit"`
	DefaultTTL           int      `yaml:"default_ttl"`
	GraceTTL             int      `yaml:"grace_ttl"`
	StaleWhileRevalidate bool     `yaml:"stale_while_revalidate"`
	PurgeKey             string   `yaml:"purge_key"`
	// VaryByQuery is a pointer so an absent setting is distinguishable from an
	// explicit false. The cache key has always included the query string, and
	// the field was never read; honouring a zero value literally would make
	// /search?q=cats and /search?q=dogs share one entry on every deployment
	// that never set it.
	VaryByQuery   *bool       `yaml:"vary_by_query,omitempty" json:"vary_by_query,omitempty"`
	VaryByHeaders []string    `yaml:"vary_by_headers"` // include specific request headers in cache key
	Redis         RedisConfig `yaml:"redis"`           // L3 Redis cache
}

// RedisConfig configures the optional L3 Redis cache tier.
type RedisConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Addr     string `yaml:"addr"` // "localhost:6379"
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
	Prefix   string `yaml:"prefix"` // key prefix
	TLS      bool   `yaml:"tls"`    // use TLS connection (Redis 6+, e.g. ElastiCache)
}

// DomainCache is the per-domain HTTP cache configuration.
type DomainCache struct {
	Enabled bool        `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	TTL     int         `yaml:"ttl,omitempty" json:"ttl,omitempty"`
	Rules   []CacheRule `yaml:"rules,omitempty" json:"rules,omitempty"`
	Tags    []string    `yaml:"tags,omitempty" json:"tags,omitempty"`
	ESI     bool        `yaml:"esi,omitempty" json:"esi,omitempty"`
}

// CacheRule defines a path-pattern-specific cache override.
type CacheRule struct {
	Match        string `yaml:"match,omitempty" json:"match,omitempty"`
	TTL          int    `yaml:"ttl,omitempty" json:"ttl,omitempty"`
	Bypass       bool   `yaml:"bypass,omitempty" json:"bypass,omitempty"`
	CacheControl string `yaml:"cache_control,omitempty" json:"cache_control,omitempty"` // Cache-Control header override
}

// QueryVaries reports whether the query string belongs in the cache key.
//
// nil means the operator never mentioned vary_by_query, and the cache key has
// always included the query — so absent has to mean true. Only an explicit
// `vary_by_query: false` collapses requests that differ only in their query.
func (c CacheConfig) QueryVaries() bool {
	return c.VaryByQuery == nil || *c.VaryByQuery
}

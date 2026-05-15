package config

// CacheConfig configures the global HTTP cache (L1 memory + L2 disk + optional L3 Redis).
type CacheConfig struct {
	Enabled              bool        `yaml:"enabled"`
	MemoryLimit          ByteSize    `yaml:"memory_limit"`
	DiskPath             string      `yaml:"disk_path"`
	DiskLimit            ByteSize    `yaml:"disk_limit"`
	DefaultTTL           int         `yaml:"default_ttl"`
	GraceTTL             int         `yaml:"grace_ttl"`
	StaleWhileRevalidate bool        `yaml:"stale_while_revalidate"`
	PurgeKey             string      `yaml:"purge_key"`
	VaryByQuery          bool        `yaml:"vary_by_query"`   // include query string in cache key
	VaryByHeaders        []string    `yaml:"vary_by_headers"` // include specific request headers in cache key
	Redis                RedisConfig `yaml:"redis"`           // L3 Redis cache
}

// RedisConfig configures the optional L3 Redis cache tier.
type RedisConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Addr     string `yaml:"addr"`     // "localhost:6379"
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

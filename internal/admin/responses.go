// Typed response structs for admin API endpoints. Replaces map[string]any
// to provide compile-time field checking and consistent response shapes.
package admin

import "time"

// PaginatedResponse wraps a list response with pagination metadata.
// Used by database, apps, audit, domains, files, and other list endpoints.
type PaginatedResponse[T any] struct {
	Items  []T `json:"items"`
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

// StatusResponse is the simple {"status": "..."} shape used by many
// mutation endpoints (create, delete, update, purge, etc.).
type StatusResponse struct {
	Status string `json:"status"`
}

// StatusDetailResponse extends StatusResponse with a detail/message field.
type StatusDetailResponse struct {
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// CachePurgeResponse is returned by the cache purge endpoint.
type CachePurgeResponse struct {
	Status string `json:"status"`
	Tag    string `json:"tag,omitempty"`
	Host   string `json:"host,omitempty"`
	// Count carries no omitempty on purpose. A purge that matched nothing is
	// the failure mode this endpoint actually has, and dropping the zero made
	// it indistinguishable from a purge that worked.
	Count int `json:"count"`
}

// HealthResponse is returned by GET /api/v1/health.
type HealthResponse struct {
	Status      string            `json:"status"`
	Uptime      string            `json:"uptime"`
	UptimeSecs  float64           `json:"uptime_secs"`
	Domains     int               `json:"domains"`
	Requests    int64             `json:"requests"`
	ActiveConns int64             `json:"active_conns"`
	Checks      map[string]string `json:"checks"`
	Version     string            `json:"version"`
}

// MetricsResponse is returned by GET /api/v1/stats.
type MetricsResponse struct {
	RequestsTotal  int64                     `json:"requests_total"`
	CacheHits      int64                     `json:"cache_hits"`
	CacheMisses    int64                     `json:"cache_misses"`
	ActiveConns    int64                     `json:"active_conns"`
	BytesSent      int64                     `json:"bytes_sent"`
	Uptime         string                    `json:"uptime"`
	SlowRequests   int64                     `json:"slow_requests"`
	LatencyP50Ms   float64                   `json:"latency_p50_ms"`
	LatencyP95Ms   float64                   `json:"latency_p95_ms"`
	LatencyP99Ms   float64                   `json:"latency_p99_ms"`
	LatencyMaxMs   float64                   `json:"latency_max_ms"`
	HandlerLatency map[string]HandlerLatency `json:"handler_latency,omitempty"`
}

// HandlerLatency is the per-handler-type latency breakdown.
type HandlerLatency struct {
	P50Ms float64 `json:"p50_ms"`
	P95Ms float64 `json:"p95_ms"`
	P99Ms float64 `json:"p99_ms"`
	MaxMs float64 `json:"max_ms"`
}

// CacheStatsResponse is returned by GET /api/v1/cache/stats.
type CacheStatsResponse struct {
	Enabled   bool              `json:"enabled"`
	Hits      int64             `json:"hits"`
	Misses    int64             `json:"misses"`
	Stales    int64             `json:"stales"`
	Entries   int64             `json:"entries"`
	UsedBytes int64             `json:"used_bytes"`
	HitRate   string            `json:"hit_rate"`
	Domains   []CacheDomainInfo `json:"domains,omitempty"`
}

// CacheDomainInfo is per-domain cache configuration shown in cache stats.
type CacheDomainInfo struct {
	Host    string             `json:"host"`
	Enabled bool               `json:"enabled"`
	TTL     int                `json:"ttl"`
	Tags    []string           `json:"tags"`
	Rules   []CacheRuleSummary `json:"rules,omitempty"`
}

// CacheRuleSummary is a cache rule shown in per-domain cache info.
type CacheRuleSummary struct {
	Match  string `json:"match"`
	TTL    int    `json:"ttl"`
	Bypass bool   `json:"bypass"`
}

// ConfigSummaryResponse is returned by GET /api/v1/config (sanitized).
type ConfigSummaryResponse struct {
	Global      ConfigGlobalSummary `json:"global"`
	DomainCount int                 `json:"domain_count"`
}

// ConfigGlobalSummary is the sanitized global config shown to clients.
type ConfigGlobalSummary struct {
	WorkerCount    string `json:"worker_count"`
	MaxConnections int    `json:"max_connections"`
	LogLevel       string `json:"log_level"`
	LogFormat      string `json:"log_format"`
}

// CloudflareStatusResponse is returned by GET /api/v1/cloudflare/status.
type CloudflareStatusResponse struct {
	Connected            bool      `json:"connected"`
	Email                string    `json:"email,omitempty"`
	AccountID            string    `json:"account_id,omitempty"`
	TokenMask            string    `json:"token_mask,omitempty"`
	UpdatedAt            time.Time `json:"updated_at,omitempty"`
	TunnelCount          int       `json:"tunnel_count,omitempty"`
	CloudflaredInstalled bool      `json:"cloudflared_installed"`
	CloudflaredVersion   string    `json:"cloudflared_version,omitempty"`
}

// CloudflareIPsResponse is returned by GET /api/v1/cloudflare/ips.
type CloudflareIPsResponse struct {
	IPRanges   []string `json:"ip_ranges"`
	LastSynced string   `json:"last_synced,omitempty"`
	Count      int      `json:"count"`
}

// LoginResponse is returned by POST /api/v1/auth/login and /auth/bootstrap.
type LoginResponse struct {
	Status    string    `json:"status"`
	Token     string    `json:"token"`
	UserID    string    `json:"user_id"`
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	Domains   []string  `json:"domains,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`
	APIKey    string    `json:"api_key,omitempty"`
}

// TicketResponse is returned by POST /api/v1/auth/ticket.
type TicketResponse struct {
	Ticket    string    `json:"ticket"`
	ExpiresAt time.Time `json:"expires_at"`
}

// CacheDisabledResponse is returned when cache endpoints are called
// but cache is not enabled.
type CacheDisabledResponse struct {
	Enabled bool   `json:"enabled"`
	Message string `json:"message,omitempty"`
}

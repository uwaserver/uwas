package analytics

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Collector tracks per-domain analytics: page views, unique IPs,
// bandwidth, status codes, and top paths using sharded maps for concurrency.
type Collector struct {
	domains sync.Map // host → *DomainStats
}

// DomainStats holds analytics data for a single domain.
type DomainStats struct {
	mu          sync.Mutex
	PageViews   int64            `json:"page_views"`
	UniqueIPs   map[string]bool  `json:"unique_ips"`
	BytesSent   int64            `json:"bytes_sent"`
	StatusCodes map[int]int64    `json:"status_codes"`
	Paths       map[string]int64 `json:"paths"`
	HourlyViews [24]int64        `json:"hourly_views"`
	Referrers   map[string]int64 `json:"referrers"`
	UserAgents  map[string]int64 `json:"user_agents"` // browser family → count

	// Rolling window: minute-level buckets for last 7 days.
	// Each bucket stores the count of views for that minute.
	minuteBuckets [minuteBucketCount]minuteBucket
	bucketPos     int
	lastBucketAt  time.Time
}

const minuteBucketCount = 7 * 24 * 60 // 7 days of minute buckets

type minuteBucket struct {
	views     int64
	bytes     int64
	timestamp time.Time
}

// New creates a new analytics Collector.
func New() *Collector {
	return &Collector{}
}

// RecordFull records a request with full context including referrer and user agent.
func (c *Collector) RecordFull(host, path, remoteAddr, referrer, userAgent string, statusCode int, bytesSent int64) {
	stats := c.getOrCreate(host)
	ip := extractIP(remoteAddr)
	now := time.Now()
	hour := now.Hour()

	stats.mu.Lock()
	defer stats.mu.Unlock()

	stats.PageViews++
	if stats.UniqueIPs == nil {
		stats.UniqueIPs = make(map[string]bool)
	}
	stats.UniqueIPs[ip] = true

	stats.BytesSent += bytesSent

	if stats.StatusCodes == nil {
		stats.StatusCodes = make(map[int]int64)
	}
	stats.StatusCodes[statusCode]++

	if stats.Paths == nil {
		stats.Paths = make(map[string]int64)
	}
	stats.Paths[path]++

	stats.HourlyViews[hour]++

	// Track referrer
	if referrer != "" {
		if stats.Referrers == nil {
			stats.Referrers = make(map[string]int64)
		}
		ref := extractRefDomain(referrer)
		if ref != "" && ref != host {
			stats.Referrers[ref]++
		}
	}

	// Track user agent (browser family)
	if userAgent != "" {
		if stats.UserAgents == nil {
			stats.UserAgents = make(map[string]int64)
		}
		browser := classifyUA(userAgent)
		stats.UserAgents[browser]++
	}

	// Rolling minute bucket
	c.advanceBuckets(stats, now)
	idx := stats.bucketPos
	stats.minuteBuckets[idx].views++
	stats.minuteBuckets[idx].bytes += bytesSent
	stats.minuteBuckets[idx].timestamp = now
}

// advanceBuckets advances the minute bucket pointer if a new minute has started.
// Must be called with stats.mu held.
func (c *Collector) advanceBuckets(stats *DomainStats, now time.Time) {
	if stats.lastBucketAt.IsZero() {
		stats.lastBucketAt = now.Truncate(time.Minute)
		return
	}

	currentMinute := now.Truncate(time.Minute)
	if currentMinute.Equal(stats.lastBucketAt) {
		return
	}

	// Advance by the number of elapsed minutes (up to the full buffer length).
	elapsed := int(currentMinute.Sub(stats.lastBucketAt).Minutes())
	if elapsed > minuteBucketCount {
		elapsed = minuteBucketCount
	}
	for i := 0; i < elapsed; i++ {
		stats.bucketPos = (stats.bucketPos + 1) % minuteBucketCount
		stats.minuteBuckets[stats.bucketPos] = minuteBucket{}
	}
	stats.lastBucketAt = currentMinute
}

func (c *Collector) getOrCreate(host string) *DomainStats {
	if v, ok := c.domains.Load(host); ok {
		return v.(*DomainStats)
	}
	stats := &DomainStats{
		UniqueIPs:   make(map[string]bool),
		StatusCodes: make(map[int]int64),
		Paths:       make(map[string]int64),
		Referrers:   make(map[string]int64),
		UserAgents:  make(map[string]int64),
	}
	actual, _ := c.domains.LoadOrStore(host, stats)
	return actual.(*DomainStats)
}

// extractRefDomain extracts the hostname from a Referer URL.
func extractRefDomain(ref string) string {
	// Fast path: skip protocol prefix
	s := ref
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	// Take host part (before first / or ?)
	if i := strings.IndexAny(s, "/?"); i >= 0 {
		s = s[:i]
	}
	// Strip port
	if h, _, err := net.SplitHostPort(s); err == nil {
		return h
	}
	return s
}

// classifyUA classifies a user agent string into a browser family.
func classifyUA(ua string) string {
	lower := strings.ToLower(ua)
	switch {
	case strings.Contains(lower, "googlebot"):
		return "Googlebot"
	case strings.Contains(lower, "bingbot"):
		return "Bingbot"
	case strings.Contains(lower, "bot") || strings.Contains(lower, "crawler") || strings.Contains(lower, "spider"):
		return "Bot"
	case strings.Contains(lower, "edg/"):
		return "Edge"
	case strings.Contains(lower, "opr/") || strings.Contains(lower, "opera"):
		return "Opera"
	case strings.Contains(lower, "chrome") && !strings.Contains(lower, "edg/"):
		return "Chrome"
	case strings.Contains(lower, "safari") && !strings.Contains(lower, "chrome"):
		return "Safari"
	case strings.Contains(lower, "firefox"):
		return "Firefox"
	case strings.Contains(lower, "curl"):
		return "curl"
	case strings.Contains(lower, "wget"):
		return "wget"
	case strings.Contains(lower, "python"):
		return "Python"
	case strings.Contains(lower, "go-http"):
		return "Go"
	default:
		return "Other"
	}
}

// Snapshot holds a point-in-time analytics snapshot for a domain.
type Snapshot struct {
	Host          string           `json:"host"`
	PageViews     int64            `json:"page_views"`
	UniqueIPs     int              `json:"unique_ips"`
	BytesSent     int64            `json:"bytes_sent"`
	StatusCodes   map[int]int64    `json:"status_codes"`
	TopPaths      map[string]int64 `json:"top_paths"`
	HourlyViews   [24]int64        `json:"hourly_views"`
	ViewsLastHour int64            `json:"views_last_hour"`
	ViewsLast24h  int64            `json:"views_last_24h"`
	ViewsLast7d   int64            `json:"views_last_7d"`
	TopReferrers  map[string]int64 `json:"top_referrers"`
	UserAgents    map[string]int64 `json:"user_agents"`
}

// GetAll returns snapshots for all tracked domains.
func (c *Collector) GetAll() []Snapshot {
	var snapshots []Snapshot
	c.domains.Range(func(key, value any) bool {
		host := key.(string)
		stats := value.(*DomainStats)
		snapshots = append(snapshots, c.snapshot(host, stats))
		return true
	})
	return snapshots
}

// GetHost returns the analytics snapshot for a single domain.
// Returns nil if the domain has no recorded data.
func (c *Collector) GetHost(host string) *Snapshot {
	v, ok := c.domains.Load(host)
	if !ok {
		return nil
	}
	snap := c.snapshot(host, v.(*DomainStats))
	return &snap
}

func (c *Collector) snapshot(host string, stats *DomainStats) Snapshot {
	stats.mu.Lock()
	defer stats.mu.Unlock()

	snap := Snapshot{
		Host:        host,
		PageViews:   stats.PageViews,
		UniqueIPs:   len(stats.UniqueIPs),
		BytesSent:   stats.BytesSent,
		StatusCodes: make(map[int]int64),
		TopPaths:    make(map[string]int64),
		HourlyViews: stats.HourlyViews,
	}

	for k, v := range stats.StatusCodes {
		snap.StatusCodes[k] = v
	}

	// Top 20 paths by views
	snap.TopPaths = topN(stats.Paths, 20)

	// Top 10 referrers
	snap.TopReferrers = topN(stats.Referrers, 10)

	// User agent breakdown
	snap.UserAgents = make(map[string]int64)
	for k, v := range stats.UserAgents {
		snap.UserAgents[k] = v
	}

	// Rolling window aggregation
	now := time.Now()
	oneHourAgo := now.Add(-1 * time.Hour)
	oneDayAgo := now.Add(-24 * time.Hour)

	for i := 0; i < minuteBucketCount; i++ {
		b := stats.minuteBuckets[i]
		if b.timestamp.IsZero() {
			continue
		}
		snap.ViewsLast7d += b.views
		if b.timestamp.After(oneDayAgo) {
			snap.ViewsLast24h += b.views
		}
		if b.timestamp.After(oneHourAgo) {
			snap.ViewsLastHour += b.views
		}
	}

	return snap
}

// topN returns the top n entries from a map by value.
func topN(m map[string]int64, n int) map[string]int64 {
	if len(m) <= n {
		result := make(map[string]int64, len(m))
		for k, v := range m {
			result[k] = v
		}
		return result
	}

	// Find nth largest value
	type kv struct {
		key string
		val int64
	}
	entries := make([]kv, 0, len(m))
	for k, v := range m {
		entries = append(entries, kv{k, v})
	}

	// Simple selection: sort by value descending and pick top n
	for i := 0; i < n && i < len(entries); i++ {
		maxIdx := i
		for j := i + 1; j < len(entries); j++ {
			if entries[j].val > entries[maxIdx].val {
				maxIdx = j
			}
		}
		entries[i], entries[maxIdx] = entries[maxIdx], entries[i]
	}

	result := make(map[string]int64, n)
	for i := 0; i < n && i < len(entries); i++ {
		result[entries[i].key] = entries[i].val
	}
	return result
}

func extractIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}

// Handler returns an HTTP handler that serves the analytics API.
// GET /api/v1/analytics - returns all domains
// GET /api/v1/analytics/{host} - returns a single domain
func (c *Collector) Handler() (allHandler, hostHandler http.HandlerFunc) {
	allHandler = func(w http.ResponseWriter, r *http.Request) {
		snapshots := c.GetAll()
		writeJSON(w, snapshots)
	}
	hostHandler = func(w http.ResponseWriter, r *http.Request) {
		host := r.PathValue("host")
		snap := c.GetHost(host)
		if snap == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"error":"domain not found"}` + "\n"))
			return
		}
		writeJSON(w, snap)
	}
	return
}

// writeJSON is a minimal JSON encoder to avoid importing encoding/json
// in the hot path. We import it here since this is the API response path.
func writeJSON(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	// Use a local import to keep the package lightweight.
	jsonEncode(w, data)
}

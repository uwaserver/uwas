package middleware

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// GeoIPConfig configures country-based access control.
type GeoIPConfig struct {
	// BlockedCountries is a list of 2-letter ISO country codes to block.
	BlockedCountries []string
	// AllowedCountries if non-empty, only these countries are allowed (whitelist mode).
	AllowedCountries []string
	// DBPath is the path to a JSON file mapping IP prefixes to country codes.
	// If empty, uses an external lookup API (ip-api.com) with caching.
	DBPath string
}

// GeoIP returns middleware that blocks or allows requests based on country.
func GeoIP(cfg GeoIPConfig) Middleware {
	if len(cfg.BlockedCountries) == 0 && len(cfg.AllowedCountries) == 0 {
		return func(next http.Handler) http.Handler { return next }
	}

	blocked := make(map[string]bool)
	for _, c := range cfg.BlockedCountries {
		blocked[strings.ToUpper(c)] = true
	}
	allowed := make(map[string]bool)
	for _, c := range cfg.AllowedCountries {
		allowed[strings.ToUpper(c)] = true
	}

	var db map[string]string // CIDR → country code
	if cfg.DBPath != "" {
		data, err := os.ReadFile(cfg.DBPath)
		if err == nil {
			json.Unmarshal(data, &db)
		}
	}

	cache := &geoCache{entries: make(map[string]geoCacheEntry), inflight: make(map[string]struct{})}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := geoExtractIP(r)
			if ip == "" || isPrivateIP(ip) {
				next.ServeHTTP(w, r)
				return
			}

			country := lookupCountry(ip, db, cache)
			if country == "" {
				next.ServeHTTP(w, r)
				return
			}

			country = strings.ToUpper(country)

			// Whitelist mode: only allow specific countries
			if len(allowed) > 0 {
				if !allowed[country] {
					http.Error(w, "Access denied", http.StatusForbidden)
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			// Blacklist mode: block specific countries
			if blocked[country] {
				http.Error(w, "Access denied", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func geoExtractIP(r *http.Request) string {
	// Use only r.RemoteAddr to prevent GeoIP bypass via spoofed headers.
	// Trusted proxy headers are already handled by the RealIP middleware
	// which rewrites r.RemoteAddr before this middleware runs.
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	return host
}

// privateIPNets is the parsed set of CIDRs treated as "do not GeoIP lookup".
// Parsed once at package init so isPrivateIP doesn't re-parse strings on every
// request (was P16).
var privateIPNets = func() []*net.IPNet {
	cidrs := []string{
		"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
		"127.0.0.0/8", "::1/128", "fc00::/7",
	}
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err == nil {
			nets = append(nets, n)
		}
	}
	return nets
}()

func isPrivateIP(ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	for _, network := range privateIPNets {
		if network.Contains(parsed) {
			return true
		}
	}
	return false
}

type geoCacheEntry struct {
	country string // "" means negative (failed lookup), still honored until expires
	expires time.Time
}

type geoCache struct {
	mu       sync.RWMutex
	entries  map[string]geoCacheEntry
	inflight map[string]struct{} // IPs with a queued/running external lookup
}

func (c *geoCache) get(ip string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[ip]
	if !ok || time.Now().After(entry.expires) {
		return "", false
	}
	return entry.country, true
}

func (c *geoCache) set(ip, country string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Successful lookups cached 24h; failed (empty) results 5min so a single
	// bad IP doesn't keep firing external requests every request.
	ttl := 24 * time.Hour
	if country == "" {
		ttl = 5 * time.Minute
	}
	c.entries[ip] = geoCacheEntry{
		country: country,
		expires: time.Now().Add(ttl),
	}
	// Limit cache size
	if len(c.entries) > 10000 {
		for k := range c.entries {
			delete(c.entries, k)
			break
		}
	}
}

// tryClaimInflight returns true if this caller should perform the external
// lookup for ip; false means another goroutine already owns it (singleflight).
func (c *geoCache) tryClaimInflight(ip string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, busy := c.inflight[ip]; busy {
		return false
	}
	c.inflight[ip] = struct{}{}
	return true
}

func (c *geoCache) releaseInflight(ip string) {
	c.mu.Lock()
	delete(c.inflight, ip)
	c.mu.Unlock()
}

// geoLookupJob queued onto the bounded worker pool.
type geoLookupJob struct {
	ip    string
	cache *geoCache
}

var (
	geoPoolOnce  sync.Once
	geoPoolQueue chan geoLookupJob
)

const (
	geoPoolWorkers  = 4
	geoPoolCapacity = 256
)

func geoPoolStart() {
	geoPoolQueue = make(chan geoLookupJob, geoPoolCapacity)
	for i := 0; i < geoPoolWorkers; i++ {
		go func() {
			for job := range geoPoolQueue {
				country := lookupExternal(job.ip)
				// Always cache — empty result acts as negative cache to prevent re-queue storms.
				job.cache.set(job.ip, country)
				job.cache.releaseInflight(job.ip)
			}
		}()
	}
}

// enqueueGeoLookup schedules an external lookup unless one is already running
// for this IP. Drops silently if the queue is full (bounded pool) — the request
// is allowed through anyway.
func enqueueGeoLookup(ip string, cache *geoCache) {
	geoPoolOnce.Do(geoPoolStart)
	if !cache.tryClaimInflight(ip) {
		return // another goroutine already looking it up
	}
	select {
	case geoPoolQueue <- geoLookupJob{ip: ip, cache: cache}:
	default:
		// Queue full — release inflight so a future request can retry.
		cache.releaseInflight(ip)
	}
}

func lookupCountry(ip string, db map[string]string, cache *geoCache) string {
	if country, ok := cache.get(ip); ok {
		return country
	}

	// Try local DB first (fast, no network)
	if len(db) > 0 {
		parsed := net.ParseIP(ip)
		for cidr, country := range db {
			_, network, err := net.ParseCIDR(cidr)
			if err != nil {
				continue
			}
			if network.Contains(parsed) {
				cache.set(ip, country)
				return country
			}
		}
	}

	// Cache miss + no local DB: queue on bounded worker pool (singleflight per IP).
	// Next request from this IP will use the cached result.
	enqueueGeoLookup(ip, cache)
	return "" // allow through on first request (default-allow until cached)
}

func lookupExternal(ip string) string {
	// Validate that ip looks like a real IP address to prevent URL injection.
	if net.ParseIP(ip) == nil {
		return ""
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://ip-api.com/json/" + ip + "?fields=countryCode")
	if err != nil {
		return ""
	}
	defer func() {
		io.Copy(io.Discard, resp.Body) // drain body to allow connection reuse
		resp.Body.Close()
	}()
	var result struct {
		CountryCode string `json:"countryCode"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return ""
	}
	return result.CountryCode
}

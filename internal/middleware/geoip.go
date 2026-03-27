package middleware

import (
	"encoding/json"
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

	cache := &geoCache{entries: make(map[string]geoCacheEntry)}

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
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.SplitN(xff, ",", 2)
		return strings.TrimSpace(parts[0])
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	return host
}

func isPrivateIP(ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	private := []string{
		"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
		"127.0.0.0/8", "::1/128", "fc00::/7",
	}
	for _, cidr := range private {
		_, network, _ := net.ParseCIDR(cidr)
		if network.Contains(parsed) {
			return true
		}
	}
	return false
}

type geoCacheEntry struct {
	country string
	expires time.Time
}

type geoCache struct {
	mu      sync.RWMutex
	entries map[string]geoCacheEntry
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
	c.entries[ip] = geoCacheEntry{
		country: country,
		expires: time.Now().Add(24 * time.Hour),
	}
	// Limit cache size
	if len(c.entries) > 10000 {
		for k := range c.entries {
			delete(c.entries, k)
			break
		}
	}
}

func lookupCountry(ip string, db map[string]string, cache *geoCache) string {
	if country, ok := cache.get(ip); ok {
		return country
	}

	// Try local DB first
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

	// Fallback: external API (rate-limited, best-effort)
	country := lookupExternal(ip)
	if country != "" {
		cache.set(ip, country)
	}
	return country
}

func lookupExternal(ip string) string {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://ip-api.com/json/" + ip + "?fields=countryCode")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var result struct {
		CountryCode string `json:"countryCode"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return ""
	}
	return result.CountryCode
}

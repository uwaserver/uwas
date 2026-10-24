package cache

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/uwaserver/uwas/internal/logger"
)

// Engine is the main cache interface combining L1 memory + L2 disk + L3 Redis.
type Engine struct {
	memory   *MemoryCache
	disk     *DiskCache
	redis    *RedisCache
	logger   *logger.Logger
	varyKeys []string      // precomputed: ["Accept-Encoding", ...configuredVaryHeaders]
	writeSem chan struct{} // bounds concurrent L2/L3 writes
	// varyByQuery mirrors global.cache.vary_by_query. It defaults to true —
	// the key has always included the query string — so an operator has to
	// ask for the collapse explicitly.
	varyByQuery bool
}

// SetVaryHeaders precomput the full vary-key list (Accept-Encoding + caller
// headers).  Call once at config load; the slice is reused on every request
// with zero per-request allocation.
func (e *Engine) SetVaryHeaders(headers []string) {
	keys := make([]string, 0, len(headers)+1)
	keys = append(keys, "Accept-Encoding")
	keys = append(keys, headers...)
	e.varyKeys = keys
}

// SetVaryByQuery controls whether the query string is part of the cache key.
//
// global.cache.vary_by_query was dead configuration: GenerateKey always
// folded the query in and nothing read the field, so an operator could not
// collapse /page?utm_source=a and /page?utm_source=b onto one entry however
// they set it.
func (e *Engine) SetVaryByQuery(vary bool) {
	e.varyByQuery = vary
}

// NewEngine creates a cache engine with memory and optional disk backing.
// The ctx parameter controls the lifetime of background cleanup goroutines.
func NewEngine(ctx context.Context, memoryLimit int64, diskPath string, diskLimit int64, log *logger.Logger) *Engine {
	e := &Engine{
		memory:      NewMemoryCache(memoryLimit),
		logger:      log,
		varyKeys:    []string{"Accept-Encoding"}, // default; extended via SetVaryHeaders
		writeSem:    make(chan struct{}, maxConcurrentWrites),
		varyByQuery: true,
	}

	if diskPath != "" {
		e.disk = NewDiskCache(diskPath, diskLimit)
		e.disk.StartCleanup(ctx, 10*time.Minute)
	}

	// Start periodic cleanup every 5 minutes
	e.memory.StartCleanup(ctx, 5*time.Minute)

	return e
}

// Key computes the cache key for a request.  Callers that need to look up
// and store a response (the normal cache miss → handler → cache set path)
// should call this once and pass the result to GetByKey / SetByKey to avoid
// recomputing the key (and reallocating the vary slice) a second time.
func (e *Engine) Key(r *http.Request) string {
	return e.key(r)
}

func (e *Engine) key(r *http.Request) string {
	if !e.varyByQuery {
		return GenerateKeyWithoutQuery(r, e.varyKeys)
	}
	return GenerateKey(r, e.varyKeys)
}

// Get looks up a cache entry: L1 (memory) → L2 (disk) → L3 (Redis) → miss.
func (e *Engine) Get(r *http.Request) (*CachedResponse, string) {
	return e.GetByKey(e.key(r))
}

func (e *Engine) Set(r *http.Request, resp *CachedResponse) {
	e.SetByKey(e.key(r), resp)
}

// GetByKey looks up a cache entry by explicit key (for ESI fragments).
func (e *Engine) GetByKey(key string) (*CachedResponse, string) {
	resp, status := e.memory.Get(key)
	if resp != nil {
		return resp, status
	}
	if e.disk != nil {
		resp, err := e.disk.Get(key)
		if err == nil && resp != nil {
			if resp.IsFresh() || resp.IsStale() {
				e.memory.Set(key, resp)
				if resp.IsFresh() {
					return resp, StatusHit
				}
				return resp, StatusStale
			}
			e.disk.Delete(key)
		}
	}
	if e.redis != nil {
		resp, err := e.redis.Get(key)
		if err == nil && resp != nil {
			if resp.IsFresh() || resp.IsStale() {
				e.memory.Set(key, resp)
				// Async promote to disk so subsequent L2 hits avoid Redis round-trips.
				if e.disk != nil {
					select {
					case e.writeSem <- struct{}{}:
						go func() {
							defer func() { <-e.writeSem }()
							_ = e.disk.Set(key, resp)
						}()
					default:
					}
				}
				if resp.IsFresh() {
					return resp, StatusHit
				}
				return resp, StatusStale
			}
		}
	}
	return nil, StatusMiss
}

// SetByKey stores a response by explicit key (for ESI fragments).
func (e *Engine) SetByKey(key string, resp *CachedResponse) {
	// The key only distinguishes the headers in varyKeys. A response that
	// varies on anything else (Origin, Accept-Language, User-Agent, ...)
	// would be served to requests carrying a different value of that header.
	if !e.varyCoveredByKey(resp.Headers) {
		return
	}
	e.memory.Set(key, resp)
	if e.disk != nil {
		select {
		case e.writeSem <- struct{}{}:
			go func() {
				defer func() { <-e.writeSem }()
				if err := e.disk.Set(key, resp); err != nil {
					e.logger.Warn("disk cache write failed", "key", key, "error", err)
				}
			}()
		default:
		}
	}
	if e.redis != nil {
		select {
		case e.writeSem <- struct{}{}:
			go func() {
				defer func() { <-e.writeSem }()
				if err := e.redis.Set(key, resp, resp.TTL+resp.GraceTTL); err != nil {
					e.logger.Warn("redis cache write failed", "key", key, "error", err)
				}
			}()
		default:
		}
	}
}

// varyCoveredByKey reports whether every header named in the response's Vary
// is part of the cache key.
func (e *Engine) varyCoveredByKey(h http.Header) bool {
	for _, value := range h.Values("Vary") {
		for _, name := range strings.Split(value, ",") {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			keyed := false
			for _, k := range e.varyKeys {
				if strings.EqualFold(name, k) {
					keyed = true
					break
				}
			}
			if !keyed {
				return false
			}
		}
	}
	return true
}

// PurgeKey removes one entry from every tier.
//
// Used when a background revalidation finds the resource is no longer
// cacheable: keeping the stale copy until its grace window runs out would
// serve content the origin has just declined to have cached.
func (e *Engine) PurgeKey(key string) {
	e.memory.Delete(key)
	if e.disk != nil {
		e.disk.Delete(key)
	}
	if e.redis != nil {
		_ = e.redis.Delete(key)
	}
}

// PurgeByTag removes entries matching tags from L1, L2, and L3.
func (e *Engine) PurgeByTag(tags ...string) int {
	count := e.memory.PurgeByTag(tags...)
	if e.disk != nil {
		count += e.disk.PurgeByTag(tags...)
	}
	if e.redis != nil {
		for _, tag := range tags {
			if err := e.redis.PurgeByTag(tag); err != nil {
				e.logger.Warn("redis PurgeByTag failed", "tag", tag, "error", err)
			}
		}
	}
	return count
}

// PurgeAll clears all caches.
func (e *Engine) PurgeAll() {
	e.memory.PurgeAll()
	if e.disk != nil {
		e.disk.PurgeAll()
	}
	if e.redis != nil {
		// Delete the keys — NOT Close(): closing only drops the connection
		// (which auto-reconnects), leaving every cached entry intact so the
		// purge would silently do nothing and stale content would be served.
		_ = e.redis.PurgeAll()
	}
}

// SetRedis attaches a Redis cache to the engine.
func (e *Engine) SetRedis(redis *RedisCache) {
	e.redis = redis
}

// Stats returns cache statistics.
func (e *Engine) Stats() map[string]int64 {
	hits, misses, stales, usedBytes := e.memory.Stats()
	return map[string]int64{
		"hits":       hits,
		"misses":     misses,
		"stales":     stales,
		"used_bytes": usedBytes,
		"entries":    int64(e.memory.Len()),
	}
}

// IsCacheable checks if a response should be cached.
func IsCacheable(r *http.Request, statusCode int, headers http.Header) bool {
	// Only cache GET/HEAD
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}

	// Never share responses to credentialed requests
	if r.Header.Get("Authorization") != "" {
		return false
	}

	// Only cache specific status codes
	switch statusCode {
	case 200, 301, 404:
	default:
		return false
	}

	return sharedCacheableHeaders(headers)
}

// sharedCacheableHeaders reports whether a response's headers allow it into
// the shared cache. ESI fragments are held to the same rule (F3401).
func sharedCacheableHeaders(headers http.Header) bool {
	// The cache key varies only on Accept-Encoding + configured headers;
	// a response that declares Vary on anything session-shaped (or on
	// everything) cannot be keyed correctly, so don't cache it.
	for _, value := range headers.Values("Vary") {
		for _, name := range strings.Split(value, ",") {
			name = strings.TrimSpace(name)
			if name == "*" || strings.EqualFold(name, "Cookie") || strings.EqualFold(name, "Authorization") {
				return false
			}
		}
	}

	// Don't cache if Set-Cookie present
	if len(headers.Values("Set-Cookie")) > 0 {
		return false
	}

	// Don't cache if Cache-Control: no-store or private. Directives are
	// case-insensitive and may be split across several header lines.
	cc := strings.ToLower(strings.Join(headers.Values("Cache-Control"), ","))
	if strings.Contains(cc, "no-store") || strings.Contains(cc, "private") {
		return false
	}

	return true
}

// ShouldBypass checks if the request should bypass the cache.
func ShouldBypass(r *http.Request) bool {
	// POST, PUT, DELETE always bypass
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return true
	}

	// Never cache .php requests — PHP output is dynamic
	if strings.HasSuffix(r.URL.Path, ".php") {
		return true
	}

	// A client's Cache-Control: no-cache / Pragma: no-cache is deliberately
	// not honoured (nginx does not honour it by default either). It used to
	// send the request straight to the backend, so a flood carrying that one
	// header bypassed the cache entirely and landed every request on the
	// origin. Content is refreshed by TTL, revalidation and purges instead.
	return false
}

// CapTTL bounds a cache entry's lifetime by the origin's own freshness
// lifetime: s-maxage, or failing that max-age, from the response's
// Cache-Control. Previously the domain TTL always won, so a page the origin
// declared fresh for 5 minutes was served for an hour.
//
// Only a positive lifetime caps the TTL. max-age=0 and no-cache leave it
// alone: honouring them would stop caching the many origins that send them by
// default on static assets, and the operator's domain TTL is the explicit
// decision for those.
func CapTTL(ttl time.Duration, headers http.Header) time.Duration {
	if origin, ok := originMaxAge(strings.Join(headers.Values("Cache-Control"), ",")); ok && origin > 0 && origin < ttl {
		return origin
	}
	return ttl
}

// originMaxAge returns s-maxage if present, else max-age.
func originMaxAge(cc string) (time.Duration, bool) {
	var maxAge, sMaxAge time.Duration
	var hasMaxAge, hasSMaxAge bool
	for _, directive := range strings.Split(cc, ",") {
		name, value, found := strings.Cut(strings.TrimSpace(directive), "=")
		if !found {
			continue
		}
		secs, err := strconv.Atoi(strings.Trim(strings.TrimSpace(value), `"`))
		if err != nil || secs < 0 {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "s-maxage":
			sMaxAge, hasSMaxAge = time.Duration(secs)*time.Second, true
		case "max-age":
			maxAge, hasMaxAge = time.Duration(secs)*time.Second, true
		}
	}
	if hasSMaxAge {
		return sMaxAge, true
	}
	return maxAge, hasMaxAge
}

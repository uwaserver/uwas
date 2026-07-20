package cache

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/uwaserver/uwas/internal/logger"
)

// maxConcurrentWrites limits concurrent L2 (disk) and L3 (Redis) write
// goroutines. This prevents unbounded goroutine growth during cache
// population under high concurrency. The semaphore is shared across
// all cache tiers so a single slow backend cannot starve others.
const maxConcurrentWrites = 16

// Engine is the main cache interface combining L1 memory + L2 disk + L3 Redis.
type Engine struct {
	memory      *MemoryCache
	disk        *DiskCache
	redis       *RedisCache
	logger      *logger.Logger
	VaryHeaders []string      // additional headers to include in cache key (from config)
	writeSem    chan struct{} // bounds concurrent L2/L3 writes
}

// NewEngine creates a cache engine with memory and optional disk backing.
// The ctx parameter controls the lifetime of background cleanup goroutines.
func NewEngine(ctx context.Context, memoryLimit int64, diskPath string, diskLimit int64, log *logger.Logger) *Engine {
	e := &Engine{
		memory:   NewMemoryCache(memoryLimit),
		logger:   log,
		writeSem: make(chan struct{}, maxConcurrentWrites),
	}

	if diskPath != "" {
		e.disk = NewDiskCache(diskPath, diskLimit)
		e.disk.StartCleanup(ctx, 10*time.Minute)
	}

	// Start periodic cleanup every 5 minutes
	e.memory.StartCleanup(ctx, 5*time.Minute)

	return e
}

// Get looks up a cache entry: L1 (memory) → L2 (disk) → L3 (Redis) → miss.
func (e *Engine) Get(r *http.Request) (*CachedResponse, string) {
	key := GenerateKey(r, e.varyKeys())

	// L1: memory
	resp, status := e.memory.Get(key)
	if resp != nil {
		return resp, status
	}

	// L2: disk (promote to memory on hit)
	if e.disk != nil {
		resp, err := e.disk.Get(key)
		if err == nil && resp != nil {
			if resp.IsFresh() || resp.IsStale() {
				e.memory.Set(key, resp) // promote
				if resp.IsFresh() {
					return resp, StatusHit
				}
				return resp, StatusStale
			}
			// Expired: reclaim the file so dead entries don't pin usedBytes
			e.disk.Delete(key)
		}
	}

	// L3: Redis (promote to memory on hit)
	if e.redis != nil {
		resp, err := e.redis.Get(key)
		if err == nil && resp != nil {
			if resp.IsFresh() || resp.IsStale() {
				e.memory.Set(key, resp) // promote
				if e.disk != nil {
					select {
					case e.writeSem <- struct{}{}:
						go func() {
							defer func() { <-e.writeSem }()
							_ = e.disk.Set(key, resp) // also promote to disk
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

// Set stores a response in L1 and async-writes to L2.
func (e *Engine) varyKeys() []string {
	keys := []string{"Accept-Encoding"}
	keys = append(keys, e.VaryHeaders...)
	return keys
}

func (e *Engine) Set(r *http.Request, resp *CachedResponse) {
	key := GenerateKey(r, e.varyKeys())
	e.memory.Set(key, resp)

	// Async disk write (bounded by writeSem — drops on overload)
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
			// L1 write already succeeded; dropping L2 is acceptable
		}
	}

	// Async Redis write (bounded by writeSem)
	if e.redis != nil {
		select {
		case e.writeSem <- struct{}{}:
			go func() {
				defer func() { <-e.writeSem }()
				// resp.TTL is already a time.Duration; include the grace
				// window so stale-while-revalidate serving works from L3.
				if err := e.redis.Set(key, resp, resp.TTL+resp.GraceTTL); err != nil {
					e.logger.Warn("redis cache write failed", "key", key, "error", err)
				}
			}()
		default:
		}
	}
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

	// The cache key varies only on Accept-Encoding + configured headers;
	// a response that declares Vary on anything session-shaped (or on
	// everything) cannot be keyed correctly, so don't cache it.
	if v := headers.Get("Vary"); v != "" {
		if v == "*" || strings.Contains(v, "Cookie") || strings.Contains(v, "Authorization") {
			return false
		}
	}

	// Only cache specific status codes
	switch statusCode {
	case 200, 301, 404:
	default:
		return false
	}

	// Don't cache if Set-Cookie present
	if headers.Get("Set-Cookie") != "" {
		return false
	}

	// Don't cache if Cache-Control: no-store or private
	cc := headers.Get("Cache-Control")
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

	// Cache-Control: no-cache
	if strings.Contains(r.Header.Get("Cache-Control"), "no-cache") {
		return true
	}

	// Pragma: no-cache (HTTP/1.0 compat)
	if r.Header.Get("Pragma") == "no-cache" {
		return true
	}

	return false
}

package cache

import (
	"net/http"
	"strings"
	"time"

	"github.com/uwaserver/uwas/internal/logger"
)

// Engine is the main cache interface combining L1 memory + L2 disk.
type Engine struct {
	memory *MemoryCache
	disk   *DiskCache
	logger *logger.Logger
}

// NewEngine creates a cache engine with memory and optional disk backing.
func NewEngine(memoryLimit int64, diskPath string, diskLimit int64, log *logger.Logger) *Engine {
	e := &Engine{
		memory: NewMemoryCache(memoryLimit),
		logger: log,
	}

	if diskPath != "" {
		e.disk = NewDiskCache(diskPath, diskLimit)
	}

	// Start periodic cleanup every 5 minutes
	e.memory.StartCleanup(5 * time.Minute)

	return e
}

// Get looks up a cache entry: L1 (memory) → L2 (disk) → miss.
func (e *Engine) Get(r *http.Request) (*CachedResponse, string) {
	key := GenerateKey(r, []string{"Accept-Encoding"})

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
		}
	}

	return nil, StatusMiss
}

// Set stores a response in L1 and async-writes to L2.
func (e *Engine) Set(r *http.Request, resp *CachedResponse) {
	key := GenerateKey(r, []string{"Accept-Encoding"})
	e.memory.Set(key, resp)

	// Async disk write
	if e.disk != nil {
		go func() {
			if err := e.disk.Set(key, resp); err != nil {
				e.logger.Warn("disk cache write failed", "key", key, "error", err)
			}
		}()
	}
}

// PurgeByTag removes entries matching tags from both L1 and L2.
func (e *Engine) PurgeByTag(tags ...string) int {
	return e.memory.PurgeByTag(tags...)
}

// PurgeAll clears all caches.
func (e *Engine) PurgeAll() {
	e.memory.PurgeAll()
	if e.disk != nil {
		e.disk.PurgeAll()
	}
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

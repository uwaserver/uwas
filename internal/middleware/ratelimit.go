package middleware

import (
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

const shardCount = 256

// RateLimiter implements a sharded token bucket rate limiter.
type RateLimiter struct {
	shards  [shardCount]rateShard
	limit   int
	window  time.Duration
	cleanup atomic.Bool
}

type rateShard struct {
	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens    int
	lastReset time.Time
}

// NewRateLimiter creates a rate limiter with the given limit per window.
func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	rl := &RateLimiter{
		limit:  limit,
		window: window,
	}
	for i := range rl.shards {
		rl.shards[i].buckets = make(map[string]*bucket)
	}

	// Background cleanup
	go rl.cleanupLoop()

	return rl
}

// RateLimit returns middleware that enforces per-IP rate limiting.
func RateLimit(limit int, window time.Duration) Middleware {
	if limit <= 0 {
		return func(next http.Handler) http.Handler { return next }
	}

	rl := NewRateLimiter(limit, window)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r)
			if !rl.Allow(ip) {
				w.Header().Set("Retry-After", fmt.Sprintf("%d", int(window.Seconds())))
				http.Error(w, "429 Too Many Requests", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Allow checks if the IP is within the rate limit.
func (rl *RateLimiter) Allow(key string) bool {
	s := &rl.shards[shardIndex(key)]
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	b, ok := s.buckets[key]
	if !ok {
		s.buckets[key] = &bucket{tokens: rl.limit - 1, lastReset: now}
		return true
	}

	// Reset if window expired
	if now.Sub(b.lastReset) >= rl.window {
		b.tokens = rl.limit - 1
		b.lastReset = now
		return true
	}

	if b.tokens > 0 {
		b.tokens--
		return true
	}

	return false
}

func (rl *RateLimiter) cleanupLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		rl.cleanup.Store(true)
		now := time.Now()
		for i := range rl.shards {
			s := &rl.shards[i]
			s.mu.Lock()
			for key, b := range s.buckets {
				if now.Sub(b.lastReset) > rl.window*2 {
					delete(s.buckets, key)
				}
			}
			s.mu.Unlock()
		}
		rl.cleanup.Store(false)
	}
}

func shardIndex(key string) uint8 {
	if len(key) == 0 {
		return 0
	}
	// FNV-1a inspired quick hash
	h := uint32(2166136261)
	for i := 0; i < len(key); i++ {
		h ^= uint32(key[i])
		h *= 16777619
	}
	return uint8(h)
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

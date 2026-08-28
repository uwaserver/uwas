package middleware

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const shardCount = 256

// RateLimiter implements a sharded token bucket rate limiter.
type RateLimiter struct {
	shards         [shardCount]rateShard
	limit          int
	window         time.Duration
	cleanup        atomic.Bool
	trustedProxies []*net.IPNet
	// keyBy mirrors security.rate_limit.by: "" or "ip" keys on the client
	// address, "header:<Name>" on that request header.
	keyBy  string
	cancel context.CancelFunc // stops the background cleanup goroutine
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
// The ctx parameter controls the lifetime of the background cleanup goroutine.
// Call Stop() to release the cleanup goroutine early (e.g. on config reload)
// when the parent ctx outlives the limiter.
func NewRateLimiter(ctx context.Context, limit int, window time.Duration) *RateLimiter {
	if window <= 0 {
		window = time.Minute
	}
	rlCtx, cancel := context.WithCancel(ctx)
	rl := &RateLimiter{
		limit:  limit,
		window: window,
		cancel: cancel,
	}
	for i := range rl.shards {
		rl.shards[i].buckets = make(map[string]*bucket)
	}

	// Background cleanup — bound to the limiter's own context so Stop() releases it.
	go rl.cleanupLoop(rlCtx)

	return rl
}

// Stop cancels the background cleanup goroutine. Safe to call multiple times.
// Use this on config reload when the old limiter is being swapped out but the
// server context is still alive.
func (rl *RateLimiter) Stop() {
	if rl == nil || rl.cancel == nil {
		return
	}
	rl.cancel()
}

// SetTrustedProxies configures CIDR ranges for trusted reverse proxies.
// Only X-Forwarded-For / X-Real-IP from these IPs will be trusted.
func (rl *RateLimiter) SetTrustedProxies(cidrs []string) {
	rl.trustedProxies = nil
	for _, cidr := range cidrs {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err == nil {
			rl.trustedProxies = append(rl.trustedProxies, ipNet)
		}
	}
}

// isTrustedProxy checks if the given IP is in the trusted proxies list.
func (rl *RateLimiter) isTrustedProxy(ip net.IP) bool {
	if rl.trustedProxies == nil {
		return false
	}
	for _, ipNet := range rl.trustedProxies {
		if ipNet.Contains(ip) {
			return true
		}
	}
	return false
}

// RateLimit returns middleware that enforces per-IP rate limiting.
// The ctx parameter controls the lifetime of the background cleanup goroutine.
func RateLimit(ctx context.Context, limit int, window time.Duration) Middleware {
	if limit <= 0 {
		return func(next http.Handler) http.Handler { return next }
	}

	rl := NewRateLimiter(ctx, limit, window)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !rl.Allow(rl.Key(r)) {
				w.Header().Set("Retry-After", fmt.Sprintf("%d", int(rl.window.Seconds())))
				http.Error(w, "429 Too Many Requests", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// SetKeyBy configures what the limiter counts against.
//
// security.rate_limit.by was dead configuration: SPECIFICATION.md documents
// `by: ip | header:X-Forwarded-For` and nothing read it, so every limiter
// keyed on the client address whatever the domain asked for.
func (rl *RateLimiter) SetKeyBy(by string) {
	rl.keyBy = strings.TrimSpace(by)
}

// KnownRateLimitKey reports whether a security.rate_limit.by value is one the
// limiter understands. An unrecognised form falls back to the client address;
// the caller warns once at startup rather than blocking the boot over a field
// that did nothing until now.
func KnownRateLimitKey(by string) bool {
	by = strings.TrimSpace(by)
	if by == "" || strings.EqualFold(by, "ip") {
		return true
	}
	name, ok := strings.CutPrefix(by, "header:")
	if !ok {
		name, ok = strings.CutPrefix(by, "HEADER:")
	}
	return ok && strings.TrimSpace(name) != ""
}

// Key returns the bucket key for a request, applying security.rate_limit.by.
//
// X-Forwarded-For and X-Real-IP route through the client-address path rather
// than being read raw: the real-IP middleware has already resolved them
// against trusted_proxies, and taking the raw header would let any client set
// its own bucket. That is what `by: header:X-Forwarded-For` means in the docs.
func (rl *RateLimiter) Key(r *http.Request) string {
	by := rl.keyBy
	if by == "" || strings.EqualFold(by, "ip") {
		return clientIP(rl, r)
	}

	name, ok := strings.CutPrefix(by, "header:")
	if !ok {
		name, ok = strings.CutPrefix(by, "HEADER:")
	}
	if !ok {
		// Unrecognised form: fall back to the address rather than putting
		// every request in one bucket.
		return clientIP(rl, r)
	}

	name = strings.TrimSpace(name)
	if strings.EqualFold(name, "X-Forwarded-For") || strings.EqualFold(name, "X-Real-IP") {
		return clientIP(rl, r)
	}
	if v := strings.TrimSpace(r.Header.Get(name)); v != "" {
		// Namespaced so a header value cannot collide with an IP key.
		return "h:" + strings.ToLower(name) + ":" + v
	}
	// A request without the header falls back to its address. One shared
	// bucket for every header-less request would be a trivial way to exhaust
	// the limit for everyone.
	return clientIP(rl, r)
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

func (rl *RateLimiter) cleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
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

func clientIP(rl *RateLimiter, r *http.Request) string {
	remoteIP := func() string {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			return r.RemoteAddr
		}
		return host
	}()

	// If we have trusted proxies configured, check X-Forwarded-For and X-Real-IP
	if rl != nil && rl.trustedProxies != nil {
		rip := net.ParseIP(remoteIP)
		if rip != nil && rl.isTrustedProxy(rip) {
			// Trust X-Forwarded-For from trusted proxies. Use the rightmost
			// UNTRUSTED IP, not the leftmost: the leftmost entry is fully
			// client-controlled, so a client behind the trusted proxy could
			// prepend a fake IP to evade or poison per-IP rate limiting.
			if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
				if ip := extractRealIP(xff, rl.trustedProxies); ip != "" {
					return ip
				}
			}
			// Fall back to X-Real-IP
			if xri := r.Header.Get("X-Real-IP"); xri != "" {
				xri = strings.TrimSpace(xri)
				if xri != "" {
					return xri
				}
			}
		}
	}

	// Otherwise use RemoteAddr directly (no spoofing possible)
	return remoteIP
}

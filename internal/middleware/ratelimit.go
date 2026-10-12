package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const shardCount = 256

// maxHeaderKeyLen bounds how much of a header value a bucket key keeps
// verbatim; longer values are replaced by their SHA-256 digest.
const maxHeaderKeyLen = 64

// RateLimiter implements a sharded token bucket rate limiter.
type RateLimiter struct {
	shards         [shardCount]rateShard
	limit          int
	window         time.Duration
	cleanup        atomic.Bool
	mu             sync.RWMutex // protects trustedProxies and keyBy
	trustedProxies []*net.IPNet
	// keyBy mirrors security.rate_limit.by: "" or "ip" keys on the client
	// address, "header:<Name>" on that request header.
	keyBy  string
	cancel context.CancelFunc // stops the background cleanup goroutine
}

// maxBucketsPerShard bounds how many client buckets one shard keeps (about one
// million across all shards). The key can be client-chosen (rate_limit.by:
// header:<Name>) or one of many source addresses, so without a bound a client
// rotating it grows the map with its request count until the once-a-minute
// sweep catches up (F10302).
var maxBucketsPerShard = 4096

type rateShard struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	swept   time.Time // last on-demand sweep of a full shard
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
	rl.mu.Lock()
	defer rl.mu.Unlock()
	// Same parser as RealIP: a bare IP is a valid entry (config validation
	// accepts it) and IPv4-mapped forms fold to IPv4 (F10900).
	rl.trustedProxies = parseCIDRs(cidrs)
}

// isTrustedProxy checks if the given IP is in the trusted proxies list.
func (rl *RateLimiter) isTrustedProxy(ip net.IP) bool {
	rl.mu.RLock()
	defer rl.mu.RUnlock()
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
	return RateLimitWithStats(ctx, limit, window, nil)
}

// RateLimitWithStats is RateLimit that also records each rejection in stats.
// The stats observer feeds the autoblocker; without it the global limiter
// answered 429 forever and never escalated to a block, however hard an IP
// pushed against it.
func RateLimitWithStats(ctx context.Context, limit int, window time.Duration, stats *SecurityStats) Middleware {
	if limit <= 0 {
		return func(next http.Handler) http.Handler { return next }
	}

	rl := NewRateLimiter(ctx, limit, window)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !rl.Allow(rl.Key(r)) {
				if stats != nil {
					stats.Record(clientIP(nil, r), r.URL.Path, "rate", r.UserAgent())
				}
				w.Header().Set("Retry-After", fmt.Sprintf("%d", int(rl.window.Seconds())))
				http.Error(w, "429 Too Many Requests", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Window reports the limiter's counting period, so callers that reject a
// request can tell the client how long to wait instead of guessing.
func (rl *RateLimiter) Window() time.Duration {
	if rl == nil {
		return 0
	}
	return rl.window
}

// SetKeyBy configures what the limiter counts against.
//
// security.rate_limit.by was dead configuration: SPECIFICATION.md documents
// `by: ip | header:X-Forwarded-For` and nothing read it, so every limiter
// keyed on the client address whatever the domain asked for.
func (rl *RateLimiter) SetKeyBy(by string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
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
	name, ok := cutHeaderPrefix(by)
	return ok && strings.TrimSpace(name) != ""
}

// cutHeaderPrefix splits "header:<Name>" in any letter case. Only "header:"
// and "HEADER:" used to be recognised, so the usual spelling "Header:X-Api-Key"
// silently counted per client address instead (F13903).
func cutHeaderPrefix(by string) (string, bool) {
	const prefix = "header:"
	if len(by) >= len(prefix) && strings.EqualFold(by[:len(prefix)], prefix) {
		return by[len(prefix):], true
	}
	return "", false
}

// Key returns the bucket key for a request, applying security.rate_limit.by.
//
// X-Forwarded-For and X-Real-IP route through the client-address path rather
// than being read raw: the real-IP middleware has already resolved them
// against trusted_proxies, and taking the raw header would let any client set
// its own bucket. That is what `by: header:X-Forwarded-For` means in the docs.
func (rl *RateLimiter) Key(r *http.Request) string {
	rl.mu.RLock()
	by := rl.keyBy
	rl.mu.RUnlock()
	if by == "" || strings.EqualFold(by, "ip") {
		return rateAddr(clientIP(rl, r))
	}

	name, ok := cutHeaderPrefix(by)
	if !ok {
		// Unrecognised form: fall back to the address rather than putting
		// every request in one bucket.
		return rateAddr(clientIP(rl, r))
	}

	name = strings.TrimSpace(name)
	if strings.EqualFold(name, "X-Forwarded-For") || strings.EqualFold(name, "X-Real-IP") {
		return rateAddr(clientIP(rl, r))
	}
	if v := strings.TrimSpace(r.Header.Get(name)); v != "" {
		// The value is client-supplied and can be as large as MaxHeaderBytes;
		// the bucket would pin it for two windows. Store a digest of long
		// values so retained memory does not scale with header size.
		if len(v) > maxHeaderKeyLen {
			sum := sha256.Sum256([]byte(v))
			v = "#" + hex.EncodeToString(sum[:])
		}
		// Namespaced so a header value cannot collide with an IP key.
		return "h:" + strings.ToLower(name) + ":" + v
	}
	// A request without the header falls back to its address. One shared
	// bucket for every header-less request would be a trivial way to exhaust
	// the limit for everyone.
	return rateAddr(clientIP(rl, r))
}

// rateAddr is the bucket identity for a client address. An IPv6 subscriber
// owns at least a /64, so keying on the full /128 would let one client rotate
// the low 64 bits per request and never meet its limit; v6 addresses are
// therefore bucketed by /64. IPv4 (and IPv4-mapped) addresses are unchanged.
func rateAddr(ip string) string {
	// A link-local peer arrives zoned ("fe80::1%eth0"), which ParseIP rejects.
	if i := strings.IndexByte(ip, '%'); i >= 0 {
		ip = ip[:i]
	}
	p := net.ParseIP(ip)
	if p == nil {
		return ip
	}
	if p4 := p.To4(); p4 != nil {
		return p4.String()
	}
	return p.Mask(net.CIDRMask(64, 128)).String() + "/64"
}

// Allow checks if the IP is within the rate limit.
func (rl *RateLimiter) Allow(key string) bool {
	s := &rl.shards[shardIndex(key)]
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	b, ok := s.buckets[key]
	if !ok {
		if len(s.buckets) >= maxBucketsPerShard {
			// A bucket whose window has passed is equivalent to a fresh one,
			// so evicting it changes no decision. Sweep at most once a second
			// so a flood of new keys cannot turn every request into a scan.
			if now.Sub(s.swept) >= time.Second {
				s.swept = now
				for k, old := range s.buckets {
					if now.Sub(old.lastReset) >= rl.window {
						delete(s.buckets, k)
					}
				}
			}
			if len(s.buckets) >= maxBucketsPerShard {
				// Full of live buckets: serve the newcomer untracked rather
				// than grow without bound or refuse everyone new.
				return true
			}
		}
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
	var trusted []*net.IPNet
	if rl != nil {
		rl.mu.RLock()
		trusted = rl.trustedProxies
		rl.mu.RUnlock()
	}
	if len(trusted) > 0 {
		// Same zone handling as extractIP (F10600).
		rip := extractIP(remoteIP)
		if rip != nil && isTrusted(rip, trusted) {
			// Trust X-Forwarded-For from trusted proxies. Use the rightmost
			// UNTRUSTED IP, not the leftmost: the leftmost entry is fully
			// client-controlled, so a client behind the trusted proxy could
			// prepend a fake IP to evade or poison per-IP rate limiting.
			// Every header line counts, as in RealIP: a proxy may add its own
			// line after a client-supplied one (F10901).
			if xff := strings.Join(r.Header.Values("X-Forwarded-For"), ","); xff != "" {
				if ip := extractRealIP(xff, trusted); ip != "" {
					return ip
				}
			}
			// Fall back to X-Real-IP
			if xri := r.Header.Get("X-Real-IP"); xri != "" {
				xri = strings.TrimSpace(xri)
				if xri != "" && net.ParseIP(xri) != nil {
					return xri
				}
			}
		}
	}

	// Otherwise use RemoteAddr directly (no spoofing possible)
	return remoteIP
}

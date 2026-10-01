package proxy

import (
	"hash/fnv"
	"math/rand/v2"
	"net"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// Balancer selects a backend from the pool.
type Balancer interface {
	Select(backends []*Backend, r *http.Request) *Backend
}

// RoundRobin implements weighted round-robin.
type RoundRobin struct {
	counter atomic.Uint64
}

// backendWeight is the weight a backend is scheduled with, treating a
// non-positive weight as 1. NewUpstreamPool already clamps, but Backend is an
// exported struct that callers and tests build directly.
func backendWeight(b *Backend) int64 {
	if b.Weight > 0 {
		return int64(b.Weight)
	}
	return 1
}

func (rr *RoundRobin) Select(backends []*Backend, _ *http.Request) *Backend {
	if len(backends) == 0 {
		return nil
	}
	// Each backend occupies Weight slots in a cycle of the total weight, so a
	// 3:1 pool gives the heavy backend three of every four requests. The
	// counter is the only shared state, so selection stays lock-free.
	//
	// `weight` used to be stored on Backend and then ignored, making it dead
	// configuration: SPECIFICATION.md documents round_robin as "Distribute in
	// order, based on weight" and README.md ships weight: 3 / weight: 1, but
	// every backend was served an equal share regardless of what was
	// configured.
	var total int64
	for _, b := range backends {
		total += backendWeight(b)
	}
	if total <= 0 {
		// Unreachable via NewUpstreamPool (weights are clamped to >= 1) and
		// unreachable with a non-empty slice, since every term is >= 1. Guard
		// anyway so a hand-built Backend can never divide by zero.
		return backends[rr.counter.Add(1)%uint64(len(backends))]
	}
	pos := int64(rr.counter.Add(1) % uint64(total))
	for _, b := range backends {
		pos -= backendWeight(b)
		if pos < 0 {
			return b
		}
	}
	// Unreachable: the slot always lands inside some backend's range. Fall back
	// to the last one rather than returning nil and 502-ing the domain.
	return backends[len(backends)-1]
}

// LeastConn selects the backend with fewest active connections.
type LeastConn struct{}

func (lc *LeastConn) Select(backends []*Backend, _ *http.Request) *Backend {
	if len(backends) == 0 {
		return nil
	}
	best := backends[0]
	bestConns := best.ActiveConns.Load()
	for _, b := range backends[1:] {
		c := b.ActiveConns.Load()
		if c < bestConns {
			best = b
			bestConns = c
		}
	}
	return best
}

// IPHash provides session affinity based on client IP.
type IPHash struct{}

func (ih *IPHash) Select(backends []*Backend, r *http.Request) *Backend {
	if len(backends) == 0 {
		return nil
	}
	h := fnv.New32a()
	clientAddr := r.RemoteAddr
	if host, _, err := net.SplitHostPort(clientAddr); err == nil {
		clientAddr = host
	}
	h.Write([]byte(clientAddr))
	idx := h.Sum32() % uint32(len(backends))
	return backends[idx]
}

// URIHash distributes by request URI for cache-friendly routing.
type URIHash struct{}

func (uh *URIHash) Select(backends []*Backend, r *http.Request) *Backend {
	if len(backends) == 0 {
		return nil
	}
	h := fnv.New32a()
	h.Write([]byte(r.URL.Path))
	idx := h.Sum32() % uint32(len(backends))
	return backends[idx]
}

// Random selects using power-of-2-choices: pick 2 random, choose least loaded.
type Random struct{}

func (rn *Random) Select(backends []*Backend, _ *http.Request) *Backend {
	n := len(backends)
	if n == 0 {
		return nil
	}
	if n == 1 {
		return backends[0]
	}
	i := rand.IntN(n)
	j := rand.IntN(n)
	if backends[i].ActiveConns.Load() <= backends[j].ActiveConns.Load() {
		return backends[i]
	}
	return backends[j]
}

// StickyBalancer provides cookie-based session affinity.
// If a client has a sticky cookie, it routes to the same backend.
// Otherwise it defers to the underlying algorithm and sets the cookie.
type StickyBalancer struct {
	CookieName string
	TTL        int // seconds
	// Fallback picks the backend for a request with no usable cookie. sticky
	// is a layer over the domain's algorithm, not a replacement for it: a
	// domain running least_conn with sticky sessions must still balance new
	// sessions by least_conn. Nil means round-robin.
	Fallback Balancer

	roundRobin RoundRobin
}

func (sb *StickyBalancer) Select(backends []*Backend, r *http.Request) *Backend {
	if len(backends) == 0 {
		return nil
	}
	// Check for existing sticky cookie
	if cookie, err := r.Cookie(sb.CookieName); err == nil && cookie.Value != "" {
		for _, b := range backends {
			if b.URL.Host == cookie.Value {
				return b
			}
		}
	}
	// No cookie, or the pinned backend is gone.
	if sb.Fallback != nil {
		return sb.Fallback.Select(backends, r)
	}
	return sb.roundRobin.Select(backends, r)
}

// SetStickyCookie sets the sticky session cookie on the response after backend selection.
func SetStickyCookie(w http.ResponseWriter, cookieName, backendHost string, ttl int, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    backendHost,
		Path:     "/",
		MaxAge:   ttl,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// NewBalancer creates a balancer by algorithm name. Accepts both dashed
// ("least-conn") and underscored ("least_conn") forms — the dashboard emits
// dashed names while internal callers historically used underscores.
func NewBalancer(algorithm string) Balancer {
	switch strings.ReplaceAll(strings.ToLower(algorithm), "-", "_") {
	case "least_conn":
		return &LeastConn{}
	case "ip_hash":
		return &IPHash{}
	case "uri_hash":
		return &URIHash{}
	case "random":
		return &Random{}
	case "sticky":
		// Historical spelling: `algorithm: sticky` with no sticky block.
		return &StickyBalancer{CookieName: DefaultStickyCookieName, TTL: DefaultStickyTTL}
	default:
		// "round_robin", "round-robin", "weighted", "" all map here. The
		// weighted variant of round-robin is built-in, so a plain RoundRobin
		// is the correct choice for "weighted" too.
		return &RoundRobin{}
	}
}

// Defaults for proxy.sticky when the domain sets the block but leaves a field
// empty. They match the values NewBalancer has always hardcoded, so a config
// that names sticky without tuning it keeps behaving as before.
const (
	DefaultStickyCookieName = "uwas_sticky"
	DefaultStickyTTL        = 3600
)

// NewBalancerFor builds the balancer for a domain's proxy block.
//
// proxy.sticky was dead configuration. The whole block — type, cookie_name,
// ttl — was documented as sitting alongside `algorithm`, but nothing read it:
// affinity was reachable only through the undocumented `algorithm: sticky`,
// and the cookie name and TTL were hardcoded to "uwas_sticky"/3600. An
// operator could set `sticky: {type: cookie, cookie_name: UWAS_UPSTREAM, ttl:
// 600}`, see the API echo it back, and get no cookie by that name at all.
//
// Affinity now layers over the configured algorithm rather than replacing it,
// which is what the documented shape means: least_conn plus sticky must still
// place new sessions by least_conn.
func NewBalancerFor(p config.ProxyConfig, log *logger.Logger) Balancer {
	base := NewBalancer(p.Algorithm)

	stickyType := strings.ToLower(strings.TrimSpace(p.Sticky.Type))
	if stickyType == "" {
		// No sticky block. `algorithm: sticky` still yields a StickyBalancer
		// from NewBalancer above; give it the configured cookie/ttl if any.
		applyStickyOptions(base, p.Sticky)
		return base
	}

	switch stickyType {
	case "cookie":
		sb := &StickyBalancer{CookieName: DefaultStickyCookieName, TTL: DefaultStickyTTL}
		// Do not nest a sticky balancer inside itself when the operator wrote
		// both `algorithm: sticky` and `sticky: {type: cookie}`.
		if _, alreadySticky := base.(*StickyBalancer); !alreadySticky {
			sb.Fallback = base
		}
		applyStickyOptions(sb, p.Sticky)
		return sb

	case "ip":
		// Affinity by client address is exactly what IPHash does.
		return &IPHash{}

	case "header":
		// Documented, but the config carries no header name to key on.
		// Say so rather than silently serving unstickied traffic.
		if log != nil {
			log.Warn("proxy.sticky.type: header is not implemented; falling back to the configured algorithm",
				"algorithm", p.Algorithm)
		}
		return base

	default:
		if log != nil {
			log.Warn("unknown proxy.sticky.type; falling back to the configured algorithm",
				"type", p.Sticky.Type, "algorithm", p.Algorithm)
		}
		return base
	}
}

// applyStickyOptions copies cookie_name/ttl onto a sticky balancer, leaving
// the defaults in place for fields the operator did not set.
func applyStickyOptions(b Balancer, s config.StickyConfig) {
	sb, ok := b.(*StickyBalancer)
	if !ok {
		return
	}
	if name := strings.TrimSpace(s.CookieName); name != "" {
		sb.CookieName = name
	}
	if s.TTL > 0 {
		sb.TTL = s.TTL
	}
}

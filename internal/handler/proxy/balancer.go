package proxy

import (
	"hash/fnv"
	"math/rand/v2"
	"net/http"
	"sync/atomic"
)

// Balancer selects a backend from the pool.
type Balancer interface {
	Select(backends []*Backend, r *http.Request) *Backend
}

// RoundRobin implements weighted smooth round-robin.
type RoundRobin struct {
	counter atomic.Uint64
}

func (rr *RoundRobin) Select(backends []*Backend, _ *http.Request) *Backend {
	if len(backends) == 0 {
		return nil
	}
	idx := rr.counter.Add(1) % uint64(len(backends))
	return backends[idx]
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
	h.Write([]byte(r.RemoteAddr))
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
// Otherwise it falls back to round-robin and sets the cookie.
type StickyBalancer struct {
	CookieName string
	TTL        int // seconds
	fallback   RoundRobin
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
	// No cookie or backend gone — fall back to round-robin
	return sb.fallback.Select(backends, r)
}

// SetStickyCookie sets the sticky session cookie on the response after backend selection.
func SetStickyCookie(w http.ResponseWriter, cookieName, backendHost string, ttl int) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    backendHost,
		Path:     "/",
		MaxAge:   ttl,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// NewBalancer creates a balancer by algorithm name.
func NewBalancer(algorithm string) Balancer {
	switch algorithm {
	case "least_conn":
		return &LeastConn{}
	case "ip_hash":
		return &IPHash{}
	case "uri_hash":
		return &URIHash{}
	case "random":
		return &Random{}
	case "sticky":
		return &StickyBalancer{CookieName: "uwas_sticky", TTL: 3600}
	default:
		return &RoundRobin{}
	}
}

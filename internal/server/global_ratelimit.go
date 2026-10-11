package server

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/uwaserver/uwas/internal/middleware"
)

// globalRateLimit is the global.rate_limit stage of the middleware chain. The
// chain is built once, so the limiter lives behind an atomic pointer that a
// reload replaces (F1991). Each limiter gets its own context so the cleanup
// goroutine of a replaced one stops instead of accumulating per reload.
type globalRateLimit struct {
	ctx context.Context
	cur atomic.Pointer[http.Handler]

	mu       sync.Mutex
	next     http.Handler
	requests int
	window   time.Duration
	cancel   context.CancelFunc
}

// set records the limit and, once the chain exists, swaps the limiter in.
func (g *globalRateLimit) set(requests int, window time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.requests, g.window = requests, window
	if g.next != nil {
		g.rebuildLocked()
	}
}

func (g *globalRateLimit) rebuildLocked() {
	parent := g.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	h := middleware.RateLimit(ctx, g.requests, g.window)(g.next)
	g.cur.Store(&h)
	if g.cancel != nil {
		g.cancel()
	}
	g.cancel = cancel
}

// middleware is the chain stage.
func (g *globalRateLimit) middleware(next http.Handler) http.Handler {
	g.mu.Lock()
	g.next = next
	g.rebuildLocked()
	g.mu.Unlock()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		(*g.cur.Load()).ServeHTTP(w, r)
	})
}

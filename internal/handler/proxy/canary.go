package proxy

import (
	"math/rand/v2"
	"net/http"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/router"
)

// CanaryRouter decides whether a request should be routed to the canary
// upstream pool or the primary pool.
type CanaryRouter struct {
	canaryPool    *UpstreamPool
	canaryBalance Balancer
	logger        *logger.Logger
}

// NewCanaryRouter creates a CanaryRouter from canary config.
func NewCanaryRouter(cfg config.CanaryConfig, algorithm string, log *logger.Logger) *CanaryRouter {
	upstreams := make([]UpstreamConfig, len(cfg.Upstreams))
	for i, u := range cfg.Upstreams {
		upstreams[i] = UpstreamConfig{
			Address: u.Address,
			Weight:  u.Weight,
		}
	}
	return &CanaryRouter{
		canaryPool:    NewUpstreamPool(upstreams),
		canaryBalance: NewBalancer(algorithm),
		logger:        log,
	}
}

// IsCanary determines whether the given request should go to the canary pool.
// It checks cookie-based stickiness first, then falls back to random weight.
func (cr *CanaryRouter) IsCanary(r *http.Request, cfg config.CanaryConfig) bool {
	if !cfg.Enabled {
		return false
	}

	cookieName := cfg.Cookie
	if cookieName == "" {
		cookieName = "X-Canary"
	}

	// Check cookie stickiness first
	if c, err := r.Cookie(cookieName); err == nil {
		return c.Value == "true"
	}

	// Random weight selection
	weight := cfg.Weight
	if weight <= 0 {
		return false
	}
	if weight >= 100 {
		return true
	}

	return rand.IntN(100) < weight
}

// Serve routes a canary request to the canary upstream pool and sets
// appropriate headers and stickiness cookie.
func (cr *CanaryRouter) Serve(ctx *router.RequestContext, domain *config.Domain, handler *Handler) {
	cfg := domain.Proxy.Canary

	cookieName := cfg.Cookie
	if cookieName == "" {
		cookieName = "X-Canary"
	}

	backends := cr.canaryPool.Healthy()
	if len(backends) == 0 {
		// Fall back to primary pool if no canary backends are healthy.
		cr.logger.Warn("no healthy canary backends, falling back to primary")
		return
	}

	// Set stickiness cookie
	http.SetCookie(ctx.Response, &http.Cookie{
		Name:     cookieName,
		Value:    "true",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	// Set canary header on response
	ctx.Response.Header().Set("X-Canary", "true")

	handler.serveWithPool(ctx, domain, cr.canaryPool, cr.canaryBalance)
}

// CanaryPool returns the canary upstream pool.
func (cr *CanaryRouter) CanaryPool() *UpstreamPool {
	return cr.canaryPool
}

// serveWithPool proxies to a specific pool (used by canary routing).
func (h *Handler) serveWithPool(ctx *router.RequestContext, domain *config.Domain, pool *UpstreamPool, balancer Balancer) {
	h.Serve(ctx, domain, pool, balancer)
}

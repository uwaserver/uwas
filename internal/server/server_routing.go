package server

import (
	"net/http"

	"github.com/uwaserver/uwas/internal/config"
	proxyhandler "github.com/uwaserver/uwas/internal/handler/proxy"
	"github.com/uwaserver/uwas/internal/middleware"
	"github.com/uwaserver/uwas/internal/rewrite"
)

func (s *Server) wafGuardFor(host string) func(http.ResponseWriter, *http.Request) bool {
	s.routeMu.RLock()
	defer s.routeMu.RUnlock()
	return s.wafGuards[host]
}

func (s *Server) ipACLGuardFor(host string) func(http.ResponseWriter, *http.Request) bool {
	s.routeMu.RLock()
	defer s.routeMu.RUnlock()
	return s.ipACLGuards[host]
}

func (s *Server) geoGuardFor(host string) func(http.ResponseWriter, *http.Request) bool {
	s.routeMu.RLock()
	defer s.routeMu.RUnlock()
	return s.geoGuards[host]
}

func (s *Server) corsGuardFor(host string) func(http.ResponseWriter, *http.Request) bool {
	s.routeMu.RLock()
	defer s.routeMu.RUnlock()
	return s.corsGuards[host]
}

func (s *Server) rateLimiterFor(host string) *middleware.RateLimiter {
	s.routeMu.RLock()
	defer s.routeMu.RUnlock()
	return s.domainRateLimiters[host]
}

func (s *Server) imageOptChainFor(host string) (middleware.Middleware, bool) {
	s.routeMu.RLock()
	defer s.routeMu.RUnlock()
	m, ok := s.imageOptChains[host]
	return m, ok
}

func (s *Server) rewriteEngineFor(host string) *rewrite.Engine {
	s.routeMu.RLock()
	defer s.routeMu.RUnlock()
	return s.rewriteCache[host]
}

func (s *Server) proxyRouteFor(host string) (*proxyhandler.UpstreamPool, proxyhandler.Balancer, *proxyhandler.CircuitBreaker, *proxyhandler.Mirror, *proxyhandler.CanaryRouter) {
	s.routeMu.RLock()
	defer s.routeMu.RUnlock()
	return s.proxyPools[host], s.proxyBalancers[host], s.proxyBreakers[host], s.proxyMirrors[host], s.proxyCanaries[host]
}

// rebuildProxyPools regenerates proxy pools, balancers, and health
// checkers from the given domains slice. Used by both reload() (after
// reading config from disk) and onDomainChange (after admin API mutates
// in-memory config, BEFORE persistConfig runs). Operates purely on the
// passed slice so it works regardless of whether the caller has
// committed to disk yet.
//
// `apps://<name>` upstreams are resolved here via resolveAppsUpstream,
// so the pool gets a concrete `http://127.0.0.1:<port>` (or the 0-port
// placeholder if the app is currently down). Re-resolution requires
// calling this method again — typically via onDomainChange or reload
// when an app state changes.
func (s *Server) rebuildProxyPools(domains []config.Domain) {
	newPools := make(map[string]*proxyhandler.UpstreamPool)
	newBalancers := make(map[string]proxyhandler.Balancer)
	newHealthChks := make(map[string]*proxyhandler.HealthChecker)
	newBreakers := make(map[string]*proxyhandler.CircuitBreaker)
	newCanaries := make(map[string]*proxyhandler.CanaryRouter)
	newMirrors := make(map[string]*proxyhandler.Mirror)

	for _, d := range domains {
		if d.Type != "proxy" || len(d.Proxy.Upstreams) == 0 {
			continue
		}
		var ups []proxyhandler.UpstreamConfig
		for _, u := range d.Proxy.Upstreams {
			ups = append(ups, proxyhandler.UpstreamConfig{
				Address: s.resolveAppsUpstream(u.Address),
				Weight:  u.Weight,
			})
		}
		newPools[d.Host] = proxyhandler.NewUpstreamPool(ups)
		newBalancers[d.Host] = proxyhandler.NewBalancer(d.Proxy.Algorithm)

		if d.Proxy.HealthCheck.Path != "" {
			hc := proxyhandler.NewHealthChecker(newPools[d.Host], proxyhandler.HealthConfig{
				Path:      d.Proxy.HealthCheck.Path,
				Interval:  d.Proxy.HealthCheck.Interval.Duration,
				Timeout:   d.Proxy.HealthCheck.Timeout.Duration,
				Threshold: d.Proxy.HealthCheck.Threshold,
				Rise:      d.Proxy.HealthCheck.Rise,
			}, s.logger)
			hc.Start(s.ctx)
			newHealthChks[d.Host] = hc
		}

		// Rebuild circuit breaker / canary / mirror too, so an admin add/remove
		// of a proxy domain doesn't leave a new domain without them or keep a
		// stale breaker after its upstreams changed.
		if d.Proxy.CircuitBreaker.Threshold > 0 {
			newBreakers[d.Host] = proxyhandler.NewCircuitBreaker(
				d.Proxy.CircuitBreaker.Threshold,
				d.Proxy.CircuitBreaker.Timeout.Duration,
			)
		}
		if d.Proxy.Canary.Enabled && len(d.Proxy.Canary.Upstreams) > 0 {
			newCanaries[d.Host] = proxyhandler.NewCanaryRouter(d.Proxy.Canary, d.Proxy.Algorithm, s.logger)
		}
		if d.Proxy.Mirror.Enabled && d.Proxy.Mirror.Backend != "" {
			newMirrors[d.Host] = proxyhandler.NewMirror(proxyhandler.MirrorConfig{
				Enabled:      d.Proxy.Mirror.Enabled,
				Backend:      d.Proxy.Mirror.Backend,
				Percent:      d.Proxy.Mirror.Percent,
				MaxBodyBytes: d.Proxy.Mirror.MaxBodyBytes,
			}, s.logger)
		}
	}

	// Swap under routeMu, then stop the old health checkers outside the lock so
	// we don't leak goroutines (and don't race two concurrent rebuilds).
	s.routeMu.Lock()
	oldHealthChks := s.proxyHealthChks
	s.proxyPools = newPools
	s.proxyBalancers = newBalancers
	s.proxyHealthChks = newHealthChks
	s.proxyBreakers = newBreakers
	s.proxyCanaries = newCanaries
	s.proxyMirrors = newMirrors
	s.routeMu.Unlock()

	for _, hc := range oldHealthChks {
		hc.Stop()
	}
}

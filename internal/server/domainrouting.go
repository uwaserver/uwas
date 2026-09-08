package server

import (
	"net/http"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/middleware"
	"github.com/uwaserver/uwas/internal/rewrite"
)

// rebuildDomainRouting rebuilds every per-domain routing map — rewrite engines,
// IP ACL / geo / CORS / WAF guards, rate limiters and image-opt chains — from
// the given domains and swaps them in under routeMu.
//
// Both the full config reload and the admin domain-CRUD callback call it. Until
// this was shared, onDomainChange updated vhosts, TLS and proxy pools but left
// these guard maps built at startup, so a per-domain security change through
// the panel — rate_limit (including turning it off), WAF, IP allow/deny, geo,
// CORS — had no effect until a full restart. The maps are all built into locals
// first, so routeMu is held only for the pointer swaps, not the O(domains)
// construction.
func (s *Server) rebuildDomainRouting(domains []config.Domain, trustedProxies []string) {
	// htaccess cache is keyed by file; drop it so a domain whose htaccess mode
	// or root changed is re-read rather than served from a stale entry.
	s.htaccessCacheMu.Lock()
	s.htaccessCache = make(map[string]*htaccessCacheEntry)
	s.htaccessCacheMu.Unlock()

	newRewriteCache := make(map[string]*rewrite.Engine)
	for _, d := range domains {
		if len(d.Rewrites) == 0 {
			continue
		}
		var cfgRewrites []rewrite.ConfigRewrite
		for _, rw := range d.Rewrites {
			cfgRewrites = append(cfgRewrites, rewrite.ConfigRewrite{
				Match: rw.Match, To: rw.To, Status: rw.Status,
				Conditions: rw.Conditions, Flags: rw.Flags,
			})
		}
		rules := rewrite.ConvertConfigRewrites(cfgRewrites)
		if len(rules) > 0 {
			newRewriteCache[d.Host] = rewrite.NewEngine(rules)
		}
	}

	newDomainChains := make(map[string]middleware.Middleware)
	newIPACLGuards := make(map[string]func(http.ResponseWriter, *http.Request) bool)
	for _, d := range domains {
		if len(d.Security.IPWhitelist) > 0 || len(d.Security.IPBlacklist) > 0 {
			cfg := middleware.IPACLConfig{
				Whitelist: d.Security.IPWhitelist,
				Blacklist: d.Security.IPBlacklist,
			}
			newDomainChains[d.Host] = middleware.IPACL(cfg)
			newIPACLGuards[d.Host] = middleware.IPACLGuard(cfg)
		}
	}

	newGeoChains := make(map[string]middleware.Middleware)
	newGeoGuards := make(map[string]func(http.ResponseWriter, *http.Request) bool)
	for _, d := range domains {
		if len(d.Security.GeoBlockCountries) > 0 || len(d.Security.GeoAllowCountries) > 0 {
			cfg := middleware.GeoIPConfig{
				BlockedCountries: d.Security.GeoBlockCountries,
				AllowedCountries: d.Security.GeoAllowCountries,
			}
			newGeoChains[d.Host] = middleware.GeoIP(cfg)
			newGeoGuards[d.Host] = middleware.GeoIPGuard(cfg)
		}
	}

	newCORSGuards := make(map[string]func(http.ResponseWriter, *http.Request) bool)
	newWAFGuards := make(map[string]func(http.ResponseWriter, *http.Request) bool)
	for _, d := range domains {
		if d.CORS.Enabled {
			newCORSGuards[d.Host] = middleware.CORSGuard(middleware.CORSConfig{
				AllowedOrigins:   d.CORS.AllowedOrigins,
				AllowedMethods:   d.CORS.AllowedMethods,
				AllowedHeaders:   d.CORS.AllowedHeaders,
				AllowCredentials: d.CORS.AllowCredentials,
				MaxAge:           d.CORS.MaxAge,
			})
		}
		if d.Security.WAF.Enabled {
			newWAFGuards[d.Host] = middleware.DomainWAFGuard(s.logger, d.Security.WAF.BypassPaths, d.Security.WAF.Rules, s.securityStats)
		}
	}

	newRateLimiters := buildDomainRateLimiters(s.ctx, domains, trustedProxies, s.logger)

	newImageOpt := make(map[string]middleware.Middleware)
	for _, d := range domains {
		if d.ImageOptimization.Enabled && d.Root != "" {
			newImageOpt[d.Host] = middleware.ImageOptimization(middleware.ImageOptConfig{
				Enabled: true,
				Formats: d.ImageOptimization.Formats,
			}, d.Root)
		}
	}

	s.routeMu.Lock()
	s.rewriteCache = newRewriteCache
	s.domainChains = newDomainChains
	s.ipACLGuards = newIPACLGuards
	s.geoChains = newGeoChains
	s.geoGuards = newGeoGuards
	s.corsGuards = newCORSGuards
	s.wafGuards = newWAFGuards
	oldRateLimiters := s.domainRateLimiters
	s.domainRateLimiters = newRateLimiters
	s.imageOptChains = newImageOpt
	s.routeMu.Unlock()

	// Stop old rate limiters' cleanup goroutines after releasing routeMu,
	// or each rebuild leaks N goroutines bound to the server-lifetime ctx.
	for _, rl := range oldRateLimiters {
		rl.Stop()
	}
}

package server

import (
	"fmt"
	"net/http"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/middleware"
	"github.com/uwaserver/uwas/internal/rewrite"
)

// reload re-reads and applies the config file.
func (s *Server) reload() error {
	if s.configPath == "" {
		return fmt.Errorf("no config path set")
	}

	newCfg, err := config.Load(s.configPath)
	if err != nil {
		return fmt.Errorf("reload config: %w", err)
	}

	// Update vhosts
	s.vhosts.Update(newCfg.Domains)

	// Update TLS domains
	s.tlsMgr.UpdateDomains(newCfg.Domains)

	// Invalidate htaccess cache (both v1 and v2)
	s.htaccessCacheMu.Lock()
	s.htaccessCache = make(map[string][]*rewrite.Rule)
	s.htaccessCacheV2 = make(map[string]*htaccessCacheEntry)
	s.htaccessCacheMu.Unlock()

	// Rebuild rewrite cache for new domains
	newRewriteCache := make(map[string]*rewrite.Engine)
	for _, d := range newCfg.Domains {
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

	// Swap all per-domain routing maps under routeMu so request goroutines
	// reading them via the *For accessors never observe a torn map header.
	s.routeMu.Lock()
	s.rewriteCache = newRewriteCache

	// Rebuild per-domain middleware chains + predicate guards
	// (refactor.md P2/P3). Two parallel forms so any composed middleware
	// path retains the chain-style entry while the hot path uses guards.
	newDomainChains := make(map[string]middleware.Middleware)
	newIPACLGuards := make(map[string]func(http.ResponseWriter, *http.Request) bool)
	for _, d := range newCfg.Domains {
		if len(d.Security.IPWhitelist) > 0 || len(d.Security.IPBlacklist) > 0 {
			cfg := middleware.IPACLConfig{
				Whitelist: d.Security.IPWhitelist,
				Blacklist: d.Security.IPBlacklist,
			}
			newDomainChains[d.Host] = middleware.IPACL(cfg)
			newIPACLGuards[d.Host] = middleware.IPACLGuard(cfg)
		}
	}
	s.domainChains = newDomainChains
	s.ipACLGuards = newIPACLGuards

	newGeoChains := make(map[string]middleware.Middleware)
	newGeoGuards := make(map[string]func(http.ResponseWriter, *http.Request) bool)
	for _, d := range newCfg.Domains {
		if len(d.Security.GeoBlockCountries) > 0 || len(d.Security.GeoAllowCountries) > 0 {
			cfg := middleware.GeoIPConfig{
				BlockedCountries: d.Security.GeoBlockCountries,
				AllowedCountries: d.Security.GeoAllowCountries,
			}
			newGeoChains[d.Host] = middleware.GeoIP(cfg)
			newGeoGuards[d.Host] = middleware.GeoIPGuard(cfg)
		}
	}
	s.geoChains = newGeoChains
	s.geoGuards = newGeoGuards

	newCORSGuards := make(map[string]func(http.ResponseWriter, *http.Request) bool)
	newWAFGuards := make(map[string]func(http.ResponseWriter, *http.Request) bool)
	for _, d := range newCfg.Domains {
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
			newWAFGuards[d.Host] = middleware.DomainWAFGuard(s.logger, d.Security.WAF.BypassPaths, s.securityStats)
		}
	}
	s.corsGuards = newCORSGuards
	s.wafGuards = newWAFGuards

	// Rebuild per-domain rate limiters. Stop the old ones' cleanup goroutines
	// before swapping the map; otherwise each reload leaks N goroutines bound
	// to s.ctx (server lifetime).
	newRateLimiters := make(map[string]*middleware.RateLimiter)
	for _, d := range newCfg.Domains {
		if d.Security.RateLimit.Requests > 0 {
			window := d.Security.RateLimit.Window.Duration
			if window == 0 {
				window = time.Minute
			}
			newRateLimiters[d.Host] = middleware.NewRateLimiter(s.ctx, d.Security.RateLimit.Requests, window)
		}
	}
	oldRateLimiters := s.domainRateLimiters
	s.domainRateLimiters = newRateLimiters

	// Rebuild image optimization chains
	newImageOpt := make(map[string]middleware.Middleware)
	for _, d := range newCfg.Domains {
		if d.ImageOptimization.Enabled && d.Root != "" {
			newImageOpt[d.Host] = middleware.ImageOptimization(middleware.ImageOptConfig{
				Enabled: true,
				Formats: d.ImageOptimization.Formats,
			}, d.Root)
		}
	}
	s.imageOptChains = newImageOpt
	s.routeMu.Unlock()

	// Stop the old rate limiters' cleanup goroutines after releasing routeMu;
	// otherwise each reload leaks N goroutines bound to s.ctx (server lifetime).
	for _, rl := range oldRateLimiters {
		rl.Stop()
	}

	// Rebuild proxy pools + balancers + health checkers against the new
	// config. Factored into rebuildProxyPools so onDomainChange (which
	// doesn't go through reload — it operates on in-memory state) can
	// reuse the same logic.
	s.rebuildProxyPools(newCfg.Domains)

	// Update bandwidth manager with new domain configs
	if s.bwMgr != nil {
		s.bwMgr.UpdateDomains(newCfg.Domains)
	}

	// Update webhook configs
	if s.webhookMgr != nil {
		s.webhookMgr.UpdateWebhooks(toWebhookConfigs(newCfg.Global.Webhooks))
	}

	// Update health monitor domains
	if s.monitor != nil {
		s.monitor.UpdateDomains(newCfg.Domains)
	}

	// Apps refresh — pick up any new YAML files in /etc/uwas/apps.d/
	// and start every enabled app that is not already running. Existing
	// running apps are left untouched; command/port changes still take
	// effect on an explicit Restart (the LoadAll contract).
	if s.appsMgr != nil {
		if _, _, err := s.appsMgr.LoadAll(); err != nil {
			s.logger.Warn("apps: reload failed", "error", err)
		} else {
			s.appsMgr.StartAll()
		}
	}

	// Update stored config IN PLACE under write lock. The admin server
	// was constructed with a *config.Config pointer that it dereferences
	// for every read; if we swapped the pointer here (s.config = newCfg)
	// admin would keep reading the stale config, which means subsequent
	// domain CRUDs through the admin API would mutate a config no other
	// subsystem references and the vhost router would never see them —
	// every request to a freshly-created domain would 421.
	s.configMu.Lock()
	*s.config = *newCfg
	s.configMu.Unlock()

	s.logger.Info("config reloaded", "domains", len(newCfg.Domains))
	return nil
}

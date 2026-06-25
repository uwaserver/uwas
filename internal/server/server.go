package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/quic-go/quic-go/http3"
	"golang.org/x/crypto/bcrypt"

	"github.com/uwaserver/uwas/internal/admin"
	"github.com/uwaserver/uwas/internal/alerting"
	"github.com/uwaserver/uwas/internal/analytics"
	"github.com/uwaserver/uwas/internal/apps"
	"github.com/uwaserver/uwas/internal/auth"
	"github.com/uwaserver/uwas/internal/backup"
	"github.com/uwaserver/uwas/internal/bandwidth"
	"github.com/uwaserver/uwas/internal/build"
	"github.com/uwaserver/uwas/internal/cache"
	cfintegration "github.com/uwaserver/uwas/internal/cloudflare"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/cronjob"
	"github.com/uwaserver/uwas/internal/database"
	"github.com/uwaserver/uwas/internal/deploy"
	"github.com/uwaserver/uwas/internal/domainroot"
	"github.com/uwaserver/uwas/internal/firewall"
	fcgihandler "github.com/uwaserver/uwas/internal/handler/fastcgi"
	proxyhandler "github.com/uwaserver/uwas/internal/handler/proxy"
	"github.com/uwaserver/uwas/internal/handler/static"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/mcp"
	"github.com/uwaserver/uwas/internal/metrics"
	"github.com/uwaserver/uwas/internal/middleware"
	"github.com/uwaserver/uwas/internal/monitor"
	"github.com/uwaserver/uwas/internal/phpmanager"
	"github.com/uwaserver/uwas/internal/rewrite"
	"github.com/uwaserver/uwas/internal/router"
	"github.com/uwaserver/uwas/internal/sftpserver"
	uwastls "github.com/uwaserver/uwas/internal/tls"
	"github.com/uwaserver/uwas/internal/webhook"
)

// Server is the main UWAS server that orchestrates all modules.
type Server struct {
	config     *config.Config
	configMu   sync.RWMutex
	configPath string
	logger     *logger.Logger
	vhosts     *router.VHostRouter
	static     *static.Handler
	php        *fcgihandler.Handler
	proxy      *proxyhandler.Handler
	tlsMgr     *uwastls.Manager
	phpMgr     *phpmanager.Manager
	cache      *cache.Engine
	metrics    *metrics.Collector
	analytics  *analytics.Collector
	admin      *admin.Server
	mcp        *mcp.Server
	monitor    *monitor.Monitor
	handler    http.Handler // compiled middleware chain
	httpSrv    *http.Server
	httpsSrv   *http.Server
	h3srv      *http3.Server
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup

	alerter     *alerting.Alerter
	backupMgr   *backup.BackupManager
	bwMgr       *bandwidth.Manager
	cronMonitor *cronjob.Monitor
	webhookMgr  *webhook.Manager
	authMgr     *auth.Manager
	sftpSrv     *sftpserver.Server
	// routeMu guards all per-domain routing maps below (proxy pools/balancers/
	// breakers/mirrors/canaries/health-checkers, rewriteCache, the *Guards, the
	// rate limiters and image-opt chains). reload()/rebuildProxyPools() swap
	// these maps while request goroutines read them; without this lock that is a
	// data race on the map header.
	routeMu         sync.RWMutex
	proxyPools      map[string]*proxyhandler.UpstreamPool
	proxyBalancers  map[string]proxyhandler.Balancer
	proxyHealthChks map[string]*proxyhandler.HealthChecker
	proxyMirrors    map[string]*proxyhandler.Mirror
	proxyBreakers   map[string]*proxyhandler.CircuitBreaker
	proxyCanaries   map[string]*proxyhandler.CanaryRouter

	unknownHosts  *router.UnknownHostTracker
	securityStats *middleware.SecurityStats
	cloudflareIPs *cfintegration.IPSet

	// htaccessCache caches parsed .htaccess rewrite rules keyed by domain root.
	// Invalidated on config reload.
	htaccessCacheMu sync.RWMutex
	htaccessCache   map[string][]*rewrite.Rule
	htaccessCacheV2 map[string]*htaccessCacheEntry

	// rewriteCache caches pre-compiled rewrite rules keyed by domain host.
	// Invalidated on config reload.
	rewriteCache map[string]*rewrite.Engine

	// connLimiter is a semaphore channel that limits concurrent connections.
	// Nil when max_connections is 0 (unlimited).
	connLimiter chan struct{}

	// domainChains holds pre-compiled per-domain IP ACL middleware.
	// Deprecated path retained only as a fallback; the hot path now uses
	// ipACLGuards / geoGuards / corsGuards predicates directly.
	domainChains map[string]middleware.Middleware
	// geoChains holds pre-compiled per-domain GeoIP middleware.
	geoChains map[string]middleware.Middleware

	// ipACLGuards / geoGuards / corsGuards / wafGuards are the
	// predicate-form (refactor.md P2/P3) of the per-domain middlewares.
	// They are called directly in handleRequest without wrapping a
	// one-shot http.Handler, saving the closure + handler allocations
	// that the old chain-with-passed-bool pattern incurred on every
	// matching request. Nil entry means "no rules for this domain".
	ipACLGuards map[string]func(http.ResponseWriter, *http.Request) bool
	geoGuards   map[string]func(http.ResponseWriter, *http.Request) bool
	corsGuards  map[string]func(http.ResponseWriter, *http.Request) bool
	wafGuards   map[string]func(http.ResponseWriter, *http.Request) bool

	// domainRateLimiters holds pre-compiled per-domain rate limiters.
	domainRateLimiters map[string]*middleware.RateLimiter

	// imageOptChains holds pre-compiled per-domain image optimization middleware.
	imageOptChains map[string]middleware.Middleware

	// domainLogs writes per-domain access log files.
	domainLogs *domainLogManager

	// esiProcessor handles Edge Side Includes fragment assembly.
	esiProcessor *cache.ESIProcessor

	// locationLimiters holds per-location rate limit counters.
	locationLimiters sync.Map

	// appsMgr supervises standalone apps (Node, Python, Ruby, Go,
	// Docker). Apps are keyed by name and persisted under
	// /etc/uwas/apps.d/<name>.yaml; domains reach them via reverse
	// proxy with `apps://<name>` upstreams.
	appsMgr *apps.Manager
}

var locationProxyHTTPClient = &http.Client{
	Timeout: 30 * time.Second,
}

// phpCacheableStaticExts is the set of file extensions whose responses are
// safe to cache on a PHP-typed domain. PHP requests themselves are always
// dynamic and bypass the cache; only requests for these static asset
// extensions enter the cache path. Lifted to package scope so we don't
// allocate a new map on every cacheable-eligible request (was P4).
var phpCacheableStaticExts = map[string]bool{
	".css":   true,
	".js":    true,
	".png":   true,
	".jpg":   true,
	".jpeg":  true,
	".gif":   true,
	".svg":   true,
	".ico":   true,
	".webp":  true,
	".avif":  true,
	".woff":  true,
	".woff2": true,
	".ttf":   true,
	".eot":   true,
	".mp4":   true,
	".webm":  true,
	".pdf":   true,
	".zip":   true,
}

// New creates a fully initialized server from config.
func New(cfg *config.Config, log *logger.Logger) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	m := metrics.New()

	// Cache engine
	var cacheEngine *cache.Engine
	if cfg.Global.Cache.Enabled {
		cacheEngine = cache.NewEngine(
			ctx,
			int64(cfg.Global.Cache.MemoryLimit),
			cfg.Global.Cache.DiskPath,
			int64(cfg.Global.Cache.DiskLimit),
			log,
		)
		cacheEngine.VaryHeaders = cfg.Global.Cache.VaryByHeaders

		// L3: Redis cache
		if cfg.Global.Cache.Redis.Enabled {
			redisCache, err := cache.NewRedisCache(cfg.Global.Cache.Redis, log)
			if err != nil {
				log.Warn("failed to initialize Redis cache", "error", err)
			} else if redisCache != nil {
				cacheEngine.SetRedis(redisCache)
				log.Info("Redis L3 cache enabled", "addr", cfg.Global.Cache.Redis.Addr)
			}
		}
	}

	// Alerting engine
	alerter := alerting.New(cfg.Global.Alerting.Enabled, cfg.Global.Alerting.WebhookURL, log)

	s := &Server{
		config:          cfg,
		logger:          log,
		vhosts:          router.NewVHostRouter(cfg.Domains),
		static:          static.New(),
		php:             fcgihandler.New(log),
		proxy:           proxyhandler.New(log),
		tlsMgr:          uwastls.NewManager(cfg.Global.ACME, cfg.Domains, log),
		cache:           cacheEngine,
		metrics:         m,
		analytics:       analytics.New(),
		alerter:         alerter,
		ctx:             ctx,
		cancel:          cancel,
		proxyPools:      make(map[string]*proxyhandler.UpstreamPool),
		proxyBalancers:  make(map[string]proxyhandler.Balancer),
		proxyHealthChks: make(map[string]*proxyhandler.HealthChecker),
		proxyMirrors:    make(map[string]*proxyhandler.Mirror),
		proxyBreakers:   make(map[string]*proxyhandler.CircuitBreaker),
		proxyCanaries:   make(map[string]*proxyhandler.CanaryRouter),
		unknownHosts:    router.NewUnknownHostTracker(),
		securityStats:   middleware.NewSecurityStats(),
		cloudflareIPs:   cfintegration.NewIPSet(),
		htaccessCache:   make(map[string][]*rewrite.Rule),
		rewriteCache:    make(map[string]*rewrite.Engine),
		domainLogs:      newDomainLogManager(),
	}

	// Pre-compile rewrite rules for each domain.
	for _, d := range cfg.Domains {
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
			s.rewriteCache[d.Host] = rewrite.NewEngine(rules)
		}
	}

	// Admin API
	if cfg.Global.Admin.Enabled {
		s.admin = admin.New(cfg, log, m)
		if cacheEngine != nil {
			s.admin.SetCache(cacheEngine)
		}
		s.admin.SetReloadFunc(s.reload)
		s.admin.SetAnalytics(s.analytics)
		s.admin.SetAlerter(alerter)

		// Initialize multi-user auth if enabled
		if cfg.Global.Users.Enabled {
			s.authMgr = auth.NewManager(cfg.Global.WebRoot, cfg.Global.Admin.APIKey)
			s.authMgr.SetAllowLegacyPlaintextKey(cfg.Global.Users.AllowLegacyPlaintextAPIKey)
			s.admin.SetAuthManager(s.authMgr)
			log.Info("multi-user auth enabled",
				"allow_reseller", cfg.Global.Users.AllowResller,
				"allow_legacy_plaintext_api_key", cfg.Global.Users.AllowLegacyPlaintextAPIKey,
			)
		}
	}

	// TLS manager + unknown host tracker + domain change callback → admin
	if s.admin != nil {
		s.admin.SetTLSManager(s.tlsMgr)
		s.admin.SetUnknownHostTracker(s.unknownHosts)
		s.admin.SetSecurityStats(s.securityStats)
		s.admin.SetOnDomainChange(func() {
			defer func() {
				if r := recover(); r != nil {
					s.logger.Error("panic in onDomainChange", "panic", r)
				}
			}()
			// Admin and server share the same *config.Config pointer,
			// so config.Domains is already updated. Sync all subsystems.
			s.configMu.RLock()
			domains := s.config.Domains
			s.configMu.RUnlock()

			s.vhosts.Update(domains)
			s.tlsMgr.UpdateDomains(domains)
			if s.bwMgr != nil {
				s.bwMgr.UpdateDomains(domains)
			}
			// Obtain certs for any new auto-SSL domains.
			go s.tlsMgr.ObtainCerts(s.ctx)

			// Rebuild proxy pools (+ circuit breakers, canaries,
			// mirrors) against the in-memory config. Operates on
			// the live domains slice — no disk roundtrip — so a
			// brand-new domain whose YAML hasn't been persisted yet
			// still gets a working pool. Without this, every
			// request to a newly-added type=proxy domain 502s
			// because the proxy handler has no upstream.
			s.rebuildProxyPools(domains)

			// Start HTTPS listener dynamically if a new SSL domain was added
			// and HTTPS isn't running yet.
			if s.httpsSrv == nil {
				for _, d := range domains {
					if d.SSL.Mode == "auto" || d.SSL.Mode == "manual" {
						s.logger.Info("SSL domain added — starting HTTPS listener")
						if err := s.startHTTPS(); err != nil {
							s.logger.Error("failed to start HTTPS", "error", err)
						}
						break
					}
				}
			}
		})
	}

	// Uptime monitor
	s.monitor = monitor.New(cfg.Domains, log)
	if s.admin != nil {
		s.admin.SetMonitor(s.monitor)
	}

	// ESI processor (requires cache engine + server as fragment fetcher)
	if cacheEngine != nil {
		s.esiProcessor = cache.NewESIProcessor(cacheEngine, s, log, 3)
	}

	// Apps — first-class objects independent of domains. Live in
	// /etc/uwas/apps.d/<name>.yaml; domains reach them via reverse
	// proxy with `apps://<name>` upstreams. The pre-v0.6 domain-keyed
	// supervisor (internal/appmanager) was removed in v0.6.0.
	s.appsMgr = apps.NewManager(nil, log)
	if loaded, skipErrs, err := s.appsMgr.LoadAll(); err != nil {
		log.Warn("apps: load failed (continuing without standalone apps)", "error", err)
	} else {
		for _, se := range skipErrs {
			log.Warn("apps: skipped invalid file", "error", se)
		}
		log.Info("apps: loaded", "count", len(loaded))
	}

	if s.admin != nil {
		s.admin.SetAppsManager(s.appsMgr)
	}

	// Deploy manager (git clone → build → restart)
	deployMgr := deploy.New(log)
	if s.admin != nil {
		s.admin.SetDeployManager(deployMgr)
	}

	// PHP Manager — detect, auto-assign to PHP domains, start all
	s.phpMgr = phpmanager.New(log)
	if configHasPHPDomains(cfg) {
		s.phpMgr.Detect()
		s.autoAssignPHP(s.phpMgr, cfg)
	}
	if s.admin != nil {
		s.admin.SetPHPManager(s.phpMgr)
	}

	// Backup manager
	if cfg.Global.Backup.Enabled {
		s.backupMgr = backup.New(cfg.Global.Backup, log)
		if s.admin != nil {
			s.admin.SetBackupManager(s.backupMgr)
		}
	}

	// Bandwidth manager
	s.bwMgr = bandwidth.NewManager(cfg.Domains)
	if s.admin != nil {
		s.admin.SetBandwidthManager(s.bwMgr)
	}

	// Cron job monitor
	s.cronMonitor = cronjob.NewMonitor(cfg.Global.WebRoot)
	if s.cronMonitor != nil && s.admin != nil {
		s.admin.SetCronMonitor(s.cronMonitor)
	}

	// Webhook manager
	s.webhookMgr = webhook.NewManager(cfg.Global.WebRoot, log)
	s.webhookMgr.UpdateWebhooks(toWebhookConfigs(cfg.Global.Webhooks))
	if s.admin != nil {
		s.admin.SetWebhookManager(s.webhookMgr)
	}

	// Wire bandwidth alerts → alerter + webhook
	s.bwMgr.SetAlertFunc(func(host, limitType string, current, limit int64) {
		s.alerter.Alert(alerting.Alert{
			Level:   "warning",
			Type:    "bandwidth_" + limitType,
			Host:    host,
			Message: fmt.Sprintf("bandwidth %s: %d/%d bytes", limitType, current, limit),
		})
	})

	// Wire cron failure alerts → alerter + webhook
	s.cronMonitor.SetAlertFunc(func(domain, command, output string, exitCode int) {
		s.alerter.Alert(alerting.Alert{
			Level:   "warning",
			Type:    "cron_failed",
			Host:    domain,
			Message: fmt.Sprintf("cron job failed: %s (exit %d)", command, exitCode),
		})
		s.webhookMgr.Fire(webhook.EventCronFailed, map[string]any{
			"domain":    domain,
			"command":   command,
			"exit_code": exitCode,
			"output":    output,
		})
	})

	// Wire TLS cert renewed → webhook
	s.tlsMgr.SetOnCertRenewed(func(host string) {
		s.webhookMgr.Fire(webhook.EventCertRenewed, map[string]any{
			"host": host,
		})
	})

	// Wire TLS cert expiry (renewal failed) → alerter + webhook
	s.tlsMgr.SetOnCertExpiry(func(host string, daysLeft int) {
		s.alerter.Alert(alerting.Alert{
			Level:   "critical",
			Type:    "cert_expiry",
			Host:    host,
			Message: fmt.Sprintf("certificate expires in %d days, renewal failed", daysLeft),
		})
		s.webhookMgr.Fire(webhook.EventCertExpiry, map[string]any{
			"host":      host,
			"days_left": daysLeft,
		})
	})

	// Wire PHP crash → alerter + webhook
	s.phpMgr.SetOnCrash(func(domain string) {
		s.alerter.Alert(alerting.Alert{
			Level:   "critical",
			Type:    "php_crashed",
			Host:    domain,
			Message: fmt.Sprintf("PHP process crashed for %s, auto-restarting", domain),
		})
		s.webhookMgr.Fire(webhook.EventPHPCrashed, map[string]any{
			"domain": domain,
		})
	})

	// Wire scheduled backup events → webhook
	if s.backupMgr != nil {
		s.backupMgr.SetOnBackup(func(info *backup.BackupInfo, err error) {
			if err != nil {
				s.webhookMgr.Fire(webhook.EventBackupFailed, map[string]any{
					"error": err.Error(),
				})
			} else if info != nil {
				s.webhookMgr.Fire(webhook.EventBackupCompleted, map[string]any{
					"name": info.Name,
					"size": info.Size,
				})
			}
		})
	}

	// MCP server
	if cfg.Global.MCP.Enabled {
		s.mcp = mcp.New(cfg, log, m)
		if cacheEngine != nil {
			s.mcp.SetCache(cacheEngine)
		}
		if s.admin != nil {
			s.admin.SetMCP(s.mcp)
		}
	}

	// Proxy pools per domain
	for _, d := range cfg.Domains {
		if d.Type != "proxy" || len(d.Proxy.Upstreams) == 0 {
			continue
		}
		var ups []proxyhandler.UpstreamConfig
		for _, u := range d.Proxy.Upstreams {
			ups = append(ups, proxyhandler.UpstreamConfig{Address: s.resolveAppsUpstream(u.Address), Weight: u.Weight})
		}
		s.proxyPools[d.Host] = proxyhandler.NewUpstreamPool(ups)
		s.proxyBalancers[d.Host] = proxyhandler.NewBalancer(d.Proxy.Algorithm)

		if d.Proxy.HealthCheck.Path != "" {
			hc := proxyhandler.NewHealthChecker(s.proxyPools[d.Host], proxyhandler.HealthConfig{
				Path:      d.Proxy.HealthCheck.Path,
				Interval:  d.Proxy.HealthCheck.Interval.Duration,
				Timeout:   d.Proxy.HealthCheck.Timeout.Duration,
				Threshold: d.Proxy.HealthCheck.Threshold,
				Rise:      d.Proxy.HealthCheck.Rise,
			}, log)
			hc.Start(ctx)
			s.proxyHealthChks[d.Host] = hc
		}

		// Circuit breaker
		if d.Proxy.CircuitBreaker.Threshold > 0 {
			s.proxyBreakers[d.Host] = proxyhandler.NewCircuitBreaker(
				d.Proxy.CircuitBreaker.Threshold,
				d.Proxy.CircuitBreaker.Timeout.Duration,
			)
		}

		// Canary routing
		if d.Proxy.Canary.Enabled && len(d.Proxy.Canary.Upstreams) > 0 {
			s.proxyCanaries[d.Host] = proxyhandler.NewCanaryRouter(d.Proxy.Canary, d.Proxy.Algorithm, log)
		}

		// Request mirroring
		if d.Proxy.Mirror.Enabled && d.Proxy.Mirror.Backend != "" {
			s.proxyMirrors[d.Host] = proxyhandler.NewMirror(proxyhandler.MirrorConfig{
				Enabled:      d.Proxy.Mirror.Enabled,
				Backend:      d.Proxy.Mirror.Backend,
				Percent:      d.Proxy.Mirror.Percent,
				MaxBodyBytes: d.Proxy.Mirror.MaxBodyBytes,
			}, log)
		}
	}

	// Per-domain IP ACL: precompile both the legacy chain form (used by
	// any out-of-band middleware composition) and the predicate-form
	// guard the hot path uses (refactor.md P2/P3).
	s.domainChains = make(map[string]middleware.Middleware)
	s.ipACLGuards = make(map[string]func(http.ResponseWriter, *http.Request) bool)
	for _, d := range cfg.Domains {
		if len(d.Security.IPWhitelist) > 0 || len(d.Security.IPBlacklist) > 0 {
			cfg := middleware.IPACLConfig{
				Whitelist: d.Security.IPWhitelist,
				Blacklist: d.Security.IPBlacklist,
			}
			s.domainChains[d.Host] = middleware.IPACL(cfg)
			s.ipACLGuards[d.Host] = middleware.IPACLGuard(cfg)
		}
	}

	// Per-domain GeoIP, same precompile-both-forms pattern.
	s.geoChains = make(map[string]middleware.Middleware)
	s.geoGuards = make(map[string]func(http.ResponseWriter, *http.Request) bool)
	for _, d := range cfg.Domains {
		if len(d.Security.GeoBlockCountries) > 0 || len(d.Security.GeoAllowCountries) > 0 {
			cfg := middleware.GeoIPConfig{
				BlockedCountries: d.Security.GeoBlockCountries,
				AllowedCountries: d.Security.GeoAllowCountries,
			}
			s.geoChains[d.Host] = middleware.GeoIP(cfg)
			s.geoGuards[d.Host] = middleware.GeoIPGuard(cfg)
		}
	}

	// Per-domain CORS + WAF guards. These were rebuilt per-request in
	// handleRequest; precompile here so we only pay the construction
	// cost once per config load (refactor.md P2).
	s.corsGuards = make(map[string]func(http.ResponseWriter, *http.Request) bool)
	s.wafGuards = make(map[string]func(http.ResponseWriter, *http.Request) bool)
	for _, d := range cfg.Domains {
		if d.CORS.Enabled {
			s.corsGuards[d.Host] = middleware.CORSGuard(middleware.CORSConfig{
				AllowedOrigins:   d.CORS.AllowedOrigins,
				AllowedMethods:   d.CORS.AllowedMethods,
				AllowedHeaders:   d.CORS.AllowedHeaders,
				AllowCredentials: d.CORS.AllowCredentials,
				MaxAge:           d.CORS.MaxAge,
			})
		}
		if d.Security.WAF.Enabled {
			s.wafGuards[d.Host] = middleware.DomainWAFGuard(log, d.Security.WAF.BypassPaths, s.securityStats)
		}
	}

	// Per-domain rate limiters
	s.domainRateLimiters = make(map[string]*middleware.RateLimiter)
	for _, d := range cfg.Domains {
		if d.Security.RateLimit.Requests > 0 {
			window := d.Security.RateLimit.Window.Duration
			if window == 0 {
				window = time.Minute
			}
			rl := middleware.NewRateLimiter(ctx, d.Security.RateLimit.Requests, window)
			rl.SetTrustedProxies(s.config.Global.TrustedProxies)
			s.domainRateLimiters[d.Host] = rl
		}
	}

	// Per-domain image optimization
	s.imageOptChains = make(map[string]middleware.Middleware)
	for _, d := range cfg.Domains {
		if d.ImageOptimization.Enabled && d.Root != "" {
			s.imageOptChains[d.Host] = middleware.ImageOptimization(middleware.ImageOptConfig{
				Enabled: true,
				Formats: d.ImageOptimization.Formats,
			}, d.Root)
		}
	}

	// Connection limiter (semaphore-based)
	if cfg.Global.MaxConnections > 0 {
		s.connLimiter = make(chan struct{}, cfg.Global.MaxConnections)
	}

	// Build middleware chain with all middleware
	s.handler = s.buildMiddlewareChain()

	return s
}

// SetConfigPath stores the config file path for reload support and config editor.
func (s *Server) SetConfigPath(path string) {
	s.configPath = path

	// Persist blocked unknown domains alongside config
	blockedPath := filepath.Join(filepath.Dir(path), "blocked-hosts.txt")
	s.unknownHosts.SetPersistPath(blockedPath)

	if s.admin != nil {
		s.admin.SetConfigPath(path)
	}
	if s.backupMgr != nil {
		certsDir := s.config.Global.ACME.Storage
		s.backupMgr.SetPaths(path, certsDir)

		// Set domain content paths for full backup (web files + databases)
		domainsDir := s.config.DomainsDir
		if domainsDir != "" && !filepath.IsAbs(domainsDir) {
			domainsDir = filepath.Join(filepath.Dir(path), domainsDir)
		}
		var roots []string
		for _, d := range s.config.Domains {
			if d.Root != "" {
				roots = append(roots, d.Root)
			}
		}
		s.backupMgr.SetDomainPaths(s.config.Global.WebRoot, domainsDir, roots)

		// Wire Docker DB dump into backup
		backup.SetDockerDumpFunc(func() map[string][]byte {
			containers, err := database.ListDockerDBs()
			if err != nil {
				return nil
			}
			dumps := make(map[string][]byte)
			for _, c := range containers {
				if !c.Running || c.Engine == database.EnginePostgreSQL {
					continue
				}
				shortName := strings.TrimPrefix(c.Name, "uwas-db-")
				dump, err := database.DockerDBExport(shortName, "--all-databases")
				if err != nil {
					s.logger.Warn("backup: docker dump failed", "container", c.Name, "error", err)
					continue
				}
				dumps[shortName] = []byte(dump)
			}
			return dumps
		})
	}
}

func (s *Server) buildMiddlewareChain() http.Handler {
	mws := []middleware.Middleware{
		middleware.Recovery(s.logger),
		middleware.RequestID(),
		middleware.RealIP(s.realIPTrustedProxies()),
		middleware.SecurityHeaders(),
		middleware.Gzip(1024), // compress responses > 1KB
	}

	// Global rate limiting (fallback for unknown domains and admin API)
	if s.config.Global.RateLimit.Requests > 0 {
		mws = append(mws, middleware.RateLimit(
			s.ctx,
			s.config.Global.RateLimit.Requests,
			s.config.Global.RateLimit.Window.Duration,
		))
	}

	// Security guard (blocked paths only) + bot guard
	var blockedPaths []string
	for _, d := range s.config.Domains {
		blockedPaths = append(blockedPaths, d.Security.BlockedPaths...)
	}
	mws = append(mws, middleware.SecurityGuard(s.logger, blockedPaths, s.securityStats))
	mws = append(mws, middleware.BotGuard(s.logger, s.securityStats))

	mws = append(mws, middleware.AccessLog(s.logger))

	chain := middleware.Chain(mws...)
	return chain(http.HandlerFunc(s.handleRequest))
}

func (s *Server) realIPTrustedProxies() []string {
	if s == nil || s.config == nil {
		return nil
	}
	trusted := append([]string(nil), s.config.Global.TrustedProxies...)
	trusted = append(trusted, s.config.Global.Cloudflare.IPRanges...)
	return trusted
}

// Start starts all listeners and blocks until shutdown.
func (s *Server) Start() error {
	workers := runtime.NumCPU()
	if s.config.Global.WorkerCount != "auto" {
		if n, err := strconv.Atoi(s.config.Global.WorkerCount); err == nil && n > 0 {
			workers = n
		} else {
			s.logger.Warn("invalid worker_count, falling back to NumCPU",
				"value", s.config.Global.WorkerCount,
				"fallback", workers,
			)
		}
	}
	runtime.GOMAXPROCS(workers)

	// Apply sane timeout defaults to prevent resource exhaustion
	s.applyTimeoutDefaults()

	if err := s.writePID(); err != nil {
		s.logger.Warn("failed to write pid file", "error", err)
	}

	// Start every registered app from /etc/uwas/apps.d/.
	if s.appsMgr != nil {
		s.appsMgr.StartAll()
	}

	s.tlsMgr.AllowSelfSigned = true
	s.tlsMgr.LoadExistingCerts()
	s.tlsMgr.LoadManualCerts()

	// Start HTTPS if any domain has SSL or HTTPS listen is explicitly configured.
	hasSSL := false
	for _, d := range s.config.Domains {
		if d.SSL.Mode == "auto" || d.SSL.Mode == "manual" {
			hasSSL = true
			break
		}
	}

	s.logger.Info("starting UWAS",
		"version", build.Version,
		"http", s.config.Global.HTTPListen,
		"https", s.config.Global.HTTPSListen,
		"workers", workers,
		"domains", len(s.config.Domains),
		"tls", hasSSL,
		"cache", s.cache != nil,
	)

	// Signal handling
	s.wg.Add(1)
	go s.handleSignals()

	// HTTP listener
	if err := s.startHTTP(); err != nil {
		return err
	}

	// HTTPS listener
	if hasSSL {
		if err := s.startHTTPS(); err != nil {
			return err
		}
		s.logger.SafeGo("tls.obtain", func() { s.tlsMgr.ObtainCerts(s.ctx) })
		s.tlsMgr.StartRenewal(s.ctx)

		// HTTP/3 (QUIC) listener on same port
		if s.config.Global.HTTP3Enabled {
			if err := s.startHTTP3(); err != nil {
				s.logger.Warn("http/3 start failed", "error", err)
			}
		}
	}

	// Uptime monitor
	if s.monitor != nil {
		s.logger.SafeGo("monitor", func() { s.monitor.Start(s.ctx) })
	}

	// Backup scheduler
	if s.backupMgr != nil {
		if cron := s.config.Global.Backup.Cron; cron != "" {
			s.backupMgr.ScheduleBackupCron(cron)
			s.logger.Info("backup scheduler started", "cron", cron)
		} else if sched := s.config.Global.Backup.Schedule; sched != "" {
			if d, err := time.ParseDuration(sched); err == nil && d > 0 {
				s.backupMgr.ScheduleBackup(d)
				s.logger.Info("backup scheduler started", "interval", d)
			}
		}
	}

	// Built-in SFTP server
	if s.config.Global.SFTPListen != "" {
		users := make(map[string]sftpserver.User)
		apiKey := s.config.Global.Admin.APIKey
		var appStore *apps.Store
		if s.appsMgr != nil {
			appStore = s.appsMgr.Store()
		}
		for _, d := range s.config.Domains {
			root, err := domainroot.ForDomain(d, appStore)
			if err != nil {
				s.logger.Warn("SFTP domain root unavailable", "domain", d.Host, "error", err)
				continue
			}
			if root != "" {
				// Create an SFTP user per domain with a unique password
				// derived from API key + domain (so compromising one doesn't
				// expose all domains).
				domainPass := deriveSFTPPassword(apiKey, d.Host)
				passHash, err := bcrypt.GenerateFromPassword([]byte(domainPass), bcrypt.DefaultCost)
				if err != nil {
					s.logger.Warn("failed to hash SFTP password", "domain", d.Host, "error", err)
					continue
				}
				users[d.Host] = sftpserver.User{
					Password: string(passHash),
					Root:     root,
				}
			}
		}
		s.sftpSrv = sftpserver.New(sftpserver.Config{
			Listen: s.config.Global.SFTPListen,
			Users:  users,
		}, s.logger)
		if err := s.sftpSrv.Start(); err != nil {
			s.logger.Warn("SFTP server start failed", "error", err)
		}
	}

	// Protect admin port from firewall deny
	firewall.SetAdminPort(s.config.Global.Admin.Listen)

	// Admin API
	if s.admin != nil {
		go func() {
			if err := s.admin.Start(); err != nil && err != http.ErrServerClosed {
				s.logger.Error("admin API error", "error", err)
			}
		}()
	}

	// Start log rotation cleanup
	s.domainLogs.StartCleanup()

	<-s.ctx.Done()
	s.shutdown()
	s.wg.Wait()
	s.removePID()
	s.logger.Info("UWAS stopped")
	return nil
}

func (s *Server) startHTTP() error {
	addr := s.config.Global.HTTPListen

	s.httpSrv = &http.Server{
		Handler:           http.HandlerFunc(s.handleHTTP),
		ReadTimeout:       s.config.Global.Timeouts.Read.Duration,
		ReadHeaderTimeout: s.config.Global.Timeouts.ReadHeader.Duration,
		WriteTimeout:      s.config.Global.Timeouts.Write.Duration,
		IdleTimeout:       s.config.Global.Timeouts.Idle.Duration,
		MaxHeaderBytes:    s.config.Global.Timeouts.MaxHeaderBytes,
		ErrorLog:          s.logger.StdLogger(),
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}

	s.logger.Info("listening", "address", addr, "protocol", "HTTP")

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if err := s.httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
			s.logger.Error("http serve error", "error", err)
		}
	}()

	return nil
}

func (s *Server) startHTTPS() error {
	addr := s.config.Global.HTTPSListen
	tlsCfg := s.tlsMgr.TLSConfig()

	s.httpsSrv = &http.Server{
		Handler:           s.handler,
		TLSConfig:         tlsCfg,
		ReadTimeout:       s.config.Global.Timeouts.Read.Duration,
		ReadHeaderTimeout: s.config.Global.Timeouts.ReadHeader.Duration,
		WriteTimeout:      s.config.Global.Timeouts.Write.Duration,
		IdleTimeout:       s.config.Global.Timeouts.Idle.Duration,
		MaxHeaderBytes:    s.config.Global.Timeouts.MaxHeaderBytes,
		ErrorLog:          s.logger.StdLogger(),
	}

	tcpLn, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}

	// Wrap with PROXY protocol if enabled (HAProxy, Cloudflare, etc.)
	var ln net.Listener = tcpLn
	if s.config.Global.ProxyProtocol {
		ln = newProxyProtoListener(ln)
	}
	ln = tls.NewListener(ln, tlsCfg)

	s.logger.Info("listening", "address", addr, "protocol", "HTTPS")

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if err := s.httpsSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
			s.logger.Error("https serve error", "error", err)
		}
	}()

	return nil
}

// handleHTTP handles port 80: ACME challenges, HTTPS redirect, or serve non-SSL domains.
// Unknown/blocked domains are rejected immediately — they never touch the middleware chain.
// Wrapped in recovery to prevent panics from crashing the connection handler.
func (s *Server) handleHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if rec := recover(); rec != nil {
			s.logger.Error("panic recovered in handleHTTP", "error", rec, "path", r.URL.Path)
			http.Error(w, "500 Internal Server Error", http.StatusInternalServerError)
		}
	}()

	// ACME challenges must pass through (for cert issuance).
	if s.tlsMgr.HandleHTTPChallenge(w, r) {
		return
	}

	// Unknown domains: track + reject if host is not a configured domain.
	// Fallback domain exists for serving configured domains, not for tracking unknown ones.
	domain, configured := s.vhosts.LookupWithStatus(r.Host)
	if !configured {
		// Host does not match any configured domain — record as unknown and reject.
		blocked := s.unknownHosts.Record(r.Host)
		if blocked {
			w.Header().Set("Connection", "close")
			http.Error(w, "403 Forbidden", http.StatusForbidden)
		} else {
			renderErrorPage(w, 421)
		}
		return
	}
	if s.rejectNonCloudflareOrigin(w, r, domain) {
		return
	}
	// Only redirect to HTTPS automatically when a usable certificate is loaded.
	// For auto-SSL domains whose ACME issuance is still pending (new domain,
	// DNS not yet pointed, rate-limited, etc.), redirecting blindly produces
	// an unrecoverable TLS handshake error. Falling through to plain HTTP
	// keeps the site reachable until the cert is obtained, after which the
	// redirect path kicks in automatically on the next request. Force SSL is
	// the operator's explicit override for domains that must always redirect.
	sslEnabled := domain.SSL.Mode == "auto" || domain.SSL.Mode == "manual"
	if sslEnabled && (domain.SSL.ForceSSL || s.tlsMgr.HasCert(r.Host)) {
		target := "https://" + r.Host + r.URL.RequestURI()
		w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		http.Redirect(w, r, target, http.StatusMovedPermanently)
		return
	}

	// Non-SSL configured domain, or SSL configured but cert not yet loaded —
	// serve over plain HTTP so the upstream/static handler can respond.
	s.handler.ServeHTTP(w, r)
}

func (s *Server) rejectNonCloudflareOrigin(w http.ResponseWriter, r *http.Request, domain *config.Domain) bool {
	if domain == nil || !domain.Security.CloudflareOnly {
		return false
	}
	// Snapshot the Cloudflare ranges under configMu — reload() overwrites the
	// whole *s.config struct under the write lock, so reading the slice header
	// here without the read lock is a data race.
	s.configMu.RLock()
	cfRanges := s.config.Global.Cloudflare.IPRanges
	s.configMu.RUnlock()
	originIP := directPeerIP(r)
	allowed := originIP != "" && s.cloudflareIPs.Contains(originIP, cfRanges)
	if allowed {
		return false
	}
	clientIP := r.RemoteAddr
	if host, _, err := net.SplitHostPort(clientIP); err == nil {
		clientIP = host
	}
	reason := "cloudflare_only"
	if len(cfRanges) == 0 {
		reason = "cloudflare_only_no_ranges"
	}
	if s.securityStats != nil {
		s.securityStats.Record(clientIP, r.URL.RequestURI(), reason, r.UserAgent())
	}
	s.logger.Warn("blocked non-Cloudflare origin request",
		"host", r.Host,
		"domain", domain.Host,
		"origin_ip", originIP,
		"client_ip", clientIP,
		"path", r.URL.RequestURI(),
		"user_agent", r.UserAgent(),
	)
	w.Header().Set("Connection", "close")
	renderErrorPage(w, 421)
	return true
}

func directPeerIP(r *http.Request) string {
	if ip := strings.TrimSpace(middleware.DirectIP(r)); ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

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

func (s *Server) handleSignals() {
	defer s.wg.Done()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	for {
		select {
		case sig := <-sigCh:
			switch sig {
			case syscall.SIGHUP:
				s.logger.Info("received SIGHUP, reloading config")
				if err := s.reload(); err != nil {
					s.logger.Error("config reload failed", "error", err)
				}
			case syscall.SIGINT, syscall.SIGTERM:
				s.logger.Info("received signal, shutting down", "signal", sig)
				s.cancel()
				return
			}
		case <-s.ctx.Done():
			return
		}
	}
}

func (s *Server) shutdown() {
	grace := s.config.Global.Timeouts.ShutdownGrace.Duration
	s.logger.Info("shutting down gracefully...", "grace_period", grace)

	ctx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()

	// Drain in-flight requests on HTTP and HTTPS listeners.
	if s.httpSrv != nil {
		if err := s.httpSrv.Shutdown(ctx); err != nil {
			s.logger.Warn("http shutdown error", "error", err)
		}
	}
	if s.httpsSrv != nil {
		if err := s.httpsSrv.Shutdown(ctx); err != nil {
			s.logger.Warn("https shutdown error", "error", err)
		}
	}

	// HTTP/3 (QUIC) graceful shutdown: quic-go http3.Server doesn't have a
	// Shutdown() method, so we close it immediately. This is acceptable since
	// QUIC connections are established quickly and browsers retry on TCP.
	if s.h3srv != nil {
		s.h3srv.Close()
	}

	// Gracefully shut down the admin API server.
	if s.admin != nil {
		if srv := s.admin.HTTPServer(); srv != nil {
			if err := srv.Shutdown(ctx); err != nil {
				s.logger.Warn("admin shutdown error", "error", err)
			}
		}
		s.admin.Close()
	}

	// Stop all standalone app processes (Node.js, Python, Docker, etc.)
	if s.appsMgr != nil {
		s.appsMgr.StopAll()
		s.logger.Info("all app processes stopped")
	}

	// Stop all PHP processes.
	if s.phpMgr != nil {
		for _, inst := range s.phpMgr.GetDomainInstances() {
			if inst.Running {
				if err := s.phpMgr.StopDomain(inst.Domain); err != nil {
					s.logger.Warn("failed to stop PHP", "domain", inst.Domain, "error", err)
				}
			}
		}
		s.logger.Info("all PHP processes stopped")
	}

	// Close per-domain log files.
	if s.domainLogs != nil {
		s.domainLogs.Close()
	}

	// Stop webhook manager.
	if s.webhookMgr != nil {
		s.webhookMgr.Close()
	}

	// Stop the backup scheduler if running.
	if s.backupMgr != nil {
		s.backupMgr.Stop()
	}

	// Stop built-in SFTP server.
	if s.sftpSrv != nil {
		s.sftpSrv.Shutdown()
	}

	s.logger.Info("shutdown complete")
}

// autoAssignPHP assigns PHP to all PHP-type domains on server startup.
// Uses system php-fpm socket if available (shared), otherwise starts per-domain processes.
// FetchFragment makes an internal sub-request for an ESI fragment.
// Implements cache.ESIFragmentFetcher.
func (s *Server) FetchFragment(host, path string, parentReq *http.Request) ([]byte, int, http.Header, error) {
	req, _ := http.NewRequestWithContext(parentReq.Context(), "GET", path, nil)
	req.Host = host
	req.URL.Path = path
	req.Header.Set("Accept", "text/html, */*")
	req.Header.Set("Cookie", parentReq.Header.Get("Cookie"))
	req.Header.Set("X-ESI-Subrequest", "true")

	domain := s.vhosts.Lookup(host)
	if domain == nil {
		return nil, 0, nil, fmt.Errorf("ESI: domain not found: %s", host)
	}

	rec := httptest.NewRecorder()
	ctx := router.AcquireContext(rec, req)
	defer router.ReleaseContext(ctx)
	ctx.VHostName = domain.Host
	ctx.DocumentRoot = domain.Root

	if domain.Type == "redirect" {
		return nil, 0, nil, fmt.Errorf("ESI: unsupported type: %s", domain.Type)
	}
	s.dispatchHandler(ctx, domain)

	result := rec.Result()
	body, _ := io.ReadAll(result.Body)
	result.Body.Close()
	return body, result.StatusCode, result.Header, nil
}

func (s *Server) autoAssignPHP(phpMgr *phpmanager.Manager, cfg *config.Config) {
	status := phpMgr.Status()
	if len(status) == 0 {
		s.logger.Warn("no PHP-CGI/FPM detected — PHP sites will not work")
		return
	}

	defaultVer := status[0].Version

	// Assign all PHP-type domains.
	for _, d := range cfg.Domains {
		if d.Type != "php" {
			continue
		}

		// If domain already has a working FPM address (from config file), register it
		if d.PHP.FPMAddress != "" && isAddrReachable(d.PHP.FPMAddress) {
			// Register in phpMgr so it shows in PHP page domain list
			phpMgr.RegisterExistingDomain(d.Host, defaultVer, d.PHP.FPMAddress, d.Root, d.PHP.ConfigOverrides)
			s.logger.Info("using configured PHP address", "domain", d.Host, "address", d.PHP.FPMAddress)
			continue
		}
		if d.PHP.FPMAddress != "" {
			s.logger.Warn("configured PHP address unreachable, re-assigning", "domain", d.Host, "address", d.PHP.FPMAddress)
		}

		inst, err := phpMgr.AssignDomain(d.Host, defaultVer)
		if err != nil {
			continue // already assigned
		}
		if err := phpMgr.StartDomain(d.Host); err != nil {
			s.logger.Warn("PHP auto-start failed", "domain", d.Host, "error", err)
			continue
		}
		// Sync FPM address in config.
		for i := range cfg.Domains {
			if cfg.Domains[i].Host == d.Host {
				cfg.Domains[i].PHP.FPMAddress = inst.ListenAddr
				break
			}
		}
		s.logger.Info("PHP assigned to domain", "domain", d.Host, "version", defaultVer, "listen", inst.ListenAddr)
	}
}

func configHasPHPDomains(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	for _, d := range cfg.Domains {
		if d.Type == "php" {
			return true
		}
	}
	return false
}

// isAddrReachable checks if an FPM address (tcp or unix socket) is reachable.
func isAddrReachable(addr string) bool {
	network := "tcp"
	dialAddr := addr
	if strings.HasPrefix(addr, "unix:") {
		network = "unix"
		dialAddr = addr[5:]
	} else if strings.HasPrefix(addr, "/") {
		network = "unix"
		dialAddr = addr
	}
	conn, err := net.DialTimeout(network, dialAddr, 2*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// applyTimeoutDefaults sets sane timeout defaults to prevent resource exhaustion.
// These only apply when the user hasn't configured explicit values.
func (s *Server) applyTimeoutDefaults() {
	t := &s.config.Global.Timeouts
	if t.Read.Duration == 0 {
		t.Read.Duration = 30 * time.Second
	}
	if t.ReadHeader.Duration == 0 {
		t.ReadHeader.Duration = 10 * time.Second
	}
	if t.Write.Duration == 0 {
		t.Write.Duration = 120 * time.Second // PHP can be slow
	}
	if t.Idle.Duration == 0 {
		t.Idle.Duration = 120 * time.Second
	}
	if t.ShutdownGrace.Duration == 0 {
		t.ShutdownGrace.Duration = 15 * time.Second
	}
	if t.MaxHeaderBytes == 0 {
		t.MaxHeaderBytes = 1 << 20 // 1MB
	}
}

func (s *Server) writePID() error {
	pidFile := s.config.Global.PIDFile
	if pidFile == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(pidFile), 0755); err != nil {
		return err
	}
	return os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0644)
}

func (s *Server) removePID() {
	if s.config.Global.PIDFile != "" {
		os.Remove(s.config.Global.PIDFile)
	}
}

// regexCache caches compiled regex patterns to avoid recompilation on every request.
var regexCache sync.Map

// matchPath checks if a URL path matches a regex pattern from cache rules.
func matchPath(path, pattern string) bool {
	var re *regexp.Regexp
	if v, ok := regexCache.Load(pattern); ok {
		re = v.(*regexp.Regexp)
	} else {
		var err error
		re, err = regexp.Compile(pattern)
		if err != nil {
			return false
		}
		regexCache.Store(pattern, re)
	}
	return re.MatchString(path)
}

// toWebhookConfigs converts config.WebhookConfig to webhook.WebhookConfig.
func toWebhookConfigs(cfgs []config.WebhookConfig) []webhook.WebhookConfig {
	result := make([]webhook.WebhookConfig, len(cfgs))
	for i, cfg := range cfgs {
		events := make([]webhook.EventType, len(cfg.Events))
		for j, e := range cfg.Events {
			events[j] = webhook.EventType(e)
		}
		result[i] = webhook.WebhookConfig{
			URL:      cfg.URL,
			Events:   events,
			Headers:  cfg.Headers,
			Secret:   cfg.Secret,
			RetryMax: cfg.Retry,
			Timeout:  cfg.Timeout.Duration,
			Enabled:  cfg.Enabled,
		}
	}
	return result
}

// deriveSFTPPassword creates a unique per-domain SFTP password from the
// API key and domain name using HMAC-SHA256. This ensures that compromising
// one domain's SFTP password doesn't expose others.
func deriveSFTPPassword(apiKey, domain string) string {
	mac := hmac.New(sha256.New, []byte(apiKey))
	mac.Write([]byte("sftp:" + domain))
	return hex.EncodeToString(mac.Sum(nil))[:32]
}

// rateLimitEntry tracks per-location rate limit state.
type rateLimitEntry struct {
	mu          sync.Mutex
	windowStart time.Time
	count       int64
	lastAccess  time.Time
}

// matchLocation checks if a URL path matches a location pattern.
// Prefix match: "/api/" matches "/api/users"
// Regex match (prefix ~): "~\\.php$" matches "/index.php"
func matchLocation(path, pattern string) bool {
	if strings.HasPrefix(pattern, "~") {
		// Regex match — use cache to avoid recompiling on every request
		regexStr := strings.TrimSpace(pattern[1:])
		var re *regexp.Regexp
		if v, ok := regexCache.Load(regexStr); ok {
			re = v.(*regexp.Regexp)
		} else {
			var err error
			re, err = regexp.Compile(regexStr)
			if err != nil {
				return false
			}
			regexCache.Store(regexStr, re)
		}
		return re.MatchString(path)
	}
	// Prefix match
	return strings.HasPrefix(path, pattern)
}

func enforceBasicAuth(w http.ResponseWriter, r *http.Request, host string, cfg config.BasicAuthConfig) bool {
	if !cfg.Enabled || len(cfg.Users) == 0 {
		return true
	}

	passed := false
	realm := cfg.Realm
	if realm == "" {
		realm = host
	}

	middleware.BasicAuth(cfg.Users, realm)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		passed = true
	})).ServeHTTP(w, r)

	return passed
}

package server

import (
	"bytes"
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

	"github.com/uwaserver/uwas/internal/admin"
	"github.com/uwaserver/uwas/internal/alerting"
	"github.com/uwaserver/uwas/internal/analytics"
	"github.com/uwaserver/uwas/internal/appmanager"
	"github.com/uwaserver/uwas/internal/auth"
	"github.com/uwaserver/uwas/internal/backup"
	"github.com/uwaserver/uwas/internal/bandwidth"
	"github.com/uwaserver/uwas/internal/build"
	"github.com/uwaserver/uwas/internal/cache"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/cronjob"
	"github.com/uwaserver/uwas/internal/database"
	"github.com/uwaserver/uwas/internal/deploy"
	"github.com/uwaserver/uwas/internal/firewall"
	fcgihandler "github.com/uwaserver/uwas/internal/handler/fastcgi"
	proxyhandler "github.com/uwaserver/uwas/internal/handler/proxy"
	"github.com/uwaserver/uwas/internal/handler/static"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/mcp"
	"github.com/uwaserver/uwas/internal/metrics"
	"github.com/uwaserver/uwas/internal/middleware"
	"github.com/uwaserver/uwas/internal/monitor"
	"github.com/uwaserver/uwas/internal/pathsafe"
	"github.com/uwaserver/uwas/internal/phpmanager"
	"github.com/uwaserver/uwas/internal/rewrite"
	"github.com/uwaserver/uwas/internal/rlimit"
	"github.com/uwaserver/uwas/internal/router"
	"github.com/uwaserver/uwas/internal/sftpserver"
	uwastls "github.com/uwaserver/uwas/internal/tls"
	"github.com/uwaserver/uwas/internal/webhook"
	"github.com/uwaserver/uwas/pkg/htaccess"
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

	alerter        *alerting.Alerter
	backupMgr      *backup.BackupManager
	bwMgr          *bandwidth.Manager
	cronMonitor    *cronjob.Monitor
	webhookMgr     *webhook.Manager
	authMgr        *auth.Manager
	sftpSrv        *sftpserver.Server
	proxyPools     map[string]*proxyhandler.UpstreamPool
	proxyBalancers map[string]proxyhandler.Balancer
	proxyMirrors   map[string]*proxyhandler.Mirror
	proxyBreakers  map[string]*proxyhandler.CircuitBreaker
	proxyCanaries  map[string]*proxyhandler.CanaryRouter

	unknownHosts  *router.UnknownHostTracker
	securityStats *middleware.SecurityStats

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
	domainChains map[string]middleware.Middleware
	// geoChains holds pre-compiled per-domain GeoIP middleware.
	geoChains map[string]middleware.Middleware

	// domainRateLimiters holds pre-compiled per-domain rate limiters.
	domainRateLimiters map[string]*middleware.RateLimiter

	// imageOptChains holds pre-compiled per-domain image optimization middleware.
	imageOptChains map[string]middleware.Middleware

	// domainLogs writes per-domain access log files.
	domainLogs *domainLogManager

	// esiProcessor handles Edge Side Includes fragment assembly.
	esiProcessor *cache.ESIProcessor

	// locationLimiters holds per-location rate limit counters.
	locationLimiters *sync.Map

	// appMgr manages non-PHP application processes (Node.js, Python, etc.)
	appMgr *appmanager.Manager
}

const maxMirrorBodyBytes = 2 << 20 // 2MB safety cap for mirror body buffering

var locationProxyHTTPClient = &http.Client{
	Timeout: 30 * time.Second,
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
	}

	// Alerting engine
	alerter := alerting.New(cfg.Global.Alerting.Enabled, cfg.Global.Alerting.WebhookURL, log)

	s := &Server{
		config:         cfg,
		logger:         log,
		vhosts:         router.NewVHostRouter(cfg.Domains),
		static:         static.New(),
		php:            fcgihandler.New(log),
		proxy:          proxyhandler.New(log),
		tlsMgr:         uwastls.NewManager(cfg.Global.ACME, cfg.Domains, log),
		cache:          cacheEngine,
		metrics:        m,
		analytics:      analytics.New(),
		alerter:        alerter,
		ctx:            ctx,
		cancel:         cancel,
		proxyPools:     make(map[string]*proxyhandler.UpstreamPool),
		proxyBalancers: make(map[string]proxyhandler.Balancer),
		proxyMirrors:   make(map[string]*proxyhandler.Mirror),
		proxyBreakers:  make(map[string]*proxyhandler.CircuitBreaker),
		proxyCanaries:  make(map[string]*proxyhandler.CanaryRouter),
		unknownHosts:   router.NewUnknownHostTracker(),
		securityStats:  middleware.NewSecurityStats(),
		htaccessCache:  make(map[string][]*rewrite.Rule),
		rewriteCache:   make(map[string]*rewrite.Engine),
		domainLogs:     newDomainLogManager(),
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
			s.admin.SetAuthManager(s.authMgr)
			log.Info("multi-user auth enabled", "allow_reseller", cfg.Global.Users.AllowResller)
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

	// App Manager — Node.js, Python, Ruby, Go, custom processes
	s.appMgr = appmanager.New(log)
	for _, d := range cfg.Domains {
		if d.Type == "app" && (d.App.Command != "" || d.App.Runtime != "") {
			if err := s.appMgr.Register(d.Host, d.App, d.Root); err != nil {
				log.Warn("app register failed", "domain", d.Host, "error", err)
				continue
			}
			// Apply resource limits (cgroups) if configured
			if d.Resources.CPUPercent > 0 || d.Resources.MemoryMB > 0 || d.Resources.PIDMax > 0 {
				cgPath, err := rlimit.Apply(d.Host, rlimit.Limits{
					CPUPercent: d.Resources.CPUPercent,
					MemoryMB:   d.Resources.MemoryMB,
					PIDMax:     d.Resources.PIDMax,
				})
				if err != nil {
					log.Warn("cgroup apply failed", "domain", d.Host, "error", err)
				} else if cgPath != "" {
					s.appMgr.SetCgroupPath(d.Host, cgPath)
				}
			}
			if err := s.appMgr.Start(d.Host); err != nil {
				log.Warn("app start failed", "domain", d.Host, "error", err)
			}
		}
	}
	if s.admin != nil {
		s.admin.SetAppManager(s.appMgr)
	}

	// Deploy manager (git clone → build → restart)
	deployMgr := deploy.New(log)
	if s.admin != nil {
		s.admin.SetDeployManager(deployMgr)
	}

	// PHP Manager — detect, auto-assign to PHP domains, start all
	s.phpMgr = phpmanager.New(log)
	s.phpMgr.Detect()
	s.autoAssignPHP(s.phpMgr, cfg)
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
			ups = append(ups, proxyhandler.UpstreamConfig{Address: u.Address, Weight: u.Weight})
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
				Enabled: d.Proxy.Mirror.Enabled,
				Backend: d.Proxy.Mirror.Backend,
				Percent: d.Proxy.Mirror.Percent,
			}, log)
		}
	}

	// Per-domain IP ACL middleware
	s.domainChains = make(map[string]middleware.Middleware)
	for _, d := range cfg.Domains {
		if len(d.Security.IPWhitelist) > 0 || len(d.Security.IPBlacklist) > 0 {
			s.domainChains[d.Host] = middleware.IPACL(middleware.IPACLConfig{
				Whitelist: d.Security.IPWhitelist,
				Blacklist: d.Security.IPBlacklist,
			})
		}
	}

	// Per-domain GeoIP middleware
	s.geoChains = make(map[string]middleware.Middleware)
	for _, d := range cfg.Domains {
		if len(d.Security.GeoBlockCountries) > 0 || len(d.Security.GeoAllowCountries) > 0 {
			s.geoChains[d.Host] = middleware.GeoIP(middleware.GeoIPConfig{
				BlockedCountries: d.Security.GeoBlockCountries,
				AllowedCountries: d.Security.GeoAllowCountries,
			})
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
			s.domainRateLimiters[d.Host] = middleware.NewRateLimiter(ctx, d.Security.RateLimit.Requests, window)
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
		middleware.RealIP(s.config.Global.TrustedProxies),
		middleware.SecurityHeaders(),
		middleware.Gzip(1024), // compress responses > 1KB
	}

	// Global rate limiting (use first domain's rate limit as global default)
	for _, d := range s.config.Domains {
		if d.Security.RateLimit.Requests > 0 {
			mws = append(mws, middleware.RateLimit(
				s.ctx,
				d.Security.RateLimit.Requests,
				d.Security.RateLimit.Window.Duration,
			))
			break
		}
	}

	// Security guard with WAF
	var blockedPaths []string
	wafEnabled := false
	for _, d := range s.config.Domains {
		blockedPaths = append(blockedPaths, d.Security.BlockedPaths...)
		if d.Security.WAF.Enabled {
			wafEnabled = true
		}
	}
	if len(blockedPaths) > 0 || wafEnabled {
		mws = append(mws, middleware.SecurityGuard(s.logger, blockedPaths, wafEnabled, s.securityStats))
		mws = append(mws, middleware.BotGuard(s.logger, s.securityStats))
	}

	mws = append(mws, middleware.AccessLog(s.logger))

	chain := middleware.Chain(mws...)
	return chain(http.HandlerFunc(s.handleRequest))
}

// Start starts all listeners and blocks until shutdown.
func (s *Server) Start() error {
	workers := runtime.NumCPU()
	if s.config.Global.WorkerCount != "auto" {
		if n, err := strconv.Atoi(s.config.Global.WorkerCount); err == nil && n > 0 {
			workers = n
		}
	}
	runtime.GOMAXPROCS(workers)

	// Apply sane timeout defaults to prevent resource exhaustion
	s.applyTimeoutDefaults()

	if err := s.writePID(); err != nil {
		s.logger.Warn("failed to write pid file", "error", err)
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
		if sched := s.config.Global.Backup.Schedule; sched != "" {
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
		for _, d := range s.config.Domains {
			if d.Root != "" {
				// Create an SFTP user per domain with a unique password
				// derived from API key + domain (so compromising one doesn't
				// expose all domains).
				domainPass := deriveSFTPPassword(apiKey, d.Host)
				users[d.Host] = sftpserver.User{
					Password: domainPass,
					Root:     d.Root,
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
func (s *Server) handleHTTP(w http.ResponseWriter, r *http.Request) {
	// ACME challenges must pass through (for cert issuance).
	if s.tlsMgr.HandleHTTPChallenge(w, r) {
		return
	}

	// Unknown domains: track + reject immediately.
	if !s.vhosts.IsConfigured(r.Host) {
		blocked := s.unknownHosts.Record(r.Host)
		if blocked {
			w.Header().Set("Connection", "close")
			http.Error(w, "403 Forbidden", http.StatusForbidden)
		} else {
			renderErrorPage(w, 421)
		}
		return
	}

	// Configured domain — redirect to HTTPS if SSL enabled.
	domain := s.vhosts.Lookup(r.Host)
	if domain != nil && (domain.SSL.Mode == "auto" || domain.SSL.Mode == "manual") {
		target := "https://" + r.Host + r.URL.RequestURI()
		w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		http.Redirect(w, r, target, http.StatusMovedPermanently)
		return
	}

	// Non-SSL configured domain — serve normally.
	s.handler.ServeHTTP(w, r)
}

// handleRequest is the core dispatch handler called after the middleware chain.
func (s *Server) handleRequest(w http.ResponseWriter, r *http.Request) {
	// Connection limiter: reject with 503 when at capacity.
	if s.connLimiter != nil {
		select {
		case s.connLimiter <- struct{}{}:
			defer func() { <-s.connLimiter }()
		default:
			renderErrorPage(w, http.StatusServiceUnavailable)
			return
		}
	}

	// Health check on main port (no auth, fast path)
	if r.URL.Path == "/.well-known/health" || r.URL.Path == "/healthz" {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
		return
	}

	ctx := router.AcquireContext(w, r)
	defer router.ReleaseContext(ctx)

	ctx.Response.Header().Set("Server", "UWAS/"+build.Version)

	// Advertise HTTP/3 support via Alt-Svc header
	if altSvc := s.altSvcHeader(); altSvc != "" {
		ctx.Response.Header().Set("Alt-Svc", altSvc)
	}

	if r.TLS != nil {
		ctx.IsHTTPS = true
	}

	// Metrics + log tracking
	start := time.Now()
	s.metrics.ActiveConns.Add(1)
	defer func() {
		s.metrics.ActiveConns.Add(-1)
		s.metrics.RequestsTotal.Add(1)
		s.metrics.RecordRequest(ctx.Response.StatusCode())
		s.metrics.RecordLatency(time.Since(start))
		s.metrics.BytesSent.Add(ctx.Response.BytesWritten())
		s.metrics.RecordDomain(r.Host, ctx.Response.StatusCode(), ctx.Response.BytesWritten())

		// Record to admin log ring buffer (skip internal health checks and monitor)
		isMonitor := r.UserAgent() == "UWAS-Monitor/1.0"
		if s.admin != nil && !isMonitor && r.Host != "localhost:80" && r.Host != "localhost" {
			elapsed := time.Since(start)
			// Use real client IP from X-Forwarded-For or X-Real-IP
			remoteIP := r.RemoteAddr
			if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
				if parts := strings.SplitN(xff, ",", 2); len(parts) > 0 {
					remoteIP = strings.TrimSpace(parts[0])
				}
			} else if xri := r.Header.Get("X-Real-IP"); xri != "" {
				remoteIP = xri
			}
			s.admin.RecordLog(admin.LogEntry{
				Time:       start,
				Host:       r.Host,
				Method:     r.Method,
				Path:       r.URL.Path,
				Status:     ctx.Response.StatusCode(),
				Bytes:      ctx.Response.BytesWritten(),
				DurationMS: float64(elapsed.Microseconds()) / 1000.0,
				Duration:   elapsed.Round(time.Millisecond).String(),
				RemoteAddr: r.RemoteAddr,
				Remote:     remoteIP,
				UserAgent:  r.UserAgent(),
			})
		}

		// Record bandwidth usage
		if s.bwMgr != nil {
			blocked, _ := s.bwMgr.Record(r.Host, ctx.Response.BytesWritten())
			_ = blocked // blocking is applied on next request via pre-handler check
		}

		// Record analytics
		if s.analytics != nil {
			s.analytics.RecordFull(r.Host, r.URL.Path, r.RemoteAddr,
				r.Referer(), r.UserAgent(),
				ctx.Response.StatusCode(), ctx.Response.BytesWritten())
		}

		// Record for alerting (error spike detection)
		if s.alerter != nil {
			s.alerter.RecordRequest(ctx.Response.StatusCode() >= 500)
		}

		// Slow request logging
		elapsed := time.Since(start)
		if s.metrics.SlowThreshold > 0 && elapsed >= s.metrics.SlowThreshold {
			s.logger.Warn("slow request",
				"host", r.Host,
				"method", r.Method,
				"path", r.URL.Path,
				"status", ctx.Response.StatusCode(),
				"duration", elapsed.String(),
				"bytes", ctx.Response.BytesWritten(),
			)
		}
	}()

	// Virtual host lookup
	domain := s.vhosts.Lookup(r.Host)
	if domain == nil {
		renderErrorPage(ctx.Response, http.StatusNotFound)
		return
	}

	// Track and reject unconfigured hosts hitting the fallback domain.
	if !s.vhosts.IsConfigured(r.Host) {
		blocked := s.unknownHosts.Record(r.Host)
		if blocked {
			ctx.Response.Header().Set("Connection", "close")
			renderErrorPage(ctx.Response, http.StatusForbidden)
			return
		}
		// Not blocked but unconfigured — serve 421 Misdirected Request.
		renderErrorPage(ctx.Response, 421)
		return
	}

	ctx.VHostName = domain.Host
	ctx.DocumentRoot = domain.Root

	// Maintenance mode: serve 503 with Retry-After for all non-allowed IPs
	if domain.Maintenance.Enabled {
		allowed := false
		clientAddr := ctx.RemoteIP
		if clientAddr == "" {
			clientAddr, _, _ = net.SplitHostPort(r.RemoteAddr)
		}
		for _, ip := range domain.Maintenance.AllowedIPs {
			if clientAddr == ip {
				allowed = true
				break
			}
		}
		if !allowed {
			ctx.Response.Header().Set("Content-Type", "text/html; charset=utf-8")
			if domain.Maintenance.RetryAfter > 0 {
				ctx.Response.Header().Set("Retry-After", strconv.Itoa(domain.Maintenance.RetryAfter))
			}
			ctx.Response.WriteHeader(http.StatusServiceUnavailable)
			msg := domain.Maintenance.Message
			if msg == "" {
				msg = "<html><body style=\"font-family:sans-serif;text-align:center;padding:50px\"><h1>Under Maintenance</h1><p>We'll be back shortly.</p></body></html>"
			}
			ctx.Response.Write([]byte(msg))
			return
		}
	}

	// Bandwidth limit check: block requests to domains that exceeded their limit
	if s.bwMgr != nil && domain.Bandwidth.Enabled && s.bwMgr.IsBlocked(domain.Host) {
		ctx.Response.Header().Set("Retry-After", "3600")
		renderDomainError(ctx.Response, http.StatusServiceUnavailable, domain)
		return
	}

	// Per-domain security headers (CSP, COEP, COOP, CORP, Permissions-Policy)
	if sh := domain.SecurityHeaders; sh.ContentSecurityPolicy != "" {
		ctx.Response.Header().Set("Content-Security-Policy", sh.ContentSecurityPolicy)
	}
	if sh := domain.SecurityHeaders; sh.PermissionsPolicy != "" {
		ctx.Response.Header().Set("Permissions-Policy", sh.PermissionsPolicy)
	}
	if sh := domain.SecurityHeaders; sh.CrossOriginEmbedder != "" {
		ctx.Response.Header().Set("Cross-Origin-Embedder-Policy", sh.CrossOriginEmbedder)
	}
	if sh := domain.SecurityHeaders; sh.CrossOriginOpener != "" {
		ctx.Response.Header().Set("Cross-Origin-Opener-Policy", sh.CrossOriginOpener)
	}
	if sh := domain.SecurityHeaders; sh.CrossOriginResource != "" {
		ctx.Response.Header().Set("Cross-Origin-Resource-Policy", sh.CrossOriginResource)
	}

	// Per-path location overrides (headers, cache-control, proxy, redirect, static root)
	for _, loc := range domain.Locations {
		if !matchLocation(r.URL.Path, loc.Match) {
			continue
		}

		// Apply headers + cache-control
		for k, v := range loc.Headers {
			ctx.Response.Header().Set(k, v)
		}
		if loc.CacheControl != "" {
			ctx.Response.Header().Set("Cache-Control", loc.CacheControl)
		}

		// Per-path rate limit
		if loc.RateLimit != nil && loc.RateLimit.Requests > 0 {
			clientAddr := ctx.RemoteIP
			if clientAddr == "" {
				clientAddr, _, _ = net.SplitHostPort(r.RemoteAddr)
			}
			limiterKey := domain.Host + "|" + loc.Match + "|" + clientAddr
			if s.locationLimiters == nil {
				s.locationLimiters = &sync.Map{}
			}
			val, _ := s.locationLimiters.LoadOrStore(limiterKey, &rateLimitEntry{})
			entry := val.(*rateLimitEntry)
			window := loc.RateLimit.Window.Duration
			if window == 0 {
				window = time.Minute
			}
			now := time.Now()
			entry.mu.Lock()
			if now.Sub(entry.windowStart) > window {
				entry.windowStart = now
				entry.count = 0
			}
			entry.count++
			exceeded := entry.count > int64(loc.RateLimit.Requests)
			entry.mu.Unlock()
			if exceeded {
				ctx.Response.Header().Set("Retry-After", strconv.Itoa(int(window.Seconds())))
				renderDomainError(ctx.Response, http.StatusTooManyRequests, domain)
				return
			}
		}

		// Location-level redirect (e.g. /old-path → https://example.com/new-path)
		if loc.Redirect != "" {
			code := loc.RedirectCode
			if code == 0 {
				code = http.StatusMovedPermanently
			}
			http.Redirect(ctx.Response, r, loc.Redirect, code)
			return
		}

		// Location-level proxy_pass (e.g. /api/ → http://127.0.0.1:4000)
		if loc.ProxyPass != "" {
			path := r.URL.Path
			if loc.StripPrefix && !strings.HasPrefix(loc.Match, "~") {
				path = strings.TrimPrefix(path, strings.TrimSuffix(loc.Match, "/"))
				if path == "" {
					path = "/"
				}
			}
			targetURL := loc.ProxyPass + path
			if r.URL.RawQuery != "" {
				targetURL += "?" + r.URL.RawQuery
			}
			// Simple single-backend proxy for location blocks
			proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, r.Body)
			if err != nil {
				renderDomainError(ctx.Response, http.StatusBadGateway, domain)
				return
			}
			for k, vv := range r.Header {
				for _, v := range vv {
					proxyReq.Header.Add(k, v)
				}
			}
			proxyReq.Header.Set("X-Forwarded-For", r.RemoteAddr)
			proxyReq.Header.Set("X-Forwarded-Host", r.Host)
			resp, err := locationProxyHTTPClient.Do(proxyReq)
			if err != nil {
				s.logger.Error("location proxy error", "match", loc.Match, "target", loc.ProxyPass, "error", err)
				renderDomainError(ctx.Response, http.StatusBadGateway, domain)
				return
			}
			defer resp.Body.Close()
			for k, vv := range resp.Header {
				for _, v := range vv {
					ctx.Response.Header().Add(k, v)
				}
			}
			ctx.Response.WriteHeader(resp.StatusCode)
			io.Copy(ctx.Response, resp.Body)
			return
		}

		// Location-level static root (e.g. /docs/ → /var/www/docs)
		if loc.Root != "" {
			path := r.URL.Path
			if !strings.HasPrefix(loc.Match, "~") {
				path = strings.TrimPrefix(path, strings.TrimSuffix(loc.Match, "/"))
			}
			filePath := filepath.Join(loc.Root, filepath.Clean("/"+path))
			// Security: must stay within loc.Root.
			if !pathsafe.IsWithinBase(loc.Root, filePath) || !pathsafe.IsWithinBaseResolved(loc.Root, filePath) {
				renderDomainError(ctx.Response, http.StatusForbidden, domain)
				return
			}
			http.ServeFile(ctx.Response, r, filePath)
			return
		}

		break // first match wins (like Nginx) — if no handler, continue to normal dispatch
	}

	// Per-domain blocked paths
	for _, blocked := range domain.Security.BlockedPaths {
		if strings.Contains(r.URL.Path, blocked) {
			renderDomainError(ctx.Response, http.StatusForbidden, domain)
			return
		}
	}

	// Per-domain IP ACL (whitelist/blacklist)
	if chain := s.domainChains[domain.Host]; chain != nil {
		passed := false
		chain(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			passed = true
		})).ServeHTTP(ctx.Response, r)
		if !passed {
			return
		}
	}

	// Per-domain GeoIP blocking
	if chain := s.geoChains[domain.Host]; chain != nil {
		passed := false
		chain(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			passed = true
		})).ServeHTTP(ctx.Response, r)
		if !passed {
			return
		}
	}

	// Per-domain rate limiting
	if rl := s.domainRateLimiters[domain.Host]; rl != nil {
		ip, _, _ := net.SplitHostPort(r.RemoteAddr)
		if ip == "" {
			ip = r.RemoteAddr
		}
		if !rl.Allow(ip) {
			ctx.Response.Header().Set("Retry-After", "60")
			ctx.Response.WriteHeader(http.StatusTooManyRequests)
			ctx.Response.Write([]byte("429 Too Many Requests"))
			return
		}
	}

	// Per-domain Basic Authentication
	if domain.BasicAuth.Enabled && len(domain.BasicAuth.Users) > 0 {
		passed := false
		realm := domain.BasicAuth.Realm
		if realm == "" {
			realm = domain.Host
		}
		middleware.BasicAuth(domain.BasicAuth.Users, realm)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			passed = true
		})).ServeHTTP(ctx.Response, r)
		if !passed {
			return
		}
	}

	// Per-domain CORS
	if domain.CORS.Enabled {
		corsMiddleware := middleware.CORS(middleware.CORSConfig{
			AllowedOrigins:   domain.CORS.AllowedOrigins,
			AllowedMethods:   domain.CORS.AllowedMethods,
			AllowedHeaders:   domain.CORS.AllowedHeaders,
			AllowCredentials: domain.CORS.AllowCredentials,
			MaxAge:           domain.CORS.MaxAge,
		})
		// CORS always calls next (even for preflight it writes headers first).
		// For OPTIONS preflight, it handles the response and may not call next.
		passed := false
		corsMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			passed = true
		})).ServeHTTP(ctx.Response, r)
		if !passed {
			return
		}
	}

	// .htaccess import (runtime parse)
	// Skip rewrite for paths that should be served directly:
	// - /wp-admin, /wp-includes, /wp-content (WordPress core)
	// - Direct .php file requests (already resolved, no rewrite needed)
	if domain.Htaccess.Mode == "import" && domain.Root != "" {
		p := r.URL.Path
		skipRewrite := strings.HasPrefix(p, "/wp-admin") ||
			strings.HasPrefix(p, "/wp-includes") ||
			strings.HasPrefix(p, "/wp-content") ||
			strings.HasSuffix(p, ".php")
		if !skipRewrite {
			s.applyHtaccess(ctx, domain)
		}
	}

	// Rewrite engine (from YAML config)
	if len(domain.Rewrites) > 0 {
		if s.applyRewrites(ctx, domain) {
			return
		}
	}

	// Per-domain header transforms
	if h := domain.Headers; len(h.RequestAdd) > 0 || len(h.RequestRemove) > 0 {
		for k, v := range h.RequestAdd {
			r.Header.Set(k, v)
		}
		for _, k := range h.RequestRemove {
			r.Header.Del(k)
		}
	}
	if h := domain.Headers; len(h.Add) > 0 || len(h.Remove) > 0 ||
		len(h.ResponseAdd) > 0 || len(h.ResponseRemove) > 0 {
		w := ctx.Response.Header()
		for k, v := range h.Add {
			w.Set(k, v)
		}
		for k, v := range h.ResponseAdd {
			w.Set(k, v)
		}
		for _, k := range h.Remove {
			w.Del(k)
		}
		for _, k := range h.ResponseRemove {
			w.Del(k)
		}
	}

	// Cache lookup — check global bypass + per-domain bypass rules
	// PHP domains: only cache static assets (images, CSS, JS), never PHP output.
	// PHP responses go through FastCGI and are always dynamic.
	cacheEnabled := s.cache != nil && domain.Cache.Enabled && !cache.ShouldBypass(r)
	if cacheEnabled && domain.Type == "php" {
		// Only cache requests for known static file extensions on PHP domains
		ext := strings.ToLower(filepath.Ext(r.URL.Path))
		staticExts := map[string]bool{
			".css": true, ".js": true, ".png": true, ".jpg": true, ".jpeg": true,
			".gif": true, ".svg": true, ".ico": true, ".webp": true, ".avif": true,
			".woff": true, ".woff2": true, ".ttf": true, ".eot": true,
			".mp4": true, ".webm": true, ".pdf": true, ".zip": true,
		}
		if !staticExts[ext] {
			cacheEnabled = false
		}
	}
	if cacheEnabled {
		// Check per-domain cache bypass rules + set Cache-Control from rules
		for _, rule := range domain.Cache.Rules {
			if matchPath(r.URL.Path, rule.Match) {
				if rule.Bypass {
					cacheEnabled = false
					break
				}
				if rule.CacheControl != "" {
					ctx.Response.Header().Set("Cache-Control", rule.CacheControl)
				}
			}
		}
		// Bypass cache for WordPress admin/login paths (always dynamic)
		p := r.URL.Path
		if strings.HasPrefix(p, "/wp-admin") ||
			strings.HasPrefix(p, "/wp-login") ||
			strings.HasPrefix(p, "/wp-cron") ||
			strings.HasPrefix(p, "/wp-json") ||
			strings.HasPrefix(p, "/xmlrpc") ||
			p == "/wp-comments-post.php" {
			cacheEnabled = false
		}
		// Bypass cache if request has session cookies (WordPress, PHP sessions)
		if cacheEnabled {
			if cookie := r.Header.Get("Cookie"); cookie != "" {
				if strings.Contains(cookie, "wordpress_logged_in") ||
					strings.Contains(cookie, "wp-settings") ||
					strings.Contains(cookie, "PHPSESSID") ||
					strings.Contains(cookie, "comment_author") ||
					strings.Contains(cookie, "woocommerce_cart") ||
					strings.Contains(cookie, "woocommerce_items") {
					cacheEnabled = false
				}
			}
		}
	}
	if cacheEnabled {
		cached, status := s.cache.Get(r)
		if cached != nil && (status == cache.StatusHit || status == cache.StatusStale) {
			ctx.CacheStatus = status
			s.metrics.RecordCache(status)
			ctx.Response.Header().Set("X-Cache", status)
			ctx.Response.Header().Set("Age", strconv.FormatInt(int64(cached.Age().Seconds()), 10))
			for k, vals := range cached.Headers {
				for _, v := range vals {
					ctx.Response.Header().Set(k, v)
				}
			}

			// Handle conditional requests against cached ETag
			if etag := ctx.Response.Header().Get("Etag"); etag != "" {
				if match := r.Header.Get("If-None-Match"); match != "" && match == etag {
					ctx.Response.WriteHeader(http.StatusNotModified)
					return
				}
			}

			body := cached.Body
			// ESI assembly on cache hit: replace ESI tags with cached/fetched fragments
			if cached.ESITemplate && domain.Cache.ESI && s.esiProcessor != nil &&
				r.Header.Get("X-ESI-Subrequest") == "" {
				assembled, err := s.esiProcessor.Process(body, r.Host, r, domain.Cache.Tags, 0)
				if err == nil {
					body = assembled
				}
				ctx.Response.Header().Set("Content-Length", strconv.Itoa(len(body)))
			}
			ctx.Response.WriteHeader(cached.StatusCode)
			ctx.Response.Write(body)
			return
		}
		ctx.CacheStatus = cache.StatusMiss
		s.metrics.RecordCache(cache.StatusMiss)
	}

	// Wrap the response writer to capture the response for caching.
	var capture *responseCapture
	if cacheEnabled {
		capture = newResponseCapture(ctx.Response.ResponseWriter)
	}

	// Record handler type for per-handler metrics
	s.metrics.RecordHandlerType(domain.Type)

	// Dispatch to handler
	if capture != nil {
		// Temporarily swap the underlying writer so handlers write through the capture.
		origWriter := ctx.Response.ResponseWriter
		ctx.Response.ResponseWriter = capture
		switch domain.Type {
		case "redirect":
			s.handleRedirect(ctx, domain)
		case "static", "php":
			s.handleFileRequest(ctx, domain)
		case "proxy":
			s.handleProxy(ctx, domain)
		case "app":
			s.handleAppProxy(ctx, domain)
		default:
			renderDomainError(ctx.Response, http.StatusInternalServerError, domain)
		}
		// Restore the original writer.
		ctx.Response.ResponseWriter = origWriter

		// Store the response in cache if it is cacheable and not too large.
		hdrs := capture.capturedHeaders()
		if !capture.overflow && cache.IsCacheable(r, ctx.Response.StatusCode(), hdrs) {
			ttl := time.Duration(domain.Cache.TTL) * time.Second
			if ttl <= 0 {
				ttl = 60 * time.Second
			}
			capturedBody := capture.body.Bytes()
			isESI := domain.Cache.ESI && s.esiProcessor != nil &&
				strings.Contains(hdrs.Get("Content-Type"), "text/html") &&
				cache.ContainsESI(capturedBody) &&
				r.Header.Get("X-ESI-Subrequest") == ""
			s.cache.Set(r, &cache.CachedResponse{
				StatusCode:  ctx.Response.StatusCode(),
				Headers:     hdrs,
				Body:        capturedBody,
				Created:     time.Now(),
				TTL:         ttl,
				Tags:        domain.Cache.Tags,
				ESITemplate: isESI,
			})
		}
	} else {
		switch domain.Type {
		case "redirect":
			s.handleRedirect(ctx, domain)
		case "static", "php":
			s.handleFileRequest(ctx, domain)
		case "proxy":
			s.handleProxy(ctx, domain)
		case "app":
			s.handleAppProxy(ctx, domain)
		default:
			renderDomainError(ctx.Response, http.StatusInternalServerError, domain)
		}
	}

	// Per-domain access log file
	if domain.AccessLog.Path != "" {
		s.domainLogs.Write(
			r.Host, domain.AccessLog.Path, domain.AccessLog.Rotate,
			r.Method, r.URL.RequestURI(),
			r.RemoteAddr, r.UserAgent(),
			ctx.Response.StatusCode(), int(ctx.Response.BytesWritten()),
			time.Since(start),
		)
	}
}

func (s *Server) handleFileRequest(ctx *router.RequestContext, domain *config.Domain) {
	// Save original URI before any rewriting (PHP needs this for SCRIPT_NAME)
	if ctx.OriginalURI == "" {
		ctx.OriginalURI = ctx.Request.URL.RequestURI()
	}

	// Check if the raw path points to a directory (for directory listing)
	if domain.DirectoryListing && domain.Root != "" {
		rawPath := filepath.Join(domain.Root, filepath.Clean("/"+ctx.Request.URL.Path))
		if info, err := os.Stat(rawPath); err == nil && info.IsDir() {
			static.ServeDirListing(ctx, rawPath, ctx.Request.URL.Path)
			return
		}
	}

	if !static.ResolveRequest(ctx, domain) {
		renderDomainError(ctx.Response, http.StatusNotFound, domain)
		return
	}

	resolved := ctx.ResolvedPath

	info, err := os.Stat(resolved)
	if err != nil {
		renderDomainError(ctx.Response, http.StatusNotFound, domain)
		return
	}

	if info.IsDir() {
		renderDomainError(ctx.Response, http.StatusForbidden, domain)
		return
	}

	if domain.Type == "php" && strings.HasSuffix(resolved, ".php") {
		// Ensure FPM address is set — fall back to phpMgr's actual listen addr.
		if domain.PHP.FPMAddress == "" && s.phpMgr != nil {
			for _, inst := range s.phpMgr.GetDomainInstances() {
				if inst.Domain == domain.Host && inst.Running {
					domain.PHP.FPMAddress = inst.ListenAddr
					break
				}
			}
			// Still empty? Try global default.
			if domain.PHP.FPMAddress == "" {
				domain.PHP.FPMAddress = "127.0.0.1:9000"
			}
		}
		s.php.Serve(ctx, domain)
		return
	}

	// Image optimization: serve pre-converted WebP/AVIF if available
	if _, ok := s.imageOptChains[domain.Host]; ok {
		accept := ctx.Request.Header.Get("Accept")
		ext := filepath.Ext(resolved)
		if ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".gif" {
			for _, fmt := range domain.ImageOptimization.Formats {
				if strings.Contains(accept, "image/"+fmt) {
					optPath := resolved + "." + fmt
					if _, err := os.Stat(optPath); err == nil {
						ctx.ResolvedPath = optPath
						ctx.Response.Header().Set("Content-Type", "image/"+fmt)
						ctx.Response.Header().Add("Vary", "Accept")
						break
					}
				}
			}
		}
	}

	s.static.Serve(ctx)
}

func (s *Server) handleProxy(ctx *router.RequestContext, domain *config.Domain) {
	pool := s.proxyPools[domain.Host]
	if pool == nil {
		renderDomainError(ctx.Response, http.StatusBadGateway, domain)
		return
	}

	balancer := s.proxyBalancers[domain.Host]
	if balancer == nil {
		balancer = proxyhandler.NewBalancer("round_robin")
	}

	// Circuit breaker: reject if circuit is open
	cb := s.proxyBreakers[domain.Host]
	if cb != nil && !cb.Allow() {
		renderDomainError(ctx.Response, http.StatusServiceUnavailable, domain)
		return
	}

	// Request mirroring: fire-and-forget copy to mirror backend
	if mirror := s.proxyMirrors[domain.Host]; mirror != nil && mirror.ShouldMirror() {
		var bodyBytes []byte
		shouldMirror := true

		// Mirror request bodies only when size is known and small enough.
		// This avoids unbounded buffering for large uploads.
		if ctx.Request.Body != nil {
			if ctx.Request.ContentLength < 0 || ctx.Request.ContentLength > maxMirrorBodyBytes {
				shouldMirror = false
				s.logger.Debug("skipping mirror for large/unknown request body",
					"host", domain.Host,
					"content_length", ctx.Request.ContentLength,
					"limit_bytes", maxMirrorBodyBytes,
				)
			} else {
				limited := io.LimitReader(ctx.Request.Body, maxMirrorBodyBytes+1)
				buf, err := io.ReadAll(limited)
				if err != nil {
					renderDomainError(ctx.Response, http.StatusBadRequest, domain)
					return
				}
				if int64(len(buf)) > maxMirrorBodyBytes {
					shouldMirror = false
				}
				ctx.Request.Body.Close()
				ctx.Request.Body = io.NopCloser(bytes.NewReader(buf))
				bodyBytes = buf
			}
		}
		if shouldMirror {
			mirror.Send(ctx.Request, bodyBytes)
		}
	}

	// Canary routing: route a percentage of traffic to canary upstreams
	if cr := s.proxyCanaries[domain.Host]; cr != nil && cr.IsCanary(ctx.Request, domain.Proxy.Canary) {
		cr.Serve(ctx, domain, s.proxy)
	} else {
		s.proxy.Serve(ctx, domain, pool, balancer)
	}

	// Record circuit breaker result
	if cb != nil {
		if ctx.Response.StatusCode() >= 500 {
			cb.RecordFailure()
		} else {
			cb.RecordSuccess()
		}
	}
}

func (s *Server) handleRedirect(ctx *router.RequestContext, domain *config.Domain) {
	target := domain.Redirect.Target
	if domain.Redirect.PreservePath {
		target = strings.TrimRight(target, "/") + ctx.Request.URL.RequestURI()
	}
	status := domain.Redirect.Status
	if status == 0 {
		status = http.StatusMovedPermanently
	}
	http.Redirect(ctx.Response, ctx.Request, target, status)
}

func (s *Server) applyRewrites(ctx *router.RequestContext, domain *config.Domain) bool {
	engine := s.rewriteCache[domain.Host]
	if engine == nil {
		return false
	}

	vars := rewrite.BuildVariables(ctx.Request, domain.Root, ctx.ResolvedPath, ctx.IsHTTPS)
	result := engine.Process(ctx.Request.URL.Path, ctx.Request.URL.RawQuery, vars)

	if result.Forbidden {
		renderDomainError(ctx.Response, http.StatusForbidden, domain)
		return true
	}
	if result.Gone {
		renderDomainError(ctx.Response, http.StatusGone, domain)
		return true
	}
	if result.Redirect {
		http.Redirect(ctx.Response, ctx.Request, result.URI, result.StatusCode)
		return true
	}
	if result.Modified {
		ctx.Request.URL.Path = result.URI
		if result.Query != "" {
			ctx.Request.URL.RawQuery = result.Query
		}
		ctx.RewrittenURI = result.URI
	}
	return false
}

// applyHtaccess reads and applies .htaccess rewrite rules from the document root.
// Parsed rules are cached per domain root and invalidated on config reload.
func (s *Server) applyHtaccess(ctx *router.RequestContext, domain *config.Domain) {
	ruleSet := s.getHtaccessRuleSet(domain.Root)
	if ruleSet == nil || ruleSet.raw == nil {
		return
	}

	// 1. Apply rewrite rules
	if ruleSet.raw.RewriteEnabled && len(ruleSet.compiledRules) > 0 {
		engine := rewrite.NewEngine(ruleSet.compiledRules)
		requestFilename := filepath.Join(domain.Root, filepath.Clean("/"+ctx.Request.URL.Path))
		vars := rewrite.BuildVariables(ctx.Request, domain.Root, requestFilename, ctx.IsHTTPS)
		result := engine.Process(ctx.Request.URL.Path, ctx.Request.URL.RawQuery, vars)

		if result.Modified {
			ctx.Request.URL.Path = result.URI
			if result.Query != "" {
				ctx.Request.URL.RawQuery = result.Query
			}
			ctx.RewrittenURI = result.URI
		}
	}

	// 2. Apply Header directives
	for _, h := range ruleSet.raw.Headers {
		switch h.Action {
		case "set":
			ctx.Response.Header().Set(h.Name, h.Value)
		case "unset":
			ctx.Response.Header().Del(h.Name)
		case "append":
			ctx.Response.Header().Add(h.Name, h.Value)
		case "add":
			ctx.Response.Header().Add(h.Name, h.Value)
		}
	}

	// 3. Apply ExpiresByType as Cache-Control headers
	if ruleSet.raw.ExpiresActive {
		ct := ctx.Response.Header().Get("Content-Type")
		if ct != "" {
			// Strip charset: "text/html; charset=utf-8" → "text/html"
			if idx := strings.Index(ct, ";"); idx != -1 {
				ct = strings.TrimSpace(ct[:idx])
			}
			if dur, ok := ruleSet.raw.ExpiresByType[ct]; ok {
				ctx.Response.Header().Set("Cache-Control", "max-age="+parseExpiresDuration(dur))
			}
		}
	}

	// 4. Apply ErrorDocument — merge into domain's ErrorPages
	for code, page := range ruleSet.raw.ErrorDocuments {
		if domain.ErrorPages == nil {
			domain.ErrorPages = make(map[int]string)
		}
		if _, exists := domain.ErrorPages[code]; !exists {
			domain.ErrorPages[code] = page
		}
	}

	// 5. Apply php_value / php_flag — pass as PHP_VALUE env var
	// PHP-FPM reads PHP_VALUE and PHP_ADMIN_VALUE from FastCGI env to override ini settings.
	if len(ruleSet.raw.PHPValues) > 0 || len(ruleSet.raw.PHPFlags) > 0 {
		var phpValues []string
		for k, v := range ruleSet.raw.PHPValues {
			phpValues = append(phpValues, k+" = "+v)
		}
		for k, v := range ruleSet.raw.PHPFlags {
			phpValues = append(phpValues, k+" = "+v)
		}
		if domain.PHP.Env == nil {
			domain.PHP.Env = make(map[string]string)
		}
		domain.PHP.Env["PHP_VALUE"] = strings.Join(phpValues, "\n")
	}
}

// parseExpiresDuration converts Apache Expires format to seconds.
// e.g. "access plus 1 month" → "2592000", "access plus 1 year" → "31536000"
func parseExpiresDuration(expr string) string {
	expr = strings.ToLower(expr)
	expr = strings.Replace(expr, "access plus ", "", 1)
	expr = strings.Replace(expr, "modification plus ", "", 1)

	seconds := 0
	parts := strings.Fields(expr)
	for i := 0; i+1 < len(parts); i += 2 {
		n := 0
		fmt.Sscanf(parts[i], "%d", &n)
		unit := parts[i+1]
		switch {
		case strings.HasPrefix(unit, "second"):
			seconds += n
		case strings.HasPrefix(unit, "minute"):
			seconds += n * 60
		case strings.HasPrefix(unit, "hour"):
			seconds += n * 3600
		case strings.HasPrefix(unit, "day"):
			seconds += n * 86400
		case strings.HasPrefix(unit, "week"):
			seconds += n * 604800
		case strings.HasPrefix(unit, "month"):
			seconds += n * 2592000
		case strings.HasPrefix(unit, "year"):
			seconds += n * 31536000
		}
	}
	if seconds == 0 {
		seconds = 3600 // 1 hour default
	}
	return fmt.Sprintf("%d", seconds)
}

// htaccessCacheEntry holds both raw and compiled htaccess rules.
type htaccessCacheEntry struct {
	raw           *htaccess.RuleSet
	compiledRules []*rewrite.Rule
	modTime       time.Time // file modification time for auto-invalidation
}

func (s *Server) getHtaccessRuleSet(root string) *htaccessCacheEntry {
	htPath := filepath.Join(root, ".htaccess")

	s.htaccessCacheMu.RLock()
	if entry, ok := s.htaccessCacheV2[root]; ok {
		s.htaccessCacheMu.RUnlock()
		// Check if file changed since last parse
		if info, err := os.Stat(htPath); err == nil {
			if !info.ModTime().Equal(entry.modTime) {
				// File changed — re-parse
				newEntry := s.parseHtaccessFull(root)
				s.htaccessCacheMu.Lock()
				s.htaccessCacheV2[root] = newEntry
				s.htaccessCacheMu.Unlock()
				return newEntry
			}
		} else if entry.raw == nil {
			// File still doesn't exist and cache is nil — that's fine
			return entry
		} else {
			// File was deleted — invalidate
			s.htaccessCacheMu.Lock()
			delete(s.htaccessCacheV2, root)
			s.htaccessCacheMu.Unlock()
			return nil
		}
		return entry
	}
	s.htaccessCacheMu.RUnlock()

	entry := s.parseHtaccessFull(root)

	s.htaccessCacheMu.Lock()
	if s.htaccessCacheV2 == nil {
		s.htaccessCacheV2 = make(map[string]*htaccessCacheEntry)
	}
	s.htaccessCacheV2[root] = entry
	s.htaccessCacheMu.Unlock()

	return entry
}

func (s *Server) parseHtaccessFull(root string) *htaccessCacheEntry {
	htPath := filepath.Join(root, ".htaccess")
	f, err := os.Open(htPath)
	if err != nil {
		return &htaccessCacheEntry{} // cache "no file" to avoid repeated stat
	}
	defer f.Close()

	info, _ := f.Stat()

	directives, err := htaccess.Parse(f)
	if err != nil {
		s.logger.Warn("htaccess parse error", "path", htPath, "error", err)
		return &htaccessCacheEntry{}
	}

	ruleSet := htaccess.Convert(directives)
	entry := &htaccessCacheEntry{raw: ruleSet}
	if info != nil {
		entry.modTime = info.ModTime()
	}

	// Compile rewrite rules
	if ruleSet.RewriteEnabled {
		for _, rw := range ruleSet.Rewrites {
			rule, err := rewrite.ParseRule(rw.Pattern, rw.Target, rw.Flags)
			if err != nil {
				continue
			}
			for _, cond := range rw.Conditions {
				c, err := rewrite.ParseCondition(cond.Variable, cond.Pattern, cond.Flags)
				if err != nil {
					continue
				}
				rule.Conditions = append(rule.Conditions, *c)
			}
			rule.Flags.Last = true
			entry.compiledRules = append(entry.compiledRules, rule)
		}
	}

	return entry
}

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
	s.rewriteCache = newRewriteCache

	// Rebuild per-domain middleware chains
	newDomainChains := make(map[string]middleware.Middleware)
	for _, d := range newCfg.Domains {
		if len(d.Security.IPWhitelist) > 0 || len(d.Security.IPBlacklist) > 0 {
			newDomainChains[d.Host] = middleware.IPACL(middleware.IPACLConfig{
				Whitelist: d.Security.IPWhitelist,
				Blacklist: d.Security.IPBlacklist,
			})
		}
	}
	s.domainChains = newDomainChains

	// Rebuild per-domain GeoIP chains
	newGeoChains := make(map[string]middleware.Middleware)
	for _, d := range newCfg.Domains {
		if len(d.Security.GeoBlockCountries) > 0 || len(d.Security.GeoAllowCountries) > 0 {
			newGeoChains[d.Host] = middleware.GeoIP(middleware.GeoIPConfig{
				BlockedCountries: d.Security.GeoBlockCountries,
				AllowedCountries: d.Security.GeoAllowCountries,
			})
		}
	}
	s.geoChains = newGeoChains

	// Rebuild per-domain rate limiters
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

	// Rebuild proxy pools and balancers
	newPools := make(map[string]*proxyhandler.UpstreamPool)
	newBalancers := make(map[string]proxyhandler.Balancer)
	for _, d := range newCfg.Domains {
		if d.Type == "proxy" && len(d.Proxy.Upstreams) > 0 {
			var ups []proxyhandler.UpstreamConfig
			for _, u := range d.Proxy.Upstreams {
				ups = append(ups, proxyhandler.UpstreamConfig{Address: u.Address, Weight: u.Weight})
			}
			newPools[d.Host] = proxyhandler.NewUpstreamPool(ups)
			newBalancers[d.Host] = proxyhandler.NewBalancer(d.Proxy.Algorithm)
		}
	}
	s.proxyPools = newPools
	s.proxyBalancers = newBalancers

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

	// Sync app manager: stop removed apps, register new ones
	if s.appMgr != nil {
		newAppDomains := make(map[string]bool)
		for _, d := range newCfg.Domains {
			if d.Type == "app" {
				newAppDomains[d.Host] = true
			}
		}
		// Stop apps for removed domains
		for _, inst := range s.appMgr.Instances() {
			if !newAppDomains[inst.Domain] {
				s.appMgr.Unregister(inst.Domain)
				s.logger.Info("unregistered removed app", "domain", inst.Domain)
			}
		}
		// Register new app domains
		for _, d := range newCfg.Domains {
			if d.Type == "app" && s.appMgr.Get(d.Host) == nil {
				if err := s.appMgr.Register(d.Host, d.App, d.Root); err == nil {
					s.appMgr.Start(d.Host)
				}
			}
		}
	}

	// Update stored config under write lock
	s.configMu.Lock()
	s.config = newCfg
	s.configMu.Unlock()

	s.logger.Info("config reloaded", "domains", len(newCfg.Domains))
	return nil
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
	if s.h3srv != nil {
		if err := s.h3srv.Close(); err != nil {
			s.logger.Warn("http/3 shutdown error", "error", err)
		}
	}

	// Gracefully shut down the admin API server.
	if s.admin != nil {
		if srv := s.admin.HTTPServer(); srv != nil {
			if err := srv.Shutdown(ctx); err != nil {
				s.logger.Warn("admin shutdown error", "error", err)
			}
		}
	}

	// Stop all app processes (Node.js, Python, etc.)
	if s.appMgr != nil {
		s.appMgr.StopAll()
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
		s.sftpSrv.Stop()
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

	switch domain.Type {
	case "static", "php":
		s.handleFileRequest(ctx, domain)
	case "proxy":
		s.handleProxy(ctx, domain)
	case "app":
		s.handleAppProxy(ctx, domain)
	default:
		return nil, 0, nil, fmt.Errorf("ESI: unsupported type: %s", domain.Type)
	}

	result := rec.Result()
	body, _ := io.ReadAll(result.Body)
	result.Body.Close()
	return body, result.StatusCode, result.Header, nil
}

// handleAppProxy proxies the request to the app process listening on its assigned port.
func (s *Server) handleAppProxy(ctx *router.RequestContext, domain *config.Domain) {
	if s.appMgr == nil {
		renderDomainError(ctx.Response, http.StatusBadGateway, domain)
		return
	}
	addr := s.appMgr.ListenAddr(domain.Host)
	if addr == "" {
		renderDomainError(ctx.Response, http.StatusBadGateway, domain)
		return
	}
	// Build a temporary proxy config pointing to the app's port
	proxyDomain := *domain
	proxyDomain.Type = "proxy"
	proxyDomain.Proxy = config.ProxyConfig{
		Upstreams: []config.Upstream{{Address: "http://" + addr, Weight: 1}},
	}
	s.handleProxy(ctx, &proxyDomain)
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
			phpMgr.RegisterExistingDomain(d.Host, defaultVer, d.PHP.FPMAddress, d.Root)
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

// matchPath checks if a URL path matches a regex pattern from cache rules.
func matchPath(path, pattern string) bool {
	matched, err := regexp.MatchString(pattern, path)
	return err == nil && matched
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
}

// matchLocation checks if a URL path matches a location pattern.
// Prefix match: "/api/" matches "/api/users"
// Regex match (prefix ~): "~\\.php$" matches "/index.php"
func matchLocation(path, pattern string) bool {
	if strings.HasPrefix(pattern, "~") {
		// Regex match
		re, err := regexp.Compile(strings.TrimSpace(pattern[1:]))
		if err != nil {
			return false
		}
		return re.MatchString(path)
	}
	// Prefix match
	return strings.HasPrefix(path, pattern)
}

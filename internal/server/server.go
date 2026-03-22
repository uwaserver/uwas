package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/uwaserver/uwas/internal/admin"
	"github.com/uwaserver/uwas/internal/analytics"
	"github.com/uwaserver/uwas/internal/build"
	"github.com/uwaserver/uwas/internal/cache"
	"github.com/uwaserver/uwas/internal/config"
	fcgihandler "github.com/uwaserver/uwas/internal/handler/fastcgi"
	proxyhandler "github.com/uwaserver/uwas/internal/handler/proxy"
	"github.com/uwaserver/uwas/internal/handler/static"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/mcp"
	"github.com/uwaserver/uwas/internal/metrics"
	"github.com/uwaserver/uwas/internal/middleware"
	"github.com/uwaserver/uwas/internal/monitor"
	"github.com/uwaserver/uwas/internal/rewrite"
	"github.com/uwaserver/uwas/internal/router"
	uwastls "github.com/uwaserver/uwas/internal/tls"
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
	cache      *cache.Engine
	metrics    *metrics.Collector
	analytics  *analytics.Collector
	admin      *admin.Server
	mcp        *mcp.Server
	monitor    *monitor.Monitor
	handler    http.Handler // compiled middleware chain
	httpSrv    *http.Server
	httpsSrv   *http.Server
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup

	proxyPools     map[string]*proxyhandler.UpstreamPool
	proxyBalancers map[string]proxyhandler.Balancer

	// htaccessCache caches parsed .htaccess rewrite rules keyed by domain root.
	// Invalidated on config reload.
	htaccessCacheMu sync.RWMutex
	htaccessCache   map[string][]*rewrite.Rule

	// rewriteCache caches pre-compiled rewrite rules keyed by domain host.
	// Invalidated on config reload.
	rewriteCache map[string]*rewrite.Engine
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
	}

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
		ctx:            ctx,
		cancel:         cancel,
		proxyPools:     make(map[string]*proxyhandler.UpstreamPool),
		proxyBalancers: make(map[string]proxyhandler.Balancer),
		htaccessCache:  make(map[string][]*rewrite.Rule),
		rewriteCache:   make(map[string]*rewrite.Engine),
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
	}

	// Uptime monitor
	s.monitor = monitor.New(cfg.Domains, log)
	if s.admin != nil {
		s.admin.SetMonitor(s.monitor)
	}

	// MCP server
	if cfg.Global.MCP.Enabled {
		s.mcp = mcp.New(cfg, log, m)
		if cacheEngine != nil {
			s.mcp.SetCache(cacheEngine)
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
	}

	// Build middleware chain with all middleware
	s.handler = s.buildMiddlewareChain()

	return s
}

// SetConfigPath stores the config file path for reload support.
func (s *Server) SetConfigPath(path string) {
	s.configPath = path
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
		mws = append(mws, middleware.SecurityGuard(s.logger, blockedPaths, wafEnabled))
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

	if err := s.writePID(); err != nil {
		s.logger.Warn("failed to write pid file", "error", err)
	}

	s.tlsMgr.LoadExistingCerts()
	s.tlsMgr.LoadManualCerts()

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
		go s.tlsMgr.ObtainCerts(s.ctx)
		s.tlsMgr.StartRenewal(s.ctx)
	}

	// Uptime monitor
	if s.monitor != nil {
		go s.monitor.Start(s.ctx)
	}

	// Admin API
	if s.admin != nil {
		go func() {
			if err := s.admin.Start(); err != nil && err != http.ErrServerClosed {
				s.logger.Error("admin API error", "error", err)
			}
		}()
	}

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
		Handler:      http.HandlerFunc(s.handleHTTP),
		ReadTimeout:  s.config.Global.Timeouts.Read.Duration,
		WriteTimeout: s.config.Global.Timeouts.Write.Duration,
		IdleTimeout:  s.config.Global.Timeouts.Idle.Duration,
		ErrorLog:     s.logger.StdLogger(),
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
		Handler:      s.handler,
		TLSConfig:    tlsCfg,
		ReadTimeout:  s.config.Global.Timeouts.Read.Duration,
		WriteTimeout: s.config.Global.Timeouts.Write.Duration,
		IdleTimeout:  s.config.Global.Timeouts.Idle.Duration,
		ErrorLog:     s.logger.StdLogger(),
	}

	ln, err := tls.Listen("tcp", addr, tlsCfg)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}

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
func (s *Server) handleHTTP(w http.ResponseWriter, r *http.Request) {
	if s.tlsMgr.HandleHTTPChallenge(w, r) {
		return
	}

	domain := s.vhosts.Lookup(r.Host)
	if domain != nil && (domain.SSL.Mode == "auto" || domain.SSL.Mode == "manual") {
		target := "https://" + domain.Host + r.URL.RequestURI()
		w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		http.Redirect(w, r, target, http.StatusMovedPermanently)
		return
	}

	s.handler.ServeHTTP(w, r)
}

// handleRequest is the core dispatch handler called after the middleware chain.
func (s *Server) handleRequest(w http.ResponseWriter, r *http.Request) {
	// Health check on main port (no auth, fast path)
	if r.URL.Path == "/.well-known/health" || r.URL.Path == "/healthz" {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
		return
	}

	ctx := router.AcquireContext(w, r)
	defer router.ReleaseContext(ctx)

	ctx.Response.Header().Set("Server", "UWAS/"+build.Version)

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
		s.metrics.BytesSent.Add(ctx.Response.BytesWritten())

		// Record to admin log ring buffer
		if s.admin != nil {
			s.admin.RecordLog(admin.LogEntry{
				Time:       start,
				Host:       r.Host,
				Method:     r.Method,
				Path:       r.URL.Path,
				Status:     ctx.Response.StatusCode(),
				Duration:   time.Since(start).String(),
				RemoteAddr: r.RemoteAddr,
			})
		}

		// Record analytics
		if s.analytics != nil {
			s.analytics.Record(r.Host, r.URL.Path, r.RemoteAddr, ctx.Response.StatusCode(), ctx.Response.BytesWritten())
		}
	}()

	// Virtual host lookup
	domain := s.vhosts.Lookup(r.Host)
	if domain == nil {
		renderErrorPage(ctx.Response, http.StatusNotFound)
		return
	}
	ctx.VHostName = domain.Host
	ctx.DocumentRoot = domain.Root

	// Per-domain blocked paths
	for _, blocked := range domain.Security.BlockedPaths {
		if strings.Contains(r.URL.Path, blocked) {
			renderErrorPage(ctx.Response, http.StatusForbidden)
			return
		}
	}

	// .htaccess import (runtime parse)
	if domain.Htaccess.Mode == "import" && domain.Root != "" {
		s.applyHtaccess(ctx, domain)
	}

	// Rewrite engine (from YAML config)
	if len(domain.Rewrites) > 0 {
		if s.applyRewrites(ctx, domain) {
			return
		}
	}

	// Cache lookup — check global bypass + per-domain bypass rules
	cacheEnabled := s.cache != nil && domain.Cache.Enabled && !cache.ShouldBypass(r)
	if cacheEnabled {
		// Check per-domain cache bypass rules
		for _, rule := range domain.Cache.Rules {
			if rule.Bypass && matchPath(r.URL.Path, rule.Match) {
				cacheEnabled = false
				break
			}
		}
		// Bypass cache if request has session cookies (WordPress, PHP sessions)
		if cookie := r.Header.Get("Cookie"); cookie != "" {
			if strings.Contains(cookie, "wordpress_logged_in") ||
				strings.Contains(cookie, "wp-settings") ||
				strings.Contains(cookie, "PHPSESSID") {
				cacheEnabled = false
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

			ctx.Response.WriteHeader(cached.StatusCode)
			ctx.Response.Write(cached.Body)
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
		default:
			renderErrorPage(ctx.Response, http.StatusInternalServerError)
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
			s.cache.Set(r, &cache.CachedResponse{
				StatusCode: ctx.Response.StatusCode(),
				Headers:    hdrs,
				Body:       capture.body.Bytes(),
				Created:    time.Now(),
				TTL:        ttl,
				Tags:       domain.Cache.Tags,
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
		default:
			renderErrorPage(ctx.Response, http.StatusInternalServerError)
		}
	}
}

func (s *Server) handleFileRequest(ctx *router.RequestContext, domain *config.Domain) {
	// Check if the raw path points to a directory (for directory listing)
	if domain.DirectoryListing && domain.Root != "" {
		rawPath := filepath.Join(domain.Root, filepath.Clean("/"+ctx.Request.URL.Path))
		if info, err := os.Stat(rawPath); err == nil && info.IsDir() {
			static.ServeDirListing(ctx, rawPath, ctx.Request.URL.Path)
			return
		}
	}

	if !static.ResolveRequest(ctx, domain) {
		renderErrorPage(ctx.Response, http.StatusNotFound)
		return
	}

	resolved := ctx.ResolvedPath

	info, err := os.Stat(resolved)
	if err != nil {
		renderErrorPage(ctx.Response, http.StatusNotFound)
		return
	}

	if info.IsDir() {
		renderErrorPage(ctx.Response, http.StatusForbidden)
		return
	}

	if domain.Type == "php" && strings.HasSuffix(resolved, ".php") {
		s.php.Serve(ctx, domain)
		return
	}

	s.static.Serve(ctx)
}

func (s *Server) handleProxy(ctx *router.RequestContext, domain *config.Domain) {
	pool := s.proxyPools[domain.Host]
	if pool == nil {
		renderErrorPage(ctx.Response, http.StatusBadGateway)
		return
	}

	balancer := s.proxyBalancers[domain.Host]
	if balancer == nil {
		balancer = proxyhandler.NewBalancer("round_robin")
	}

	s.proxy.Serve(ctx, domain, pool, balancer)
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
		renderErrorPage(ctx.Response, http.StatusForbidden)
		return true
	}
	if result.Gone {
		renderErrorPage(ctx.Response, http.StatusGone)
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
	rules := s.getHtaccessRules(domain.Root)
	if len(rules) == 0 {
		return
	}

	engine := rewrite.NewEngine(rules)
	// Construct filesystem path for -f/-d condition checks
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

// getHtaccessRules returns cached htaccess rules for the given root, parsing
// the .htaccess file on first access.
func (s *Server) getHtaccessRules(root string) []*rewrite.Rule {
	s.htaccessCacheMu.RLock()
	if rules, ok := s.htaccessCache[root]; ok {
		s.htaccessCacheMu.RUnlock()
		return rules
	}
	s.htaccessCacheMu.RUnlock()

	// Parse .htaccess and cache the result (including nil for missing files).
	rules := s.parseHtaccess(root)

	s.htaccessCacheMu.Lock()
	s.htaccessCache[root] = rules
	s.htaccessCacheMu.Unlock()

	return rules
}

// parseHtaccess reads and parses .htaccess rules from the given document root.
func (s *Server) parseHtaccess(root string) []*rewrite.Rule {
	htPath := filepath.Join(root, ".htaccess")
	f, err := os.Open(htPath)
	if err != nil {
		return nil // no .htaccess file, skip silently
	}
	defer f.Close()

	directives, err := htaccess.Parse(f)
	if err != nil {
		s.logger.Warn("htaccess parse error", "path", htPath, "error", err)
		return nil
	}

	ruleSet := htaccess.Convert(directives)
	if !ruleSet.RewriteEnabled || len(ruleSet.Rewrites) == 0 {
		return nil
	}

	var rules []*rewrite.Rule
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
		rules = append(rules, rule)
	}

	return rules
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

	// Invalidate htaccess cache
	s.htaccessCacheMu.Lock()
	s.htaccessCache = make(map[string][]*rewrite.Rule)
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
	ctx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()

	if s.httpSrv != nil {
		s.httpSrv.Shutdown(ctx)
	}
	if s.httpsSrv != nil {
		s.httpsSrv.Shutdown(ctx)
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

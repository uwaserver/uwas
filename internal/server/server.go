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
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/uwaserver/uwas/internal/admin"
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
	"github.com/uwaserver/uwas/internal/rewrite"
	"github.com/uwaserver/uwas/internal/router"
	uwastls "github.com/uwaserver/uwas/internal/tls"
)

type Server struct {
	config   *config.Config
	logger   *logger.Logger
	vhosts   *router.VHostRouter
	static   *static.Handler
	php      *fcgihandler.Handler
	proxy    *proxyhandler.Handler
	tlsMgr   *uwastls.Manager
	cache    *cache.Engine
	metrics  *metrics.Collector
	admin    *admin.Server
	mcp      *mcp.Server
	handler  http.Handler // compiled middleware chain
	httpSrv  *http.Server
	httpsSrv *http.Server
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup

	// Proxy state per domain
	proxyPools    map[string]*proxyhandler.UpstreamPool
	proxyBalancers map[string]proxyhandler.Balancer
}

func New(cfg *config.Config, log *logger.Logger) *Server {
	ctx, cancel := context.WithCancel(context.Background())

	m := metrics.New()

	// Initialize cache engine
	var cacheEngine *cache.Engine
	if cfg.Global.Cache.Enabled {
		cacheEngine = cache.NewEngine(
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
		ctx:            ctx,
		cancel:         cancel,
		proxyPools:     make(map[string]*proxyhandler.UpstreamPool),
		proxyBalancers: make(map[string]proxyhandler.Balancer),
	}

	// Initialize admin API
	if cfg.Global.Admin.Enabled {
		s.admin = admin.New(cfg, log, m)
	}

	// Initialize MCP server
	if cfg.Global.MCP.Enabled {
		s.mcp = mcp.New(cfg, log, m)
	}

	// Initialize proxy pools for proxy-type domains
	for _, d := range cfg.Domains {
		if d.Type != "proxy" || len(d.Proxy.Upstreams) == 0 {
			continue
		}
		var upstreams []proxyhandler.UpstreamConfig
		for _, u := range d.Proxy.Upstreams {
			upstreams = append(upstreams, proxyhandler.UpstreamConfig{
				Address: u.Address,
				Weight:  u.Weight,
			})
		}
		s.proxyPools[d.Host] = proxyhandler.NewUpstreamPool(upstreams)
		s.proxyBalancers[d.Host] = proxyhandler.NewBalancer(d.Proxy.Algorithm)

		// Start health checking
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

	// Build middleware chain
	s.handler = s.buildMiddlewareChain()

	return s
}

func (s *Server) buildMiddlewareChain() http.Handler {
	chain := middleware.Chain(
		middleware.Recovery(s.logger),
		middleware.RequestID(),
		middleware.SecurityHeaders(),
		middleware.AccessLog(s.logger),
	)
	return chain(http.HandlerFunc(s.handleRequest))
}

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

	// TLS
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

	// Admin API
	if s.admin != nil {
		go func() {
			if err := s.admin.Start(); err != nil && err != http.ErrServerClosed {
				s.logger.Error("admin API error", "error", err)
			}
		}()
	}

	// Block until shutdown
	<-s.ctx.Done()
	s.shutdown()
	s.wg.Wait()
	s.removePID()
	s.logger.Info("UWAS stopped")
	return nil
}

func (s *Server) startHTTP() error {
	addr := ":80"
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
	addr := ":443"
	tlsCfg := s.tlsMgr.TLSConfig()

	s.httpsSrv = &http.Server{
		Handler:      s.handler, // full middleware chain
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

func (s *Server) handleHTTP(w http.ResponseWriter, r *http.Request) {
	if s.tlsMgr.HandleHTTPChallenge(w, r) {
		return
	}

	domain := s.vhosts.Lookup(r.Host)
	if domain != nil && (domain.SSL.Mode == "auto" || domain.SSL.Mode == "manual") {
		target := "https://" + r.Host + r.URL.RequestURI()
		w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		http.Redirect(w, r, target, http.StatusMovedPermanently)
		return
	}

	// Serve via middleware chain for non-SSL domains
	s.handler.ServeHTTP(w, r)
}

func (s *Server) handleRequest(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	ctx := router.AcquireContext(w, r)
	defer router.ReleaseContext(ctx)

	ctx.Response.Header().Set("Server", "UWAS/"+build.Version)

	if r.TLS != nil {
		ctx.IsHTTPS = true
	}

	// Metrics
	s.metrics.ActiveConns.Add(1)
	defer func() {
		s.metrics.ActiveConns.Add(-1)
		s.metrics.RequestsTotal.Add(1)
		s.metrics.RecordRequest(ctx.Response.StatusCode())
		s.metrics.BytesSent.Add(ctx.Response.BytesWritten())
		_ = start
	}()

	// Virtual host lookup
	domain := s.vhosts.Lookup(r.Host)
	if domain == nil {
		renderErrorPage(ctx.Response, http.StatusNotFound)
		return
	}
	ctx.VHostName = domain.Host
	ctx.DocumentRoot = domain.Root

	// Security guard: blocked paths
	for _, blocked := range domain.Security.BlockedPaths {
		if strings.Contains(r.URL.Path, blocked) {
			renderErrorPage(ctx.Response, http.StatusForbidden)
			return
		}
	}

	// Apply rewrite rules
	if len(domain.Rewrites) > 0 {
		if s.applyRewrites(ctx, domain) {
			return
		}
	}

	// Cache lookup (GET/HEAD only)
	if s.cache != nil && domain.Cache.Enabled && !cache.ShouldBypass(r) {
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
			ctx.Response.WriteHeader(cached.StatusCode)
			ctx.Response.Write(cached.Body)
			return
		}
		ctx.CacheStatus = cache.StatusMiss
		s.metrics.RecordCache(cache.StatusMiss)
	}

	// Dispatch
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

func (s *Server) handleFileRequest(ctx *router.RequestContext, domain *config.Domain) {
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
	pool, ok := s.proxyPools[domain.Host]
	if !ok || pool == nil {
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
	var configRewrites []rewrite.ConfigRewrite
	for _, rw := range domain.Rewrites {
		configRewrites = append(configRewrites, rewrite.ConfigRewrite{
			Match:      rw.Match,
			To:         rw.To,
			Status:     rw.Status,
			Conditions: rw.Conditions,
			Flags:      rw.Flags,
		})
	}

	rules := rewrite.ConvertConfigRewrites(configRewrites)
	if len(rules) == 0 {
		return false
	}

	engine := rewrite.NewEngine(rules)
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

func (s *Server) handleSignals() {
	defer s.wg.Done()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		s.logger.Info("received signal, shutting down", "signal", sig)
		s.cancel()
	case <-s.ctx.Done():
	}
}

func (s *Server) shutdown() {
	grace := s.config.Global.Timeouts.ShutdownGrace.Duration
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), grace)
	defer shutdownCancel()

	if s.httpSrv != nil {
		s.httpSrv.Shutdown(shutdownCtx)
	}
	if s.httpsSrv != nil {
		s.httpsSrv.Shutdown(shutdownCtx)
	}
}

func (s *Server) writePID() error {
	pidFile := s.config.Global.PIDFile
	if pidFile == "" {
		return nil
	}
	dir := filepath.Dir(pidFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	return os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0644)
}

func (s *Server) removePID() {
	pidFile := s.config.Global.PIDFile
	if pidFile == "" {
		return
	}
	os.Remove(pidFile)
}

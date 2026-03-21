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

	"github.com/uwaserver/uwas/internal/build"
	"github.com/uwaserver/uwas/internal/config"
	fcgihandler "github.com/uwaserver/uwas/internal/handler/fastcgi"
	"github.com/uwaserver/uwas/internal/handler/static"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/rewrite"
	"github.com/uwaserver/uwas/internal/router"
	uwastls "github.com/uwaserver/uwas/internal/tls"
)

type Server struct {
	config    *config.Config
	logger    *logger.Logger
	vhosts    *router.VHostRouter
	static    *static.Handler
	php       *fcgihandler.Handler
	tlsMgr    *uwastls.Manager
	httpSrv   *http.Server
	httpsSrv  *http.Server
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

func New(cfg *config.Config, log *logger.Logger) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		config: cfg,
		logger: log,
		vhosts: router.NewVHostRouter(cfg.Domains),
		static: static.New(),
		php:    fcgihandler.New(log),
		tlsMgr: uwastls.NewManager(cfg.Global.ACME, cfg.Domains, log),
		ctx:    ctx,
		cancel: cancel,
	}
}

func (s *Server) Start() error {
	// Worker count
	workers := runtime.NumCPU()
	if s.config.Global.WorkerCount != "auto" {
		if n, err := strconv.Atoi(s.config.Global.WorkerCount); err == nil && n > 0 {
			workers = n
		}
	}
	runtime.GOMAXPROCS(workers)

	// Write PID file
	if err := s.writePID(); err != nil {
		s.logger.Warn("failed to write pid file", "error", err)
	}

	// Load existing certificates
	s.tlsMgr.LoadExistingCerts()
	s.tlsMgr.LoadManualCerts()

	// Check if any domain uses SSL
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
	)

	// Signal handling
	s.wg.Add(1)
	go s.handleSignals()

	// Start HTTP listener (:80)
	if err := s.startHTTP(); err != nil {
		return err
	}

	// Start HTTPS listener (:443) if any domain uses SSL
	if hasSSL {
		if err := s.startHTTPS(); err != nil {
			return err
		}

		// Obtain ACME certs for auto-SSL domains
		go s.tlsMgr.ObtainCerts(s.ctx)

		// Start renewal checker
		s.tlsMgr.StartRenewal(s.ctx)
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
		Handler:      http.HandlerFunc(s.handleRequest),
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

// handleHTTP handles port 80 requests:
// 1. ACME HTTP-01 challenge
// 2. Redirect to HTTPS (if SSL enabled for host)
// 3. Serve normally (if SSL is off for host)
func (s *Server) handleHTTP(w http.ResponseWriter, r *http.Request) {
	// ACME challenge check
	if s.tlsMgr.HandleHTTPChallenge(w, r) {
		return
	}

	// Check if this domain uses SSL → redirect
	domain := s.vhosts.Lookup(r.Host)
	if domain != nil && (domain.SSL.Mode == "auto" || domain.SSL.Mode == "manual") {
		target := "https://" + r.Host + r.URL.RequestURI()
		w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		http.Redirect(w, r, target, http.StatusMovedPermanently)
		return
	}

	// No SSL — serve normally
	s.handleRequest(w, r)
}

func (s *Server) handleRequest(w http.ResponseWriter, r *http.Request) {
	ctx := router.AcquireContext(w, r)
	defer router.ReleaseContext(ctx)

	ctx.Response.Header().Set("Server", "UWAS/"+build.Version)

	// Detect HTTPS
	if r.TLS != nil {
		ctx.IsHTTPS = true
	}

	// Virtual host lookup
	domain := s.vhosts.Lookup(r.Host)
	if domain == nil {
		ctx.Response.Error(http.StatusNotFound, "404 Not Found — no matching host")
		return
	}
	ctx.VHostName = domain.Host
	ctx.DocumentRoot = domain.Root

	// Apply rewrite rules (from YAML config)
	if len(domain.Rewrites) > 0 {
		if s.applyRewrites(ctx, domain) {
			return // redirect or forbidden already sent
		}
	}

	// Dispatch based on domain type
	switch domain.Type {
	case "redirect":
		s.handleRedirect(ctx, domain)
	case "static", "php":
		s.handleFileRequest(ctx, domain)
	case "proxy":
		ctx.Response.Error(http.StatusBadGateway, "502 — proxy not yet implemented")
	default:
		ctx.Response.Error(http.StatusInternalServerError, "500 — unknown domain type")
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
		ctx.Response.Error(http.StatusNotFound, "404 Not Found")
		return
	}

	if info.IsDir() {
		ctx.Response.Error(http.StatusForbidden, "403 Forbidden")
		return
	}

	// PHP files → FastCGI
	if domain.Type == "php" && strings.HasSuffix(resolved, ".php") {
		s.php.Serve(ctx, domain)
		return
	}

	s.static.Serve(ctx)
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
	// Convert config rewrites to engine rules
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
		ctx.Response.Error(http.StatusForbidden, "403 Forbidden")
		return true
	}
	if result.Gone {
		ctx.Response.Error(http.StatusGone, "410 Gone")
		return true
	}
	if result.Redirect {
		http.Redirect(ctx.Response, ctx.Request, result.URI, result.StatusCode)
		return true
	}

	// Update the request URI for downstream handlers
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

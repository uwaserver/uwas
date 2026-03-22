package admin

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/uwaserver/uwas/internal/admin/dashboard"
	"github.com/uwaserver/uwas/internal/alerting"
	"github.com/uwaserver/uwas/internal/build"
	"github.com/uwaserver/uwas/internal/analytics"
	"github.com/uwaserver/uwas/internal/backup"
	"github.com/uwaserver/uwas/internal/cache"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/metrics"
	"github.com/uwaserver/uwas/internal/mcp"
	"github.com/uwaserver/uwas/internal/monitor"
	"github.com/uwaserver/uwas/internal/phpmanager"
)

// ReloadFunc is called when a config reload is requested.
type ReloadFunc func() error

// LogEntry represents a single access log entry stored in the ring buffer.
type LogEntry struct {
	Time       time.Time `json:"time"`
	Host       string    `json:"host"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	Status     int       `json:"status"`
	Duration   string    `json:"duration"`
	RemoteAddr string    `json:"remote_addr"`
}

const maxLogEntries = 1000

// Server is the admin REST API server.
type Server struct {
	config         *config.Config
	configMu       sync.RWMutex
	configPath     string
	logger         *logger.Logger
	metrics        *metrics.Collector
	analytics      *analytics.Collector
	cache          *cache.Engine
	reloadFn       ReloadFunc
	onDomainChange func()
	mux            *http.ServeMux
	httpSrv        *http.Server

	monitor   *monitor.Monitor
	alerter   *alerting.Alerter
	phpMgr    *phpmanager.Manager
	backupMgr *backup.BackupManager
	mcpSrv    *mcp.Server

	logMu      sync.Mutex
	logEntries []LogEntry
	logPos     int
	logFull    bool

	// Audit log ring buffer
	auditMu      sync.Mutex
	auditEntries []AuditEntry
	auditPos     int
	auditFull    bool

	// Rate limiting for auth failures
	rlMu      sync.Mutex
	rateLimit map[string]*rateLimitEntry
	rlDone    chan struct{}
}

func New(cfg *config.Config, log *logger.Logger, m *metrics.Collector) *Server {
	s := &Server{
		config:  cfg,
		logger:  log,
		metrics: m,
		mux:     http.NewServeMux(),
	}
	s.initAudit()
	s.registerRoutes()
	return s
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	s.mux.HandleFunc("GET /api/v1/system", s.handleSystem)
	s.mux.HandleFunc("GET /api/v1/stats", s.handleStats)
	s.mux.HandleFunc("GET /api/v1/domains", s.handleDomains)
	s.mux.HandleFunc("GET /api/v1/config", s.handleConfig)
	s.mux.HandleFunc("POST /api/v1/reload", s.handleReload)
	s.mux.HandleFunc("POST /api/v1/cache/purge", s.handleCachePurge)
	s.mux.HandleFunc("GET /api/v1/cache/stats", s.handleCacheStats)
	s.mux.Handle("GET /api/v1/metrics", s.metrics.Handler())
	s.mux.HandleFunc("POST /api/v1/domains", s.handleAddDomain)
	s.mux.HandleFunc("DELETE /api/v1/domains/{host}", s.handleDeleteDomain)
	s.mux.HandleFunc("PUT /api/v1/domains/{host}", s.handleUpdateDomain)
	s.mux.HandleFunc("GET /api/v1/logs", s.handleLogs)
	s.mux.HandleFunc("GET /api/v1/sse/stats", s.handleSSEStats)
	s.mux.HandleFunc("GET /api/v1/config/export", s.handleConfigExport)
	s.mux.HandleFunc("GET /api/v1/certs", s.handleCerts)
	s.mux.HandleFunc("GET /api/v1/domains/{host}", s.handleDomainDetail)
	s.mux.HandleFunc("GET /api/v1/config/raw", s.handleConfigRawGet)
	s.mux.HandleFunc("PUT /api/v1/config/raw", s.handleConfigRawPut)
	s.mux.HandleFunc("GET /api/v1/config/domains/{host}/raw", s.handleDomainRawGet)
	s.mux.HandleFunc("PUT /api/v1/config/domains/{host}/raw", s.handleDomainRawPut)
	s.mux.HandleFunc("GET /api/v1/monitor", s.handleMonitor)
	s.mux.HandleFunc("GET /api/v1/alerts", s.handleAlerts)

	// PHP manager
	s.mux.HandleFunc("GET /api/v1/php", s.handlePHPList)
	s.mux.HandleFunc("GET /api/v1/php/{version}/config", s.handlePHPConfig)
	s.mux.HandleFunc("PUT /api/v1/php/{version}/config", s.handlePHPConfigUpdate)
	s.mux.HandleFunc("GET /api/v1/php/{version}/extensions", s.handlePHPExtensions)
	s.mux.HandleFunc("POST /api/v1/php/{version}/start", s.handlePHPStart)
	s.mux.HandleFunc("POST /api/v1/php/{version}/stop", s.handlePHPStop)

	// Per-domain PHP instances
	s.mux.HandleFunc("GET /api/v1/php/domains", s.handlePHPDomainsList)
	s.mux.HandleFunc("POST /api/v1/php/domains", s.handlePHPDomainAssign)
	s.mux.HandleFunc("DELETE /api/v1/php/domains/{domain}", s.handlePHPDomainUnassign)
	s.mux.HandleFunc("POST /api/v1/php/domains/{domain}/start", s.handlePHPDomainStart)
	s.mux.HandleFunc("POST /api/v1/php/domains/{domain}/stop", s.handlePHPDomainStop)
	s.mux.HandleFunc("GET /api/v1/php/domains/{domain}/config", s.handlePHPDomainConfigGet)
	s.mux.HandleFunc("PUT /api/v1/php/domains/{domain}/config", s.handlePHPDomainConfigPut)

	// Audit log
	s.mux.HandleFunc("GET /api/v1/audit", s.handleAudit)

	// Backup endpoints
	s.mux.HandleFunc("GET /api/v1/backups", s.handleBackupList)
	s.mux.HandleFunc("POST /api/v1/backups", s.handleBackupCreate)
	s.mux.HandleFunc("POST /api/v1/backups/restore", s.handleBackupRestore)
	s.mux.HandleFunc("DELETE /api/v1/backups/{name}", s.handleBackupDelete)
	s.mux.HandleFunc("GET /api/v1/backups/schedule", s.handleBackupScheduleGet)
	s.mux.HandleFunc("PUT /api/v1/backups/schedule", s.handleBackupSchedulePut)

	// MCP endpoints
	s.mux.HandleFunc("GET /api/v1/mcp/tools", s.handleMCPTools)
	s.mux.HandleFunc("POST /api/v1/mcp/call", s.handleMCPCall)

	// Dashboard UI (embedded SPA)
	distFS, err := fs.Sub(dashboard.Assets, "dist")
	if err == nil {
		s.mux.Handle("/_uwas/dashboard/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Strip prefix to get the file path
			path := strings.TrimPrefix(r.URL.Path, "/_uwas/dashboard/")

			// Try to serve the actual file
			if path != "" {
				if _, err := fs.Stat(distFS, path); err == nil {
					http.StripPrefix("/_uwas/dashboard/", http.FileServer(http.FS(distFS))).ServeHTTP(w, r)
					return
				}
			}

			// SPA fallback: serve index.html for all other routes
			indexData, err := fs.ReadFile(distFS, "index.html")
			if err != nil {
				http.Error(w, "dashboard not found", 500)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(indexData)
		}))
	}
}

// Start starts the admin API server.
func (s *Server) Start() error {
	addr := s.config.Global.Admin.Listen
	if addr == "" {
		addr = "127.0.0.1:9443"
	}

	s.httpSrv = &http.Server{
		Handler:      s.authMiddleware(s.mux),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	s.logger.Info("admin API listening", "address", addr)
	return s.httpSrv.Serve(ln)
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	apiKey := s.config.Global.Admin.APIKey
	if apiKey == "" {
		return next // no auth if no key configured
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// CORS: only allow the dashboard's own origin (or localhost for dev).
		if origin := r.Header.Get("Origin"); origin != "" {
			if isAllowedOrigin(origin, r) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
				w.Header().Set("Vary", "Origin")
			}
		}
		if r.Method == "OPTIONS" {
			w.WriteHeader(204)
			return
		}

		// Rate limiting: check if IP is blocked before auth check.
		ip := requestIP(r)
		if s.checkRateLimit(ip) {
			w.Header().Set("Retry-After", "300")
			jsonError(w, "too many failed attempts, try again later", http.StatusTooManyRequests)
			return
		}
		// Public endpoints: health check and dashboard UI
		if r.URL.Path == "/api/v1/health" || strings.HasPrefix(r.URL.Path, "/_uwas/dashboard") {
			next.ServeHTTP(w, r)
			return
		}
		auth := r.Header.Get("Authorization")
		if auth != "Bearer "+apiKey {
			s.recordAuthFailure(ip)
			jsonError(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	checks := make(map[string]string)
	overallStatus := "ok"

	// Cache health
	if s.cache != nil {
		checks["cache"] = "ok"
	} else {
		checks["cache"] = "disabled"
	}

	// Monitor health
	if s.monitor != nil {
		checks["monitor"] = "ok"
	} else {
		checks["monitor"] = "disabled"
	}

	// Backup manager
	if s.backupMgr != nil {
		checks["backup"] = "ok"
	} else {
		checks["backup"] = "disabled"
	}

	// Domain count
	s.configMu.RLock()
	domainCount := len(s.config.Domains)
	s.configMu.RUnlock()

	resp := map[string]any{
		"status":       overallStatus,
		"uptime":       time.Since(s.metrics.StartTime).String(),
		"uptime_secs":  time.Since(s.metrics.StartTime).Seconds(),
		"domains":      domainCount,
		"requests":     s.metrics.RequestsTotal.Load(),
		"active_conns": s.metrics.ActiveConns.Load(),
		"checks":       checks,
		"version":      build.Version,
	}

	jsonResponse(w, resp)
}

func (s *Server) handleSystem(w http.ResponseWriter, r *http.Request) {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	jsonResponse(w, map[string]any{
		"version":      build.Version,
		"commit":       build.Commit,
		"go_version":   runtime.Version(),
		"os":           runtime.GOOS,
		"arch":         runtime.GOARCH,
		"cpus":         runtime.NumCPU(),
		"goroutines":   runtime.NumGoroutine(),
		"memory_alloc": memStats.Alloc,
		"memory_sys":   memStats.Sys,
		"gc_cycles":    memStats.NumGC,
	})
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	p50, p95, p99, max := s.metrics.Percentiles()
	jsonResponse(w, map[string]any{
		"requests_total":   s.metrics.RequestsTotal.Load(),
		"cache_hits":       s.metrics.CacheHits.Load(),
		"cache_misses":     s.metrics.CacheMisses.Load(),
		"active_conns":     s.metrics.ActiveConns.Load(),
		"bytes_sent":       s.metrics.BytesSent.Load(),
		"uptime":           time.Since(s.metrics.StartTime).String(),
		"slow_requests":    s.metrics.SlowRequests.Load(),
		"latency_p50_ms":   p50 * 1000,
		"latency_p95_ms":   p95 * 1000,
		"latency_p99_ms":   p99 * 1000,
		"latency_max_ms":   max * 1000,
	})
}

func (s *Server) handleDomains(w http.ResponseWriter, r *http.Request) {
	type domainInfo struct {
		Host    string   `json:"host"`
		Aliases []string `json:"aliases"`
		Type    string   `json:"type"`
		SSL     string   `json:"ssl"`
		Root    string   `json:"root,omitempty"`
	}

	s.configMu.RLock()
	var domains []domainInfo
	for _, d := range s.config.Domains {
		domains = append(domains, domainInfo{
			Host:    d.Host,
			Aliases: d.Aliases,
			Type:    d.Type,
			SSL:     d.SSL.Mode,
			Root:    d.Root,
		})
	}
	s.configMu.RUnlock()
	jsonResponse(w, domains)
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	// Return sanitized config (no secrets)
	jsonResponse(w, map[string]any{
		"global": map[string]any{
			"worker_count":    s.config.Global.WorkerCount,
			"max_connections": s.config.Global.MaxConnections,
			"log_level":       s.config.Global.LogLevel,
			"log_format":      s.config.Global.LogFormat,
		},
		"domain_count": len(s.config.Domains),
	})
}

// SetCache sets the cache engine for purge operations.
func (s *Server) SetCache(c *cache.Engine) { s.cache = c }

// SetAnalytics sets the analytics collector and registers analytics routes.
func (s *Server) SetAnalytics(a *analytics.Collector) {
	s.analytics = a
	allHandler, hostHandler := a.Handler()
	s.mux.HandleFunc("GET /api/v1/analytics", allHandler)
	s.mux.HandleFunc("GET /api/v1/analytics/{host}", hostHandler)
}

// Analytics returns the analytics collector, if set.
func (s *Server) Analytics() *analytics.Collector { return s.analytics }

// SetMonitor sets the uptime monitor for the /api/v1/monitor endpoint.
func (s *Server) SetMonitor(m *monitor.Monitor) { s.monitor = m }

func (s *Server) handleMonitor(w http.ResponseWriter, r *http.Request) {
	if s.monitor == nil {
		jsonError(w, "monitor not enabled", http.StatusNotImplemented)
		return
	}
	jsonResponse(w, s.monitor.Results())
}

// SetAlerter sets the alerting engine for the /api/v1/alerts endpoint.
func (s *Server) SetAlerter(a *alerting.Alerter) { s.alerter = a }

func (s *Server) handleAlerts(w http.ResponseWriter, r *http.Request) {
	if s.alerter == nil {
		jsonError(w, "alerting not enabled", http.StatusNotImplemented)
		return
	}
	alerts := s.alerter.Alerts()
	if alerts == nil {
		alerts = []alerting.Alert{}
	}
	jsonResponse(w, alerts)
}

// SetPHPManager sets the PHP manager for the PHP API endpoints and wires up
// the domain change callback so that starting a per-domain PHP instance
// automatically updates the domain's php.fpm_address in the running config.
func (s *Server) SetPHPManager(m *phpmanager.Manager) {
	s.phpMgr = m

	// Auto-wire: when a domain PHP starts, update the running config.
	m.SetDomainChangeFunc(func(domain, fpmAddr string) {
		s.configMu.Lock()
		for i, d := range s.config.Domains {
			if d.Host == domain {
				s.config.Domains[i].PHP.FPMAddress = fpmAddr
				break
			}
		}
		s.configMu.Unlock()
		s.notifyDomainChange()
	})
}

func (s *Server) handlePHPList(w http.ResponseWriter, r *http.Request) {
	if s.phpMgr == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	jsonResponse(w, s.phpMgr.Status())
}

func (s *Server) handlePHPConfig(w http.ResponseWriter, r *http.Request) {
	if s.phpMgr == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	version := r.PathValue("version")
	cfg, err := s.phpMgr.GetConfig(version)
	if err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	jsonResponse(w, cfg)
}

func (s *Server) handlePHPConfigUpdate(w http.ResponseWriter, r *http.Request) {
	if s.phpMgr == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	version := r.PathValue("version")

	var req struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Key == "" {
		jsonError(w, "key is required", http.StatusBadRequest)
		return
	}

	if err := s.phpMgr.SetConfig(version, req.Key, req.Value); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "updated", "key": req.Key, "value": req.Value})
}

func (s *Server) handlePHPExtensions(w http.ResponseWriter, r *http.Request) {
	if s.phpMgr == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	version := r.PathValue("version")
	exts, err := s.phpMgr.GetExtensions(version)
	if err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	jsonResponse(w, exts)
}

func (s *Server) handlePHPStart(w http.ResponseWriter, r *http.Request) {
	if s.phpMgr == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	version := r.PathValue("version")

	var req struct {
		ListenAddr string `json:"listen_addr"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.ListenAddr == "" {
		req.ListenAddr = "127.0.0.1:9000"
	}

	if err := s.phpMgr.StartFPM(version, req.ListenAddr); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "started", "version": version, "listen": req.ListenAddr})
}

func (s *Server) handlePHPStop(w http.ResponseWriter, r *http.Request) {
	if s.phpMgr == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	version := r.PathValue("version")

	if err := s.phpMgr.StopFPM(version); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "stopped", "version": version})
}

// --- Per-domain PHP endpoints ---

func (s *Server) handlePHPDomainsList(w http.ResponseWriter, r *http.Request) {
	if s.phpMgr == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	jsonResponse(w, s.phpMgr.GetDomainInstances())
}

func (s *Server) handlePHPDomainAssign(w http.ResponseWriter, r *http.Request) {
	if s.phpMgr == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	var req struct {
		Domain  string `json:"domain"`
		Version string `json:"version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Domain == "" {
		jsonError(w, "domain is required", http.StatusBadRequest)
		return
	}
	if req.Version == "" {
		jsonError(w, "version is required", http.StatusBadRequest)
		return
	}

	dp, err := s.phpMgr.AssignDomain(req.Domain, req.Version)
	if err != nil {
		jsonError(w, err.Error(), http.StatusConflict)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(dp)
}

func (s *Server) handlePHPDomainUnassign(w http.ResponseWriter, r *http.Request) {
	if s.phpMgr == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	domain := r.PathValue("domain")
	s.phpMgr.UnassignDomain(domain)
	jsonResponse(w, map[string]string{"status": "unassigned", "domain": domain})
}

func (s *Server) handlePHPDomainStart(w http.ResponseWriter, r *http.Request) {
	if s.phpMgr == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	domain := r.PathValue("domain")

	if err := s.phpMgr.StartDomain(domain); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "started", "domain": domain})
}

func (s *Server) handlePHPDomainStop(w http.ResponseWriter, r *http.Request) {
	if s.phpMgr == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	domain := r.PathValue("domain")

	if err := s.phpMgr.StopDomain(domain); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "stopped", "domain": domain})
}

func (s *Server) handlePHPDomainConfigGet(w http.ResponseWriter, r *http.Request) {
	if s.phpMgr == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	domain := r.PathValue("domain")

	cfg := s.phpMgr.GetDomainConfig(domain)
	if cfg == nil {
		jsonError(w, "domain not found or no PHP assignment", http.StatusNotFound)
		return
	}
	jsonResponse(w, cfg)
}

func (s *Server) handlePHPDomainConfigPut(w http.ResponseWriter, r *http.Request) {
	if s.phpMgr == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	domain := r.PathValue("domain")

	var req struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Key == "" {
		jsonError(w, "key is required", http.StatusBadRequest)
		return
	}

	if err := s.phpMgr.SetDomainConfig(domain, req.Key, req.Value); err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	jsonResponse(w, map[string]string{"status": "updated", "domain": domain, "key": req.Key, "value": req.Value})
}

// HTTPServer returns the underlying http.Server for shutdown during upgrades.
func (s *Server) HTTPServer() *http.Server { return s.httpSrv }

// SetReloadFunc sets the callback for config reload.
func (s *Server) SetReloadFunc(fn ReloadFunc) { s.reloadFn = fn }

func (s *Server) handleReload(w http.ResponseWriter, r *http.Request) {
	ip := requestIP(r)
	if s.reloadFn == nil {
		s.RecordAudit("config.reload", "reload not supported", ip, false)
		jsonError(w, "reload not supported", http.StatusNotImplemented)
		return
	}
	if err := s.reloadFn(); err != nil {
		s.RecordAudit("config.reload", "error: "+err.Error(), ip, false)
		jsonError(w, "reload failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.RecordAudit("config.reload", "", ip, true)
	jsonResponse(w, map[string]string{"status": "reloaded"})
}

func (s *Server) handleCachePurge(w http.ResponseWriter, r *http.Request) {
	ip := requestIP(r)
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if s.cache == nil {
		s.RecordAudit("cache.purge", "cache not enabled", ip, false)
		jsonError(w, "cache not enabled", http.StatusNotImplemented)
		return
	}

	var req struct {
		Tag string `json:"tag"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	if req.Tag != "" {
		count := s.cache.PurgeByTag(req.Tag)
		s.RecordAudit("cache.purge", "tag: "+req.Tag, ip, true)
		jsonResponse(w, map[string]any{"status": "purged", "tag": req.Tag, "count": count})
	} else {
		s.cache.PurgeAll()
		s.RecordAudit("cache.purge", "all", ip, true)
		jsonResponse(w, map[string]string{"status": "all purged"})
	}
}

func (s *Server) handleCacheStats(w http.ResponseWriter, r *http.Request) {
	if s.cache == nil {
		jsonResponse(w, map[string]any{
			"enabled":  false,
			"message":  "cache not enabled",
		})
		return
	}

	cacheStats := s.cache.Stats()

	// Per-domain cache info
	s.configMu.RLock()
	var domainCache []map[string]any
	for _, d := range s.config.Domains {
		dc := map[string]any{
			"host":    d.Host,
			"enabled": d.Cache.Enabled,
			"ttl":     d.Cache.TTL,
			"tags":    d.Cache.Tags,
		}
		if len(d.Cache.Rules) > 0 {
			var rules []map[string]any
			for _, rule := range d.Cache.Rules {
				rules = append(rules, map[string]any{
					"match":  rule.Match,
					"ttl":    rule.TTL,
					"bypass": rule.Bypass,
				})
			}
			dc["rules"] = rules
		}
		domainCache = append(domainCache, dc)
	}
	s.configMu.RUnlock()

	total := cacheStats["hits"] + cacheStats["misses"]
	var hitRate float64
	if total > 0 {
		hitRate = float64(cacheStats["hits"]) / float64(total) * 100
	}

	jsonResponse(w, map[string]any{
		"enabled":    true,
		"hits":       cacheStats["hits"],
		"misses":     cacheStats["misses"],
		"stales":     cacheStats["stales"],
		"entries":    cacheStats["entries"],
		"used_bytes": cacheStats["used_bytes"],
		"hit_rate":   fmt.Sprintf("%.1f%%", hitRate),
		"domains":    domainCache,
	})
}

// handleSSEStats streams server stats as Server-Sent Events every second.
func (s *Server) handleSSEStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	// Remove the write deadline so the SSE stream is not killed by WriteTimeout.
	rc := http.NewResponseController(w)
	rc.SetWriteDeadline(time.Time{})

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	// Send an initial event immediately so the client doesn't wait 1s.
	s.writeSSEStats(w, flusher)

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			s.writeSSEStats(w, flusher)
		}
	}
}

func (s *Server) writeSSEStats(w http.ResponseWriter, flusher http.Flusher) {
	stats := map[string]any{
		"requests_total": s.metrics.RequestsTotal.Load(),
		"cache_hits":     s.metrics.CacheHits.Load(),
		"cache_misses":   s.metrics.CacheMisses.Load(),
		"active_conns":   s.metrics.ActiveConns.Load(),
		"bytes_sent":     s.metrics.BytesSent.Load(),
		"uptime":         time.Since(s.metrics.StartTime).String(),
	}
	data, _ := json.Marshal(stats)
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()
}

// handleConfigExport returns the current configuration as a YAML file download.
func (s *Server) handleConfigExport(w http.ResponseWriter, r *http.Request) {
	// Build a sanitized copy: strip secrets.
	s.configMu.RLock()
	export := *s.config
	s.configMu.RUnlock()
	export.Global.Admin.APIKey = ""
	export.Global.ACME.DNSCredentials = nil
	export.Global.Cache.PurgeKey = ""

	// Strip per-domain secrets.
	sanitized := make([]config.Domain, len(export.Domains))
	copy(sanitized, export.Domains)
	for i := range sanitized {
		sanitized[i].PHP.Env = nil
	}
	export.Domains = sanitized

	out, err := yaml.Marshal(&export)
	if err != nil {
		jsonError(w, "failed to marshal config: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-yaml")
	w.Header().Set("Content-Disposition", "attachment; filename=uwas.yaml")
	w.Write(out)
}

func jsonResponse(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// --- Domain CRUD ---

// SetOnDomainChange sets a callback invoked after domain add/update/delete.
func (s *Server) SetOnDomainChange(fn func()) { s.onDomainChange = fn }

func (s *Server) notifyDomainChange() {
	if s.onDomainChange != nil {
		s.onDomainChange()
	}
}

func (s *Server) handleAddDomain(w http.ResponseWriter, r *http.Request) {
	ip := requestIP(r)
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var d config.Domain
	if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if d.Host == "" {
		jsonError(w, "host is required", http.StatusBadRequest)
		return
	}

	s.configMu.Lock()
	// Check for duplicates.
	for _, existing := range s.config.Domains {
		if existing.Host == d.Host {
			s.configMu.Unlock()
			s.RecordAudit("domain.create", "domain: "+d.Host+" (duplicate)", ip, false)
			jsonError(w, "domain already exists", http.StatusConflict)
			return
		}
	}
	s.config.Domains = append(s.config.Domains, d)
	s.configMu.Unlock()

	s.RecordAudit("domain.create", "domain: "+d.Host, ip, true)
	s.notifyDomainChange()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(d)
}

func (s *Server) handleDeleteDomain(w http.ResponseWriter, r *http.Request) {
	ip := requestIP(r)
	host := r.PathValue("host")

	s.configMu.Lock()
	found := false
	for i, d := range s.config.Domains {
		if d.Host == host {
			s.config.Domains = append(s.config.Domains[:i], s.config.Domains[i+1:]...)
			found = true
			break
		}
	}
	s.configMu.Unlock()

	if !found {
		s.RecordAudit("domain.delete", "domain: "+host+" (not found)", ip, false)
		jsonError(w, "domain not found", http.StatusNotFound)
		return
	}
	s.RecordAudit("domain.delete", "domain: "+host, ip, true)
	s.notifyDomainChange()
	jsonResponse(w, map[string]string{"status": "deleted"})
}

func (s *Server) handleUpdateDomain(w http.ResponseWriter, r *http.Request) {
	ip := requestIP(r)
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	host := r.PathValue("host")
	var d config.Domain
	if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	s.configMu.Lock()
	found := false
	for i, existing := range s.config.Domains {
		if existing.Host == host {
			if d.Host == "" {
				d.Host = host
			}
			s.config.Domains[i] = d
			found = true
			break
		}
	}
	s.configMu.Unlock()

	if !found {
		s.RecordAudit("domain.update", "domain: "+host+" (not found)", ip, false)
		jsonError(w, "domain not found", http.StatusNotFound)
		return
	}
	s.RecordAudit("domain.update", "domain: "+host, ip, true)
	s.notifyDomainChange()
	jsonResponse(w, d)
}

// --- Logs ring buffer ---

// RecordLog appends a log entry to the ring buffer. Safe for concurrent use.
func (s *Server) RecordLog(e LogEntry) {
	s.logMu.Lock()
	defer s.logMu.Unlock()

	if s.logEntries == nil {
		s.logEntries = make([]LogEntry, maxLogEntries)
	}
	s.logEntries[s.logPos] = e
	s.logPos = (s.logPos + 1) % maxLogEntries
	if s.logPos == 0 {
		s.logFull = true
	}
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	s.logMu.Lock()
	defer s.logMu.Unlock()

	const returnLimit = 100

	var count int
	if s.logFull {
		count = maxLogEntries
	} else {
		count = s.logPos
	}
	if count == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]\n"))
		return
	}

	// Collect entries in chronological order (oldest first).
	var start int
	if s.logFull {
		start = s.logPos // oldest entry
	}
	// We only return the most recent `returnLimit` entries.
	skip := 0
	if count > returnLimit {
		skip = count - returnLimit
		count = returnLimit
	}

	result := make([]LogEntry, 0, count)
	for i := 0; i < count+skip; i++ {
		idx := (start + i) % maxLogEntries
		if i >= skip {
			result = append(result, s.logEntries[idx])
		}
	}
	jsonResponse(w, result)
}

// isAllowedOrigin returns true when the Origin header belongs to the
// dashboard itself (same scheme+host as the admin listener) or is a
// localhost address (for local development).
func isAllowedOrigin(origin string, r *http.Request) bool {
	// Allow any localhost origin for dev convenience.
	lower := strings.ToLower(origin)
	if strings.HasPrefix(lower, "http://localhost") ||
		strings.HasPrefix(lower, "https://localhost") ||
		strings.HasPrefix(lower, "http://127.0.0.1") ||
		strings.HasPrefix(lower, "https://127.0.0.1") {
		return true
	}

	// Allow the dashboard's own origin: derive it from the Host header
	// which is the admin listener itself.
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	dashboardOrigin := scheme + "://" + r.Host
	return origin == dashboardOrigin
}

// --- Certificates ---

func (s *Server) handleCerts(w http.ResponseWriter, r *http.Request) {
	s.configMu.RLock()
	defer s.configMu.RUnlock()

	type certInfo struct {
		Host     string `json:"host"`
		SSLMode  string `json:"ssl_mode"`
		Status   string `json:"status"`  // "active", "pending", "expired", "none"
		Issuer   string `json:"issuer"`
		Expiry   string `json:"expiry"`
		DaysLeft int    `json:"days_left"`
	}

	var certs []certInfo
	for _, d := range s.config.Domains {
		ci := certInfo{
			Host:    d.Host,
			SSLMode: d.SSL.Mode,
		}
		switch d.SSL.Mode {
		case "off":
			ci.Status = "none"
		case "auto":
			ci.Status = "pending"
			ci.Issuer = "Let's Encrypt"
		case "manual":
			ci.Status = "active"
			ci.Issuer = "Manual"
		}
		certs = append(certs, ci)
	}
	jsonResponse(w, certs)
}

// --- Domain detail ---

func (s *Server) handleDomainDetail(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")
	s.configMu.RLock()
	defer s.configMu.RUnlock()

	for _, d := range s.config.Domains {
		if d.Host == host {
			jsonResponse(w, d)
			return
		}
	}
	jsonError(w, "domain not found", http.StatusNotFound)
}

// --- Config path for raw YAML editor ---

// SetConfigPath stores the main config file path so the raw YAML endpoints
// can read/write the file.
func (s *Server) SetConfigPath(path string) { s.configPath = path }

// --- Raw YAML config editor ---

// handleConfigRawGet returns the raw YAML content of the main config file.
func (s *Server) handleConfigRawGet(w http.ResponseWriter, r *http.Request) {
	if s.configPath == "" {
		jsonError(w, "config path not set", http.StatusNotImplemented)
		return
	}

	data, err := os.ReadFile(s.configPath)
	if err != nil {
		jsonError(w, "failed to read config: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-yaml")
	w.Write(data)
}

// handleConfigRawPut validates and writes raw YAML content to the main config
// file, then triggers a reload.
func (s *Server) handleConfigRawPut(w http.ResponseWriter, r *http.Request) {
	if s.configPath == "" {
		jsonError(w, "config path not set", http.StatusNotImplemented)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MB limit
	data, err := io.ReadAll(r.Body)
	if err != nil {
		jsonError(w, "failed to read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Validate YAML syntax.
	var probe config.Config
	if err := yaml.Unmarshal(data, &probe); err != nil {
		jsonError(w, "invalid YAML: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Atomic write: write to temp file, then rename.
	dir := filepath.Dir(s.configPath)
	tmp, err := os.CreateTemp(dir, ".uwas-config-*.yaml")
	if err != nil {
		jsonError(w, "failed to create temp file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		jsonError(w, "failed to write temp file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		jsonError(w, "failed to close temp file: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if err := os.Rename(tmpName, s.configPath); err != nil {
		os.Remove(tmpName)
		jsonError(w, "failed to rename config: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Trigger reload if available.
	if s.reloadFn != nil {
		if err := s.reloadFn(); err != nil {
			// File is already written, report reload failure.
			jsonError(w, "config saved but reload failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	jsonResponse(w, map[string]string{"status": "saved"})
}

// handleDomainRawGet returns the raw YAML content of a single domain file
// from the domains.d/ directory.
func (s *Server) handleDomainRawGet(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")

	// Try reading from domains.d/ file first
	path, err := s.domainFilePath(host)
	if err == nil {
		data, err := os.ReadFile(path)
		if err == nil {
			w.Header().Set("Content-Type", "application/x-yaml")
			w.Write(data)
			return
		}
	}

	// Fallback: generate YAML from the in-memory config for this domain
	s.configMu.RLock()
	var found *config.Domain
	for i := range s.config.Domains {
		if s.config.Domains[i].Host == host {
			d := s.config.Domains[i]
			found = &d
			break
		}
	}
	s.configMu.RUnlock()

	if found == nil {
		jsonError(w, "domain not found", http.StatusNotFound)
		return
	}

	data, err := yaml.Marshal(found)
	if err != nil {
		jsonError(w, "failed to marshal domain config", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-yaml")
	w.Write(data)
}

// handleDomainRawPut validates and writes raw YAML content for a single
// domain file in domains.d/, then triggers a reload.
func (s *Server) handleDomainRawPut(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")

	path, err := s.domainFilePath(host)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MB limit
	data, err := io.ReadAll(r.Body)
	if err != nil {
		jsonError(w, "failed to read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Validate YAML syntax by parsing as a domain.
	var probe config.Domain
	if err := yaml.Unmarshal(data, &probe); err != nil {
		jsonError(w, "invalid YAML: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Ensure the domains.d directory exists.
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		jsonError(w, "failed to create domains directory: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Atomic write: temp file then rename.
	tmp, err := os.CreateTemp(dir, ".uwas-domain-*.yaml")
	if err != nil {
		jsonError(w, "failed to create temp file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		jsonError(w, "failed to write temp file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		jsonError(w, "failed to close temp file: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		jsonError(w, "failed to rename domain file: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Trigger reload if available.
	if s.reloadFn != nil {
		if err := s.reloadFn(); err != nil {
			jsonError(w, "domain saved but reload failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	jsonResponse(w, map[string]string{"status": "saved"})
}

// domainFilePath resolves the on-disk path for a domain's YAML file inside
// the domains.d/ directory adjacent to the main config file.
func (s *Server) domainFilePath(host string) (string, error) {
	if s.configPath == "" {
		return "", fmt.Errorf("config path not set")
	}

	// Reject path traversal characters before any transformation
	if strings.ContainsAny(host, `/\`) || strings.Contains(host, "..") {
		return "", fmt.Errorf("invalid host name")
	}

	// Sanitize host: replace port separator for filesystem safety
	clean := strings.ReplaceAll(host, ":", "_")
	clean = filepath.Base(clean)
	if clean == "." || clean == ".." {
		return "", fmt.Errorf("invalid host name")
	}

	baseDir := filepath.Dir(s.configPath)

	// Use configured domains_dir if present, else default to domains.d/
	s.configMu.RLock()
	domainsDir := s.config.DomainsDir
	s.configMu.RUnlock()

	if domainsDir == "" {
		domainsDir = "domains.d"
	}
	if !filepath.IsAbs(domainsDir) {
		domainsDir = filepath.Join(baseDir, domainsDir)
	}

	return filepath.Join(domainsDir, host+".yaml"), nil
}

// --- MCP ---

// SetMCP sets the MCP server for AI tool management endpoints.
func (s *Server) SetMCP(m *mcp.Server) { s.mcpSrv = m }

func (s *Server) handleMCPTools(w http.ResponseWriter, r *http.Request) {
	if s.mcpSrv == nil {
		jsonError(w, "MCP not enabled", http.StatusServiceUnavailable)
		return
	}
	jsonResponse(w, s.mcpSrv.ListTools())
}

func (s *Server) handleMCPCall(w http.ResponseWriter, r *http.Request) {
	if s.mcpSrv == nil {
		jsonError(w, "MCP not enabled", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	result, err := s.mcpSrv.CallTool(req.Name, req.Input)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	jsonResponse(w, result)
}

// --- Backup ---

// SetBackupManager sets the backup manager for the backup API endpoints.
func (s *Server) SetBackupManager(m *backup.BackupManager) { s.backupMgr = m }

func (s *Server) handleBackupList(w http.ResponseWriter, r *http.Request) {
	if s.backupMgr == nil {
		jsonError(w, "backup not enabled", http.StatusNotImplemented)
		return
	}
	backups := s.backupMgr.ListBackups()
	if backups == nil {
		backups = make([]backup.BackupInfo, 0)
	}
	jsonResponse(w, backups)
}

func (s *Server) handleBackupCreate(w http.ResponseWriter, r *http.Request) {
	ip := requestIP(r)
	if s.backupMgr == nil {
		s.RecordAudit("backup.create", "backup not enabled", ip, false)
		jsonError(w, "backup not enabled", http.StatusNotImplemented)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	var req struct {
		Provider string `json:"provider"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Provider == "" {
		req.Provider = "local"
	}

	info, err := s.backupMgr.CreateBackup(req.Provider)
	if err != nil {
		s.RecordAudit("backup.create", "provider: "+req.Provider+", error: "+err.Error(), ip, false)
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.RecordAudit("backup.create", "provider: "+req.Provider, ip, true)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(info)
}

func (s *Server) handleBackupRestore(w http.ResponseWriter, r *http.Request) {
	ip := requestIP(r)
	if s.backupMgr == nil {
		s.RecordAudit("backup.restore", "backup not enabled", ip, false)
		jsonError(w, "backup not enabled", http.StatusNotImplemented)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	var req struct {
		Name     string `json:"name"`
		Provider string `json:"provider"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		jsonError(w, "name is required", http.StatusBadRequest)
		return
	}
	if req.Provider == "" {
		req.Provider = "local"
	}

	if err := s.backupMgr.RestoreBackup(req.Name, req.Provider); err != nil {
		s.RecordAudit("backup.restore", "name: "+req.Name+", error: "+err.Error(), ip, false)
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.RecordAudit("backup.restore", "name: "+req.Name, ip, true)
	jsonResponse(w, map[string]string{"status": "restored", "name": req.Name})
}

func (s *Server) handleBackupDelete(w http.ResponseWriter, r *http.Request) {
	ip := requestIP(r)
	if s.backupMgr == nil {
		s.RecordAudit("backup.delete", "backup not enabled", ip, false)
		jsonError(w, "backup not enabled", http.StatusNotImplemented)
		return
	}
	name := r.PathValue("name")
	if name == "" {
		jsonError(w, "backup name required", http.StatusBadRequest)
		return
	}
	provider := r.URL.Query().Get("provider")
	if provider == "" {
		provider = "local"
	}

	if err := s.backupMgr.DeleteBackup(name, provider); err != nil {
		s.RecordAudit("backup.delete", "name: "+name+", error: "+err.Error(), ip, false)
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.RecordAudit("backup.delete", "name: "+name, ip, true)
	jsonResponse(w, map[string]string{"status": "deleted", "name": name})
}

func (s *Server) handleBackupScheduleGet(w http.ResponseWriter, r *http.Request) {
	if s.backupMgr == nil {
		jsonError(w, "backup not enabled", http.StatusNotImplemented)
		return
	}
	interval, active := s.backupMgr.ScheduleStatus()
	jsonResponse(w, map[string]any{
		"interval": interval.String(),
		"active":   active,
	})
}

func (s *Server) handleBackupSchedulePut(w http.ResponseWriter, r *http.Request) {
	ip := requestIP(r)
	if s.backupMgr == nil {
		s.RecordAudit("backup.schedule", "backup not enabled", ip, false)
		jsonError(w, "backup not enabled", http.StatusNotImplemented)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	var req struct {
		Interval string `json:"interval"`
		Enabled  *bool  `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	if req.Enabled != nil && !*req.Enabled {
		s.backupMgr.ScheduleBackup(0)
		s.RecordAudit("backup.schedule", "disabled", ip, true)
		jsonResponse(w, map[string]any{"status": "stopped", "active": false})
		return
	}

	if req.Interval == "" {
		jsonError(w, "interval is required", http.StatusBadRequest)
		return
	}
	d, err := time.ParseDuration(req.Interval)
	if err != nil {
		jsonError(w, "invalid interval: "+err.Error(), http.StatusBadRequest)
		return
	}
	if d < time.Minute {
		jsonError(w, "interval must be at least 1m", http.StatusBadRequest)
		return
	}

	s.backupMgr.ScheduleBackup(d)
	s.RecordAudit("backup.schedule", "interval: "+d.String(), ip, true)
	jsonResponse(w, map[string]any{
		"status":   "scheduled",
		"interval": d.String(),
		"active":   true,
	})
}

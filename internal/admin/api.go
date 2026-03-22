package admin

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/uwaserver/uwas/internal/admin/dashboard"
	"github.com/uwaserver/uwas/internal/cache"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/metrics"
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
	logger         *logger.Logger
	metrics        *metrics.Collector
	cache          *cache.Engine
	reloadFn       ReloadFunc
	onDomainChange func()
	mux            *http.ServeMux
	httpSrv        *http.Server

	logMu      sync.Mutex
	logEntries []LogEntry
	logPos     int
	logFull    bool
}

func New(cfg *config.Config, log *logger.Logger, m *metrics.Collector) *Server {
	s := &Server{
		config:  cfg,
		logger:  log,
		metrics: m,
		mux:     http.NewServeMux(),
	}
	s.registerRoutes()
	return s
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("GET /api/v1/health", s.handleHealth)
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
		// Public endpoints: health check and dashboard UI
		if r.URL.Path == "/api/v1/health" || strings.HasPrefix(r.URL.Path, "/_uwas/dashboard") {
			next.ServeHTTP(w, r)
			return
		}
		auth := r.Header.Get("Authorization")
		if auth != "Bearer "+apiKey {
			jsonError(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, map[string]any{
		"status": "ok",
		"uptime": time.Since(s.metrics.StartTime).String(),
	})
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, map[string]any{
		"requests_total":   s.metrics.RequestsTotal.Load(),
		"cache_hits":       s.metrics.CacheHits.Load(),
		"cache_misses":     s.metrics.CacheMisses.Load(),
		"active_conns":     s.metrics.ActiveConns.Load(),
		"bytes_sent":       s.metrics.BytesSent.Load(),
		"uptime":           time.Since(s.metrics.StartTime).String(),
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

// SetReloadFunc sets the callback for config reload.
func (s *Server) SetReloadFunc(fn ReloadFunc) { s.reloadFn = fn }

func (s *Server) handleReload(w http.ResponseWriter, r *http.Request) {
	if s.reloadFn == nil {
		jsonError(w, "reload not supported", http.StatusNotImplemented)
		return
	}
	if err := s.reloadFn(); err != nil {
		jsonError(w, "reload failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "reloaded"})
}

func (s *Server) handleCachePurge(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if s.cache == nil {
		jsonError(w, "cache not enabled", http.StatusNotImplemented)
		return
	}

	var req struct {
		Tag string `json:"tag"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	if req.Tag != "" {
		count := s.cache.PurgeByTag(req.Tag)
		jsonResponse(w, map[string]any{"status": "purged", "tag": req.Tag, "count": count})
	} else {
		s.cache.PurgeAll()
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
			jsonError(w, "domain already exists", http.StatusConflict)
			return
		}
	}
	s.config.Domains = append(s.config.Domains, d)
	s.configMu.Unlock()

	s.notifyDomainChange()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(d)
}

func (s *Server) handleDeleteDomain(w http.ResponseWriter, r *http.Request) {
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
		jsonError(w, "domain not found", http.StatusNotFound)
		return
	}
	s.notifyDomainChange()
	jsonResponse(w, map[string]string{"status": "deleted"})
}

func (s *Server) handleUpdateDomain(w http.ResponseWriter, r *http.Request) {
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
		jsonError(w, "domain not found", http.StatusNotFound)
		return
	}
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

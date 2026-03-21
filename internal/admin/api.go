package admin

import (
	"encoding/json"
	"net"
	"net/http"
	"time"

	"github.com/uwaserver/uwas/internal/cache"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/metrics"
)

// ReloadFunc is called when a config reload is requested.
type ReloadFunc func() error

// Server is the admin REST API server.
type Server struct {
	config   *config.Config
	logger   *logger.Logger
	metrics  *metrics.Collector
	cache    *cache.Engine
	reloadFn ReloadFunc
	mux      *http.ServeMux
	httpSrv  *http.Server
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
	s.mux.Handle("GET /api/v1/metrics", s.metrics.Handler())
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

func jsonResponse(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

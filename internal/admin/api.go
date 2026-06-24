package admin

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

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
	"github.com/uwaserver/uwas/internal/deploy"
	"github.com/uwaserver/uwas/internal/filemanager"
	"github.com/uwaserver/uwas/internal/install"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/mcp"
	"github.com/uwaserver/uwas/internal/metrics"
	"github.com/uwaserver/uwas/internal/middleware"
	"github.com/uwaserver/uwas/internal/monitor"
	"github.com/uwaserver/uwas/internal/phpmanager"
	"github.com/uwaserver/uwas/internal/respond"
	"github.com/uwaserver/uwas/internal/router"
	uwastls "github.com/uwaserver/uwas/internal/tls"
	"github.com/uwaserver/uwas/internal/webhook"
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
	Bytes      int64     `json:"bytes"`
	DurationMS float64   `json:"duration_ms"`
	Duration   string    `json:"duration"`
	RemoteAddr string    `json:"remote_addr"`
	Remote     string    `json:"remote"`
	UserAgent  string    `json:"user_agent,omitempty"`
}

const maxLogEntries = 1000

type muxer interface {
	HandleFunc(string, func(http.ResponseWriter, *http.Request))
	Handle(string, http.Handler)
	ServeHTTP(http.ResponseWriter, *http.Request)
}

// Server is the admin REST API server.
type Server struct {
	config         *config.Config
	configMu       sync.RWMutex
	persistMu      sync.Mutex // serializes persistConfig so concurrent writes can't interleave temp+rename
	configPath     string
	logger         *logger.Logger
	metrics        *metrics.Collector
	analytics      *analytics.Collector
	cache          *cache.Engine
	reloadFn       ReloadFunc
	onDomainChange func()
	mux            muxer
	httpSrvMu      sync.RWMutex
	httpSrv        *http.Server

	monitor       *monitor.Monitor
	alerter       *alerting.Alerter
	phpMgr        *phpmanager.Manager
	appsMgr       *apps.Manager // standalone apps supervisor
	deployMgr     *deploy.Manager
	backupMgr     *backup.BackupManager
	bwMgr         *bandwidth.Manager
	cronMonitor   *cronjob.Monitor
	webhookMgr    *webhook.Manager
	mcpSrv        *mcp.Server
	tlsMgr        *uwastls.Manager
	unknownHT     *router.UnknownHostTracker
	securityStats *middleware.SecurityStats
	cfRunner      *cfintegration.Runner

	// Global installation task manager (serializes apt/dpkg operations).
	// PHP install state lives entirely in taskMgr (queryable via ActiveByType("php")).
	taskMgr *install.Queue

	// In-memory log + audit ring buffers. Both are populated lazily in
	// initAudit / RecordLog. Tests reach into the buffer fields directly.
	logBuf   *ringBuffer[LogEntry]
	auditBuf *ringBuffer[AuditEntry]

	// 2FA pending setup (per-user, keyed by username)
	pendingTOTPMu sync.Mutex
	pendingTOTP   map[string]string

	// Rate limiting for auth failures
	rlMu      sync.Mutex
	rateLimit map[string]*rateLimitEntry
	// Per-username rate limiting (tracks failed logins by username).
	userRateLimits map[string]*rateLimitEntry
	rlDone         chan struct{}

	// Short-lived auth tickets for SSE/WebSocket (avoids token in URL query params).
	ticketMu sync.Mutex
	tickets  map[string]*authTicket

	// Cached expensive system info (apt updates, web root disk usage).
	sysInfoCacheMu    sync.Mutex
	sysInfoCacheTime  time.Time
	sysInfoPkgUpdates string
	sysInfoDiskUsed   int64

	// Auth manager for multi-user support
	authMgr AuthManager
}

// AuthManager interface for authentication (implemented by auth.Manager)
type AuthManager interface {
	Authenticate(username, password string) (*auth.Session, error)
	AuthenticateAPIKey(key string) (*auth.User, error)
	ValidateSession(token string) (*auth.Session, error)
	Logout(token string)
	HasPermission(role auth.Role, perm auth.Permission) bool
	CanManageDomain(user *auth.User, domain string) bool
	GetUser(username string) (*auth.User, bool)
	GetUserByID(id string) (*auth.User, bool)
	ListUsers() []*auth.User
	CreateUser(username, email, password string, role auth.Role, domains []string) (*auth.User, error)
	UpdateUser(username string, updates *auth.User) error
	DeleteUser(username string) error
	RegenerateAPIKey(username string) (string, error)
	ChangePassword(username, currentPassword, newPassword string) error
}

func New(cfg *config.Config, log *logger.Logger, m *metrics.Collector) *Server {
	// Wire the respond package logger so jsonError / jsonErrorCause (and
	// any direct respond.Error callers) surface 5xx context to operators
	// via the same logger the rest of the admin server uses. Refs:
	// refactor.md A10, O6.
	respond.SetLogger(log)
	s := &Server{
		config:  cfg,
		logger:  log,
		metrics: m,
		mux:     http.NewServeMux(),
		taskMgr: install.New(),
	}
	s.cfRunner = cfintegration.NewRunner(log)
	s.initAudit()
	if err := s.loadAuditLog(); err != nil {
		log.Warn("audit log restore failed", "error", err.Error())
	}
	s.registerRoutes()
	if err := s.loadCloudflareState(); err != nil {
		log.Error("cloudflare state load failed", "error", err.Error())
	}
	return s
}

// isExpensiveGET reports whether a GET endpoint has enough side-effect cost
// (full database dump, full config dump, etc.) that an attacker forcing the
// admin's browser to fetch it via an <img>/<iframe> CSRF would constitute a
// real denial-of-service even though the attacker never reads the response.
// The list is path-suffix based — sub-paths (e.g. Docker DB export) match.
func isExpensiveGET(path string) bool {
	switch {
	case strings.HasSuffix(path, "/export"):
		return true
	case strings.HasSuffix(path, "/backup"):
		return true
	case strings.HasSuffix(path, "/download"):
		return true
	}
	return false
}

// isLoopbackListenAddr reports whether the host portion of a "host:port"
// listen address binds only to loopback. It accepts "127.0.0.1", "::1", and
// the literal "localhost". A bare ":port" or "0.0.0.0:port" binds to all
// interfaces and is treated as non-loopback.
func isLoopbackListenAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1", "[::1]":
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// Start starts the admin API server.
func (s *Server) Start() error {
	s.configMu.RLock()
	addr := s.config.Global.Admin.Listen
	apiKey := s.config.Global.Admin.APIKey
	multiUserEnabled := s.config.Global.Users.Enabled
	s.configMu.RUnlock()
	if addr == "" {
		addr = "127.0.0.1:9443"
	}

	// If no credentials are configured, the auth middleware injects a virtual
	// admin user for backward compatibility with first-run setups. That is
	// safe only as long as the listener is bound to loopback — otherwise the
	// entire 221-endpoint API is publicly exposed with no authentication.
	// Refuse to bind in that case and tell the operator how to recover.
	if apiKey == "" && !multiUserEnabled && !isLoopbackListenAddr(addr) {
		return fmt.Errorf(
			"admin API listen address %q exposes the API without authentication; "+
				"either set global.admin.api_key (or enable global.users.enabled) or "+
				"bind to 127.0.0.1 / ::1", addr)
	}
	if apiKey == "" && !multiUserEnabled {
		s.logger.Warn("admin API has no credentials configured — every request will be granted admin role",
			"listen", addr,
			"fix", "set global.admin.api_key or enable global.users.enabled")
	}

	httpSrv := &http.Server{
		Handler:      middleware.RequestID()(s.authMiddleware(requireJSONMiddleware(s.mux))),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 5 * time.Minute, // SSE, DB export, backup can take minutes
	}
	s.httpSrvMu.Lock()
	s.httpSrv = httpSrv
	s.httpSrvMu.Unlock()

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	// Use TLS if certificate is configured
	s.configMu.RLock()
	tlsCert := s.config.Global.Admin.TLSCert
	tlsKey := s.config.Global.Admin.TLSKey
	s.configMu.RUnlock()

	if tlsCert != "" && tlsKey != "" {
		s.logger.Info("admin API listening (TLS)", "address", addr)
		return httpSrv.ServeTLS(ln, tlsCert, tlsKey)
	}

	s.logger.Info("admin API listening", "address", addr)
	return httpSrv.Serve(ln)
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read auth config per-request so config changes take effect without restart
		s.configMu.RLock()
		apiKey := s.config.Global.Admin.APIKey
		multiUserEnabled := s.config.Global.Users.Enabled
		s.configMu.RUnlock()

		// If no auth configured at all, allow all (inject virtual admin for compat)
		if apiKey == "" && !multiUserEnabled {
			user := &auth.User{ID: "local", Username: "admin", Role: auth.RoleAdmin, Enabled: true}
			next.ServeHTTP(w, r.WithContext(auth.WithUser(r.Context(), user)))
			return
		}
		// CORS: only allow the dashboard's own origin (or localhost for dev).
		if origin := r.Header.Get("Origin"); origin != "" {
			if isAllowedOrigin(origin, r) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Session-Token, X-TOTP-Code, X-Pin-Code")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
				w.Header().Add("Vary", "Origin")
			}
		}
		if r.Method == "OPTIONS" {
			w.WriteHeader(204)
			return
		}

		// Public endpoints: health check, dashboard UI, read-only login chrome,
		// deploy webhooks (no auth needed).
		if r.URL.Path == "/api/v1/health" ||
			(r.URL.Path == "/api/v1/settings/branding" && r.Method == "GET") ||
			strings.HasPrefix(r.URL.Path, "/_uwas/dashboard") ||
			(strings.HasPrefix(r.URL.Path, "/api/v1/apps/") && strings.HasSuffix(r.URL.Path, "/webhook") && r.Method == "POST") {
			next.ServeHTTP(w, r)
			return
		}

		// Login endpoint: public but rate-limited
		if r.URL.Path == "/api/v1/auth/login" {
			ip := requestIP(r)
			if s.checkRateLimit(ip, "") {
				w.Header().Set("Retry-After", "300")
				jsonError(w, "too many failed attempts, try again later", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/api/v1/auth/bootstrap" && r.Method == "POST" {
			ip := requestIP(r)
			if s.checkRateLimit(ip, "") {
				w.Header().Set("Retry-After", "300")
				jsonError(w, "too many failed attempts, try again later", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
			return
		}

		// Rate limiting: check if IP is blocked before auth check.
		ip := requestIP(r)
		if s.checkRateLimit(ip, "") {
			w.Header().Set("Retry-After", "300")
			jsonError(w, "too many failed attempts, try again later", http.StatusTooManyRequests)
			return
		}

		var authenticated bool
		var user *auth.User

		// Try multi-user auth first if enabled
		if multiUserEnabled && s.authMgr != nil {
			// Try API key (Authorization: Bearer <key>)
			authHeader := r.Header.Get("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				key := strings.TrimPrefix(authHeader, "Bearer ")
				if u, err := s.authMgr.AuthenticateAPIKey(key); err == nil {
					authenticated = true
					user = u
				}
			}

			// Try session token (X-Session-Token header)
			if !authenticated {
				if token := r.Header.Get("X-Session-Token"); token != "" {
					if session, err := s.authMgr.ValidateSession(token); err == nil {
						if u, exists := s.authMgr.GetUserByID(session.UserID); exists {
							authenticated = true
							user = u
						}
					}
				}
			}

			// Check ticket query param for SSE/WebSocket (short-lived, single-use).
			if !authenticated {
				if ticket := r.URL.Query().Get("ticket"); ticket != "" {
					if realToken := s.redeemTicket(ticket); realToken != "" {
						// Try as session token first
						if session, err := s.authMgr.ValidateSession(realToken); err == nil {
							if u, exists := s.authMgr.GetUserByID(session.UserID); exists {
								authenticated = true
								user = u
							}
						}
						// Try as API key
						if !authenticated {
							if u, err := s.authMgr.AuthenticateAPIKey(realToken); err == nil {
								authenticated = true
								user = u
							}
						}
					}
					if authenticated {
						q := r.URL.Query()
						q.Del("ticket")
						r.URL.RawQuery = q.Encode()
					}
				}
			}
			// Also check token query param for SSE/WebSocket (legacy fallback)
			if !authenticated {
				if token := r.URL.Query().Get("token"); token != "" {
					if session, err := s.authMgr.ValidateSession(token); err == nil {
						if u, exists := s.authMgr.GetUserByID(session.UserID); exists {
							authenticated = true
							user = u
						}
					}
					if !authenticated {
						if u, err := s.authMgr.AuthenticateAPIKey(token); err == nil {
							authenticated = true
							user = u
						}
					}
					if authenticated {
						q := r.URL.Query()
						q.Del("token")
						r.URL.RawQuery = q.Encode()
					}
				}
			}
		}

		// Fall back to legacy API key auth if multi-user auth failed or not enabled
		if !authenticated && apiKey != "" {
			authHeader := r.Header.Get("Authorization")
			// Check ticket query param first (preferred), then legacy token param
			if authHeader == "" {
				if ticket := r.URL.Query().Get("ticket"); ticket != "" {
					if realToken := s.redeemTicket(ticket); realToken != "" {
						authHeader = "Bearer " + realToken
					}
				}
			}
			if authHeader == "" {
				if token := r.URL.Query().Get("token"); token != "" {
					authHeader = "Bearer " + token
				}
			}
			if subtle.ConstantTimeCompare([]byte(authHeader), []byte("Bearer "+apiKey)) == 1 {
				authenticated = true
				// Create a virtual admin user for legacy auth
				user = &auth.User{
					ID:       "admin",
					Username: "admin",
					Role:     auth.RoleAdmin,
					Enabled:  true,
				}
			}
		}

		if !authenticated {
			s.recordAuthFailure(ip, "")
			jsonError(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		// Strip token from URL after successful auth to prevent leaking
		if r.URL.Query().Get("token") != "" {
			q := r.URL.Query()
			q.Del("token")
			r.URL.RawQuery = q.Encode()
		}

		// Store user in context for handlers to access
		ctx := auth.WithUser(r.Context(), user)
		r = r.WithContext(ctx)

		// 2FA check for legacy auth: if TOTP is enabled, require valid code.
		// Skip for multi-user auth (TOTP handled separately) and 2FA management endpoints.
		s.configMu.RLock()
		totpSecret := s.config.Global.Admin.TOTPSecret
		s.configMu.RUnlock()
		if totpSecret != "" && user.Role == auth.RoleAdmin &&
			!strings.HasPrefix(r.URL.Path, "/api/v1/auth/2fa/") {
			totpCode := r.Header.Get("X-TOTP-Code")
			if totpCode == "" {
				w.Header().Set("X-2FA-Required", "true")
				jsonError(w, "2fa_required", http.StatusForbidden)
				return
			}
			valid, _ := ValidateTOTP(totpSecret, totpCode)
			if !valid {
				s.recordAuthFailure(ip, "")
				jsonError(w, "invalid 2FA code", http.StatusForbidden)
				return
			}
		}

		// CSRF protection: state-changing methods must send X-Requested-With
		// or come from the dashboard origin. We also apply the same check to
		// a handful of expensive GET endpoints (database export, config
		// export, etc.) — those are technically reads but a CSRF-triggered
		// request can pin a CPU to a full mysqldump even though the attacker
		// never sees the response body. Treat them as state-changing for
		// CSRF purposes.
		needsCSRFCheck := r.Method == "POST" || r.Method == "PUT" || r.Method == "PATCH" || r.Method == "DELETE"
		if !needsCSRFCheck && r.Method == "GET" && isExpensiveGET(r.URL.Path) {
			needsCSRFCheck = true
		}
		if needsCSRFCheck {
			if r.Header.Get("X-Requested-With") != "XMLHttpRequest" {
				// Also allow if Origin/Referer exactly matches the dashboard origin.
				origin := r.Header.Get("Origin")
				referer := r.Header.Get("Referer")
				sameOrigin := origin != "" && isAllowedOrigin(origin, r)
				if !sameOrigin && referer != "" {
					if u, err := url.Parse(referer); err == nil {
						sameOrigin = isAllowedOrigin(u.Scheme+"://"+u.Host, r)
					}
				}
				if !sameOrigin {
					jsonError(w, "csrf: invalid origin", http.StatusForbidden)
					return
				}
			}
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
		"uptime":       humanDuration(time.Since(s.metrics.StartTime)),
		"uptime_secs":  time.Since(s.metrics.StartTime).Seconds(),
		"domains":      domainCount,
		"requests":     s.metrics.RequestsTotal.Load(),
		"active_conns": s.metrics.ActiveConns.Load(),
		"checks":       checks,
		"version":      build.Version,
	}

	jsonResponse(w, resp)
}

// featureStatus describes whether an optional subsystem is wired up at runtime.
type featureStatus struct {
	Enabled bool   `json:"enabled"`
	Reason  string `json:"reason,omitempty"`
}

// handleFeatures reports which optional subsystems are initialized. Used by
// dashboard pages to show a "feature not enabled" banner instead of a
// misleading empty list. Read-only; does not require admin.
func (s *Server) handleFeatures(w http.ResponseWriter, r *http.Request) {
	disabled := func(reason string) featureStatus { return featureStatus{Enabled: false, Reason: reason} }
	enabled := featureStatus{Enabled: true}

	out := map[string]featureStatus{}

	if s.appsMgr == nil {
		out["apps"] = disabled("App supervisor not initialized")
	} else {
		out["apps"] = enabled
	}
	if s.bwMgr == nil {
		out["bandwidth"] = disabled("Bandwidth manager not initialized — set bandwidth.enabled in uwas.yaml")
	} else {
		out["bandwidth"] = enabled
	}
	if s.cronMonitor == nil {
		out["cron_monitor"] = disabled("Cron monitor not initialized — set cron.enabled in uwas.yaml")
	} else {
		out["cron_monitor"] = enabled
	}
	if s.unknownHT == nil {
		out["unknown_domains"] = disabled("Unknown-host tracker disabled in config")
	} else {
		out["unknown_domains"] = enabled
	}
	if s.securityStats == nil {
		out["security_stats"] = disabled("Security stats collector not initialized")
	} else {
		out["security_stats"] = enabled
	}
	if s.deployMgr == nil {
		out["deploys"] = disabled("Deploy manager not initialized")
	} else {
		out["deploys"] = enabled
	}
	if s.backupMgr == nil {
		out["backups"] = disabled("Backup manager not initialized")
	} else {
		out["backups"] = enabled
	}
	if s.webhookMgr == nil {
		out["webhooks"] = disabled("Webhook manager not initialized")
	} else {
		out["webhooks"] = enabled
	}
	if s.tlsMgr == nil {
		out["tls"] = disabled("TLS manager not initialized")
	} else {
		out["tls"] = enabled
	}
	if s.alerter == nil {
		out["alerting"] = disabled("Alerter not initialized")
	} else {
		out["alerting"] = enabled
	}
	if s.monitor == nil {
		out["uptime_monitor"] = disabled("Uptime monitor not initialized")
	} else {
		out["uptime_monitor"] = enabled
	}
	if s.phpMgr == nil {
		out["php"] = disabled("PHP manager not initialized")
	} else {
		out["php"] = enabled
	}

	jsonResponse(w, out)
}

func (s *Server) handleSystem(w http.ResponseWriter, r *http.Request) {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	hostname, _ := os.Hostname()

	result := map[string]any{
		"version":      build.Version,
		"commit":       build.Commit,
		"go_version":   runtime.Version(),
		"os":           runtime.GOOS,
		"arch":         runtime.GOARCH,
		"hostname":     hostname,
		"cpus":         runtime.NumCPU(),
		"goroutines":   runtime.NumGoroutine(),
		"memory_alloc": memStats.Alloc,
		"memory_sys":   memStats.Sys,
		"gc_cycles":    memStats.NumGC,
		"pid":          os.Getpid(),
		"uptime":       humanDuration(time.Since(s.metrics.StartTime)),
		"uptime_secs":  time.Since(s.metrics.StartTime).Seconds(),
	}

	// OS-level info (Linux)
	if runtime.GOOS == "linux" {
		if data, err := os.ReadFile("/etc/os-release"); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(line, "PRETTY_NAME=") {
					result["os_name"] = strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), "\"")
				}
			}
		}
		if out, err := exec.Command("uname", "-r").Output(); err == nil {
			result["kernel"] = strings.TrimSpace(string(out))
		}
		// Total RAM
		if data, err := os.ReadFile("/proc/meminfo"); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(line, "MemTotal:") {
					fields := strings.Fields(line)
					if len(fields) >= 2 {
						if kb, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
							result["ram_total_bytes"] = kb * 1024
							result["ram_total_human"] = formatDiskSize(kb * 1024)
						}
					}
				}
				if strings.HasPrefix(line, "MemAvailable:") {
					fields := strings.Fields(line)
					if len(fields) >= 2 {
						if kb, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
							result["ram_available_bytes"] = kb * 1024
							result["ram_available_human"] = formatDiskSize(kb * 1024)
						}
					}
				}
			}
		}
		// Load average
		if data, err := os.ReadFile("/proc/loadavg"); err == nil {
			fields := strings.Fields(string(data))
			if len(fields) >= 3 {
				result["load_1m"] = fields[0]
				result["load_5m"] = fields[1]
				result["load_15m"] = fields[2]
			}
		}
		// Disk total/free for root partition
		if out, err := exec.Command("df", "-B1", "/").Output(); err == nil {
			lines := strings.Split(string(out), "\n")
			if len(lines) >= 2 {
				fields := strings.Fields(lines[1])
				if len(fields) >= 4 {
					if total, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
						result["disk_total_bytes"] = total
						result["disk_total_human"] = formatDiskSize(total)
					}
					if used, err := strconv.ParseInt(fields[2], 10, 64); err == nil {
						result["disk_root_used_bytes"] = used
					}
					if free, err := strconv.ParseInt(fields[3], 10, 64); err == nil {
						result["disk_free_bytes"] = free
						result["disk_free_human"] = formatDiskSize(free)
					}
				}
			}
		}
		// Timezone
		if out, err := exec.Command("timedatectl", "show", "--property=Timezone", "--value").Output(); err == nil {
			result["timezone"] = strings.TrimSpace(string(out))
		} else if tz, err := os.Readlink("/etc/localtime"); err == nil {
			if idx := strings.Index(tz, "zoneinfo/"); idx >= 0 {
				result["timezone"] = tz[idx+9:]
			}
		}
		// Package updates available (cached — expensive subprocess)
		s.sysInfoCacheMu.Lock()
		if time.Since(s.sysInfoCacheTime) > 10*time.Minute {
			if out, err := exec.Command("bash", "-c", "apt list --upgradable 2>/dev/null | grep -c upgradable || echo 0").Output(); err == nil {
				s.sysInfoPkgUpdates = strings.TrimSpace(string(out))
			}
			// Web root disk usage (cached together)
			s.configMu.RLock()
			wr := s.config.Global.WebRoot
			s.configMu.RUnlock()
			if wr != "" {
				if du, err := filemanager.DiskUsage(wr); err == nil {
					s.sysInfoDiskUsed = du
				}
			}
			s.sysInfoCacheTime = time.Now()
		}
		pkgUpdates := s.sysInfoPkgUpdates
		s.sysInfoCacheMu.Unlock()
		if pkgUpdates != "" {
			result["package_updates"] = pkgUpdates
		}
	}

	// Web root and domain count
	s.configMu.RLock()
	webRoot := s.config.Global.WebRoot
	domainCount := len(s.config.Domains)
	s.configMu.RUnlock()

	result["web_root"] = webRoot
	result["domain_count"] = domainCount

	s.sysInfoCacheMu.Lock()
	diskUsedCached := s.sysInfoDiskUsed
	s.sysInfoCacheMu.Unlock()
	if diskUsedCached > 0 {
		result["disk_used_bytes"] = diskUsedCached
		result["disk_used_human"] = formatDiskSize(diskUsedCached)
	}

	jsonResponse(w, result)
}

func formatDiskSize(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	p50, p95, p99, max := s.metrics.Percentiles()
	// Per-handler latency percentiles surfaced for the Metrics dashboard
	// so operators can compare "static p99 = 5ms" vs "proxy p99 = 800ms"
	// without scraping Prometheus. Refs: refactor.md O4.
	handlerLatency := make(map[string]map[string]float64, 5)
	for _, h := range []string{"static", "php", "proxy", "redirect", "app"} {
		hp50, hp95, hp99, hmax := s.metrics.HandlerPercentiles(h)
		handlerLatency[h] = map[string]float64{
			"p50_ms": hp50 * 1000,
			"p95_ms": hp95 * 1000,
			"p99_ms": hp99 * 1000,
			"max_ms": hmax * 1000,
		}
	}
	jsonResponse(w, map[string]any{
		"requests_total":  s.metrics.RequestsTotal.Load(),
		"cache_hits":      s.metrics.CacheHits.Load(),
		"cache_misses":    s.metrics.CacheMisses.Load(),
		"active_conns":    s.metrics.ActiveConns.Load(),
		"bytes_sent":      s.metrics.BytesSent.Load(),
		"uptime":          humanDuration(time.Since(s.metrics.StartTime)),
		"slow_requests":   s.metrics.SlowRequests.Load(),
		"latency_p50_ms":  p50 * 1000,
		"latency_p95_ms":  p95 * 1000,
		"latency_p99_ms":  p99 * 1000,
		"latency_max_ms":  max * 1000,
		"handler_latency": handlerLatency,
	})
}

func (s *Server) handleStatsDomains(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, s.metrics.DomainStatsSnapshot())
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

// --- Task API handlers ---

func (s *Server) handleTaskList(w http.ResponseWriter, r *http.Request) {
	tasks := s.taskMgr.List()
	if tasks == nil {
		tasks = []install.Task{}
	}
	limit, offset := parsePagination(r)
	tasks, total := paginateSlice(tasks, limit, offset)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"items":  tasks,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

func (s *Server) handleTaskGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	task := s.taskMgr.Get(id)
	if task == nil {
		jsonError(w, "task not found", http.StatusNotFound)
		return
	}
	jsonResponse(w, task)
}

// HTTPServer returns the underlying http.Server for shutdown during upgrades.
func (s *Server) HTTPServer() *http.Server {
	s.httpSrvMu.RLock()
	defer s.httpSrvMu.RUnlock()
	return s.httpSrv
}

// persistDomainPHPOverrides saves the current in-memory PHP config overrides
// for a domain into its domains.d/*.yaml file so they survive server restarts.

// Close releases background resources used by the admin module.
func (s *Server) Close() {
	s.stopAudit()
}

// SetReloadFunc sets the callback for config reload.
func (s *Server) SetReloadFunc(fn ReloadFunc) { s.reloadFn = fn }

func (s *Server) handleReload(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.reloadFn == nil {
		s.recordAuditR(r, "config.reload", "reload not supported", false)
		jsonError(w, "reload not supported", http.StatusNotImplemented)
		return
	}
	if err := s.reloadFn(); err != nil {
		s.recordAuditR(r, "config.reload", "error: "+err.Error(), false)
		jsonError(w, "reload failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.recordAuditR(r, "config.reload", "", true)
	jsonResponse(w, map[string]string{"status": "reloaded"})
}

func (s *Server) handleCachePurge(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if s.cache == nil {
		s.recordAuditR(r, "cache.purge", "cache not enabled", false)
		jsonError(w, "cache not enabled", http.StatusNotImplemented)
		return
	}

	var req struct {
		Tag string `json:"tag"`
	}
	// Body is optional — nil/empty means "purge all"
	if r.Body != nil {
		json.NewDecoder(r.Body).Decode(&req) // ignore error; empty body = purge all
	}

	if req.Tag != "" {
		count := s.cache.PurgeByTag(req.Tag)
		s.recordAuditR(r, "cache.purge", "tag: "+req.Tag, true)
		jsonResponse(w, map[string]any{"status": "purged", "tag": req.Tag, "count": count})
	} else {
		s.cache.PurgeAll()
		s.recordAuditR(r, "cache.purge", "all", true)
		jsonResponse(w, map[string]string{"status": "all purged"})
	}
}

func (s *Server) handleCacheStats(w http.ResponseWriter, r *http.Request) {
	if s.cache == nil {
		jsonResponse(w, map[string]any{
			"enabled": false,
			"message": "cache not enabled",
		})
		return
	}

	cacheStats := s.cache.Stats()

	// Per-domain cache info
	s.configMu.RLock()
	domainCache := make([]map[string]any, 0)
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
	p50, p95, p99, max := s.metrics.Percentiles()
	stats := map[string]any{
		"requests_total": s.metrics.RequestsTotal.Load(),
		"cache_hits":     s.metrics.CacheHits.Load(),
		"cache_misses":   s.metrics.CacheMisses.Load(),
		"active_conns":   s.metrics.ActiveConns.Load(),
		"bytes_sent":     s.metrics.BytesSent.Load(),
		"uptime":         humanDuration(time.Since(s.metrics.StartTime)),
		"slow_requests":  s.metrics.SlowRequests.Load(),
		"latency_p50_ms": p50 * 1000,
		"latency_p95_ms": p95 * 1000,
		"latency_p99_ms": p99 * 1000,
		"latency_max_ms": max * 1000,
	}
	data, _ := json.Marshal(stats)
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()
}

// handleSSELogs streams new log entries as Server-Sent Events.
func (s *Server) handleSSELogs(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	rc := http.NewResponseController(w)
	rc.SetWriteDeadline(time.Time{})

	// Optional domain filter
	domainFilter := r.URL.Query().Get("domain")

	// Track last seen position
	var lastSeen int
	if s.logBuf != nil {
		lastSeen, _ = s.logBuf.PosAndEntries()
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if s.logBuf == nil {
				continue
			}
			pos, entries := s.logBuf.PosAndEntries()

			if len(entries) == 0 || pos == lastSeen {
				continue
			}

			// Collect new entries since lastSeen
			for lastSeen != pos {
				if entries[lastSeen%len(entries)].Host == "" {
					lastSeen++
					continue
				}
				e := entries[lastSeen%len(entries)]
				lastSeen++

				if domainFilter != "" && e.Host != domainFilter {
					continue
				}

				data, _ := json.Marshal(e)
				fmt.Fprintf(w, "data: %s\n\n", data)
			}
			flusher.Flush()
		}
	}
}

// jsonResponse writes data as application/json with an implicit 200
// status (no WriteHeader call), preserving the legacy semantic that
// allows callers to precede it with their own w.WriteHeader(2xx).
// Refs: refactor.md A10.
func jsonResponse(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
	json.NewEncoder(w).Encode(data)
}

// jsonError writes a JSON error response, delegating to respond.Error.
// 5xx responses are logged at error level (with X-Request-ID when
// present) via the respond package's registered logger. Refs:
// refactor.md A10, O6.
func jsonError(w http.ResponseWriter, msg string, code int) {
	respond.Error(w, code, msg)
}

// jsonErrorCause is jsonError with an explicit underlying error, which
// is logged alongside the message for 5xx codes but never serialized
// to the client. Refs: refactor.md A10, O6.
func jsonErrorCause(w http.ResponseWriter, msg string, cause error, code int) {
	respond.ErrorCause(w, code, msg, cause)
}

// requireJSONMiddleware enforces Content-Type: application/json for mutation
// endpoints (POST/PUT/PATCH). File uploads and raw SQL imports are exempt.
func requireJSONMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" || r.Method == "PUT" || r.Method == "PATCH" {
			path := r.URL.Path
			if strings.Contains(path, "/upload") || strings.Contains(path, "/import") {
				next.ServeHTTP(w, r)
				return
			}
			ct := r.Header.Get("Content-Type")
			if !strings.HasPrefix(ct, "application/json") {
				jsonError(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// --- Domain CRUD ---

// SetOnDomainChange sets a callback invoked after domain add/update/delete.
func (s *Server) SetOnDomainChange(fn func()) { s.onDomainChange = fn }

func (s *Server) notifyDomainChange() {
	if s.onDomainChange != nil {
		s.onDomainChange()
	}
	// Persist config to disk so changes survive restart.
	if err := s.persistConfig(); err != nil {
		s.logger.Error("failed to persist config after domain change", "error", err)
	}
}

// persistConfig writes the global config to the main YAML file and each domain
// to its own file in domains.d/. Main config never contains domain definitions.
func (s *Server) persistConfig() error {
	if s.configPath == "" {
		return nil
	}

	// Serialize the whole persist operation. Without this, two concurrent
	// domain writes could interleave their temp-file + rename steps and leave a
	// corrupt main config or domain file on disk.
	s.persistMu.Lock()
	defer s.persistMu.Unlock()

	s.configMu.RLock()
	cfg := *s.config
	domains := make([]config.Domain, len(s.config.Domains))
	copy(domains, s.config.Domains)
	s.configMu.RUnlock()

	// 1. Write main config WITHOUT domains (global settings only)
	mainCfg := cfg
	mainCfg.Domains = nil
	if mainCfg.DomainsDir == "" {
		mainCfg.DomainsDir = "domains.d"
	}

	mainData, err := yaml.Marshal(&mainCfg)
	if err != nil {
		s.logger.Error("failed to marshal config", "error", err)
		return fmt.Errorf("marshal config: %w", err)
	}
	// Crash-safe write: unique temp file + fsync + rename.
	if err := atomicWriteFile(s.configPath, mainData, 0600); err != nil {
		s.logger.Error("failed to persist config", "path", s.configPath, "error", err)
		return fmt.Errorf("persist config: %w", err)
	}

	// 2. Write each domain to its own file in domains.d/
	domainsDir := mainCfg.DomainsDir
	if !filepath.IsAbs(domainsDir) {
		domainsDir = filepath.Join(filepath.Dir(s.configPath), domainsDir)
	}
	if err := os.MkdirAll(domainsDir, 0755); err != nil {
		s.logger.Error("failed to create domains dir", "path", domainsDir, "error", err)
		return fmt.Errorf("create domains dir: %w", err)
	}

	// Track which files should exist
	activeFiles := make(map[string]bool)
	var firstErr error
	for _, d := range domains {
		clean := strings.ReplaceAll(d.Host, ":", "_")
		clean = filepath.Base(clean)
		fname := clean + ".yaml"
		fpath := filepath.Join(domainsDir, fname)
		activeFiles[fname] = true

		domData, err := yaml.Marshal(&d)
		if err != nil {
			s.logger.Error("failed to marshal domain", "domain", d.Host, "error", err)
			if firstErr == nil {
				firstErr = fmt.Errorf("marshal domain %s: %w", d.Host, err)
			}
			continue
		}
		if err := atomicWriteFile(fpath, domData, 0600); err != nil {
			s.logger.Error("failed to write domain file", "path", fpath, "error", err)
			if firstErr == nil {
				firstErr = fmt.Errorf("write domain %s: %w", d.Host, err)
			}
		}
	}

	// 3. Orphan cleanup REMOVED in v0.5.6. Previously this step deleted any
	// .yaml file in domains.d/ that didn't match a domain currently in memory.
	// That was catastrophic when the in-memory state was incomplete for any
	// reason — load validation skipped a file, fresh install seeded an empty
	// uwas.yaml before old domain files migrated, a transient bug zeroed
	// s.config.Domains — and the very next persistConfig call (which fires
	// on settings changes, PHP auto-assign, anything) would silently wipe
	// every domain file on disk. Domain files now only get removed by the
	// explicit delete handler via removeDomainFile(); persistConfig only
	// WRITES, never destroys.
	_ = activeFiles // kept above so future "soft cleanup" features can use it
	return firstErr
}

// --- Logs ring buffer ---

// RecordLog appends a log entry to the ring buffer. Safe for concurrent use.
func (s *Server) RecordLog(e LogEntry) {
	if s.logBuf == nil {
		s.logBuf = newRingBuffer[LogEntry](maxLogEntries)
	}
	s.logBuf.Append(e)
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	const returnLimit = 100

	if s.logBuf == nil {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]\n"))
		return
	}

	all := s.logBuf.Snapshot()
	if len(all) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]\n"))
		return
	}
	if len(all) > returnLimit {
		all = all[len(all)-returnLimit:]
	}
	jsonResponse(w, all)
}

// --- Unknown domains ---

// SetUnknownHostTracker sets the unknown host tracker for the API.
func (s *Server) SetUnknownHostTracker(t *router.UnknownHostTracker) { s.unknownHT = t }

// --- Config path for raw YAML editor ---

// SetConfigPath stores the main config file path so the raw YAML endpoints
// can read/write the file.
func (s *Server) SetConfigPath(path string) {
	s.configPath = path
	if err := s.loadCloudflareState(); err != nil {
		s.logger.Error("cloudflare state load failed", "error", err.Error(), "path", path)
	}
}

// --- Raw YAML config editor ---

func toInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case string:
		var i int
		fmt.Sscanf(n, "%d", &i)
		return i
	}
	return 0
}

func parseDur(s string) config.Duration {
	d, _ := time.ParseDuration(s)
	return config.Duration{Duration: d}
}

// humanDuration formats a duration as "2d 5h 30m 12s" (no nanoseconds).
func humanDuration(d time.Duration) string {
	if d < time.Second {
		return "0s"
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	secs := int(d.Seconds()) % 60
	var parts []string
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%dd", days))
	}
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	if mins > 0 {
		parts = append(parts, fmt.Sprintf("%dm", mins))
	}
	if secs > 0 || len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%ds", secs))
	}
	return strings.Join(parts, " ")
}

func byteSizeStr(b config.ByteSize) string {
	v, _ := b.MarshalYAML()
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%d", int64(b))
}

func parseBS(s string) config.ByteSize {
	// Reuse YAML unmarshal logic by creating a temporary wrapper.
	var b config.ByteSize
	data := []byte(fmt.Sprintf("val: %s", s))
	var tmp struct {
		Val config.ByteSize `yaml:"val"`
	}
	if err := yaml.Unmarshal(data, &tmp); err == nil {
		b = tmp.Val
	}
	return b
}

// handleDomainRawGet returns the raw YAML content of a single domain file
// from the domains.d/ directory.
func (s *Server) handleDomainRawGet(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")

	// Check domain permissions for non-admin users
	if s.authMgr != nil {
		if user, ok := auth.UserFromContext(r.Context()); ok && user.Role != auth.RoleAdmin {
			if !s.authMgr.CanManageDomain(user, host) {
				s.recordAuditR(r, "domain.read_raw", "domain: "+host+" (forbidden)", false)
				jsonError(w, "forbidden: cannot view this domain", http.StatusForbidden)
				return
			}
		}
	}

	// Try reading from domains.d/ file first
	path, err := s.domainFilePath(host)
	if err == nil {
		data, err := os.ReadFile(path)
		if err == nil {
			jsonResponse(w, map[string]string{"content": string(data)})
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

	jsonResponse(w, map[string]string{"content": string(data)})
}

// handleDomainRawPut validates and writes raw YAML content for a single
// domain file in domains.d/, then triggers a reload.
func (s *Server) handleDomainRawPut(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")

	// Check domain permissions for non-admin users
	if s.authMgr != nil {
		if user, ok := auth.UserFromContext(r.Context()); ok && user.Role != auth.RoleAdmin {
			if !s.authMgr.CanManageDomain(user, host) {
				s.recordAuditR(r, "domain.update_raw", "domain: "+host+" (forbidden)", false)
				jsonError(w, "forbidden: cannot modify this domain", http.StatusForbidden)
				return
			}
		}
	}

	path, err := s.domainFilePath(host)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MB limit
	var req struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	data := []byte(req.Content)

	// Validate YAML syntax by parsing as a domain.
	var probe config.Domain
	if err := yaml.Unmarshal(data, &probe); err != nil {
		jsonError(w, "invalid YAML: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Validate domain semantics before persisting.
	tmpCfg := config.Config{
		Global: config.GlobalConfig{
			LogLevel:  "info",
			LogFormat: "json",
			Admin:     config.AdminConfig{Listen: "127.0.0.1:9443"},
			WebRoot:   "/var/www",
		},
		Domains: []config.Domain{probe},
	}
	if err := config.Validate(&tmpCfg); err != nil {
		jsonError(w, "validation failed: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Ensure the domains.d directory exists.
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		s.logger.Error("domain raw put: mkdir failed", "error", err)
		jsonError(w, "failed to save domain configuration", http.StatusInternalServerError)
		return
	}

	// Crash-safe write: unique temp file + fsync + rename.
	s.persistMu.Lock()
	writeErr := atomicWriteFile(path, data, 0600)
	s.persistMu.Unlock()
	if writeErr != nil {
		s.logger.Error("domain raw put: write failed", "error", writeErr)
		jsonError(w, "failed to save domain configuration", http.StatusInternalServerError)
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

	return filepath.Join(domainsDir, clean+".yaml"), nil
}

// --- MCP ---

// SetMCP sets the MCP server for AI tool management endpoints.
func (s *Server) SetMCP(m *mcp.Server) { s.mcpSrv = m }

func (s *Server) handleMCPTools(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.mcpSrv == nil {
		jsonError(w, "MCP not enabled", http.StatusServiceUnavailable)
		return
	}
	jsonResponse(w, s.mcpSrv.ListTools())
}

func (s *Server) handleMCPCall(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.mcpSrv == nil {
		jsonError(w, "MCP not enabled", http.StatusServiceUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
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

// SetBandwidthManager sets the bandwidth manager for bandwidth monitoring and limits.
func (s *Server) SetBandwidthManager(m *bandwidth.Manager) { s.bwMgr = m }

func (s *Server) handleBackupList(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.backupMgr == nil {
		jsonError(w, "backup not enabled", http.StatusNotImplemented)
		return
	}
	backups := s.backupMgr.ListBackups()
	if backups == nil {
		backups = make([]backup.BackupInfo, 0)
	}
	limit, offset := parsePagination(r)
	backups, total := paginateSlice(backups, limit, offset)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"items":  backups,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

func (s *Server) handleBackupCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.backupMgr == nil {
		s.recordAuditR(r, "backup.create", "backup not enabled", false)
		jsonError(w, "backup not enabled", http.StatusNotImplemented)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	var req struct {
		Provider string `json:"provider"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Provider == "" {
		req.Provider = "local"
	}

	info, err := s.backupMgr.CreateBackup(req.Provider)
	if err != nil {
		s.recordAuditR(r, "backup.create", "provider: "+req.Provider+", error: "+err.Error(), false)
		if s.webhookMgr != nil {
			s.webhookMgr.Fire(webhook.EventBackupFailed, map[string]any{
				"provider": req.Provider,
				"error":    err.Error(),
			})
		}
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.recordAuditR(r, "backup.create", "provider: "+req.Provider, true)
	if s.webhookMgr != nil {
		s.webhookMgr.Fire(webhook.EventBackupCompleted, map[string]any{
			"provider": req.Provider,
			"name":     info.Name,
			"size":     info.Size,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(info)
}

func (s *Server) handleBackupDomain(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.backupMgr == nil {
		jsonError(w, "backup not enabled", http.StatusNotImplemented)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Domain   string `json:"domain"`
		Provider string `json:"provider"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Domain == "" {
		jsonError(w, "domain is required", http.StatusBadRequest)
		return
	}
	if req.Provider == "" {
		req.Provider = "local"
	}

	// Find domain root and DB name from config
	var webRoot, dbName string
	s.configMu.RLock()
	for _, d := range s.config.Domains {
		if d.Host == req.Domain {
			webRoot = d.Root
			break
		}
	}
	s.configMu.RUnlock()

	// Try to detect DB name from wp-config.php
	wpConfig := filepath.Join(webRoot, "wp-config.php")
	if data, err := os.ReadFile(wpConfig); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, "DB_NAME") {
				parts := strings.Split(line, "'")
				if len(parts) >= 4 {
					dbName = parts[3]
				}
			}
		}
	}

	info, err := s.backupMgr.CreateDomainBackup(req.Domain, webRoot, dbName, req.Provider)
	if err != nil {
		s.recordAuditR(r, "backup.domain", req.Domain+": "+err.Error(), false)
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.recordAuditR(r, "backup.domain", req.Domain, true)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(info)
}

func (s *Server) handleBackupRestore(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) || !s.requirePin(w, r) {
		return
	}
	if s.backupMgr == nil {
		s.recordAuditR(r, "backup.restore", "backup not enabled", false)
		jsonError(w, "backup not enabled", http.StatusNotImplemented)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	var req struct {
		Name     string `json:"name"`
		Provider string `json:"provider"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
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
		s.recordAuditR(r, "backup.restore", "name: "+req.Name+", error: "+err.Error(), false)
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.recordAuditR(r, "backup.restore", "name: "+req.Name, true)
	jsonResponse(w, map[string]string{"status": "restored", "name": req.Name})
}

func (s *Server) handleBackupDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) || !s.requirePin(w, r) {
		return
	}
	if s.backupMgr == nil {
		s.recordAuditR(r, "backup.delete", "backup not enabled", false)
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
		s.recordAuditR(r, "backup.delete", "name: "+name+", error: "+err.Error(), false)
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.recordAuditR(r, "backup.delete", "name: "+name, true)
	jsonResponse(w, map[string]string{"status": "deleted", "name": name})
}

func (s *Server) handleBackupScheduleGet(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.backupMgr == nil {
		jsonError(w, "backup not enabled", http.StatusNotImplemented)
		return
	}
	jsonResponse(w, s.backupMgr.ScheduleDetail())
}

func (s *Server) handleBackupSchedulePut(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.backupMgr == nil {
		s.recordAuditR(r, "backup.schedule", "backup not enabled", false)
		jsonError(w, "backup not enabled", http.StatusNotImplemented)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	var req struct {
		Interval string `json:"interval"`
		Enabled  *bool  `json:"enabled"`
		Keep     int    `json:"keep"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	// Update keep count if provided
	if req.Keep > 0 {
		s.backupMgr.SetKeepCount(req.Keep)
	}

	if req.Enabled != nil && !*req.Enabled {
		s.backupMgr.ScheduleBackup(0)
		s.recordAuditR(r, "backup.schedule", "disabled", true)
		jsonResponse(w, s.backupMgr.ScheduleDetail())
		return
	}

	if req.Interval == "" {
		jsonError(w, "interval is required", http.StatusBadRequest)
		return
	}
	d, err := time.ParseDuration(req.Interval)
	if err != nil {
		// Try common formats: "24h", "7d"
		switch req.Interval {
		case "7d":
			d = 7 * 24 * time.Hour
		default:
			jsonError(w, "invalid interval: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	if d < time.Minute {
		jsonError(w, "interval must be at least 1m", http.StatusBadRequest)
		return
	}

	s.backupMgr.ScheduleBackup(d)
	s.recordAuditR(r, "backup.schedule", "interval: "+d.String(), true)
	jsonResponse(w, s.backupMgr.ScheduleDetail())
}

// ── Multi-User Authentication ───────────────────────────────────────────────

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.authMgr == nil {
		jsonError(w, "multi-user auth not enabled", http.StatusNotImplemented)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if req.Username == "" || req.Password == "" {
		jsonError(w, "username and password required", http.StatusBadRequest)
		return
	}

	session, err := s.authMgr.Authenticate(req.Username, req.Password)
	if err != nil {
		ip := requestIP(r)
		s.recordAuthFailure(ip, req.Username)
		if s.webhookMgr != nil {
			s.webhookMgr.Fire(webhook.EventLoginFailed, map[string]any{
				"username": req.Username,
				"ip":       ip,
			})
		}
		jsonError(w, "invalid credentials", http.StatusUnauthorized)
		return
	}

	ip := requestIP(r)
	// Login is unauthenticated when invoked, but we know who succeeded — pass
	// the username explicitly rather than relying on context (no middleware ran).
	s.RecordAuditUser("auth.login", req.Username, ip, req.Username, true)
	if s.webhookMgr != nil {
		s.webhookMgr.Fire(webhook.EventLoginSuccess, map[string]any{
			"username": req.Username,
			"ip":       ip,
		})
	}

	jsonResponse(w, map[string]any{
		"status":     "authenticated",
		"token":      session.Token,
		"user_id":    session.UserID,
		"username":   session.Username,
		"role":       session.Role,
		"domains":    session.Domains,
		"expires_at": session.ExpiresAt,
	})
}

func (s *Server) handleAuthBootstrap(w http.ResponseWriter, r *http.Request) {
	s.ensureAuthManagerFromConfig()
	if s.authMgr == nil {
		jsonError(w, "multi-user auth not enabled", http.StatusNotImplemented)
		return
	}

	s.configMu.RLock()
	apiKey := s.config.Global.Admin.APIKey
	usersEnabled := s.config.Global.Users.Enabled
	s.configMu.RUnlock()
	if !usersEnabled || apiKey != "" {
		jsonError(w, "bootstrap is not available", http.StatusForbidden)
		return
	}
	if len(s.authMgr.ListUsers()) != 0 {
		jsonError(w, "bootstrap is already complete", http.StatusConflict)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Username string `json:"username"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Username == "" || req.Password == "" {
		jsonError(w, "username and password required", http.StatusBadRequest)
		return
	}

	user, err := s.authMgr.CreateUser(req.Username, req.Email, req.Password, auth.RoleAdmin, nil)
	if err != nil {
		ip := requestIP(r)
		s.recordAuthFailure(ip, req.Username)
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	session, err := s.authMgr.Authenticate(req.Username, req.Password)
	if err != nil {
		jsonError(w, "bootstrap login failed", http.StatusInternalServerError)
		return
	}

	ip := requestIP(r)
	s.RecordAuditUser("auth.bootstrap", req.Username, ip, req.Username, true)
	jsonResponse(w, map[string]any{
		"status":     "authenticated",
		"token":      session.Token,
		"user_id":    session.UserID,
		"username":   session.Username,
		"role":       session.Role,
		"domains":    session.Domains,
		"expires_at": session.ExpiresAt,
		"api_key":    user.FullAPIKey,
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if s.authMgr == nil {
		jsonError(w, "multi-user auth not enabled", http.StatusNotImplemented)
		return
	}

	// Try to get token from header or body
	token := r.Header.Get("X-Session-Token")
	if token == "" {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		var req struct {
			Token string `json:"token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err == nil {
			token = req.Token
		}
	}

	if token != "" {
		s.authMgr.Logout(token)
	}

	s.recordAuditR(r, "auth.logout", "", true)

	jsonResponse(w, map[string]string{"status": "logged_out"})
}

func (s *Server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	jsonResponse(w, adminUserDTO(user, false))
}

func (s *Server) handleUserListAuth(w http.ResponseWriter, r *http.Request) {
	if s.authMgr == nil {
		jsonError(w, "multi-user auth not enabled", http.StatusNotImplemented)
		return
	}

	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Only admin can list all users; resellers can only see themselves
	if user.Role != auth.RoleAdmin {
		jsonResponse(w, []adminUserResponse{adminUserDTO(user, false)})
		return
	}

	users := s.authMgr.ListUsers()
	result := make([]adminUserResponse, 0, len(users))
	for _, u := range users {
		result = append(result, adminUserDTO(u, false))
	}

	jsonResponse(w, result)
}

func (s *Server) handleUserGetAuth(w http.ResponseWriter, r *http.Request) {
	if s.authMgr == nil {
		jsonError(w, "multi-user auth not enabled", http.StatusNotImplemented)
		return
	}

	currentUser, ok := auth.UserFromContext(r.Context())
	if !ok {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	username := r.PathValue("username")

	// Users can only get their own info unless they're admin
	if currentUser.Role != auth.RoleAdmin && currentUser.Username != username {
		jsonError(w, "forbidden", http.StatusForbidden)
		return
	}

	user, exists := s.authMgr.GetUser(username)
	if !exists {
		jsonError(w, "user not found", http.StatusNotFound)
		return
	}

	jsonResponse(w, adminUserDTO(user, false))
}

func (s *Server) handleUserCreateAuth(w http.ResponseWriter, r *http.Request) {
	if s.authMgr == nil {
		jsonError(w, "multi-user auth not enabled", http.StatusNotImplemented)
		return
	}

	currentUser, ok := auth.UserFromContext(r.Context())
	if !ok {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Username string   `json:"username"`
		Email    string   `json:"email"`
		Password string   `json:"password"`
		Role     string   `json:"role"`
		Domains  []string `json:"domains"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	// Only admin can create users; resellers can create users only if allowed
	if currentUser.Role != auth.RoleAdmin {
		jsonError(w, "forbidden", http.StatusForbidden)
		return
	}

	role := auth.Role(req.Role)
	if role != auth.RoleAdmin && role != auth.RoleUser && role != auth.RoleReseller {
		jsonError(w, "invalid role", http.StatusBadRequest)
		return
	}

	// Check if reseller role is allowed
	s.configMu.RLock()
	allowReseller := s.config.Global.Users.AllowResller
	s.configMu.RUnlock()
	if role == auth.RoleReseller && !allowReseller {
		jsonError(w, "reseller role not allowed", http.StatusBadRequest)
		return
	}

	user, err := s.authMgr.CreateUser(req.Username, req.Email, req.Password, role, req.Domains)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.recordAuditR(r, "auth.user.create", req.Username+" ("+req.Role+")", true)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(adminUserDTO(user, true))
}

func (s *Server) handleUserUpdateAuth(w http.ResponseWriter, r *http.Request) {
	if s.authMgr == nil {
		jsonError(w, "multi-user auth not enabled", http.StatusNotImplemented)
		return
	}

	currentUser, ok := auth.UserFromContext(r.Context())
	if !ok {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	username := r.PathValue("username")

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Email    *string  `json:"email,omitempty"`
		Password *string  `json:"password,omitempty"`
		Enabled  *bool    `json:"enabled,omitempty"`
		Domains  []string `json:"domains,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	// Users can only update themselves unless they're admin
	if currentUser.Role != auth.RoleAdmin && currentUser.Username != username {
		jsonError(w, "forbidden", http.StatusForbidden)
		return
	}

	updates := &auth.User{}
	if req.Email != nil {
		updates.Email = *req.Email
	}
	if req.Password != nil {
		updates.Password = *req.Password
	}
	if req.Enabled != nil {
		updates.Enabled = *req.Enabled
		updates.EnabledSet = true
	}
	if req.Domains != nil {
		// Only admin can change domains
		if currentUser.Role != auth.RoleAdmin {
			jsonError(w, "forbidden: only admin can change domains", http.StatusForbidden)
			return
		}
		updates.Domains = req.Domains
	}

	if err := s.authMgr.UpdateUser(username, updates); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.recordAuditR(r, "auth.user.update", username, true)

	jsonResponse(w, map[string]string{"status": "updated"})
}

func (s *Server) handleUserDeleteAuth(w http.ResponseWriter, r *http.Request) {
	if !s.requirePin(w, r) {
		return
	}
	if s.authMgr == nil {
		jsonError(w, "multi-user auth not enabled", http.StatusNotImplemented)
		return
	}

	currentUser, ok := auth.UserFromContext(r.Context())
	if !ok {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	username := r.PathValue("username")

	// Users cannot delete themselves; only admin can delete other users
	if currentUser.Username == username {
		jsonError(w, "cannot delete yourself", http.StatusBadRequest)
		return
	}
	if currentUser.Role != auth.RoleAdmin {
		jsonError(w, "forbidden", http.StatusForbidden)
		return
	}

	if err := s.authMgr.DeleteUser(username); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.recordAuditR(r, "auth.user.delete", username, true)

	jsonResponse(w, map[string]string{"status": "deleted"})
}

func (s *Server) handleUserRegenerateAPIKeyAuth(w http.ResponseWriter, r *http.Request) {
	if s.authMgr == nil {
		jsonError(w, "multi-user auth not enabled", http.StatusNotImplemented)
		return
	}

	currentUser, ok := auth.UserFromContext(r.Context())
	if !ok {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	username := r.PathValue("username")

	// Users can only regenerate their own API key unless they're admin
	if currentUser.Role != auth.RoleAdmin && currentUser.Username != username {
		jsonError(w, "forbidden", http.StatusForbidden)
		return
	}

	newKey, err := s.authMgr.RegenerateAPIKey(username)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.recordAuditR(r, "auth.user.apikey", username, true)

	jsonResponse(w, map[string]string{"api_key": newKey})
}

// ── Bandwidth Management ────────────────────────────────────────────────────

func (s *Server) handleBandwidthList(w http.ResponseWriter, r *http.Request) {
	if s.bwMgr == nil {
		jsonResponse(w, []any{})
		return
	}
	statuses := s.bwMgr.GetAllStatus()
	jsonResponse(w, statuses)
}

func (s *Server) handleBandwidthGet(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")
	if s.bwMgr == nil {
		jsonError(w, "bandwidth manager not initialized", http.StatusServiceUnavailable)
		return
	}
	status := s.bwMgr.GetStatus(host)
	if status == nil {
		jsonError(w, "domain not found or bandwidth not enabled", http.StatusNotFound)
		return
	}
	jsonResponse(w, status)
}

func (s *Server) handleBandwidthReset(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")
	if !s.requireDomainAccess(w, r, host, "bandwidth.reset") {
		return
	}
	if s.bwMgr == nil {
		jsonError(w, "bandwidth manager not initialized", http.StatusServiceUnavailable)
		return
	}
	s.bwMgr.Reset(host)
	s.recordAuditR(r, "bandwidth.reset", host, true)
	jsonResponse(w, map[string]string{"status": "reset", "host": host})
}

// --- Cron Monitoring ---

// SetCronMonitor sets the cron job monitor for execution tracking.
func (s *Server) SetCronMonitor(m *cronjob.Monitor) { s.cronMonitor = m }

// SetAuthManager sets the auth manager for multi-user authentication.
func (s *Server) SetAuthManager(m AuthManager) { s.authMgr = m }

// --- Webhook Management ---

// SetWebhookManager sets the webhook manager for event delivery.
func (s *Server) SetWebhookManager(m *webhook.Manager) { s.webhookMgr = m }

// SetAppsManager sets the standalone apps supervisor.
func (s *Server) SetAppsManager(m *apps.Manager) { s.appsMgr = m }

// SetDeployManager sets the deployment manager.
func (s *Server) SetDeployManager(m *deploy.Manager) { s.deployMgr = m }

func (s *Server) handleCronMonitorList(w http.ResponseWriter, r *http.Request) {
	if s.cronMonitor == nil {
		jsonResponse(w, []any{})
		return
	}
	statuses := s.cronMonitor.GetAllStatus()
	jsonResponse(w, statuses)
}

func (s *Server) handleCronMonitorDomain(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")
	if s.cronMonitor == nil {
		jsonError(w, "cron monitor not initialized", http.StatusServiceUnavailable)
		return
	}
	statuses := s.cronMonitor.GetDomainStatus(host)
	if statuses == nil {
		statuses = []cronjob.JobStatus{}
	}
	jsonResponse(w, statuses)
}

func (s *Server) handleCronExecute(w http.ResponseWriter, r *http.Request) {
	// Admin-only: cron execute runs arbitrary shell commands
	if s.authMgr != nil {
		user, ok := auth.UserFromContext(r.Context())
		if ok && user.Role != auth.RoleAdmin {
			jsonError(w, "forbidden: admin only", http.StatusForbidden)
			return
		}
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Domain   string `json:"domain"`
		Schedule string `json:"schedule"`
		Command  string `json:"command"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Domain == "" || req.Command == "" {
		jsonError(w, "domain and command are required", http.StatusBadRequest)
		return
	}
	if s.cronMonitor == nil {
		jsonError(w, "cron monitor not initialized", http.StatusServiceUnavailable)
		return
	}

	// Execute the job asynchronously and return the record
	record := s.cronMonitor.Execute(req.Domain, req.Schedule, req.Command)

	s.recordAuditR(r, "cron.execute", req.Domain+": "+req.Command, record.Success)

	w.Header().Set("Content-Type", "application/json")
	if record.Success {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusOK) // Still 200, but success=false in body
	}
	json.NewEncoder(w).Encode(record)
}

// parsePagination reads limit/offset from query parameters.
// Defaults: limit=50, offset=0. Max limit=500.
func parsePagination(r *http.Request) (limit, offset int) {
	limit = 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 500 {
		limit = 500
	}
	offset = 0
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	return
}

// paginateSlice returns a paginated slice and the total count.
func paginateSlice[T any](items []T, limit, offset int) ([]T, int) {
	total := len(items)
	if offset >= total {
		return []T{}, total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return items[offset:end], total
}

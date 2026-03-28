package admin

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/uwaserver/uwas/internal/admin/dashboard"
	"github.com/uwaserver/uwas/internal/alerting"
	"github.com/uwaserver/uwas/internal/analytics"
	"github.com/uwaserver/uwas/internal/auth"
	"github.com/uwaserver/uwas/internal/backup"
	"github.com/uwaserver/uwas/internal/bandwidth"
	"github.com/uwaserver/uwas/internal/build"
	"github.com/uwaserver/uwas/internal/cache"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/cronjob"
	"github.com/uwaserver/uwas/internal/webhook"
	"github.com/uwaserver/uwas/internal/filemanager"
	"github.com/uwaserver/uwas/internal/install"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/mcp"
	"github.com/uwaserver/uwas/internal/metrics"
	"github.com/uwaserver/uwas/internal/middleware"
	"github.com/uwaserver/uwas/internal/monitor"
	"github.com/uwaserver/uwas/internal/appmanager"
	"github.com/uwaserver/uwas/internal/deploy"
	"github.com/uwaserver/uwas/internal/phpmanager"
	"github.com/uwaserver/uwas/internal/router"
	"github.com/uwaserver/uwas/internal/serverip"
	"github.com/uwaserver/uwas/internal/siteuser"
	uwastls "github.com/uwaserver/uwas/internal/tls"
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

// phpInstallState tracks a background PHP installation.
type phpInstallState struct {
	Version string `json:"version"`
	Status  string `json:"status"` // "running", "done", "error"
	Output  string `json:"output"`
	Error   string `json:"error,omitempty"`
	Distro  string `json:"distro"`
}

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

	monitor       *monitor.Monitor
	alerter       *alerting.Alerter
	phpMgr        *phpmanager.Manager
	appMgr        *appmanager.Manager
	deployMgr     *deploy.Manager
	backupMgr     *backup.BackupManager
	bwMgr         *bandwidth.Manager
	cronMonitor   *cronjob.Monitor
	webhookMgr    *webhook.Manager
	mcpSrv        *mcp.Server
	tlsMgr        *uwastls.Manager
	unknownHT     *router.UnknownHostTracker
	securityStats *middleware.SecurityStats

	// Global installation task manager (serializes apt/dpkg operations)
	taskMgr *install.Manager

	// PHP install state (legacy, kept for backward compat)
	phpInstallMu     sync.Mutex
	phpInstallStatus *phpInstallState

	logMu      sync.Mutex
	logEntries []LogEntry
	logPos     int
	logFull    bool

	// Audit log ring buffer
	auditMu      sync.Mutex
	auditEntries []AuditEntry
	auditPos     int
	auditFull    bool

	// 2FA pending setup (per-user, keyed by username)
	pendingTOTPMu sync.Mutex
	pendingTOTP   map[string]string

	// Rate limiting for auth failures
	rlMu      sync.Mutex
	rateLimit map[string]*rateLimitEntry
	rlDone    chan struct{}

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
	s := &Server{
		config:  cfg,
		logger:  log,
		metrics: m,
		mux:     http.NewServeMux(),
		taskMgr: install.New(),
	}
	s.initAudit()
	s.registerRoutes()
	return s
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	s.mux.HandleFunc("GET /api/v1/system", s.handleSystem)
	s.mux.HandleFunc("GET /api/v1/stats", s.handleStats)
	s.mux.HandleFunc("GET /api/v1/stats/domains", s.handleStatsDomains)
	s.mux.HandleFunc("GET /api/v1/domains", s.handleDomains)
	s.mux.HandleFunc("GET /api/v1/config", s.handleConfig)
	s.mux.HandleFunc("POST /api/v1/reload", s.handleReload)
	s.mux.HandleFunc("POST /api/v1/cache/purge", s.handleCachePurge)
	s.mux.HandleFunc("GET /api/v1/security/stats", s.handleSecurityStats)
	s.mux.HandleFunc("GET /api/v1/security/blocked", s.handleSecurityBlocked)
	s.mux.HandleFunc("GET /api/v1/cache/stats", s.handleCacheStats)
	s.mux.Handle("GET /api/v1/metrics", s.metrics.Handler())
	s.mux.HandleFunc("POST /api/v1/domains", s.handleAddDomain)
	s.mux.HandleFunc("DELETE /api/v1/domains/{host}", s.handleDeleteDomain)
	s.mux.HandleFunc("PUT /api/v1/domains/{host}", s.handleUpdateDomain)
	s.mux.HandleFunc("GET /api/v1/logs", s.handleLogs)
	s.mux.HandleFunc("GET /api/v1/sse/stats", s.handleSSEStats)
	s.mux.HandleFunc("GET /api/v1/sse/logs", s.handleSSELogs)
	s.mux.HandleFunc("GET /api/v1/config/export", s.handleConfigExport)
	s.mux.HandleFunc("GET /api/v1/certs", s.handleCerts)
	s.mux.HandleFunc("POST /api/v1/certs/{host}/renew", s.handleCertRenew)
	s.mux.HandleFunc("GET /api/v1/domains/{host}", s.handleDomainDetail)
	s.mux.HandleFunc("GET /api/v1/config/raw", s.handleConfigRawGet)
	s.mux.HandleFunc("PUT /api/v1/config/raw", s.handleConfigRawPut)
	s.mux.HandleFunc("GET /api/v1/settings", s.handleSettingsGet)
	s.mux.HandleFunc("PUT /api/v1/settings", s.handleSettingsPut)
	s.mux.HandleFunc("GET /api/v1/config/domains/{host}/raw", s.handleDomainRawGet)
	s.mux.HandleFunc("PUT /api/v1/config/domains/{host}/raw", s.handleDomainRawPut)
	s.mux.HandleFunc("GET /api/v1/monitor", s.handleMonitor)
	s.mux.HandleFunc("GET /api/v1/alerts", s.handleAlerts)

	// PHP manager
	s.mux.HandleFunc("GET /api/v1/php", s.handlePHPList)
	s.mux.HandleFunc("GET /api/v1/php/install-info", s.handlePHPInstallInfo)
	s.mux.HandleFunc("GET /api/v1/php/{version}/config/raw", s.handlePHPConfigRawGet)
	s.mux.HandleFunc("PUT /api/v1/php/{version}/config/raw", s.handlePHPConfigRawPut)
	s.mux.HandleFunc("POST /api/v1/php/{version}/enable", s.handlePHPEnable)
	s.mux.HandleFunc("POST /api/v1/php/{version}/disable", s.handlePHPDisable)
	s.mux.HandleFunc("POST /api/v1/php/install", s.handlePHPInstall)
	s.mux.HandleFunc("GET /api/v1/php/install/status", s.handlePHPInstallStatus)
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

	// App process management (Node.js, Python, etc.)
	s.mux.HandleFunc("GET /api/v1/apps", s.handleAppList)
	s.mux.HandleFunc("GET /api/v1/apps/{domain}", s.handleAppGet)
	s.mux.HandleFunc("POST /api/v1/apps/{domain}/start", s.handleAppStart)
	s.mux.HandleFunc("POST /api/v1/apps/{domain}/stop", s.handleAppStop)
	s.mux.HandleFunc("POST /api/v1/apps/{domain}/restart", s.handleAppRestart)
	s.mux.HandleFunc("PUT /api/v1/apps/{domain}/env", s.handleAppEnvUpdate)
	s.mux.HandleFunc("GET /api/v1/apps/{domain}/logs", s.handleAppLogs)
	s.mux.HandleFunc("GET /api/v1/apps/{domain}/stats", s.handleAppStats)

	// Deploy (git clone → build → restart, Docker build → run)
	s.mux.HandleFunc("POST /api/v1/apps/{domain}/deploy", s.handleDeploy)
	s.mux.HandleFunc("GET /api/v1/apps/{domain}/deploy", s.handleDeployStatus)
	s.mux.HandleFunc("GET /api/v1/deploys", s.handleDeployList)
	s.mux.HandleFunc("POST /api/v1/apps/{domain}/webhook", s.handleDeployWebhook)

	// Web terminal (WebSocket → PTY) — requires pin for security
	s.mux.HandleFunc("GET /api/v1/terminal", func(w http.ResponseWriter, r *http.Request) {
		if !s.requirePin(w, r) { return }
		s.terminalHandler().ServeHTTP(w, r)
	})

	// Installation tasks (global queue)
	s.mux.HandleFunc("GET /api/v1/tasks", s.handleTaskList)
	s.mux.HandleFunc("GET /api/v1/tasks/{id}", s.handleTaskGet)

	// Audit log
	s.mux.HandleFunc("GET /api/v1/audit", s.handleAudit)

	// Backup endpoints
	s.mux.HandleFunc("GET /api/v1/backups", s.handleBackupList)
	s.mux.HandleFunc("POST /api/v1/backups", s.handleBackupCreate)
	s.mux.HandleFunc("POST /api/v1/backups/restore", s.handleBackupRestore)
	s.mux.HandleFunc("POST /api/v1/backups/domain", s.handleBackupDomain)
	s.mux.HandleFunc("DELETE /api/v1/backups/{name}", s.handleBackupDelete)
	s.mux.HandleFunc("GET /api/v1/backups/schedule", s.handleBackupScheduleGet)
	s.mux.HandleFunc("PUT /api/v1/backups/schedule", s.handleBackupSchedulePut)

	// Unknown domains
	s.mux.HandleFunc("GET /api/v1/unknown-domains", s.handleUnknownDomainsList)
	s.mux.HandleFunc("POST /api/v1/unknown-domains/{host}/block", s.handleUnknownDomainsBlock)
	s.mux.HandleFunc("POST /api/v1/unknown-domains/{host}/unblock", s.handleUnknownDomainsUnblock)
	s.mux.HandleFunc("DELETE /api/v1/unknown-domains/{host}", s.handleUnknownDomainsDismiss)

	// SFTP user management
	s.mux.HandleFunc("GET /api/v1/users", s.handleUserList)
	s.mux.HandleFunc("POST /api/v1/users", s.handleUserCreate)
	s.mux.HandleFunc("DELETE /api/v1/users/{domain}", s.handleUserDelete)

	// Database management
	s.mux.HandleFunc("GET /api/v1/database/status", s.handleDBStatus)
	s.mux.HandleFunc("GET /api/v1/database/list", s.handleDBList)
	s.mux.HandleFunc("POST /api/v1/database/create", s.handleDBCreate)
	s.mux.HandleFunc("DELETE /api/v1/database/{name}", s.handleDBDrop)
	s.mux.HandleFunc("POST /api/v1/database/install", s.handleDBInstall)
	s.mux.HandleFunc("POST /api/v1/database/uninstall", s.handleDBUninstall)
	s.mux.HandleFunc("POST /api/v1/database/force-uninstall", s.handleDBForceUninstall)
	s.mux.HandleFunc("POST /api/v1/database/repair", s.handleDBRepair)
	s.mux.HandleFunc("GET /api/v1/database/diagnose", s.handleDBDiagnose)
	s.mux.HandleFunc("GET /api/v1/database/users", s.handleDBUsers)
	s.mux.HandleFunc("POST /api/v1/database/users/password", s.handleDBChangePassword)
	s.mux.HandleFunc("GET /api/v1/database/{name}/export", s.handleDBExport)
	s.mux.HandleFunc("POST /api/v1/database/{name}/import", s.handleDBImport)

	// DNS checker + management
	s.mux.HandleFunc("GET /api/v1/dns/{domain}", s.handleDNSCheck)
	s.mux.HandleFunc("GET /api/v1/dns/{domain}/records", s.handleDNSRecords)
	s.mux.HandleFunc("POST /api/v1/dns/{domain}/records", s.handleDNSRecordCreate)
	s.mux.HandleFunc("PUT /api/v1/dns/{domain}/records/{id}", s.handleDNSRecordUpdate)
	s.mux.HandleFunc("DELETE /api/v1/dns/{domain}/records/{id}", s.handleDNSRecordDelete)
	s.mux.HandleFunc("POST /api/v1/dns/{domain}/sync", s.handleDNSSync)

	// System services
	s.mux.HandleFunc("GET /api/v1/services", s.handleServicesList)
	s.mux.HandleFunc("POST /api/v1/services/{name}/start", s.handleServiceStart)
	s.mux.HandleFunc("POST /api/v1/services/{name}/stop", s.handleServiceStop)
	s.mux.HandleFunc("POST /api/v1/services/{name}/restart", s.handleServiceRestart)

	// Database service control
	// Docker database containers
	s.mux.HandleFunc("GET /api/v1/database/docker", s.handleDockerDBList)
	s.mux.HandleFunc("POST /api/v1/database/docker", s.handleDockerDBCreate)
	s.mux.HandleFunc("POST /api/v1/database/docker/{name}/start", s.handleDockerDBStart)
	s.mux.HandleFunc("POST /api/v1/database/docker/{name}/stop", s.handleDockerDBStop)
	s.mux.HandleFunc("DELETE /api/v1/database/docker/{name}", s.handleDockerDBRemove)
	s.mux.HandleFunc("GET /api/v1/database/docker/{name}/databases", s.handleDockerDBListDatabases)
	s.mux.HandleFunc("POST /api/v1/database/docker/{name}/databases", s.handleDockerDBCreateDatabase)
	s.mux.HandleFunc("DELETE /api/v1/database/docker/{name}/databases/{db}", s.handleDockerDBDropDatabase)
	s.mux.HandleFunc("GET /api/v1/database/docker/{name}/databases/{db}/export", s.handleDockerDBExport)
	s.mux.HandleFunc("POST /api/v1/database/docker/{name}/databases/{db}/import", s.handleDockerDBImport)

	s.mux.HandleFunc("POST /api/v1/database/start", s.handleDBStart)
	s.mux.HandleFunc("POST /api/v1/database/stop", s.handleDBStop)
	s.mux.HandleFunc("POST /api/v1/database/restart", s.handleDBRestart)

	// Notifications
	s.mux.HandleFunc("POST /api/v1/notify/test", s.handleNotifyTest)

	// WordPress installer + management
	s.mux.HandleFunc("POST /api/v1/wordpress/install", s.handleWPInstall)
	s.mux.HandleFunc("GET /api/v1/wordpress/install/status", s.handleWPInstallStatus)
	s.mux.HandleFunc("GET /api/v1/wordpress/sites", s.handleWPSites)
	s.mux.HandleFunc("GET /api/v1/wordpress/sites/{domain}/detail", s.handleWPSiteDetail)
	s.mux.HandleFunc("POST /api/v1/wordpress/sites/{domain}/update-core", s.handleWPUpdateCore)
	s.mux.HandleFunc("POST /api/v1/wordpress/sites/{domain}/update-plugins", s.handleWPUpdatePlugins)
	s.mux.HandleFunc("POST /api/v1/wordpress/sites/{domain}/plugin/{action}/{plugin}", s.handleWPPluginAction)
	s.mux.HandleFunc("POST /api/v1/wordpress/sites/{domain}/fix-permissions", s.handleWPFixPermissions)
	s.mux.HandleFunc("POST /api/v1/wordpress/sites/{domain}/reinstall", s.handleWPReinstall)
	s.mux.HandleFunc("POST /api/v1/wordpress/sites/{domain}/debug", s.handleWPToggleDebug)
	s.mux.HandleFunc("GET /api/v1/wordpress/sites/{domain}/error-log", s.handleWPErrorLog)
	s.mux.HandleFunc("GET /api/v1/wordpress/sites/{domain}/users", s.handleWPUsers)
	s.mux.HandleFunc("POST /api/v1/wordpress/sites/{domain}/change-password", s.handleWPChangePassword)
	s.mux.HandleFunc("GET /api/v1/wordpress/sites/{domain}/security", s.handleWPSecurityStatus)
	s.mux.HandleFunc("POST /api/v1/wordpress/sites/{domain}/harden", s.handleWPHarden)
	s.mux.HandleFunc("POST /api/v1/wordpress/sites/{domain}/optimize-db", s.handleWPOptimizeDB)

	// File manager
	s.mux.HandleFunc("GET /api/v1/files/{domain}/list", s.handleFileList)
	s.mux.HandleFunc("GET /api/v1/files/{domain}/read", s.handleFileRead)
	s.mux.HandleFunc("PUT /api/v1/files/{domain}/write", s.handleFileWrite)
	s.mux.HandleFunc("DELETE /api/v1/files/{domain}/delete", s.handleFileDelete)
	s.mux.HandleFunc("POST /api/v1/files/{domain}/mkdir", s.handleFileMkdir)
	s.mux.HandleFunc("POST /api/v1/files/{domain}/upload", s.handleFileUpload)
	s.mux.HandleFunc("GET /api/v1/files/{domain}/disk-usage", s.handleDiskUsage)

	// Cron jobs
	s.mux.HandleFunc("GET /api/v1/cron", s.handleCronList)
	s.mux.HandleFunc("POST /api/v1/cron", s.handleCronAdd)
	s.mux.HandleFunc("DELETE /api/v1/cron", s.handleCronDelete)

	// Firewall
	s.mux.HandleFunc("GET /api/v1/firewall", s.handleFirewallStatus)
	s.mux.HandleFunc("POST /api/v1/firewall/allow", s.handleFirewallAllow)
	s.mux.HandleFunc("POST /api/v1/firewall/deny", s.handleFirewallDeny)
	s.mux.HandleFunc("DELETE /api/v1/firewall/{number}", s.handleFirewallDelete)
	s.mux.HandleFunc("POST /api/v1/firewall/enable", s.handleFirewallEnable)
	s.mux.HandleFunc("POST /api/v1/firewall/disable", s.handleFirewallDisable)

	// SSH keys
	s.mux.HandleFunc("GET /api/v1/users/{domain}/ssh-keys", s.handleSSHKeyList)
	s.mux.HandleFunc("POST /api/v1/users/{domain}/ssh-keys", s.handleSSHKeyAdd)
	s.mux.HandleFunc("DELETE /api/v1/users/{domain}/ssh-keys", s.handleSSHKeyDelete)

	// Server IPs
	s.mux.HandleFunc("GET /api/v1/system/resources", s.handleSystemResources)
	s.mux.HandleFunc("GET /api/v1/system/ips", s.handleServerIPs)
	s.mux.HandleFunc("GET /api/v1/domains/health", s.handleDomainHealth)
	s.mux.HandleFunc("GET /api/v1/domains/{host}/debug", s.handleDomainDebug)

	// 2FA / TOTP
	s.mux.HandleFunc("GET /api/v1/auth/2fa/status", s.handle2FAStatus)
	s.mux.HandleFunc("POST /api/v1/auth/2fa/setup", s.handle2FASetup)
	s.mux.HandleFunc("POST /api/v1/auth/2fa/verify", s.handle2FAVerify)
	s.mux.HandleFunc("POST /api/v1/auth/2fa/disable", s.handle2FADisable)

	// Multi-user auth (login/logout)
	s.mux.HandleFunc("POST /api/v1/auth/login", s.handleLogin)
	s.mux.HandleFunc("POST /api/v1/auth/logout", s.handleLogout)
	s.mux.HandleFunc("GET /api/v1/auth/me", s.handleAuthMe)

	// User management (admin/reseller only)
	s.mux.HandleFunc("GET /api/v1/auth/users", s.handleUserListAuth)
	s.mux.HandleFunc("POST /api/v1/auth/users", s.handleUserCreateAuth)
	s.mux.HandleFunc("GET /api/v1/auth/users/{username}", s.handleUserGetAuth)
	s.mux.HandleFunc("PUT /api/v1/auth/users/{username}", s.handleUserUpdateAuth)
	s.mux.HandleFunc("DELETE /api/v1/auth/users/{username}", s.handleUserDeleteAuth)
	s.mux.HandleFunc("POST /api/v1/auth/users/{username}/apikey", s.handleUserRegenerateAPIKeyAuth)
	s.mux.HandleFunc("POST /api/v1/auth/users/{username}/password", s.handleUserChangePasswordAuth)

	// Doctor
	s.mux.HandleFunc("GET /api/v1/doctor", s.handleDoctor)
	s.mux.HandleFunc("POST /api/v1/doctor/fix", s.handleDoctorFix)

	// Self-update
	s.mux.HandleFunc("GET /api/v1/system/update-check", s.handleUpdateCheck)
	s.mux.HandleFunc("POST /api/v1/system/update", s.handleUpdate)

	// Package installer
	s.mux.HandleFunc("GET /api/v1/packages", s.handlePackageList)
	s.mux.HandleFunc("POST /api/v1/packages/install", s.handlePackageInstall)

	// Site migration + clone
	s.mux.HandleFunc("POST /api/v1/migrate", s.handleMigrate)
	s.mux.HandleFunc("POST /api/v1/clone", s.handleClone)

	// Bandwidth management
	s.mux.HandleFunc("GET /api/v1/bandwidth", s.handleBandwidthList)
	s.mux.HandleFunc("GET /api/v1/bandwidth/{host}", s.handleBandwidthGet)
	s.mux.HandleFunc("POST /api/v1/bandwidth/{host}/reset", s.handleBandwidthReset)

	// Cron monitoring
	s.mux.HandleFunc("GET /api/v1/cron/monitor", s.handleCronMonitorList)
	s.mux.HandleFunc("GET /api/v1/cron/monitor/{host}", s.handleCronMonitorDomain)
	s.mux.HandleFunc("POST /api/v1/cron/execute", s.handleCronExecute)

	// Webhooks
	s.mux.HandleFunc("GET /api/v1/webhooks", s.handleWebhookList)
	s.mux.HandleFunc("POST /api/v1/webhooks", s.handleWebhookCreate)
	s.mux.HandleFunc("DELETE /api/v1/webhooks/{id}", s.handleWebhookDelete)
	s.mux.HandleFunc("POST /api/v1/webhooks/test", s.handleWebhookTest)

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
	s.configMu.RLock()
	addr := s.config.Global.Admin.Listen
	s.configMu.RUnlock()
	if addr == "" {
		addr = "127.0.0.1:9443"
	}

	s.httpSrv = &http.Server{
		Handler:      s.authMiddleware(s.mux),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 5 * time.Minute, // SSE, DB export, backup can take minutes
	}

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
		return s.httpSrv.ServeTLS(ln, tlsCert, tlsKey)
	}

	s.logger.Info("admin API listening", "address", addr)
	return s.httpSrv.Serve(ln)
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read auth config per-request so config changes take effect without restart
		s.configMu.RLock()
		apiKey := s.config.Global.Admin.APIKey
		multiUserEnabled := s.config.Global.Users.Enabled
		s.configMu.RUnlock()

		// If no auth configured at all, allow all
		if apiKey == "" && !multiUserEnabled {
			next.ServeHTTP(w, r)
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

		// Public endpoints: health check, dashboard UI, deploy webhooks (no auth needed)
		if r.URL.Path == "/api/v1/health" ||
			strings.HasPrefix(r.URL.Path, "/_uwas/dashboard") ||
			(strings.HasPrefix(r.URL.Path, "/api/v1/apps/") && strings.HasSuffix(r.URL.Path, "/webhook") && r.Method == "POST") {
			next.ServeHTTP(w, r)
			return
		}

		// Login endpoint: public but rate-limited
		if r.URL.Path == "/api/v1/auth/login" {
			ip := requestIP(r)
			if s.checkRateLimit(ip) {
				w.Header().Set("Retry-After", "300")
				jsonError(w, "too many failed attempts, try again later", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
			return
		}

		// Rate limiting: check if IP is blocked before auth check.
		ip := requestIP(r)
		if s.checkRateLimit(ip) {
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

			// Also check token query param for SSE/WebSocket (can't set headers)
			if !authenticated {
				if token := r.URL.Query().Get("token"); token != "" {
					if session, err := s.authMgr.ValidateSession(token); err == nil {
						if u, exists := s.authMgr.GetUserByID(session.UserID); exists {
							authenticated = true
							user = u
						}
					}
					// Also try as multi-user API key
					if !authenticated {
						if u, err := s.authMgr.AuthenticateAPIKey(token); err == nil {
							authenticated = true
							user = u
						}
					}
					// Only strip token from URL after all checks
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
			// Also check token query param for SSE/WebSocket
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
			s.recordAuthFailure(ip)
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
			!strings.HasPrefix(r.URL.Path, "/api/v1/auth/2fa/") &&
			!multiUserEnabled {
			totpCode := r.Header.Get("X-TOTP-Code")
			if totpCode == "" {
				totpCode = r.URL.Query().Get("totp")
			}
			if totpCode == "" {
				w.Header().Set("X-2FA-Required", "true")
				jsonError(w, "2fa_required", http.StatusForbidden)
				return
			}
			if !ValidateTOTP(totpSecret, totpCode) {
				s.recordAuthFailure(ip)
				jsonError(w, "invalid 2FA code", http.StatusForbidden)
				return
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
		// Package updates available
		if out, err := exec.Command("bash", "-c", "apt list --upgradable 2>/dev/null | grep -c upgradable || echo 0").Output(); err == nil {
			result["package_updates"] = strings.TrimSpace(string(out))
		}
	}

	// Web root and domain count
	s.configMu.RLock()
	webRoot := s.config.Global.WebRoot
	domainCount := len(s.config.Domains)
	s.configMu.RUnlock()

	result["web_root"] = webRoot
	result["domain_count"] = domainCount

	if webRoot != "" {
		if du, err := filemanager.DiskUsage(webRoot); err == nil {
			result["disk_used_bytes"] = du
			result["disk_used_human"] = formatDiskSize(du)
		}
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
	jsonResponse(w, map[string]any{
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
	})
}

func (s *Server) handleStatsDomains(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, s.metrics.DomainStatsSnapshot())
}

func (s *Server) handleDomains(w http.ResponseWriter, r *http.Request) {
	type domainInfo struct {
		Host    string   `json:"host"`
		IP      string   `json:"ip,omitempty"`
		Aliases []string `json:"aliases"`
		Type    string   `json:"type"`
		SSL     string   `json:"ssl"`
		Root    string   `json:"root,omitempty"`
	}

	// Get current user for domain filtering
	var allowedDomains map[string]bool
	if s.authMgr != nil {
		if user, ok := auth.UserFromContext(r.Context()); ok {
			if user.Role != auth.RoleAdmin {
				// Resellers and users can only see their assigned domains
				allowedDomains = make(map[string]bool)
				for _, d := range user.Domains {
					allowedDomains[d] = true
				}
			}
		}
	}

	s.configMu.RLock()
	domains := make([]domainInfo, 0)
	for _, d := range s.config.Domains {
		// Filter domains for non-admin users
		if allowedDomains != nil && !allowedDomains[d.Host] {
			continue
		}
		domains = append(domains, domainInfo{
			Host:    d.Host,
			IP:      d.IP,
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

func (s *Server) handlePHPInstallInfo(w http.ResponseWriter, r *http.Request) {
	version := r.URL.Query().Get("version")
	if version == "" {
		version = "8.3"
	}
	info := phpmanager.GetInstallInfo(version)
	jsonResponse(w, info)
}

func (s *Server) handlePHPInstall(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Version == "" {
		req.Version = "8.3"
	}

	// Check if any install task is already running
	if active := s.taskMgr.Active(); active != nil {
		jsonError(w, fmt.Sprintf("another installation in progress: %s (%s)", active.Name, active.ID), http.StatusConflict)
		return
	}

	info := phpmanager.GetInstallInfo(req.Version)
	s.logger.Info("starting PHP install", "version", req.Version, "distro", info.Distro)

	phpMgr := s.phpMgr
	task := s.taskMgr.Submit("php", "PHP "+req.Version, "install", func(appendOutput func(string)) error {
		output, err := phpmanager.RunInstall(req.Version)
		appendOutput(output)
		if err != nil {
			s.logger.Error("PHP install failed", "version", req.Version, "error", err)
			return err
		}
		s.logger.Info("PHP install complete", "version", req.Version)
		if phpMgr != nil {
			phpMgr.Detect()
		}
		return nil
	})

	// Also update legacy status for backward compat
	s.phpInstallMu.Lock()
	s.phpInstallStatus = &phpInstallState{
		Version: req.Version,
		Status:  "running",
		Distro:  info.Distro,
	}
	s.phpInstallMu.Unlock()

	// Sync legacy status from task in background
	go func() {
		for {
			t := s.taskMgr.Get(task.ID)
			if t == nil || (t.Status != "running" && t.Status != "queued") {
				s.phpInstallMu.Lock()
				if t != nil {
					s.phpInstallStatus.Status = string(t.Status)
					s.phpInstallStatus.Output = t.Output
					s.phpInstallStatus.Error = t.Error
				}
				s.phpInstallMu.Unlock()
				break
			}
			s.phpInstallMu.Lock()
			s.phpInstallStatus.Output = t.Output
			s.phpInstallMu.Unlock()
			time.Sleep(500 * time.Millisecond)
		}
	}()

	jsonResponse(w, map[string]string{
		"status":  "started",
		"task_id": task.ID,
		"version": req.Version,
		"distro":  info.Distro,
	})
}

func (s *Server) handlePHPInstallStatus(w http.ResponseWriter, r *http.Request) {
	// First check task manager for active PHP task
	if t := s.taskMgr.ActiveByType("php"); t != nil {
		jsonResponse(w, map[string]interface{}{
			"status":  t.Status,
			"output":  t.Output,
			"error":   t.Error,
			"task_id": t.ID,
			"version": t.Name,
		})
		return
	}

	// Fall back to legacy state
	s.phpInstallMu.Lock()
	state := s.phpInstallStatus
	s.phpInstallMu.Unlock()

	if state == nil {
		jsonResponse(w, map[string]string{"status": "idle"})
		return
	}
	jsonResponse(w, state)
}

// --- Task API handlers ---

func (s *Server) handleTaskList(w http.ResponseWriter, r *http.Request) {
	tasks := s.taskMgr.List()
	if tasks == nil {
		tasks = []install.Task{}
	}
	jsonResponse(w, tasks)
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
	if r.Body != nil {
		json.NewDecoder(r.Body).Decode(&req) // optional body
	}
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

func (s *Server) handlePHPConfigRawGet(w http.ResponseWriter, r *http.Request) {
	if s.phpMgr == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	version := r.PathValue("version")
	content, err := s.phpMgr.GetConfigRaw(version)
	if err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	jsonResponse(w, map[string]string{"content": content, "version": version})
}

func (s *Server) handlePHPConfigRawPut(w http.ResponseWriter, r *http.Request) {
	if s.phpMgr == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20) // 2MB
	version := r.PathValue("version")
	var req struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.phpMgr.SetConfigRaw(version, req.Content); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.logger.Info("PHP config raw updated", "version", version)
	jsonResponse(w, map[string]string{"status": "saved", "version": version})
}

func (s *Server) handlePHPEnable(w http.ResponseWriter, r *http.Request) {
	if s.phpMgr == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	version := r.PathValue("version")
	s.phpMgr.EnableVersion(version)
	s.logger.Info("PHP version enabled", "version", version)
	jsonResponse(w, map[string]string{"status": "enabled", "version": version})
}

func (s *Server) handlePHPDisable(w http.ResponseWriter, r *http.Request) {
	if s.phpMgr == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	version := r.PathValue("version")
	if err := s.phpMgr.DisableVersion(version); err != nil {
		jsonError(w, err.Error(), http.StatusConflict)
		return
	}
	s.logger.Info("PHP version disabled", "version", version)
	jsonResponse(w, map[string]string{"status": "disabled", "version": version})
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

	// Find domain root from config for open_basedir isolation
	var domRoot string
	s.configMu.RLock()
	for _, dom := range s.config.Domains {
		if dom.Host == req.Domain {
			domRoot = dom.Root
			break
		}
	}
	s.configMu.RUnlock()
	dp, err := s.phpMgr.AssignDomainWithRoot(req.Domain, req.Version, domRoot)
	if err != nil {
		jsonError(w, err.Error(), http.StatusConflict)
		return
	}

	// Persist FPM address to domain config so it survives restart
	s.configMu.Lock()
	for i, dom := range s.config.Domains {
		if dom.Host == req.Domain {
			s.config.Domains[i].PHP.FPMAddress = dp.ListenAddr
			break
		}
	}
	s.configMu.Unlock()
	s.persistConfig()
	s.notifyDomainChange()

	// Start the PHP process
	if err := s.phpMgr.StartDomain(req.Domain); err != nil {
		s.logger.Warn("PHP start after assign failed", "domain", req.Domain, "error", err)
	}

	ip := requestIP(r)
	s.RecordAudit("php.assign", req.Domain+": PHP "+req.Version+" → "+dp.ListenAddr, ip, true)

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

	// Check domain permissions for non-admin users
	if s.authMgr != nil {
		if user, ok := auth.UserFromContext(r.Context()); ok && user.Role != auth.RoleAdmin {
			if !s.authMgr.CanManageDomain(user, domain) {
				s.RecordAudit("php.domain_start", "domain: "+domain+" (forbidden)", requestIP(r), false)
				jsonError(w, "forbidden: cannot manage this domain", http.StatusForbidden)
				return
			}
		}
	}

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

	// Check domain permissions for non-admin users
	if s.authMgr != nil {
		if user, ok := auth.UserFromContext(r.Context()); ok && user.Role != auth.RoleAdmin {
			if !s.authMgr.CanManageDomain(user, domain) {
				s.RecordAudit("php.domain_stop", "domain: "+domain+" (forbidden)", requestIP(r), false)
				jsonError(w, "forbidden: cannot manage this domain", http.StatusForbidden)
				return
			}
		}
	}

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

	// Check domain permissions for non-admin users
	if s.authMgr != nil {
		if user, ok := auth.UserFromContext(r.Context()); ok && user.Role != auth.RoleAdmin {
			if !s.authMgr.CanManageDomain(user, domain) {
				s.RecordAudit("php.domain_config_get", "domain: "+domain+" (forbidden)", requestIP(r), false)
				jsonError(w, "forbidden: cannot manage this domain", http.StatusForbidden)
				return
			}
		}
	}

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

	// Check domain permissions for non-admin users
	if s.authMgr != nil {
		if user, ok := auth.UserFromContext(r.Context()); ok && user.Role != auth.RoleAdmin {
			if !s.authMgr.CanManageDomain(user, domain) {
				s.RecordAudit("php.domain_config_put", "domain: "+domain+" (forbidden)", requestIP(r), false)
				jsonError(w, "forbidden: cannot manage this domain", http.StatusForbidden)
				return
			}
		}
	}

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
	// Body is optional — nil/empty means "purge all"
	if r.Body != nil {
		json.NewDecoder(r.Body).Decode(&req) // ignore error; empty body = purge all
	}

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
	stats := map[string]any{
		"requests_total": s.metrics.RequestsTotal.Load(),
		"cache_hits":     s.metrics.CacheHits.Load(),
		"cache_misses":   s.metrics.CacheMisses.Load(),
		"active_conns":   s.metrics.ActiveConns.Load(),
		"bytes_sent":     s.metrics.BytesSent.Load(),
		"uptime":         humanDuration(time.Since(s.metrics.StartTime)),
	}
	data, _ := json.Marshal(stats)
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()
}

// handleSSELogs streams new log entries as Server-Sent Events.
func (s *Server) handleSSELogs(w http.ResponseWriter, r *http.Request) {
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
	s.logMu.Lock()
	lastSeen = s.logPos
	s.logMu.Unlock()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			s.logMu.Lock()
			pos := s.logPos
			entries := s.logEntries
			s.logMu.Unlock()

			if entries == nil || pos == lastSeen {
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

// handleConfigExport returns the current configuration as a YAML file download.
func (s *Server) handleConfigExport(w http.ResponseWriter, r *http.Request) {
	// Build a sanitized copy: strip secrets.
	s.configMu.RLock()
	export := *s.config
	s.configMu.RUnlock()
	export.Global.Admin.APIKey = ""
	export.Global.Admin.PinCode = ""
	export.Global.Admin.TOTPSecret = ""
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
	// Persist config to disk so changes survive restart.
	s.persistConfig()
}

// persistConfig writes the global config to the main YAML file and each domain
// to its own file in domains.d/. Main config never contains domain definitions.
func (s *Server) persistConfig() {
	if s.configPath == "" {
		return
	}

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
		return
	}
	// Atomic write: temp file + rename to prevent corruption on crash
	tmpMain := s.configPath + ".tmp"
	if err := os.WriteFile(tmpMain, mainData, 0644); err != nil {
		s.logger.Error("failed to persist config", "path", s.configPath, "error", err)
		return
	}
	if err := os.Rename(tmpMain, s.configPath); err != nil {
		s.logger.Error("failed to rename config", "path", s.configPath, "error", err)
		return
	}

	// 2. Write each domain to its own file in domains.d/
	domainsDir := mainCfg.DomainsDir
	if !filepath.IsAbs(domainsDir) {
		domainsDir = filepath.Join(filepath.Dir(s.configPath), domainsDir)
	}
	if err := os.MkdirAll(domainsDir, 0755); err != nil {
		s.logger.Error("failed to create domains dir", "path", domainsDir, "error", err)
		return
	}

	// Track which files should exist
	activeFiles := make(map[string]bool)
	for _, d := range domains {
		clean := strings.ReplaceAll(d.Host, ":", "_")
		clean = filepath.Base(clean)
		fname := clean + ".yaml"
		fpath := filepath.Join(domainsDir, fname)
		activeFiles[fname] = true

		domData, err := yaml.Marshal(&d)
		if err != nil {
			s.logger.Error("failed to marshal domain", "host", d.Host, "error", err)
			continue
		}
		if err := os.WriteFile(fpath, domData, 0644); err != nil {
			s.logger.Error("failed to write domain file", "path", fpath, "error", err)
		}
	}

	// 3. Remove orphan domain files (deleted domains)
	entries, err := os.ReadDir(domainsDir)
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() && (strings.HasSuffix(e.Name(), ".yaml") || strings.HasSuffix(e.Name(), ".yml")) {
				if !activeFiles[e.Name()] {
					os.Remove(filepath.Join(domainsDir, e.Name()))
					s.logger.Debug("removed orphan domain file", "file", e.Name())
				}
			}
		}
	}
}

func (s *Server) handleAddDomain(w http.ResponseWriter, r *http.Request) {
	ip := requestIP(r)

	// Check domain permissions for non-admin users
	if s.authMgr != nil {
		user, ok := auth.UserFromContext(r.Context())
		if ok && user.Role != auth.RoleAdmin {
			// Resellers can only add domains in their allowed list
			if !s.authMgr.CanManageDomain(user, r.URL.Query().Get("host")) {
				s.RecordAudit("domain.create", "domain: "+r.URL.Query().Get("host")+" (forbidden)", ip, false)
				jsonError(w, "forbidden: cannot manage this domain", http.StatusForbidden)
				return
			}
		}
	}

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

	// Check domain permissions for resellers
	if s.authMgr != nil {
		user, ok := auth.UserFromContext(r.Context())
		if ok && user.Role != auth.RoleAdmin {
			if !s.authMgr.CanManageDomain(user, d.Host) {
				s.RecordAudit("domain.create", "domain: "+d.Host+" (forbidden)", ip, false)
				jsonError(w, "forbidden: cannot manage this domain", http.StatusForbidden)
				return
			}
		}
	}

	if !isValidHostname(d.Host) {
		jsonError(w, "invalid hostname: must be a valid domain name", http.StatusBadRequest)
		return
	}
	if d.Type == "" {
		d.Type = "static"
	}
	if d.SSL.Mode == "" {
		d.SSL.Mode = "auto"
	}

	// ── Pre-save validation ──
	if err := validateDomainConfig(&d, s); err != nil {
		jsonError(w, "validation failed: "+err.Error(), http.StatusBadRequest)
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

	// ── Auto-fill defaults based on domain type ──

	// Web root: always auto-created
	webRoot := s.config.Global.WebRoot
	if webRoot == "" {
		webRoot = "/var/www"
	}
	if d.Root == "" {
		d.Root = filepath.Join(webRoot, d.Host, "public_html")
	}

	// PHP defaults
	if d.Type == "php" {
		if len(d.PHP.IndexFiles) == 0 {
			d.PHP.IndexFiles = []string{"index.php", "index.html"}
		}
		// FPM address will be set by auto-assign after unlock
		d.Htaccess = config.HtaccessConfig{Mode: "import"}
		if !d.Security.WAF.Enabled {
			d.Security.WAF.Enabled = true
		}
		if len(d.Security.BlockedPaths) == 0 {
			d.Security.BlockedPaths = []string{".git", ".env", "wp-config.php"}
		}
	}

	// Static defaults
	if d.Type == "static" {
		d.Compression = config.CompressionConfig{
			Enabled:    true,
			Algorithms: []string{"gzip", "br"},
		}
	}

	// Cache defaults (all types except redirect)
	if d.Type != "redirect" && !d.Cache.Enabled {
		d.Cache.Enabled = true
		d.Cache.TTL = 3600
	}
	if d.Root != "" {
		// Create web root with parent directory
		parentDir := filepath.Dir(d.Root) // e.g. /var/www/example.com
		if err := os.MkdirAll(d.Root, 0755); err != nil {
			s.logger.Warn("failed to create web root", "path", d.Root, "error", err)
		}
		idx := filepath.Join(d.Root, "index.html")
		if _, err := os.Stat(idx); os.IsNotExist(err) {
			placeholder := fmt.Sprintf(`<!DOCTYPE html>
<html><head><title>%s</title></head>
<body style="font-family:system-ui;display:flex;justify-content:center;align-items:center;min-height:100vh;margin:0;background:#0f172a;color:#e2e8f0">
<div style="text-align:center"><h1>%s</h1><p style="color:#94a3b8">Site is ready. Upload your files via SFTP or place them in:<br><code>%s</code></p></div>
</body></html>`, d.Host, d.Host, d.Root)
			if err := os.WriteFile(idx, []byte(placeholder), 0644); err != nil {
				s.logger.Warn("failed to write placeholder index", "path", idx, "error", err)
			}
		}
		// Set ownership to www-data so PHP/WordPress can write files
		// (.htaccess, wp-content/uploads, cache, etc.)
		if runtime.GOOS == "linux" {
			exec.Command("chown", "-R", "www-data:www-data", parentDir).Run()
			exec.Command("chmod", "-R", "755", parentDir).Run()
			// public_html needs to be writable for WordPress uploads
			exec.Command("chmod", "-R", "775", d.Root).Run()
		}
	}

	// Auto-create .htaccess for PHP domains (WordPress/Laravel friendly)
	if d.Type == "php" && d.Root != "" {
		htaccessPath := filepath.Join(d.Root, ".htaccess")
		if _, err := os.Stat(htaccessPath); os.IsNotExist(err) {
			htContent := "# UWAS — PHP front-controller rewrite\n" +
				"# Works with WordPress, Laravel, Drupal, etc.\n" +
				"<IfModule mod_rewrite.c>\n" +
				"RewriteEngine On\n" +
				"RewriteBase /\n" +
				"RewriteRule ^index\\.php$ - [L]\n" +
				"RewriteCond %{REQUEST_FILENAME} !-f\n" +
				"RewriteCond %{REQUEST_FILENAME} !-d\n" +
				"RewriteRule . /index.php [L]\n" +
				"</IfModule>\n"
			os.WriteFile(htaccessPath, []byte(htContent), 0644)
		}
	}

	// Auto-set per-domain access log path if not configured.
	if d.AccessLog.Path == "" && d.Root != "" {
		logDir := filepath.Join(filepath.Dir(d.Root), "logs")
		os.MkdirAll(logDir, 0755)
		d.AccessLog.Path = filepath.Join(logDir, "access.log")
	}

	// PHP assignment: use user-provided FPM address, or auto-assign.
	if d.Type == "php" && s.phpMgr != nil {
		if d.PHP.FPMAddress != "" {
			// User explicitly provided FPM address — register so it shows in PHP page.
			phpStatus := s.phpMgr.Status()
			ver := ""
			if len(phpStatus) > 0 {
				ver = phpStatus[0].Version
			}
			s.phpMgr.RegisterExistingDomain(d.Host, ver, d.PHP.FPMAddress, d.Root)
			s.logger.Info("using user-provided PHP address", "domain", d.Host, "address", d.PHP.FPMAddress)
		} else {
			// Auto-assign: prefer FPM socket, fallback to CGI TCP port
			phpStatus := s.phpMgr.Status()
			if len(phpStatus) > 0 {
				// Pick best version: prefer FPM over CGI
				version := phpStatus[0].Version
				for _, st := range phpStatus {
					if strings.Contains(st.SAPI, "fpm") && st.Running {
						version = st.Version
						break
					}
				}
				if inst, err := s.phpMgr.AssignDomainWithRoot(d.Host, version, d.Root); err == nil {
					d.PHP.FPMAddress = inst.ListenAddr
					if err := s.phpMgr.StartDomain(d.Host); err != nil {
						s.logger.Warn("PHP auto-start failed", "domain", d.Host, "error", err)
					} else {
						s.logger.Info("auto-assigned PHP to domain", "domain", d.Host, "version", version, "listen", inst.ListenAddr)
					}
				} else {
					s.logger.Warn("PHP auto-assign failed", "domain", d.Host, "error", err)
				}
			}
		}
	}

	// App type: register with app manager and start
	if d.Type == "app" && s.appMgr != nil && (d.App.Command != "" || d.App.Runtime != "") {
		if err := s.appMgr.Register(d.Host, d.App, d.Root); err != nil {
			s.logger.Warn("app register on create failed", "domain", d.Host, "error", err)
		} else {
			if err := s.appMgr.Start(d.Host); err != nil {
				s.logger.Warn("app start on create failed", "domain", d.Host, "error", err)
			} else {
				s.logger.Info("app started on domain create", "domain", d.Host, "runtime", d.App.Runtime)
			}
		}
	}

	s.config.Domains = append(s.config.Domains, d)
	s.configMu.Unlock()

	s.RecordAudit("domain.create", "domain: "+d.Host, ip, true)
	s.notifyDomainChange()

	// Fire webhook event
	if s.webhookMgr != nil {
		s.webhookMgr.Fire(webhook.EventDomainAdd, map[string]any{
			"host": d.Host,
			"type": d.Type,
			"root": d.Root,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(d)
}

func (s *Server) handleDeleteDomain(w http.ResponseWriter, r *http.Request) {
	if !s.requirePin(w, r) {
		return
	}
	ip := requestIP(r)
	host := r.PathValue("host")
	cleanup := r.URL.Query().Get("cleanup") == "true"

	// Protect default/system domains from deletion
	if host == "localhost" || host == "localhost:80" || host == "localhost:443" || host == "127.0.0.1" {
		jsonError(w, "cannot delete default domain: "+host, http.StatusForbidden)
		return
	}

	// Check domain permissions for non-admin users
	if s.authMgr != nil {
		user, ok := auth.UserFromContext(r.Context())
		if ok && user.Role != auth.RoleAdmin {
			if !s.authMgr.CanManageDomain(user, host) {
				s.RecordAudit("domain.delete", "domain: "+host+" (forbidden)", ip, false)
				jsonError(w, "forbidden: cannot manage this domain", http.StatusForbidden)
				return
			}
		}
	}

	s.configMu.Lock()
	found := false
	var domainRoot string
	for i, d := range s.config.Domains {
		if d.Host == host {
			domainRoot = d.Root
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

	// Cleanup: stop PHP, remove SFTP user, delete files
	if cleanup {
		if s.phpMgr != nil {
			s.phpMgr.StopDomain(host)
			s.phpMgr.UnassignDomain(host)
		}
		siteuser.DeleteUser(host)
		// Delete web root — only the domain-specific directory.
		// Safety: never delete system dirs, shared roots, or paths too close to /
		if domainRoot != "" {
			// Find the domain-specific directory to delete.
			// Convention: /var/www/{domain}/public_html → delete /var/www/{domain}
			// But if root IS /var/www (no subdirectory), don't delete anything.
			absRoot, _ := filepath.Abs(domainRoot)
			parent := filepath.Dir(absRoot) // e.g. /var/www/domain.com

			protectedPaths := map[string]bool{
				"/": true, "/var": true, "/var/www": true, "/home": true,
				"/etc": true, "/tmp": true, "/root": true, "/opt": true,
				"/usr": true, "/srv": true, "/var/lib": true, "/var/log": true,
			}

			// Only delete if:
			// 1. Parent is a domain-specific dir (not a system/shared path)
			// 2. Parent has at least 3 path components (e.g. /var/www/domain.com)
			// 3. Parent is not the webRoot itself
			webRoot := s.config.Global.WebRoot
			if webRoot == "" {
				webRoot = "/var/www"
			}
			absWebRoot, _ := filepath.Abs(webRoot)

			safe := parent != "" &&
				!protectedPaths[parent] &&
				!protectedPaths[absRoot] &&
				parent != absWebRoot &&
				absRoot != absWebRoot &&
				len(strings.Split(parent, string(filepath.Separator))) >= 4

			if safe {
				os.RemoveAll(parent)
				s.logger.Info("deleted domain files", "domain", host, "path", parent)
			} else if absRoot != absWebRoot && !protectedPaths[absRoot] {
				// Try deleting just the root directory itself (not parent)
				os.RemoveAll(absRoot)
				s.logger.Info("deleted domain root", "domain", host, "path", absRoot)
			} else {
				s.logger.Warn("skipped file deletion (protected path)", "domain", host, "path", parent)
			}
		}
	}

	s.RecordAudit("domain.delete", "domain: "+host, ip, true)
	s.notifyDomainChange()

	// Fire webhook event
	if s.webhookMgr != nil {
		s.webhookMgr.Fire(webhook.EventDomainDelete, map[string]any{
			"host":    host,
			"cleanup": cleanup,
		})
	}

	jsonResponse(w, map[string]string{"status": "deleted", "cleanup": fmt.Sprintf("%v", cleanup)})
}

func (s *Server) handleUpdateDomain(w http.ResponseWriter, r *http.Request) {
	ip := requestIP(r)
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	host := r.PathValue("host")

	// Check domain permissions for non-admin users
	if s.authMgr != nil {
		user, ok := auth.UserFromContext(r.Context())
		if ok && user.Role != auth.RoleAdmin {
			if !s.authMgr.CanManageDomain(user, host) {
				s.RecordAudit("domain.update", "domain: "+host+" (forbidden)", ip, false)
				jsonError(w, "forbidden: cannot manage this domain", http.StatusForbidden)
				return
			}
		}
	}

	var d config.Domain
	if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	s.configMu.Lock()
	found := false
	for i, existing := range s.config.Domains {
		if existing.Host == host {
			// Merge: preserve existing values when incoming field is zero/empty
			merged := existing
			if d.Host != "" {
				merged.Host = d.Host
			}
			if d.Type != "" {
				merged.Type = d.Type
			}
			if d.IP != "" {
				merged.IP = d.IP
			}
			if d.Root != "" {
				merged.Root = d.Root
			}
			if d.SSL.Mode != "" {
				merged.SSL = d.SSL
			}
			if len(d.Aliases) > 0 {
				merged.Aliases = d.Aliases
			}
			// PHP: only override if provided (preserve existing FPM address)
			if d.PHP.FPMAddress != "" {
				merged.PHP.FPMAddress = d.PHP.FPMAddress
			}
			if len(d.PHP.IndexFiles) > 0 {
				merged.PHP.IndexFiles = d.PHP.IndexFiles
			}
			if d.PHP.MaxUpload > 0 {
				merged.PHP.MaxUpload = d.PHP.MaxUpload
			}
			if len(d.PHP.Env) > 0 {
				merged.PHP.Env = d.PHP.Env
			}
			// Proxy
			if len(d.Proxy.Upstreams) > 0 {
				merged.Proxy = d.Proxy
			}
			// Redirect
			if d.Redirect.Target != "" {
				merged.Redirect = d.Redirect
			}
			// App
			if d.App.Command != "" || d.App.Runtime != "" {
				merged.App = d.App
			}
			// Resources
			if d.Resources.CPUPercent > 0 || d.Resources.MemoryMB > 0 || d.Resources.PIDMax > 0 {
				merged.Resources = d.Resources
			}
			// Htaccess
			if d.Htaccess.Mode != "" {
				merged.Htaccess = d.Htaccess
			}
			// Cache, Security, Compression:
			// ?replace=true → full replace (allows disabling features)
			// default → merge (only override non-zero fields)
			if r.URL.Query().Get("replace") == "true" {
				merged.Cache = d.Cache
				merged.Security = d.Security
				merged.Compression = d.Compression
			} else {
				if d.Cache.TTL > 0 || d.Cache.Enabled {
					merged.Cache = d.Cache
				}
				if len(d.Security.BlockedPaths) > 0 || d.Security.WAF.Enabled ||
					len(d.Security.IPWhitelist) > 0 || len(d.Security.IPBlacklist) > 0 ||
					len(d.Security.GeoBlockCountries) > 0 || len(d.Security.GeoAllowCountries) > 0 {
					merged.Security = d.Security
				}
				if d.Compression.Enabled || len(d.Compression.Algorithms) > 0 {
					merged.Compression = d.Compression
				}
			}
			s.config.Domains[i] = merged
			d = merged // use merged for subsequent operations
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
	if s.webhookMgr != nil {
		s.webhookMgr.Fire(webhook.EventDomainUpdate, map[string]any{
			"host": host,
		})
	}
	s.notifyDomainChange()

	// Auto-assign PHP if domain type changed to php and no assignment exists.
	if d.Type == "php" && s.phpMgr != nil {
		instances := s.phpMgr.GetDomainInstances()
		hasAssign := false
		for _, inst := range instances {
			if inst.Domain == d.Host {
				hasAssign = true
				break
			}
		}
		if !hasAssign {
			phpStatus := s.phpMgr.Status()
			if len(phpStatus) > 0 {
				version := phpStatus[0].Version
				if inst, err := s.phpMgr.AssignDomainWithRoot(d.Host, version, d.Root); err == nil {
					s.phpMgr.StartDomain(d.Host)
					s.configMu.Lock()
					for i := range s.config.Domains {
						if s.config.Domains[i].Host == d.Host {
							s.config.Domains[i].PHP.FPMAddress = inst.ListenAddr
							break
						}
					}
					s.configMu.Unlock()
					s.persistConfig()
					s.notifyDomainChange() // sync VHost router with new FPM address
					s.logger.Info("auto-assigned PHP on update", "domain", d.Host, "version", version)
				}
			}
		}
	}

	// Auto-register app if domain type changed to app
	if d.Type == "app" && s.appMgr != nil && (d.App.Command != "" || d.App.Runtime != "") {
		if s.appMgr.Get(d.Host) == nil {
			if err := s.appMgr.Register(d.Host, d.App, d.Root); err == nil {
				s.appMgr.Start(d.Host)
				s.logger.Info("auto-registered app on update", "domain", d.Host)
			}
		}
	}

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

// SetTLSManager sets the TLS manager for certificate status and renewal.
func (s *Server) SetTLSManager(m *uwastls.Manager) { s.tlsMgr = m }

func (s *Server) handleCerts(w http.ResponseWriter, r *http.Request) {
	s.configMu.RLock()
	defer s.configMu.RUnlock()

	type certInfo struct {
		Host     string `json:"host"`
		SSLMode  string `json:"ssl_mode"`
		Status   string `json:"status"`
		Issuer   string `json:"issuer"`
		Expiry   string `json:"expiry,omitempty"`
		DaysLeft int    `json:"days_left"`
	}

	certs := make([]certInfo, 0)
	for _, d := range s.config.Domains {
		ci := certInfo{
			Host:    d.Host,
			SSLMode: d.SSL.Mode,
		}
		switch d.SSL.Mode {
		case "off":
			ci.Status = "none"
		case "auto":
			// Check real cert status from TLS manager.
			if s.tlsMgr != nil {
				if info := s.tlsMgr.CertStatus(d.Host); info != nil {
					ci.Status = "active"
					ci.Issuer = info.Issuer
					ci.Expiry = info.Expiry.Format(time.RFC3339)
					ci.DaysLeft = info.DaysLeft
					if info.DaysLeft <= 0 {
						ci.Status = "expired"
					}
				} else {
					ci.Status = "pending"
					ci.Issuer = "Let's Encrypt"
				}
			} else {
				ci.Status = "pending"
				ci.Issuer = "Let's Encrypt"
			}
		case "manual":
			ci.Status = "active"
			ci.Issuer = "Manual"
			if s.tlsMgr != nil {
				if info := s.tlsMgr.CertStatus(d.Host); info != nil {
					ci.Issuer = info.Issuer
					ci.Expiry = info.Expiry.Format(time.RFC3339)
					ci.DaysLeft = info.DaysLeft
				}
			}
		}
		certs = append(certs, ci)
	}
	jsonResponse(w, certs)
}

func (s *Server) handleCertRenew(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")
	if s.tlsMgr == nil {
		jsonError(w, "TLS manager not available", http.StatusServiceUnavailable)
		return
	}
	if err := s.tlsMgr.RenewCert(r.Context(), host); err != nil {
		jsonError(w, "renewal failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if s.webhookMgr != nil {
		s.webhookMgr.Fire(webhook.EventCertRenewed, map[string]any{
			"host": host,
		})
	}
	jsonResponse(w, map[string]string{"status": "renewed", "host": host})
}

// --- Unknown domains ---

// SetUnknownHostTracker sets the unknown host tracker for the API.
func (s *Server) SetUnknownHostTracker(t *router.UnknownHostTracker) { s.unknownHT = t }

func (s *Server) handleUnknownDomainsList(w http.ResponseWriter, r *http.Request) {
	if s.unknownHT == nil {
		jsonResponse(w, []any{})
		return
	}
	jsonResponse(w, s.unknownHT.List())
}

func (s *Server) handleUnknownDomainsBlock(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")

	// Only admins can block unknown domains (global security setting)
	if s.authMgr != nil {
		if user, ok := auth.UserFromContext(r.Context()); ok && user.Role != auth.RoleAdmin {
			s.RecordAudit("unknown_domain.block", "host: "+host+" (forbidden)", requestIP(r), false)
			jsonError(w, "forbidden: admin access required", http.StatusForbidden)
			return
		}
	}

	if s.unknownHT == nil {
		jsonError(w, "tracker not available", http.StatusServiceUnavailable)
		return
	}
	s.unknownHT.Block(host)
	s.logger.Info("blocked unknown domain", "host", host)
	jsonResponse(w, map[string]string{"status": "blocked", "host": host})
}

func (s *Server) handleUnknownDomainsUnblock(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")

	// Only admins can unblock unknown domains (global security setting)
	if s.authMgr != nil {
		if user, ok := auth.UserFromContext(r.Context()); ok && user.Role != auth.RoleAdmin {
			s.RecordAudit("unknown_domain.unblock", "host: "+host+" (forbidden)", requestIP(r), false)
			jsonError(w, "forbidden: admin access required", http.StatusForbidden)
			return
		}
	}

	if s.unknownHT == nil {
		jsonError(w, "tracker not available", http.StatusServiceUnavailable)
		return
	}
	s.unknownHT.Unblock(host)
	s.logger.Info("unblocked unknown domain", "host", host)
	jsonResponse(w, map[string]string{"status": "unblocked", "host": host})
}

func (s *Server) handleUnknownDomainsDismiss(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")

	// Only admins can dismiss unknown domains (global security setting)
	if s.authMgr != nil {
		if user, ok := auth.UserFromContext(r.Context()); ok && user.Role != auth.RoleAdmin {
			s.RecordAudit("unknown_domain.dismiss", "host: "+host+" (forbidden)", requestIP(r), false)
			jsonError(w, "forbidden: admin access required", http.StatusForbidden)
			return
		}
	}

	if s.unknownHT == nil {
		jsonError(w, "tracker not available", http.StatusServiceUnavailable)
		return
	}
	s.unknownHT.Dismiss(host)
	jsonResponse(w, map[string]string{"status": "dismissed", "host": host})
}

// --- SFTP Users ---

func (s *Server) handleUserList(w http.ResponseWriter, r *http.Request) {
	users := siteuser.ListUsers()
	if users == nil {
		users = []siteuser.User{}
	}
	jsonResponse(w, users)
}

func (s *Server) handleUserCreate(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Domain string `json:"domain"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Domain == "" {
		jsonError(w, "domain is required", http.StatusBadRequest)
		return
	}

	webRoot := "/var/www"
	s.configMu.RLock()
	if s.config.Global.WebRoot != "" {
		webRoot = s.config.Global.WebRoot
	}
	s.configMu.RUnlock()

	user, password, err := siteuser.CreateUser(webRoot, req.Domain)
	if err != nil {
		jsonError(w, "create user: "+err.Error(), http.StatusInternalServerError)
		return
	}

	s.logger.Info("SFTP user created", "domain", req.Domain, "username", user.Username)
	jsonResponse(w, map[string]string{
		"username":  user.Username,
		"domain":    user.Domain,
		"password":  password,
		"home_dir":  user.HomeDir,
		"web_dir":   user.WebDir,
		"server_ip": serverip.PublicIP(),
		"port":      "22",
	})
}

func (s *Server) handleUserDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requirePin(w, r) {
		return
	}
	domain := r.PathValue("domain")
	if err := siteuser.DeleteUser(domain); err != nil {
		jsonError(w, "delete user: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.logger.Info("SFTP user deleted", "domain", domain)
	jsonResponse(w, map[string]string{"status": "deleted", "domain": domain})
}

// --- Domain detail ---

func (s *Server) handleDomainDetail(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")

	// Check domain permissions for non-admin users
	if s.authMgr != nil {
		if user, ok := auth.UserFromContext(r.Context()); ok && user.Role != auth.RoleAdmin {
			if !s.authMgr.CanManageDomain(user, host) {
				s.RecordAudit("domain.read", "domain: "+host+" (forbidden)", requestIP(r), false)
				jsonError(w, "forbidden: cannot view this domain", http.StatusForbidden)
				return
			}
		}
	}

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
// Secrets (api_key, pin_code, totp_secret) are masked with asterisks.
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

	// Mask secrets in raw YAML before sending to dashboard
	content := string(data)
	for _, key := range []string{"api_key", "pin_code", "totp_secret", "secret_key", "password"} {
		content = maskYAMLValue(content, key)
	}

	jsonResponse(w, map[string]string{"content": content})
}

// handleConfigRawPut validates and writes raw YAML content to the main config
// file, then triggers a reload.
func (s *Server) handleConfigRawPut(w http.ResponseWriter, r *http.Request) {
	if s.configPath == "" {
		jsonError(w, "config path not set", http.StatusNotImplemented)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MB limit
	var req struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	data := []byte(req.Content)

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

// handleSettingsGet returns all global config fields as flat key-value pairs.
func (s *Server) handleSettingsGet(w http.ResponseWriter, _ *http.Request) {
	s.configMu.RLock()
	g := s.config.Global
	s.configMu.RUnlock()

	result := map[string]any{
		// Server
		"global.http_listen":    g.HTTPListen,
		"global.https_listen":   g.HTTPSListen,
		"global.http3":          g.HTTP3Enabled,
		"global.worker_count":   g.WorkerCount,
		"global.max_connections": g.MaxConnections,
		"global.pid_file":       g.PIDFile,
		"global.web_root":       g.WebRoot,
		"global.log_level":      g.LogLevel,
		"global.log_format":     g.LogFormat,
		// Timeouts
		"global.timeouts.read":             g.Timeouts.Read.String(),
		"global.timeouts.read_header":      g.Timeouts.ReadHeader.String(),
		"global.timeouts.write":            g.Timeouts.Write.String(),
		"global.timeouts.idle":             g.Timeouts.Idle.String(),
		"global.timeouts.shutdown_grace":   g.Timeouts.ShutdownGrace.String(),
		"global.timeouts.max_header_bytes": g.Timeouts.MaxHeaderBytes,
		// Admin
		"global.admin.enabled": g.Admin.Enabled,
		"global.admin.listen":  g.Admin.Listen,
		"global.admin.api_key":  maskSecret(g.Admin.APIKey),
		// Multi-User Auth
		"global.users.enabled":        g.Users.Enabled,
		"global.users.allow_reseller": g.Users.AllowResller,
		// MCP
		"global.mcp.enabled": g.MCP.Enabled,
		// ACME
		"global.acme.email":        g.ACME.Email,
		"global.acme.ca_url":       g.ACME.CAURL,
		"global.acme.storage":      g.ACME.Storage,
		"global.acme.dns_provider": g.ACME.DNSProvider,
		// Cache
		"global.cache.enabled":      g.Cache.Enabled,
		"global.cache.memory_limit": byteSizeStr(g.Cache.MemoryLimit),
		"global.cache.disk_path":    g.Cache.DiskPath,
		"global.cache.default_ttl":  g.Cache.DefaultTTL,
		// Alerting
		"global.alerting.enabled":          g.Alerting.Enabled,
		"global.alerting.webhook_url":      g.Alerting.WebhookURL,
		"global.alerting.slack_url":        maskSecret(g.Alerting.SlackURL),
		"global.alerting.telegram_token":   maskSecret(g.Alerting.TelegramToken),
		"global.alerting.telegram_chat_id": g.Alerting.TelegramChatID,
		// Backup
		"global.backup.enabled":  g.Backup.Enabled,
		"global.backup.provider": g.Backup.Provider,
		"global.backup.schedule": g.Backup.Schedule,
		"global.backup.keep":     g.Backup.Keep,
		"global.backup.local.path":   g.Backup.Local.Path,
		"global.backup.s3.endpoint":  g.Backup.S3.Endpoint,
		"global.backup.s3.bucket":    g.Backup.S3.Bucket,
		"global.backup.s3.region":    g.Backup.S3.Region,
		"global.backup.sftp.host":    g.Backup.SFTP.Host,
		"global.backup.sftp.port":    g.Backup.SFTP.Port,
		"global.backup.sftp.user":    g.Backup.SFTP.User,
	}
	jsonResponse(w, result)
}

// handleSettingsPut accepts flat key-value pairs and updates the global config.
func (s *Server) handleSettingsPut(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var updates map[string]any
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	s.configMu.Lock()
	g := &s.config.Global

	for key, val := range updates {
		sv := fmt.Sprintf("%v", val)
		switch key {
		// Server
		case "global.http_listen":    g.HTTPListen = sv
		case "global.https_listen":   g.HTTPSListen = sv
		case "global.http3":          g.HTTP3Enabled = sv == "true"
		case "global.worker_count":   g.WorkerCount = sv
		case "global.max_connections": g.MaxConnections = toInt(val)
		case "global.pid_file":       g.PIDFile = sv
		case "global.web_root":       g.WebRoot = sv
		case "global.log_level":      g.LogLevel = sv
		case "global.log_format":     g.LogFormat = sv
		// Timeouts
		case "global.timeouts.read":             g.Timeouts.Read = parseDur(sv)
		case "global.timeouts.read_header":      g.Timeouts.ReadHeader = parseDur(sv)
		case "global.timeouts.write":            g.Timeouts.Write = parseDur(sv)
		case "global.timeouts.idle":             g.Timeouts.Idle = parseDur(sv)
		case "global.timeouts.shutdown_grace":   g.Timeouts.ShutdownGrace = parseDur(sv)
		case "global.timeouts.max_header_bytes": g.Timeouts.MaxHeaderBytes = toInt(val)
		// Admin
		case "global.admin.enabled": g.Admin.Enabled = sv == "true"
		case "global.admin.listen":  g.Admin.Listen = sv
		case "global.admin.api_key": g.Admin.APIKey = sv
		// pin_code is intentionally not settable via API — must be set in YAML config
		// Multi-User Auth
		case "global.users.enabled":        g.Users.Enabled = sv == "true"
		case "global.users.allow_reseller": g.Users.AllowResller = sv == "true"
		// MCP
		case "global.mcp.enabled": g.MCP.Enabled = sv == "true"
		case "global.mcp.listen":  g.MCP.Listen = sv
		// ACME
		case "global.acme.email":        g.ACME.Email = sv
		case "global.acme.ca_url":       g.ACME.CAURL = sv
		case "global.acme.storage":      g.ACME.Storage = sv
		case "global.acme.dns_provider": g.ACME.DNSProvider = sv
		case "global.acme.on_demand":    g.ACME.OnDemand = sv == "true"
		case "global.acme.on_demand_ask": g.ACME.OnDemandAsk = sv
		// Cache
		case "global.cache.enabled":      g.Cache.Enabled = sv == "true"
		case "global.cache.memory_limit": g.Cache.MemoryLimit = parseBS(sv)
		case "global.cache.disk_path":    g.Cache.DiskPath = sv
		case "global.cache.disk_limit":   g.Cache.DiskLimit = parseBS(sv)
		case "global.cache.default_ttl":  g.Cache.DefaultTTL = toInt(val)
		case "global.cache.grace_ttl":    g.Cache.GraceTTL = toInt(val)
		case "global.cache.stale_while_revalidate": g.Cache.StaleWhileRevalidate = sv == "true"
		case "global.cache.purge_key":   g.Cache.PurgeKey = sv
		// Alerting
		case "global.alerting.enabled":          g.Alerting.Enabled = sv == "true"
		case "global.alerting.webhook_url":      g.Alerting.WebhookURL = sv
		case "global.alerting.slack_url":        g.Alerting.SlackURL = sv
		case "global.alerting.telegram_token":   g.Alerting.TelegramToken = sv
		case "global.alerting.telegram_chat_id": g.Alerting.TelegramChatID = sv
		// Backup
		case "global.backup.enabled":     g.Backup.Enabled = sv == "true"
		case "global.backup.provider":    g.Backup.Provider = sv
		case "global.backup.schedule":    g.Backup.Schedule = sv
		case "global.backup.keep":        g.Backup.Keep = toInt(val)
		case "global.backup.local.path":  g.Backup.Local.Path = sv
		case "global.backup.s3.endpoint": g.Backup.S3.Endpoint = sv
		case "global.backup.s3.bucket":   g.Backup.S3.Bucket = sv
		case "global.backup.s3.region":      g.Backup.S3.Region = sv
		case "global.backup.s3.access_key":  g.Backup.S3.AccessKey = sv
		case "global.backup.s3.secret_key":  g.Backup.S3.SecretKey = sv
		case "global.backup.sftp.host":      g.Backup.SFTP.Host = sv
		case "global.backup.sftp.port":      g.Backup.SFTP.Port = toInt(val)
		case "global.backup.sftp.user":      g.Backup.SFTP.User = sv
		case "global.backup.sftp.key_file":  g.Backup.SFTP.KeyFile = sv
		case "global.backup.sftp.password":  g.Backup.SFTP.Password = sv
		case "global.backup.sftp.remote_path": g.Backup.SFTP.RemotePath = sv
		// Alerting email
		case "global.alerting.email_smtp_host": g.Alerting.EmailSMTP = sv
		case "global.alerting.email_from":      g.Alerting.EmailFrom = sv
		case "global.alerting.email_to":        g.Alerting.EmailTo = sv
		}
	}
	s.configMu.Unlock()

	s.persistConfig()
	ip := requestIP(r)
	s.RecordAudit("settings.update", fmt.Sprintf("%d fields", len(updates)), ip, true)
	jsonResponse(w, map[string]any{"status": "saved", "updated": len(updates)})
}

func toInt(v any) int {
	switch n := v.(type) {
	case float64: return int(n)
	case int:     return n
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

// validateDomainConfig performs comprehensive pre-save validation.
func validateDomainConfig(d *config.Domain, s *Server) error {
	// Type validation
	validTypes := map[string]bool{"static": true, "php": true, "proxy": true, "app": true, "redirect": true}
	if !validTypes[d.Type] {
		return fmt.Errorf("invalid type %q — must be static, php, proxy, app, or redirect", d.Type)
	}

	// SSL mode validation
	validSSL := map[string]bool{"auto": true, "manual": true, "off": true, "": true}
	if !validSSL[d.SSL.Mode] {
		return fmt.Errorf("invalid ssl.mode %q — must be auto, manual, or off", d.SSL.Mode)
	}
	if d.SSL.Mode == "manual" && (d.SSL.Cert == "" || d.SSL.Key == "") {
		return fmt.Errorf("ssl.mode=manual requires cert and key paths")
	}

	// PHP type: verify PHP is available
	if d.Type == "php" && s.phpMgr != nil {
		phpStatus := s.phpMgr.Status()
		activePHP := 0
		for _, p := range phpStatus {
			if !p.Disabled {
				activePHP++
			}
		}
		if activePHP == 0 {
			return fmt.Errorf("no active PHP versions available — install or enable PHP first")
		}
	}

	// Proxy type: must have upstreams
	if d.Type == "proxy" && len(d.Proxy.Upstreams) == 0 {
		return fmt.Errorf("proxy type requires at least one upstream")
	}

	// Redirect type: must have target
	if d.Type == "redirect" && d.Redirect.Target == "" {
		return fmt.Errorf("redirect type requires a target URL")
	}

	// Root path: must be under web root (prevent serving /etc, /root, etc.)
	if d.Root != "" && d.Type != "redirect" {
		webRoot := "/var/www"
		if s != nil {
			s.configMu.RLock()
			if s.config.Global.WebRoot != "" {
				webRoot = s.config.Global.WebRoot
			}
			s.configMu.RUnlock()
		}
		absRoot, _ := filepath.Abs(d.Root)
		absWebRoot, _ := filepath.Abs(webRoot)
		if !strings.HasPrefix(absRoot, absWebRoot) {
			return fmt.Errorf("root path must be under %s (got %s)", webRoot, d.Root)
		}
	}

	// Cache TTL sanity
	if d.Cache.TTL < 0 {
		return fmt.Errorf("cache TTL cannot be negative")
	}

	// Rate limit sanity
	if d.Security.RateLimit.Requests < 0 {
		return fmt.Errorf("rate limit requests cannot be negative")
	}

	return nil
}

// isValidHostname checks if s is a valid domain name (no path traversal, no injection).
func isValidHostname(s string) bool {
	if len(s) == 0 || len(s) > 253 {
		return false
	}
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '.' || c == '*') {
			return false
		}
	}
	return !strings.Contains(s, "..") && s[0] != '-' && s[0] != '.'
}

// maskSecret returns "****" + last 4 chars for non-empty secrets, "" for empty.
// maskYAMLValue replaces the value of a YAML key with "********" in raw YAML text.
func maskYAMLValue(content, key string) string {
	var result strings.Builder
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, key+":") {
			idx := strings.Index(line, key+":")
			result.WriteString(line[:idx] + key + `: "********"`)
		} else {
			result.WriteString(line)
		}
		result.WriteByte('\n')
	}
	return strings.TrimSuffix(result.String(), "\n")
}

func maskSecret(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 4 {
		return "****"
	}
	return "****" + s[len(s)-4:]
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
	var tmp struct{ Val config.ByteSize `yaml:"val"` }
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
				s.RecordAudit("domain.read_raw", "domain: "+host+" (forbidden)", requestIP(r), false)
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
				s.RecordAudit("domain.update_raw", "domain: "+host+" (forbidden)", requestIP(r), false)
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
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	data := []byte(req.Content)

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

// SetBandwidthManager sets the bandwidth manager for bandwidth monitoring and limits.
func (s *Server) SetBandwidthManager(m *bandwidth.Manager) { s.bwMgr = m }

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
		if s.webhookMgr != nil {
			s.webhookMgr.Fire(webhook.EventBackupFailed, map[string]any{
				"provider": req.Provider,
				"error":    err.Error(),
			})
		}
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.RecordAudit("backup.create", "provider: "+req.Provider, ip, true)
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
	ip := requestIP(r)
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
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
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
		s.RecordAudit("backup.domain", req.Domain+": "+err.Error(), ip, false)
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.RecordAudit("backup.domain", req.Domain, ip, true)
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
	if !s.requirePin(w, r) {
		return
	}
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

// ── 2FA / TOTP ──────────────────────────────────────────────────────────────

func (s *Server) handle2FAStatus(w http.ResponseWriter, r *http.Request) {
	s.configMu.RLock()
	enabled := s.config.Global.Admin.TOTPSecret != ""
	s.configMu.RUnlock()
	jsonResponse(w, map[string]bool{"enabled": enabled})
}

func (s *Server) handle2FASetup(w http.ResponseWriter, r *http.Request) {
	s.configMu.RLock()
	existing := s.config.Global.Admin.TOTPSecret
	s.configMu.RUnlock()
	if existing != "" {
		jsonError(w, "2FA is already enabled; disable it first to reconfigure", http.StatusConflict)
		return
	}

	secret, err := GenerateTOTPSecret()
	if err != nil {
		jsonError(w, "failed to generate secret", http.StatusInternalServerError)
		return
	}

	uri := TOTPProvisioningURI(secret, "admin", "UWAS")
	// Don't save yet — user must verify with a valid code first.
	// Store per-user so concurrent 2FA setups don't overwrite each other.
	username := "admin"
	if user, ok := auth.UserFromContext(r.Context()); ok {
		username = user.Username
	}
	s.pendingTOTPMu.Lock()
	if s.pendingTOTP == nil {
		s.pendingTOTP = make(map[string]string)
	}
	s.pendingTOTP[username] = secret
	s.pendingTOTPMu.Unlock()

	jsonResponse(w, map[string]string{
		"secret": secret,
		"uri":    uri,
	})
}

func (s *Server) handle2FAVerify(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	username := "admin"
	if user, ok := auth.UserFromContext(r.Context()); ok {
		username = user.Username
	}

	s.pendingTOTPMu.Lock()
	secret := ""
	if s.pendingTOTP != nil {
		secret = s.pendingTOTP[username]
	}
	s.pendingTOTPMu.Unlock()

	if secret == "" {
		// Already enabled — validate against active secret
		s.configMu.RLock()
		secret = s.config.Global.Admin.TOTPSecret
		s.configMu.RUnlock()
	}

	if secret == "" {
		jsonError(w, "no 2FA setup pending; call /auth/2fa/setup first", http.StatusBadRequest)
		return
	}

	if !ValidateTOTP(secret, req.Code) {
		jsonError(w, "invalid code", http.StatusUnauthorized)
		return
	}

	// If this was a pending setup, activate it.
	s.pendingTOTPMu.Lock()
	pending := ""
	if s.pendingTOTP != nil {
		pending = s.pendingTOTP[username]
		delete(s.pendingTOTP, username)
	}
	s.pendingTOTPMu.Unlock()

	if pending != "" {
		s.configMu.Lock()
		s.config.Global.Admin.TOTPSecret = pending
		s.configMu.Unlock()
	}

	s.persistConfig()
	ip := requestIP(r)
	s.RecordAudit("2fa.enabled", "TOTP activated", ip, true)

	jsonResponse(w, map[string]any{"status": "2fa_enabled"})
}

func (s *Server) handle2FADisable(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	s.configMu.RLock()
	secret := s.config.Global.Admin.TOTPSecret
	s.configMu.RUnlock()

	if secret == "" {
		jsonError(w, "2FA is not enabled", http.StatusBadRequest)
		return
	}

	if !ValidateTOTP(secret, req.Code) {
		jsonError(w, "invalid code", http.StatusUnauthorized)
		return
	}

	s.configMu.Lock()
	s.config.Global.Admin.TOTPSecret = ""
	s.configMu.Unlock()

	s.persistConfig()
	ip := requestIP(r)
	s.RecordAudit("2fa.disabled", "TOTP deactivated", ip, true)

	jsonResponse(w, map[string]any{"status": "2fa_disabled"})
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
		s.recordAuthFailure(ip)
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
	s.RecordAudit("auth.login", req.Username, ip, true)
	if s.webhookMgr != nil {
		s.webhookMgr.Fire(webhook.EventLoginSuccess, map[string]any{
			"username": req.Username,
			"ip":       ip,
		})
	}

	jsonResponse(w, map[string]any{
		"status":    "authenticated",
		"token":     session.Token,
		"user_id":   session.UserID,
		"username":  session.Username,
		"role":      session.Role,
		"domains":   session.Domains,
		"expires_at": session.ExpiresAt,
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

	ip := requestIP(r)
	s.RecordAudit("auth.logout", "", ip, true)

	jsonResponse(w, map[string]string{"status": "logged_out"})
}

func (s *Server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	jsonResponse(w, map[string]any{
		"id":         user.ID,
		"username":   user.Username,
		"email":      user.Email,
		"role":       user.Role,
		"domains":    user.Domains,
		"api_key":    maskSecret(user.APIKey),
		"enabled":    user.Enabled,
		"created_at": user.CreatedAt,
		"last_login": user.LastLogin,
	})
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
		jsonResponse(w, []*auth.User{user})
		return
	}

	users := s.authMgr.ListUsers()
	if users == nil {
		users = []*auth.User{}
	}

	// Mask password hashes
	for _, u := range users {
		u.Password = ""
	}

	jsonResponse(w, users)
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

	user.Password = "" // Mask password hash
	jsonResponse(w, user)
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
	if role != auth.RoleUser && role != auth.RoleReseller {
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

	ip := requestIP(r)
	s.RecordAudit("auth.user.create", req.Username+" ("+req.Role+")", ip, true)

	user.Password = "" // Mask password hash
	w.WriteHeader(http.StatusCreated)
	jsonResponse(w, user)
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

	ip := requestIP(r)
	s.RecordAudit("auth.user.update", username, ip, true)

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

	ip := requestIP(r)
	s.RecordAudit("auth.user.delete", username, ip, true)

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

	ip := requestIP(r)
	s.RecordAudit("auth.user.apikey", username, ip, true)

	jsonResponse(w, map[string]string{"api_key": newKey})
}

func (s *Server) handleUserChangePasswordAuth(w http.ResponseWriter, r *http.Request) {
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
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	// Users can only change their own password unless they're admin
	if currentUser.Role != auth.RoleAdmin && currentUser.Username != username {
		// Regular users must provide current password
		if req.CurrentPassword == "" {
			jsonError(w, "current_password required", http.StatusBadRequest)
			return
		}
		if err := s.authMgr.ChangePassword(username, req.CurrentPassword, req.NewPassword); err != nil {
			jsonError(w, err.Error(), http.StatusUnauthorized)
			return
		}
	} else if currentUser.Role == auth.RoleAdmin {
		// Admin can change password without current password
		// Use UpdateUser directly
		updates := &auth.User{Password: req.NewPassword}
		if err := s.authMgr.UpdateUser(username, updates); err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
	}

	ip := requestIP(r)
	s.RecordAudit("auth.user.password", username, ip, true)

	jsonResponse(w, map[string]string{"status": "password_changed"})
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
	if s.bwMgr == nil {
		jsonError(w, "bandwidth manager not initialized", http.StatusServiceUnavailable)
		return
	}
	s.bwMgr.Reset(host)
	ip := requestIP(r)
	s.RecordAudit("bandwidth.reset", host, ip, true)
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

// SetAppManager sets the application process manager.
func (s *Server) SetAppManager(m *appmanager.Manager) { s.appMgr = m }

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
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
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

	ip := requestIP(r)
	s.RecordAudit("cron.execute", req.Domain+": "+req.Command, ip, record.Success)

	w.Header().Set("Content-Type", "application/json")
	if record.Success {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusOK) // Still 200, but success=false in body
	}
	json.NewEncoder(w).Encode(record)
}

// requirePin checks the X-Pin-Code header against the configured pin_code.
// Returns true if pin is valid or no pin is configured. Returns false and
// sends 403 if pin is required but missing/wrong.
func (s *Server) requirePin(w http.ResponseWriter, r *http.Request) bool {
	s.configMu.RLock()
	pin := s.config.Global.Admin.PinCode
	s.configMu.RUnlock()

	if pin == "" {
		return true // no pin configured, allow
	}

	provided := r.Header.Get("X-Pin-Code")
	if provided == "" {
		jsonError(w, "pin_required", http.StatusForbidden)
		return false
	}
	if subtle.ConstantTimeCompare([]byte(provided), []byte(pin)) != 1 {
		s.RecordAudit("pin.failed", r.URL.Path, requestIP(r), false)
		jsonError(w, "invalid_pin", http.StatusForbidden)
		return false
	}
	return true
}

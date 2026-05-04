package admin

import (
	"bytes"
	crand "crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
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

	"github.com/uwaserver/uwas/internal/admin/dashboard"
	"github.com/uwaserver/uwas/internal/alerting"
	"github.com/uwaserver/uwas/internal/analytics"
	"github.com/uwaserver/uwas/internal/appmanager"
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
	"github.com/uwaserver/uwas/internal/pathsafe"
	"github.com/uwaserver/uwas/internal/phpmanager"
	"github.com/uwaserver/uwas/internal/router"
	"github.com/uwaserver/uwas/internal/serverip"
	"github.com/uwaserver/uwas/internal/siteuser"
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

// phpInstallState tracks a background PHP installation.
type phpInstallState struct {
	Version string `json:"version"`
	Status  string `json:"status"` // "running", "done", "error"
	Output  string `json:"output"`
	Error   string `json:"error,omitempty"`
	Distro  string `json:"distro"`
}

type muxer interface {
	HandleFunc(string, func(http.ResponseWriter, *http.Request))
	Handle(string, http.Handler)
	ServeHTTP(http.ResponseWriter, *http.Request)
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
	mux            muxer
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
	cfRunner      *cfintegration.Runner

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

type authTicket struct {
	token     string    // the real session token or API key
	created   time.Time // when the ticket was issued
	expiresAt time.Time // when the ticket expires
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
	s.cfRunner = cfintegration.NewRunner(log)
	s.initAudit()
	if err := s.loadAuditLog(); err != nil {
		log.Warn("audit log restore failed", "err", err.Error())
	}
	s.registerRoutes()
	if err := s.loadCloudflareState(); err != nil {
		log.Error("cloudflare state load failed", "err", err.Error())
	}
	return s
}

// maskCloudflareToken returns the last 4 chars of the token prefixed with stars.
func maskCloudflareToken(token string) string {
	if len(token) <= 4 {
		return "****"
	}
	return "****" + token[len(token)-4:]
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	s.mux.HandleFunc("GET /api/v1/system", s.handleSystem)
	s.mux.HandleFunc("GET /api/v1/features", s.handleFeatures)
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
	s.mux.HandleFunc("POST /api/v1/auth/ticket", s.handleAuthTicket)
	s.mux.HandleFunc("GET /api/v1/sse/stats", s.handleSSEStats)
	s.mux.HandleFunc("GET /api/v1/sse/logs", s.handleSSELogs)
	s.mux.HandleFunc("GET /api/v1/config/export", s.handleConfigExport)
	s.mux.HandleFunc("GET /api/v1/certs", s.handleCerts)
	s.mux.HandleFunc("POST /api/v1/certs/{host}/renew", s.handleCertRenew)
	s.mux.HandleFunc("POST /api/v1/certs/{host}/upload", s.handleCertUpload)

	// Bulk domain import
	s.mux.HandleFunc("POST /api/v1/domains/import", s.handleBulkDomainImport)

	// 2FA recovery codes
	s.mux.HandleFunc("POST /api/v1/auth/2fa/recovery-codes", s.handleGenRecoveryCodes)
	s.mux.HandleFunc("POST /api/v1/auth/2fa/recover", s.handleUseRecoveryCode)

	// Notification preferences
	s.mux.HandleFunc("GET /api/v1/settings/notifications", s.handleNotifyPrefsGet)
	s.mux.HandleFunc("PUT /api/v1/settings/notifications", s.handleNotifyPrefsPut)

	// White-label branding
	s.mux.HandleFunc("GET /api/v1/settings/branding", s.handleBrandingGet)
	s.mux.HandleFunc("PUT /api/v1/settings/branding", s.handleBrandingPut)
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
	s.mux.HandleFunc("POST /api/v1/php/{version}/restart", s.handlePHPRestart)

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

	// Web terminal (WebSocket → PTY) — requires admin + pin for security
	s.mux.HandleFunc("GET /api/v1/terminal", func(w http.ResponseWriter, r *http.Request) {
		if !s.requireAdmin(w, r) || !s.requirePin(w, r) {
			return
		}
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

	// Database explorer (SQL editor, table browser)
	s.mux.HandleFunc("GET /api/v1/database/explore/{db}/tables", s.handleDBExploreTables)
	s.mux.HandleFunc("GET /api/v1/database/explore/{db}/tables/{table}", s.handleDBExploreColumns)
	s.mux.HandleFunc("POST /api/v1/database/explore/{db}/query", s.handleDBExploreQuery)

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
	s.mux.HandleFunc("POST /api/v1/migrate/cpanel", s.handleMigrateCPanel)
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

	// Cloudflare integration
	s.mux.HandleFunc("GET /api/v1/cloudflare/status", s.handleCloudflareStatus)
	s.mux.HandleFunc("POST /api/v1/cloudflare/connect", s.handleCloudflareConnect)
	s.mux.HandleFunc("POST /api/v1/cloudflare/disconnect", s.handleCloudflareDisconnect)
	s.mux.HandleFunc("GET /api/v1/cloudflare/tunnels", s.handleCloudflareTunnels)
	s.mux.HandleFunc("POST /api/v1/cloudflare/tunnels", s.handleCloudflareTunnelCreate)
	s.mux.HandleFunc("DELETE /api/v1/cloudflare/tunnels/{id}", s.handleCloudflareTunnelDelete)
	s.mux.HandleFunc("POST /api/v1/cloudflare/tunnels/{id}/start", s.handleCloudflareTunnelStart)
	s.mux.HandleFunc("POST /api/v1/cloudflare/tunnels/{id}/stop", s.handleCloudflareTunnelStop)
	s.mux.HandleFunc("GET /api/v1/cloudflare/tunnels/{id}/logs", s.handleCloudflareTunnelLogs)
	s.mux.HandleFunc("POST /api/v1/cloudflare/cloudflared/install", s.handleCloudflaredInstall)
	s.mux.HandleFunc("POST /api/v1/cloudflare/cache/purge", s.handleCloudflareCachePurge)
	s.mux.HandleFunc("GET /api/v1/cloudflare/zones", s.handleCloudflareZones)
	s.mux.HandleFunc("POST /api/v1/cloudflare/zones/{id}/sync", s.handleCloudflareZoneSync)
	s.mux.HandleFunc("POST /api/v1/cloudflare/zones/{id}/import", s.handleCloudflareZoneImport)

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
		Handler:      middleware.RequestID()(s.authMiddleware(requireJSONMiddleware(s.mux))),
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

		// CSRF protection: state-changing methods must send X-Requested-With header.
		// This guards against cross-site request forgery attacks.
		// Allow requests from same origin (dashboard to itself) or with valid X-Requested-With.
		if r.Method == "POST" || r.Method == "PUT" || r.Method == "PATCH" || r.Method == "DELETE" {
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

	if s.appMgr == nil {
		out["apps"] = disabled("App manager not initialized — pass --enable-apps or set apps.enabled in uwas.yaml")
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
	limit, offset := parsePagination(r)
	domains, total := paginateSlice(domains, limit, offset)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"items":  domains,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
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
	statuses := s.phpMgr.Status()
	limit, offset := parsePagination(r)
	items, total := paginateSlice(statuses, limit, offset)
	jsonResponse(w, map[string]any{
		"items":  items,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
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
		timeout := time.After(10 * time.Minute)
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
				return
			}
			s.phpInstallMu.Lock()
			s.phpInstallStatus.Output = t.Output
			s.phpInstallMu.Unlock()
			select {
			case <-timeout:
				s.phpInstallMu.Lock()
				s.phpInstallStatus.Status = "failed"
				s.phpInstallStatus.Error = "timed out waiting for PHP install"
				s.phpInstallMu.Unlock()
				return
			case <-time.After(500 * time.Millisecond):
			}
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
	// Auto-restart PHP so updated ini takes effect
	restarted := false
	if err := s.phpMgr.RestartFPM(version); err == nil {
		restarted = true
	}
	jsonResponse(w, map[string]any{"status": "updated", "key": req.Key, "value": req.Value, "restarted": restarted})
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

func (s *Server) handlePHPRestart(w http.ResponseWriter, r *http.Request) {
	if s.phpMgr == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	version := r.PathValue("version")

	if err := s.phpMgr.RestartFPM(version); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "restarted", "version": version})
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
	// Auto-restart PHP so updated ini takes effect
	restarted := false
	if err := s.phpMgr.RestartFPM(version); err == nil {
		restarted = true
	}
	jsonResponse(w, map[string]any{"status": "saved", "version": version, "restarted": restarted})
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

	// Persist overrides to domain YAML so they survive restarts
	s.persistDomainPHPOverrides(domain)

	// Restart domain PHP so updated config takes effect
	restarted := false
	if err := s.phpMgr.RestartDomain(domain); err == nil {
		restarted = true
	}
	jsonResponse(w, map[string]any{"status": "updated", "domain": domain, "key": req.Key, "value": req.Value, "restarted": restarted})
}

// HTTPServer returns the underlying http.Server for shutdown during upgrades.
func (s *Server) HTTPServer() *http.Server { return s.httpSrv }

// persistDomainPHPOverrides saves the current in-memory PHP config overrides
// for a domain into its domains.d/*.yaml file so they survive server restarts.
func (s *Server) persistDomainPHPOverrides(domain string) {
	overrides := s.phpMgr.GetDomainConfig(domain)

	path, err := s.domainFilePath(domain)
	if err != nil {
		s.logger.Warn("cannot persist PHP overrides: bad domain path", "domain", domain, "error", err)
		return
	}

	data, err := os.ReadFile(path)
	if err != nil {
		s.logger.Warn("cannot persist PHP overrides: read failed", "domain", domain, "error", err)
		return
	}

	var domCfg config.Domain
	if err := yaml.Unmarshal(data, &domCfg); err != nil {
		s.logger.Warn("cannot persist PHP overrides: parse failed", "domain", domain, "error", err)
		return
	}

	domCfg.PHP.ConfigOverrides = overrides

	out, err := yaml.Marshal(&domCfg)
	if err != nil {
		s.logger.Warn("cannot persist PHP overrides: marshal failed", "domain", domain, "error", err)
		return
	}

	// Atomic write: temp + rename
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".uwas-php-cfg-*.yaml")
	if err != nil {
		s.logger.Warn("cannot persist PHP overrides: temp file failed", "domain", domain, "error", err)
		return
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return
	}
	tmp.Close()

	// On Windows, Rename fails if target exists - remove target first then rename
	if runtime.GOOS == "windows" {
		_ = os.Remove(path) // ignore error if file doesn't exist
	}

	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		s.logger.Warn("cannot persist PHP overrides: rename failed", "domain", domain, "error", err)
		return
	}

	s.logger.Info("persisted PHP config overrides", "domain", domain, "count", len(overrides))
}

// Close releases background resources used by the admin module.
func (s *Server) Close() {
	s.stopAudit()
}

// handleAuthTicket issues a short-lived, single-use ticket that can be passed
// as a query parameter for SSE/WebSocket connections. This avoids putting the
// real token in the URL (which leaks into logs, Referer, browser history).
func (s *Server) handleAuthTicket(w http.ResponseWriter, r *http.Request) {
	// The caller is already authenticated (middleware ran).
	// Extract the token they used to authenticate.
	authHeader := r.Header.Get("Authorization")
	realToken := strings.TrimPrefix(authHeader, "Bearer ")
	if realToken == "" || realToken == authHeader {
		jsonError(w, "bearer token required", http.StatusBadRequest)
		return
	}

	b := make([]byte, 20)
	if _, err := crand.Read(b); err != nil {
		jsonError(w, "entropy failure", http.StatusInternalServerError)
		return
	}
	ticket := hex.EncodeToString(b)

	const ticketTTL = 30 * time.Second

	s.ticketMu.Lock()
	if s.tickets == nil {
		s.tickets = make(map[string]*authTicket)
	}
	// Prune expired tickets.
	now := time.Now()
	for k, t := range s.tickets {
		if now.After(t.expiresAt) {
			delete(s.tickets, k)
		}
	}
	s.tickets[ticket] = &authTicket{token: realToken, created: now, expiresAt: now.Add(ticketTTL)}
	s.ticketMu.Unlock()

	jsonResponse(w, map[string]any{"ticket": ticket, "expires_at": now.Add(ticketTTL)})
}

// redeemTicket exchanges a single-use ticket for the real auth token.
// Returns empty string if the ticket is invalid or expired.
// Uses atomic delete — single-use: once redeemed, the ticket is deleted.
func (s *Server) redeemTicket(ticket string) string {
	s.ticketMu.Lock()
	defer s.ticketMu.Unlock()
	t, ok := s.tickets[ticket]
	if !ok {
		return ""
	}
	if time.Now().After(t.expiresAt) {
		delete(s.tickets, ticket)
		return ""
	}
	// Single-use: delete now so it cannot be redeemed again, then return the token.
	delete(s.tickets, ticket)
	return t.token
}

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
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
	json.NewEncoder(w).Encode(data)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
	w.WriteHeader(code)
	reqID := w.Header().Get("X-Request-ID")
	if reqID != "" {
		json.NewEncoder(w).Encode(map[string]string{"error": msg, "request_id": reqID})
		return
	}
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
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

func (s *Server) requireDomainAccess(w http.ResponseWriter, r *http.Request, domain, action string) bool {
	if s.canAccessDomain(r, domain) {
		return true
	}
	if action != "" {
		s.RecordAudit(action, "domain: "+domain+" (forbidden)", requestIP(r), false)
	}
	jsonError(w, "forbidden: cannot manage this domain", http.StatusForbidden)
	return false
}

func (s *Server) canAccessDomain(r *http.Request, domain string) bool {
	if s.authMgr == nil {
		return true
	}
	user, ok := auth.UserFromContext(r.Context())
	if !ok || user.Role == auth.RoleAdmin {
		return true
	}
	return s.authMgr.CanManageDomain(user, domain)
}

type adminUserResponse struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	Email     string    `json:"email"`
	Role      auth.Role `json:"role"`
	Domains   []string  `json:"domains,omitempty"`
	APIKey    string    `json:"api_key,omitempty"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	LastLogin time.Time `json:"last_login,omitempty"`
}

func adminUserDTO(user *auth.User, revealAPIKey bool) adminUserResponse {
	apiKey := maskSecret(user.APIKey)
	if revealAPIKey && user.FullAPIKey != "" {
		apiKey = user.FullAPIKey
	} else if revealAPIKey {
		apiKey = user.APIKey
	}
	return adminUserResponse{
		ID:        user.ID,
		Username:  user.Username,
		Email:     user.Email,
		Role:      user.Role,
		Domains:   append([]string(nil), user.Domains...),
		APIKey:    apiKey,
		Enabled:   user.Enabled,
		CreatedAt: user.CreatedAt,
		UpdatedAt: user.UpdatedAt,
		LastLogin: user.LastLogin,
	}
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
	if err := os.WriteFile(tmpMain, mainData, 0600); err != nil {
		s.logger.Error("failed to persist config", "path", s.configPath, "error", err)
		return
	}
	if err := os.Rename(tmpMain, s.configPath); err != nil {
		// On Windows, Rename fails if target exists — remove target first then rename
		if runtime.GOOS == "windows" {
			_ = os.Remove(s.configPath)
		}
		if err := os.Rename(tmpMain, s.configPath); err != nil {
			s.logger.Error("failed to rename config", "path", s.configPath, "error", err)
			return
		}
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
		if err := os.WriteFile(fpath, domData, 0600); err != nil {
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
			// Query param host is optional; actual JSON body host is validated below.
			if qHost := r.URL.Query().Get("host"); qHost != "" {
				if !s.authMgr.CanManageDomain(user, qHost) {
					s.RecordAudit("domain.create", "domain: "+qHost+" (forbidden)", ip, false)
					jsonError(w, "forbidden: cannot manage this domain", http.StatusForbidden)
					return
				}
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
	// Mass-assignment protection: non-admin users cannot set sensitive fields.
	if s.authMgr != nil {
		if user, ok := auth.UserFromContext(r.Context()); ok && user.Role != auth.RoleAdmin {
			d.SSL = config.SSLConfig{}
			d.Security = config.SecurityConfig{}
			d.Cache = config.DomainCache{}
			d.Compression = config.CompressionConfig{}
			d.BasicAuth = config.BasicAuthConfig{}
			d.Resources = config.ResourceLimits{}
			d.Aliases = nil
			d.Htaccess = config.HtaccessConfig{}
			d.Locations = nil
			d.PHP.FPMAddress = ""
		}
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
		if strings.EqualFold(existing.Host, d.Host) {
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
			s.phpMgr.RegisterExistingDomain(d.Host, ver, d.PHP.FPMAddress, d.Root, d.PHP.ConfigOverrides)
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

	// App type: register with app manager and start (unless user has disabled it)
	if d.Type == "app" && s.appMgr != nil && (d.App.Command != "" || d.App.Runtime != "") {
		if err := s.appMgr.Register(d.Host, d.App, d.Root); err != nil {
			s.logger.Warn("app register on create failed", "domain", d.Host, "error", err)
		} else if !d.App.Disabled {
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
	confirm := r.URL.Query().Get("confirm") == "true"
	if !confirm {
		jsonError(w, "missing confirmation: add ?confirm=true", http.StatusBadRequest)
		return
	}

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

	// Invalidate cached docroot BEFORE updating config; prevents stale handle on Windows.
	// Without this, domain deletion + re-creation with the same root would silently fail
	// because the deleted domain's os.Root handle is still held.
	if found && domainRoot != "" {
		pathsafe.InvalidateBase(domainRoot)
	}

	if !found {
		s.RecordAudit("domain.delete", "domain: "+host+" (not found)", ip, false)
		jsonError(w, "domain not found", http.StatusNotFound)
		return
	}

	// Cleanup: stop PHP, stop app, remove cron jobs, purge cache, remove SFTP user, delete files
	if cleanup {
		if s.phpMgr != nil {
			s.phpMgr.StopDomain(host)
			s.phpMgr.UnassignDomain(host)
		}
		if s.appMgr != nil {
			s.appMgr.Stop(host)
		}
		if s.cache != nil {
			s.cache.PurgeByTag("site:" + host)
		}
		cronjob.RemoveByDomain(host)
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
	var currentUser *auth.User

	// Check domain permissions for non-admin users
	if s.authMgr != nil {
		user, ok := auth.UserFromContext(r.Context())
		if ok {
			currentUser = user
		}
		if ok && user.Role != auth.RoleAdmin {
			if !s.authMgr.CanManageDomain(user, host) {
				s.RecordAudit("domain.update", "domain: "+host+" (forbidden)", ip, false)
				jsonError(w, "forbidden: cannot manage this domain", http.StatusForbidden)
				return
			}
		}
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 10<<20)) // 10MB limit
	if err != nil {
		if err == io.ErrUnexpectedEOF {
			jsonError(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		jsonError(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	var d config.Domain
	if err := json.Unmarshal(body, &d); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if d.Host != "" && !isValidHostname(d.Host) {
		s.RecordAudit("domain.update", "domain: "+host+" (invalid hostname)", ip, false)
		jsonError(w, "invalid hostname: must be a valid domain name", http.StatusBadRequest)
		return
	}
	if currentUser != nil && currentUser.Role != auth.RoleAdmin && d.Host != "" && d.Host != host {
		if !s.authMgr.CanManageDomain(currentUser, d.Host) {
			s.RecordAudit("domain.update", "domain: "+host+" (forbidden rename)", ip, false)
			jsonError(w, "forbidden: cannot rename to this domain", http.StatusForbidden)
			return
		}
	}

	var raw map[string]json.RawMessage
	_ = json.Unmarshal(body, &raw)
	_, hasAliases := raw["aliases"]
	_, hasLocations := raw["locations"]
	_, hasBasicAuth := raw["basic_auth"]
	_, hasSecurity := raw["security"]
	_, hasCache := raw["cache"]
	_, hasSSL := raw["ssl"]
	_, hasCompression := raw["compression"]
	_, hasResources := raw["resources"]
	_, hasHtaccess := raw["htaccess"]
	replaceMode := r.URL.Query().Get("replace") == "true"

	// Mass-assignment protection: non-admin users cannot modify sensitive fields.
	if currentUser != nil && currentUser.Role != auth.RoleAdmin {
		sensitive := []string{}
		if hasSSL {
			sensitive = append(sensitive, "ssl")
		}
		if hasSecurity {
			sensitive = append(sensitive, "security")
		}
		if hasCache {
			sensitive = append(sensitive, "cache")
		}
		if hasCompression {
			sensitive = append(sensitive, "compression")
		}
		if hasBasicAuth {
			sensitive = append(sensitive, "basic_auth")
		}
		if hasResources {
			sensitive = append(sensitive, "resources")
		}
		if hasAliases {
			sensitive = append(sensitive, "aliases")
		}
		if hasHtaccess {
			sensitive = append(sensitive, "htaccess")
		}
		if hasLocations {
			sensitive = append(sensitive, "locations")
		}
		if rawPHP, ok := raw["php"]; ok {
			var phpRaw map[string]json.RawMessage
			if err := json.Unmarshal(rawPHP, &phpRaw); err == nil {
				if _, ok := phpRaw["fpm_address"]; ok {
					sensitive = append(sensitive, "php.fpm_address")
				}
			}
		}
		if len(sensitive) > 0 {
			s.RecordAudit("domain.update", "domain: "+host+" (forbidden fields: "+strings.Join(sensitive, ", ")+")", ip, false)
			jsonError(w, "forbidden: non-admin users cannot modify "+strings.Join(sensitive, ", "), http.StatusForbidden)
			return
		}
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
				merged.SSL.Mode = d.SSL.Mode
				if d.SSL.Cert != "" {
					merged.SSL.Cert = d.SSL.Cert
				}
				if d.SSL.Key != "" {
					merged.SSL.Key = d.SSL.Key
				}
				if d.SSL.MinVersion != "" {
					merged.SSL.MinVersion = d.SSL.MinVersion
				}
			}
			if hasAliases {
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
			// Locations (always replace — empty list clears routes)
			if hasLocations || replaceMode {
				merged.Locations = d.Locations
			}
			// BasicAuth (replace when provided; enabled=false disables auth)
			if hasBasicAuth || replaceMode {
				merged.BasicAuth = d.BasicAuth
			}
			// Cache, Security, Compression:
			// ?replace=true → full replace (allows disabling features)
			// default → merge (only override non-zero fields)
			if replaceMode {
				merged.Cache = d.Cache
				merged.Security = d.Security
				merged.Compression = d.Compression
			} else {
				if hasCache {
					merged.Cache = d.Cache
				}
				if hasSecurity {
					merged.Security = d.Security
				}
				if d.Compression.Enabled || len(d.Compression.Algorithms) > 0 {
					merged.Compression = d.Compression
				}
			}

			if !isValidHostname(merged.Host) {
				s.configMu.Unlock()
				s.RecordAudit("domain.update", "domain: "+host+" (invalid hostname)", ip, false)
				jsonError(w, "invalid hostname: must be a valid domain name", http.StatusBadRequest)
				return
			}
			if merged.Host != host {
				for j := range s.config.Domains {
					if j != i && strings.EqualFold(s.config.Domains[j].Host, merged.Host) {
						s.configMu.Unlock()
						s.RecordAudit("domain.update", "domain: "+host+" (duplicate rename)", ip, false)
						jsonError(w, "domain already exists", http.StatusConflict)
						return
					}
				}
			}
			if err := validateDomainUpdateConfig(&merged); err != nil {
				s.configMu.Unlock()
				s.RecordAudit("domain.update", "domain: "+host+" (validation failed)", ip, false)
				jsonError(w, "validation failed: "+err.Error(), http.StatusBadRequest)
				return
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
	if !s.requireAdmin(w, r) {
		return
	}
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
					if info.DaysLeft <= 0 {
						ci.Status = "expired"
					}
				}
			}
		}
		certs = append(certs, ci)
	}
	jsonResponse(w, certs)
}

func (s *Server) handleCertRenew(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
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
	if !s.requireAdmin(w, r) {
		return
	}
	users := siteuser.ListUsers()
	if users == nil {
		users = []siteuser.User{}
	}
	limit, offset := parsePagination(r)
	users, total := paginateSlice(users, limit, offset)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"items":  users,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

func (s *Server) handleUserCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
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
	domain := r.PathValue("domain")
	if !s.requireDomainAccess(w, r, domain, "sftp.delete") {
		return
	}
	if !s.requirePin(w, r) {
		return
	}
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
func (s *Server) SetConfigPath(path string) {
	s.configPath = path
	if err := s.loadCloudflareState(); err != nil {
		s.logger.Error("cloudflare state load failed", "err", err.Error(), "path", path)
	}
}

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
	if !s.requireAdmin(w, r) || !s.requirePin(w, r) {
		return
	}
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

	// Validate config semantics before persisting.
	if err := config.Validate(&probe); err != nil {
		jsonError(w, "validation failed: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Atomic write: write to temp file, then rename.
	dir := filepath.Dir(s.configPath)
	tmp, err := os.CreateTemp(dir, ".uwas-config-*.yaml")
	if err != nil {
		s.logger.Error("config raw put: create temp failed", "error", err)
		jsonError(w, "failed to save configuration", http.StatusInternalServerError)
		return
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		s.logger.Error("config raw put: write temp failed", "error", err)
		jsonError(w, "failed to save configuration", http.StatusInternalServerError)
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		s.logger.Error("config raw put: close temp failed", "error", err)
		jsonError(w, "failed to save configuration", http.StatusInternalServerError)
		return
	}

	if err := os.Rename(tmpName, s.configPath); err != nil {
		os.Remove(tmpName)
		s.logger.Error("config raw put: rename failed", "error", err)
		jsonError(w, "failed to save configuration", http.StatusInternalServerError)
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
		"global.http_listen":     g.HTTPListen,
		"global.https_listen":    g.HTTPSListen,
		"global.http3":           g.HTTP3Enabled,
		"global.worker_count":    g.WorkerCount,
		"global.max_connections": g.MaxConnections,
		"global.pid_file":        g.PIDFile,
		"global.web_root":        g.WebRoot,
		"global.log_level":       g.LogLevel,
		"global.log_format":      g.LogFormat,
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
		"global.admin.api_key": maskSecret(g.Admin.APIKey),
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
		"global.backup.enabled":     g.Backup.Enabled,
		"global.backup.provider":    g.Backup.Provider,
		"global.backup.schedule":    g.Backup.Schedule,
		"global.backup.keep":        g.Backup.Keep,
		"global.backup.local.path":  g.Backup.Local.Path,
		"global.backup.s3.endpoint": g.Backup.S3.Endpoint,
		"global.backup.s3.bucket":   g.Backup.S3.Bucket,
		"global.backup.s3.region":   g.Backup.S3.Region,
		"global.backup.sftp.host":   g.Backup.SFTP.Host,
		"global.backup.sftp.port":   g.Backup.SFTP.Port,
		"global.backup.sftp.user":   g.Backup.SFTP.User,
	}
	jsonResponse(w, result)
}

// handleSettingsPut accepts flat key-value pairs and updates the global config.
func (s *Server) handleSettingsPut(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
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
		case "global.http_listen":
			g.HTTPListen = sv
		case "global.https_listen":
			g.HTTPSListen = sv
		case "global.http3":
			g.HTTP3Enabled = sv == "true"
		case "global.worker_count":
			g.WorkerCount = sv
		case "global.max_connections":
			g.MaxConnections = toInt(val)
		case "global.pid_file":
			g.PIDFile = sv
		case "global.web_root":
			g.WebRoot = sv
		case "global.log_level":
			g.LogLevel = sv
		case "global.log_format":
			g.LogFormat = sv
		// Timeouts
		case "global.timeouts.read":
			g.Timeouts.Read = parseDur(sv)
		case "global.timeouts.read_header":
			g.Timeouts.ReadHeader = parseDur(sv)
		case "global.timeouts.write":
			g.Timeouts.Write = parseDur(sv)
		case "global.timeouts.idle":
			g.Timeouts.Idle = parseDur(sv)
		case "global.timeouts.shutdown_grace":
			g.Timeouts.ShutdownGrace = parseDur(sv)
		case "global.timeouts.max_header_bytes":
			g.Timeouts.MaxHeaderBytes = toInt(val)
		// Admin
		case "global.admin.enabled":
			g.Admin.Enabled = sv == "true"
		case "global.admin.listen":
			g.Admin.Listen = sv
		case "global.admin.api_key":
			g.Admin.APIKey = sv
		// pin_code is intentionally not settable via API — must be set in YAML config
		// Multi-User Auth
		case "global.users.enabled":
			g.Users.Enabled = sv == "true"
		case "global.users.allow_reseller":
			g.Users.AllowResller = sv == "true"
		// MCP
		case "global.mcp.enabled":
			g.MCP.Enabled = sv == "true"
		case "global.mcp.listen":
			g.MCP.Listen = sv
		// ACME
		case "global.acme.email":
			g.ACME.Email = sv
		case "global.acme.ca_url":
			g.ACME.CAURL = sv
		case "global.acme.storage":
			g.ACME.Storage = sv
		case "global.acme.dns_provider":
			g.ACME.DNSProvider = sv
		case "global.acme.on_demand":
			g.ACME.OnDemand = sv == "true"
		case "global.acme.on_demand_ask":
			g.ACME.OnDemandAsk = sv
		// Cache
		case "global.cache.enabled":
			g.Cache.Enabled = sv == "true"
		case "global.cache.memory_limit":
			g.Cache.MemoryLimit = parseBS(sv)
		case "global.cache.disk_path":
			g.Cache.DiskPath = sv
		case "global.cache.disk_limit":
			g.Cache.DiskLimit = parseBS(sv)
		case "global.cache.default_ttl":
			g.Cache.DefaultTTL = toInt(val)
		case "global.cache.grace_ttl":
			g.Cache.GraceTTL = toInt(val)
		case "global.cache.stale_while_revalidate":
			g.Cache.StaleWhileRevalidate = sv == "true"
		case "global.cache.purge_key":
			g.Cache.PurgeKey = sv
		// Alerting
		case "global.alerting.enabled":
			g.Alerting.Enabled = sv == "true"
		case "global.alerting.webhook_url":
			g.Alerting.WebhookURL = sv
		case "global.alerting.slack_url":
			g.Alerting.SlackURL = sv
		case "global.alerting.telegram_token":
			g.Alerting.TelegramToken = sv
		case "global.alerting.telegram_chat_id":
			g.Alerting.TelegramChatID = sv
		// Backup
		case "global.backup.enabled":
			g.Backup.Enabled = sv == "true"
		case "global.backup.provider":
			g.Backup.Provider = sv
		case "global.backup.schedule":
			g.Backup.Schedule = sv
		case "global.backup.keep":
			g.Backup.Keep = toInt(val)
		case "global.backup.local.path":
			g.Backup.Local.Path = sv
		case "global.backup.s3.endpoint":
			g.Backup.S3.Endpoint = sv
		case "global.backup.s3.bucket":
			g.Backup.S3.Bucket = sv
		case "global.backup.s3.region":
			g.Backup.S3.Region = sv
		case "global.backup.s3.access_key":
			g.Backup.S3.AccessKey = sv
		case "global.backup.s3.secret_key":
			g.Backup.S3.SecretKey = sv
		case "global.backup.sftp.host":
			g.Backup.SFTP.Host = sv
		case "global.backup.sftp.port":
			g.Backup.SFTP.Port = toInt(val)
		case "global.backup.sftp.user":
			g.Backup.SFTP.User = sv
		case "global.backup.sftp.key_file":
			g.Backup.SFTP.KeyFile = sv
		case "global.backup.sftp.password":
			g.Backup.SFTP.Password = sv
		case "global.backup.sftp.remote_path":
			g.Backup.SFTP.RemotePath = sv
		// Alerting email
		case "global.alerting.email_smtp_host":
			g.Alerting.EmailSMTP = sv
		case "global.alerting.email_from":
			g.Alerting.EmailFrom = sv
		case "global.alerting.email_to":
			g.Alerting.EmailTo = sv
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

// validateDomainConfig performs comprehensive pre-save validation.
func validateDomainConfig(d *config.Domain, s *Server) error {
	webRoot := "/var/www"
	if s != nil {
		s.configMu.RLock()
		if s.config.Global.WebRoot != "" {
			webRoot = s.config.Global.WebRoot
		}
		s.configMu.RUnlock()
	}

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

	// BasicAuth sanity
	if err := validateBasicAuthConfig("domain", d.BasicAuth); err != nil {
		return err
	}
	for i, loc := range d.Locations {
		if loc.BasicAuth == nil {
			continue
		}
		scope := fmt.Sprintf("location[%d]", i)
		if loc.Match != "" {
			scope = fmt.Sprintf("location %q", loc.Match)
		}
		if err := validateBasicAuthConfig(scope, *loc.BasicAuth); err != nil {
			return err
		}
	}

	// Root path: must be under web root (prevent serving /etc, /root, etc.)
	if d.Root != "" && d.Type != "redirect" {
		if !pathsafe.IsWithinBase(webRoot, d.Root) || !pathsafe.IsWithinBaseResolved(webRoot, d.Root) {
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

func validateDomainUpdateConfig(d *config.Domain) error {
	validTypes := map[string]bool{"static": true, "php": true, "proxy": true, "app": true, "redirect": true}
	if !validTypes[d.Type] {
		return fmt.Errorf("invalid type %q", d.Type)
	}
	validSSL := map[string]bool{"auto": true, "manual": true, "off": true, "": true}
	if !validSSL[d.SSL.Mode] {
		return fmt.Errorf("invalid ssl.mode %q", d.SSL.Mode)
	}
	if d.SSL.Mode == "manual" && (d.SSL.Cert == "" || d.SSL.Key == "") {
		return fmt.Errorf("ssl.mode=manual requires cert and key paths")
	}
	if err := validateBasicAuthConfig("domain", d.BasicAuth); err != nil {
		return err
	}
	for i, loc := range d.Locations {
		if loc.BasicAuth == nil {
			continue
		}
		scope := fmt.Sprintf("location[%d]", i)
		if loc.Match != "" {
			scope = fmt.Sprintf("location %q", loc.Match)
		}
		if err := validateBasicAuthConfig(scope, *loc.BasicAuth); err != nil {
			return err
		}
	}
	if d.Cache.TTL < 0 {
		return fmt.Errorf("cache TTL cannot be negative")
	}
	if d.Security.RateLimit.Requests < 0 {
		return fmt.Errorf("rate limit requests cannot be negative")
	}
	return nil
}

func validateBasicAuthConfig(scope string, ba config.BasicAuthConfig) error {
	if strings.ContainsAny(ba.Realm, "\r\n") {
		return fmt.Errorf("%s basic_auth realm contains invalid characters", scope)
	}
	if !ba.Enabled {
		return nil
	}
	if len(ba.Users) == 0 {
		return fmt.Errorf("%s basic_auth enabled requires at least one user", scope)
	}
	for username, password := range ba.Users {
		trimmed := strings.TrimSpace(username)
		if trimmed == "" {
			return fmt.Errorf("%s basic_auth username cannot be empty", scope)
		}
		if trimmed != username {
			return fmt.Errorf("%s basic_auth username %q has leading/trailing spaces", scope, username)
		}
		if strings.ContainsAny(username, ":\r\n") {
			return fmt.Errorf("%s basic_auth username %q contains invalid characters", scope, username)
		}
		if password == "" {
			return fmt.Errorf("%s basic_auth user %q must have a non-empty password", scope, username)
		}
		if strings.ContainsAny(password, "\r\n") {
			return fmt.Errorf("%s basic_auth user %q password contains invalid characters", scope, username)
		}
	}
	return nil
}

// isValidHostname checks if s is a valid domain name per RFC 1035.
// Rejects: empty, >253 chars, labels >63 chars, leading/trailing hyphens or dots,
// path traversal sequences (..), and characters outside [a-zA-Z0-9-.*].
func isValidHostname(s string) bool {
	if len(s) == 0 || len(s) > 253 {
		return false
	}
	labels := strings.Split(s, ".")
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 {
			return false
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
	}
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '.' || c == '*') {
			return false
		}
	}
	return !strings.Contains(s, "..")
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

	// Atomic write: temp file then rename.
	tmp, err := os.CreateTemp(dir, ".uwas-domain-*.yaml")
	if err != nil {
		s.logger.Error("domain raw put: create temp failed", "error", err)
		jsonError(w, "failed to save domain configuration", http.StatusInternalServerError)
		return
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		s.logger.Error("domain raw put: write temp failed", "error", err)
		jsonError(w, "failed to save domain configuration", http.StatusInternalServerError)
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		s.logger.Error("domain raw put: close temp failed", "error", err)
		jsonError(w, "failed to save domain configuration", http.StatusInternalServerError)
		return
	}

	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		s.logger.Error("domain raw put: rename failed", "error", err)
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
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
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
	if !s.requireAdmin(w, r) {
		return
	}
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
	if !s.requireAdmin(w, r) || !s.requirePin(w, r) {
		return
	}
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
	if !s.requireAdmin(w, r) || !s.requirePin(w, r) {
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
		Keep     int    `json:"keep"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Update keep count if provided
	if req.Keep > 0 {
		s.backupMgr.SetKeepCount(req.Keep)
	}

	if req.Enabled != nil && !*req.Enabled {
		s.backupMgr.ScheduleBackup(0)
		s.RecordAudit("backup.schedule", "disabled", ip, true)
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
	s.RecordAudit("backup.schedule", "interval: "+d.String(), ip, true)
	jsonResponse(w, s.backupMgr.ScheduleDetail())
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
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
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

	valid, _ := ValidateTOTP(secret, req.Code)
	if !valid {
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
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
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

	valid, _ := ValidateTOTP(secret, req.Code)
	if !valid {
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
	s.RecordAudit("auth.login", req.Username, ip, true)
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

	if currentUser.Role == auth.RoleAdmin {
		updates := &auth.User{Password: req.NewPassword}
		if err := s.authMgr.UpdateUser(username, updates); err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
	} else {
		if currentUser.Username != username {
			jsonError(w, "forbidden", http.StatusForbidden)
			return
		}
		if req.CurrentPassword == "" {
			jsonError(w, "current_password required", http.StatusBadRequest)
			return
		}
		if err := s.authMgr.ChangePassword(username, req.CurrentPassword, req.NewPassword); err != nil {
			jsonError(w, err.Error(), http.StatusUnauthorized)
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
	// WebSocket connections can't set headers — also check query param
	if provided == "" {
		provided = r.URL.Query().Get("pin")
	}
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

// requireAdmin checks if the authenticated user has the admin role.
func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	user, ok := auth.UserFromContext(r.Context())
	if !ok || user.Role != auth.RoleAdmin {
		jsonError(w, "admin required", http.StatusForbidden)
		return false
	}
	return true
}

// requireRole checks if the authenticated user has one of the allowed roles.
func (s *Server) requireRole(w http.ResponseWriter, r *http.Request, roles ...auth.Role) bool {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	for _, role := range roles {
		if user.Role == role {
			return true
		}
	}
	jsonError(w, "insufficient privileges", http.StatusForbidden)
	return false
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

// --- Cloudflare Integration ---

type cloudflareTunnel struct {
	ID             string    `json:"id"`              // real Cloudflare tunnel UUID
	Name           string    `json:"name"`
	Hostname       string    `json:"hostname"`        // public, e.g. app.example.com
	LocalTarget    string    `json:"local_target"`    // http://localhost:8080 or tcp://localhost:22
	ConnectorToken string    `json:"connector_token"` // from Cloudflare API; never returned to client
	ZoneID         string    `json:"zone_id"`
	DNSRecordID    string    `json:"dns_record_id"`
	CreatedAt      time.Time `json:"created_at,omitempty"`

	// Legacy stub fields kept for unmarshal back-compat with v0.1.6 state files;
	// migrated to Hostname on load if non-empty.
	Domain string `json:"domain,omitempty"`
}

type cloudflareState struct {
	Token     string             `json:"token"`
	AccountID string             `json:"account_id"`
	Email     string             `json:"email"`
	Tunnels   []cloudflareTunnel `json:"tunnels"`
	Connected bool               `json:"connected"`
	UpdatedAt time.Time          `json:"updated_at"`
}

var (
	cloudflareMu     sync.RWMutex
	cloudflareConfig *cloudflareState
)

func (s *Server) handleCloudflareStatus(w http.ResponseWriter, r *http.Request) {
	cloudflareMu.RLock()
	cfg := cloudflareConfig
	cloudflareMu.RUnlock()

	cfd := cfintegration.DetectCloudflared()

	if cfg == nil {
		jsonResponse(w, map[string]any{
			"connected":             false,
			"cloudflared_installed": cfd.Installed,
			"cloudflared_version":   cfd.Version,
		})
		return
	}

	jsonResponse(w, map[string]any{
		"connected":             cfg.Connected,
		"email":                 cfg.Email,
		"account_id":            cfg.AccountID,
		"token_mask":            maskCloudflareToken(cfg.Token),
		"updated_at":            cfg.UpdatedAt,
		"tunnel_count":          len(cfg.Tunnels),
		"cloudflared_installed": cfd.Installed,
		"cloudflared_version":   cfd.Version,
	})
}

func (s *Server) handleCloudflareConnect(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Token     string `json:"token"`
		AccountID string `json:"account_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Token == "" || req.AccountID == "" {
		jsonError(w, "token and account_id are required", http.StatusBadRequest)
		return
	}

	// Validate token by fetching zones
	email, err := s.validateCloudflareToken(req.Token, req.AccountID)
	if err != nil {
		jsonError(w, "invalid token: "+err.Error(), http.StatusBadRequest)
		return
	}

	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{
		Token:     req.Token,
		AccountID: req.AccountID,
		Email:     email,
		Tunnels:   []cloudflareTunnel{},
		Connected: true,
		UpdatedAt: time.Now(),
	}
	saveErr := s.saveCloudflareStateLocked()
	cloudflareMu.Unlock()
	if saveErr != nil {
		s.logger.Error("cloudflare state save failed", "err", saveErr.Error())
	}

	s.RecordAudit("cloudflare.connect", "account: "+req.AccountID, requestIP(r), true)
	jsonResponse(w, map[string]string{"status": "connected"})
}

func (s *Server) validateCloudflareToken(token, accountID string) (string, error) {
	// Call Cloudflare API to validate token and get user info
	req, err := http.NewRequest("GET", "https://api.cloudflare.com/client/v4/user/tokens/verify", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		Success bool `json:"success"`
		Result  struct {
			ID        string `json:"id"`
			Status    string `json:"status"`
			ExpiresOn string `json:"expires_on"`
		} `json:"result"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if !result.Success {
		if len(result.Errors) > 0 {
			return "", fmt.Errorf("%s", result.Errors[0].Message)
		}
		return "", fmt.Errorf("token validation failed")
	}

	// Get account info
	req2, err := http.NewRequest("GET", "https://api.cloudflare.com/client/v4/accounts/"+accountID, nil)
	if err != nil {
		return "", err
	}
	req2.Header.Set("Authorization", "Bearer "+token)
	req2.Header.Set("Content-Type", "application/json")

	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		return "", err
	}
	defer resp2.Body.Close()

	var accResult struct {
		Success bool `json:"success"`
		Result  struct {
			Name string `json:"name"`
		} `json:"result"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&accResult); err != nil {
		return "", err
	}
	if !accResult.Success {
		if len(accResult.Errors) > 0 {
			return "", fmt.Errorf("%s", accResult.Errors[0].Message)
		}
		return "", fmt.Errorf("account validation failed")
	}

	return accResult.Result.Name, nil
}

func (s *Server) handleCloudflareDisconnect(w http.ResponseWriter, r *http.Request) {
	cloudflareMu.Lock()
	oldCfg := cloudflareConfig
	cloudflareConfig = nil
	saveErr := s.saveCloudflareStateLocked()
	cloudflareMu.Unlock()
	if saveErr != nil {
		s.logger.Error("cloudflare state save failed", "err", saveErr.Error())
	}

	if oldCfg != nil {
		s.RecordAudit("cloudflare.disconnect", "account: "+oldCfg.AccountID, requestIP(r), true)
	}

	jsonResponse(w, map[string]string{"status": "disconnected"})
}

// tunnelView is the JSON shape returned to the dashboard. Connector token is
// never exposed to the client.
type tunnelView struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Hostname    string    `json:"hostname"`
	LocalTarget string    `json:"local_target"`
	CreatedAt   time.Time `json:"created_at,omitempty"`
	Running     bool      `json:"running"`
	PID         int       `json:"pid,omitempty"`
	Uptime      string    `json:"uptime,omitempty"`
}

func (s *Server) tunnelToView(t cloudflareTunnel) tunnelView {
	view := tunnelView{
		ID:          t.ID,
		Name:        t.Name,
		Hostname:    t.Hostname,
		LocalTarget: t.LocalTarget,
		CreatedAt:   t.CreatedAt,
	}
	if s.cfRunner != nil {
		st := s.cfRunner.StatusOf(t.ID)
		view.Running = st.Running
		view.PID = st.PID
		view.Uptime = st.Uptime
	}
	return view
}

func (s *Server) handleCloudflareTunnels(w http.ResponseWriter, r *http.Request) {
	cloudflareMu.RLock()
	cfg := cloudflareConfig
	cloudflareMu.RUnlock()

	if cfg == nil || !cfg.Connected {
		jsonResponse(w, []tunnelView{})
		return
	}
	views := make([]tunnelView, 0, len(cfg.Tunnels))
	for _, t := range cfg.Tunnels {
		views = append(views, s.tunnelToView(t))
	}
	jsonResponse(w, views)
}

// validateLocalTarget enforces a small whitelist of cloudflared service URLs.
// We accept http(s)://host:port, tcp://host:port, ssh://host:port, and the
// literal "http_status:NNN" placeholder.
func validateLocalTarget(target string) error {
	target = strings.TrimSpace(target)
	if target == "" {
		return fmt.Errorf("local_target is required (e.g. http://localhost:8080)")
	}
	if strings.HasPrefix(target, "http_status:") {
		return nil
	}
	for _, scheme := range []string{"http://", "https://", "tcp://", "ssh://", "rdp://", "unix:"} {
		if strings.HasPrefix(target, scheme) {
			return nil
		}
	}
	return fmt.Errorf("local_target must start with http://, https://, tcp://, ssh://, rdp://, unix:, or http_status:")
}

func (s *Server) handleCloudflareTunnelCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	cloudflareMu.RLock()
	cfg := cloudflareConfig
	cloudflareMu.RUnlock()
	if cfg == nil || !cfg.Connected {
		jsonError(w, "not connected to Cloudflare", http.StatusBadRequest)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Name        string `json:"name"`
		Hostname    string `json:"hostname"`
		LocalTarget string `json:"local_target"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Hostname = strings.TrimSpace(strings.ToLower(req.Hostname))
	req.LocalTarget = strings.TrimSpace(req.LocalTarget)

	if req.Name == "" || req.Hostname == "" {
		jsonError(w, "name and hostname are required", http.StatusBadRequest)
		return
	}
	if !isValidHostname(req.Hostname) {
		jsonError(w, "invalid hostname", http.StatusBadRequest)
		return
	}
	if err := validateLocalTarget(req.LocalTarget); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Reject duplicate tunnel names locally — Cloudflare also enforces uniqueness.
	for _, t := range cfg.Tunnels {
		if strings.EqualFold(t.Name, req.Name) {
			jsonError(w, "a tunnel named "+req.Name+" already exists", http.StatusConflict)
			return
		}
		if strings.EqualFold(t.Hostname, req.Hostname) {
			jsonError(w, "hostname "+req.Hostname+" is already attached to a tunnel", http.StatusConflict)
			return
		}
	}

	cli := cfintegration.New(cfg.Token, cfg.AccountID)

	// 1. Resolve the zone that owns the hostname.
	zone, err := cli.FindZoneByHostname(req.Hostname)
	if err != nil {
		jsonError(w, "zone lookup: "+err.Error(), http.StatusBadRequest)
		return
	}

	// 2. Create the tunnel.
	cft, err := cli.CreateTunnel(req.Name)
	if err != nil {
		jsonError(w, "create tunnel: "+err.Error(), http.StatusBadGateway)
		return
	}

	// 3. Attach ingress rules (hostname → local target, fallback 404).
	rules := []cfintegration.IngressRule{
		{Hostname: req.Hostname, Service: req.LocalTarget},
		{Service: "http_status:404"},
	}
	if err := cli.PutTunnelConfig(cft.ID, rules); err != nil {
		_ = cli.DeleteTunnel(cft.ID)
		jsonError(w, "put tunnel config: "+err.Error(), http.StatusBadGateway)
		return
	}

	// 4. Create the proxied CNAME at hostname → <tunnel>.cfargotunnel.com.
	recordID, err := cli.CreateTunnelCNAME(zone.ID, req.Hostname, cft.ID)
	if err != nil {
		_ = cli.DeleteTunnel(cft.ID)
		jsonError(w, "create DNS CNAME: "+err.Error(), http.StatusBadGateway)
		return
	}

	// 5. Fetch the connector token (needed to run cloudflared).
	token, err := cli.GetTunnelToken(cft.ID)
	if err != nil {
		_ = cli.DeleteDNSRecord(zone.ID, recordID)
		_ = cli.DeleteTunnel(cft.ID)
		jsonError(w, "get connector token: "+err.Error(), http.StatusBadGateway)
		return
	}

	// 6. Persist.
	tunnel := cloudflareTunnel{
		ID:             cft.ID,
		Name:           req.Name,
		Hostname:       req.Hostname,
		LocalTarget:    req.LocalTarget,
		ConnectorToken: token,
		ZoneID:         zone.ID,
		DNSRecordID:    recordID,
		CreatedAt:      time.Now(),
	}
	cloudflareMu.Lock()
	cloudflareConfig.Tunnels = append(cloudflareConfig.Tunnels, tunnel)
	cloudflareConfig.UpdatedAt = time.Now()
	if err := s.saveCloudflareStateLocked(); err != nil {
		s.logger.Error("cloudflare state save failed", "err", err.Error())
	}
	cloudflareMu.Unlock()

	s.RecordAudit("cloudflare.tunnel.create", req.Name+" → "+req.Hostname, requestIP(r), true)
	jsonResponse(w, s.tunnelToView(tunnel))
}

func (s *Server) findTunnel(id string) (cloudflareTunnel, bool) {
	cloudflareMu.RLock()
	defer cloudflareMu.RUnlock()
	if cloudflareConfig == nil {
		return cloudflareTunnel{}, false
	}
	for _, t := range cloudflareConfig.Tunnels {
		if t.ID == id {
			return t, true
		}
	}
	return cloudflareTunnel{}, false
}

func (s *Server) handleCloudflareTunnelDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		jsonError(w, "tunnel id required", http.StatusBadRequest)
		return
	}
	cloudflareMu.RLock()
	cfg := cloudflareConfig
	cloudflareMu.RUnlock()
	if cfg == nil {
		jsonError(w, "not connected to Cloudflare", http.StatusBadRequest)
		return
	}
	t, ok := s.findTunnel(id)
	if !ok {
		jsonError(w, "tunnel not found", http.StatusNotFound)
		return
	}

	// 1. Stop the local cloudflared process so CF can delete the tunnel.
	if s.cfRunner != nil {
		if err := s.cfRunner.Stop(id); err != nil {
			s.logger.Warn("cloudflared stop on delete failed", "tunnel_id", id, "err", err.Error())
		}
	}

	cli := cfintegration.New(cfg.Token, cfg.AccountID)

	// 2. Delete the DNS record (best-effort).
	if t.ZoneID != "" && t.DNSRecordID != "" {
		if err := cli.DeleteDNSRecord(t.ZoneID, t.DNSRecordID); err != nil {
			s.logger.Warn("DNS record delete failed", "zone", t.ZoneID, "record", t.DNSRecordID, "err", err.Error())
		}
	}

	// 3. Delete the tunnel itself.
	if err := cli.DeleteTunnel(id); err != nil {
		jsonError(w, "delete tunnel: "+err.Error(), http.StatusBadGateway)
		return
	}

	// 4. Remove from state.
	cloudflareMu.Lock()
	newTunnels := make([]cloudflareTunnel, 0, len(cloudflareConfig.Tunnels))
	for _, x := range cloudflareConfig.Tunnels {
		if x.ID != id {
			newTunnels = append(newTunnels, x)
		}
	}
	cloudflareConfig.Tunnels = newTunnels
	cloudflareConfig.UpdatedAt = time.Now()
	if err := s.saveCloudflareStateLocked(); err != nil {
		s.logger.Error("cloudflare state save failed", "err", err.Error())
	}
	cloudflareMu.Unlock()

	if s.cfRunner != nil {
		s.cfRunner.Forget(id)
	}

	s.RecordAudit("cloudflare.tunnel.delete", "id: "+id, requestIP(r), true)
	jsonResponse(w, map[string]string{"status": "deleted"})
}

func (s *Server) handleCloudflareTunnelStart(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		jsonError(w, "tunnel id required", http.StatusBadRequest)
		return
	}
	t, ok := s.findTunnel(id)
	if !ok {
		jsonError(w, "tunnel not found", http.StatusNotFound)
		return
	}

	// Re-fetch token if missing (e.g. legacy state).
	token := t.ConnectorToken
	if token == "" {
		cloudflareMu.RLock()
		cfg := cloudflareConfig
		cloudflareMu.RUnlock()
		if cfg == nil {
			jsonError(w, "not connected to Cloudflare", http.StatusBadRequest)
			return
		}
		cli := cfintegration.New(cfg.Token, cfg.AccountID)
		fresh, err := cli.GetTunnelToken(id)
		if err != nil {
			jsonError(w, "fetch connector token: "+err.Error(), http.StatusBadGateway)
			return
		}
		token = fresh
		cloudflareMu.Lock()
		for i := range cloudflareConfig.Tunnels {
			if cloudflareConfig.Tunnels[i].ID == id {
				cloudflareConfig.Tunnels[i].ConnectorToken = token
				break
			}
		}
		_ = s.saveCloudflareStateLocked()
		cloudflareMu.Unlock()
	}

	if s.cfRunner == nil {
		jsonError(w, "tunnel runner not initialized", http.StatusInternalServerError)
		return
	}
	if err := s.cfRunner.Start(id, token); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.RecordAudit("cloudflare.tunnel.start", "id: "+id, requestIP(r), true)
	jsonResponse(w, map[string]string{"status": "started"})
}

func (s *Server) handleCloudflareTunnelStop(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		jsonError(w, "tunnel id required", http.StatusBadRequest)
		return
	}
	if _, ok := s.findTunnel(id); !ok {
		jsonError(w, "tunnel not found", http.StatusNotFound)
		return
	}
	if s.cfRunner == nil {
		jsonError(w, "tunnel runner not initialized", http.StatusInternalServerError)
		return
	}
	if err := s.cfRunner.Stop(id); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.RecordAudit("cloudflare.tunnel.stop", "id: "+id, requestIP(r), true)
	jsonResponse(w, map[string]string{"status": "stopped"})
}

// handleCloudflareTunnelLogs returns the last ~64 lines from the tunnel's
// cloudflared process. Useful for debugging connection issues from the UI.
func (s *Server) handleCloudflareTunnelLogs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.findTunnel(id); !ok {
		jsonError(w, "tunnel not found", http.StatusNotFound)
		return
	}
	if s.cfRunner == nil {
		jsonResponse(w, map[string]string{"logs": ""})
		return
	}
	jsonResponse(w, map[string]string{"logs": s.cfRunner.Tail(id)})
}

// handleCloudflaredInstall installs the cloudflared binary via the system
// package manager. Linux only.
func (s *Server) handleCloudflaredInstall(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	info, err := cfintegration.InstallCloudflared()
	if err != nil {
		s.RecordAudit("cloudflare.cloudflared.install", "failed: "+err.Error(), requestIP(r), false)
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.RecordAudit("cloudflare.cloudflared.install", "version: "+info.Version, requestIP(r), true)
	jsonResponse(w, info)
}

func (s *Server) handleCloudflareCachePurge(w http.ResponseWriter, r *http.Request) {
	cloudflareMu.RLock()
	cfg := cloudflareConfig
	cloudflareMu.RUnlock()

	if cfg == nil || !cfg.Connected {
		jsonError(w, "not connected to Cloudflare", http.StatusBadRequest)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		URL        string `json:"url"`
		Everything bool   `json:"everything"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Call Cloudflare API to purge cache
	err := s.purgeCloudflareCache(cfg.Token, req.URL, req.Everything)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.RecordAudit("cloudflare.cache.purge", "url: "+req.URL+", everything: "+fmt.Sprintf("%v", req.Everything), requestIP(r), true)
	jsonResponse(w, map[string]string{"status": "purged"})
}

func (s *Server) purgeCloudflareCache(token, url string, everything bool) error {
	// Get zones first
	zones, err := s.fetchCloudflareZones(token)
	if err != nil {
		return err
	}

	for _, zone := range zones {
		var payload []byte
		if everything {
			payload = []byte(`{"purge_everything":true}`)
		} else if url != "" {
			payload = []byte(`{"files":["` + url + `"]}`)
		} else {
			continue
		}

		req, err := http.NewRequest("POST", "https://api.cloudflare.com/client/v4/zones/"+zone.ID+"/purge_cache", bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		resp.Body.Close()
	}

	return nil
}

func (s *Server) handleCloudflareZones(w http.ResponseWriter, r *http.Request) {
	cloudflareMu.RLock()
	cfg := cloudflareConfig
	cloudflareMu.RUnlock()

	if cfg == nil || !cfg.Connected {
		jsonResponse(w, []any{})
		return
	}

	zones, err := s.fetchCloudflareZones(cfg.Token)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	jsonResponse(w, zones)
}

// fetchCloudflareZones iterates all pages of /zones (50 per page) so accounts
// with hundreds of zones get the full list. Hard-capped at 50 pages (2500
// zones) to bound memory and avoid runaway loops on a misbehaving API.
func (s *Server) fetchCloudflareZones(token string) ([]cloudflareZone, error) {
	const perPage = 50
	const maxPages = 50

	all := make([]cloudflareZone, 0, perPage)
	for page := 1; page <= maxPages; page++ {
		url := fmt.Sprintf("https://api.cloudflare.com/client/v4/zones?per_page=%d&page=%d", perPage, page)
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}

		var result struct {
			Success bool `json:"success"`
			Result  []struct {
				ID     string `json:"id"`
				Name   string `json:"name"`
				Status string `json:"status"`
				Plan   struct {
					Name string `json:"name"`
				} `json:"plan"`
			} `json:"result"`
			ResultInfo struct {
				Page       int `json:"page"`
				PerPage    int `json:"per_page"`
				Count      int `json:"count"`
				TotalCount int `json:"total_count"`
				TotalPages int `json:"total_pages"`
			} `json:"result_info"`
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			resp.Body.Close()
			return nil, err
		}
		resp.Body.Close()

		if !result.Success {
			if len(result.Errors) > 0 {
				return nil, fmt.Errorf("%s", result.Errors[0].Message)
			}
			return nil, fmt.Errorf("failed to fetch zones (page %d)", page)
		}

		for _, z := range result.Result {
			all = append(all, cloudflareZone{
				ID:     z.ID,
				Name:   z.Name,
				Status: z.Status,
				Plan:   z.Plan.Name,
			})
		}

		// Stop if we've fetched everything.
		if result.ResultInfo.TotalPages == 0 || page >= result.ResultInfo.TotalPages {
			break
		}
		// Defensive: also stop if this page returned fewer than per_page items.
		if len(result.Result) < perPage {
			break
		}
	}
	return all, nil
}

type cloudflareZone struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Plan   string `json:"plan,omitempty"`
}

func (s *Server) handleCloudflareZoneSync(w http.ResponseWriter, r *http.Request) {
	zoneID := r.PathValue("id")
	if zoneID == "" {
		jsonError(w, "zone id required", http.StatusBadRequest)
		return
	}

	cloudflareMu.RLock()
	cfg := cloudflareConfig
	cloudflareMu.RUnlock()

	if cfg == nil || !cfg.Connected {
		jsonError(w, "not connected to Cloudflare", http.StatusBadRequest)
		return
	}

	// Fetch DNS records from Cloudflare and sync with local DNS
	records, err := s.fetchCloudflareDNSRecords(cfg.Token, zoneID)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.RecordAudit("cloudflare.dns.sync", "zone: "+zoneID, requestIP(r), true)
	jsonResponse(w, map[string]any{
		"status":         "synced",
		"records_synced": len(records),
	})
}

func (s *Server) fetchCloudflareDNSRecords(token, zoneID string) ([]cloudflareDNSRecord, error) {
	req, err := http.NewRequest("GET", "https://api.cloudflare.com/client/v4/zones/"+zoneID+"/dns_records", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Success bool `json:"success"`
		Result  []struct {
			ID       string `json:"id"`
			Type     string `json:"type"`
			Name     string `json:"name"`
			Content  string `json:"content"`
			TTL      int    `json:"ttl"`
			Proxied  bool   `json:"proxied"`
			Priority int    `json:"priority"`
		} `json:"result"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	if !result.Success {
		if len(result.Errors) > 0 {
			return nil, fmt.Errorf("%s", result.Errors[0].Message)
		}
		return nil, fmt.Errorf("failed to fetch DNS records")
	}

	records := make([]cloudflareDNSRecord, len(result.Result))
	for i, r := range result.Result {
		records[i] = cloudflareDNSRecord{
			ID:       r.ID,
			Type:     r.Type,
			Name:     r.Name,
			Content:  r.Content,
			TTL:      r.TTL,
			Proxied:  r.Proxied,
			Priority: r.Priority,
		}
	}
	return records, nil
}

type cloudflareDNSRecord struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Content  string `json:"content"`
	TTL      int    `json:"ttl"`
	Proxied  bool   `json:"proxied"`
	Priority int    `json:"priority"`
}

// handleCloudflareZoneImport pulls A/AAAA/CNAME hostnames from a Cloudflare
// zone and creates UWAS domain entries for any hostname not already configured.
// Body: { "default_type": "static"|"php"|"proxy", "default_root": "/var/www/{host}/public_html" }
func (s *Server) handleCloudflareZoneImport(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	zoneID := r.PathValue("id")
	if zoneID == "" {
		jsonError(w, "zone id required", http.StatusBadRequest)
		return
	}

	cloudflareMu.RLock()
	cfg := cloudflareConfig
	cloudflareMu.RUnlock()
	if cfg == nil || !cfg.Connected {
		jsonError(w, "not connected to Cloudflare", http.StatusBadRequest)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		DefaultType string `json:"default_type"`
		DefaultRoot string `json:"default_root"`
		DryRun      bool   `json:"dry_run"`     // preview only — don't persist
		Hostnames   []string `json:"hostnames"` // optional whitelist; if set, only these are imported
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.DefaultType == "" {
		req.DefaultType = "static"
	}
	switch req.DefaultType {
	case "static", "php", "proxy", "redirect":
	default:
		jsonError(w, "default_type must be one of: static, php, proxy, redirect", http.StatusBadRequest)
		return
	}

	// Build a hostname whitelist (lowercased) if the caller supplied one.
	var whitelist map[string]bool
	if len(req.Hostnames) > 0 {
		whitelist = make(map[string]bool, len(req.Hostnames))
		for _, h := range req.Hostnames {
			whitelist[strings.ToLower(strings.TrimSuffix(h, "."))] = true
		}
	}

	records, err := s.fetchCloudflareDNSRecords(cfg.Token, zoneID)
	if err != nil {
		jsonError(w, "fetch records failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Collect unique hostnames from A/AAAA/CNAME records.
	seen := map[string]bool{}
	hostnames := make([]string, 0, len(records))
	for _, rec := range records {
		switch rec.Type {
		case "A", "AAAA", "CNAME":
		default:
			continue
		}
		host := strings.TrimSuffix(strings.ToLower(rec.Name), ".")
		if host == "" || !isValidHostname(host) {
			continue
		}
		// Skip Cloudflare tunnel infrastructure hostnames.
		if strings.HasSuffix(host, ".cfargotunnel.com") {
			continue
		}
		// If caller supplied a whitelist, only consider those hostnames.
		if whitelist != nil && !whitelist[host] {
			continue
		}
		if !seen[host] {
			seen[host] = true
			hostnames = append(hostnames, host)
		}
	}

	added := []string{}
	skipped := []string{}

	s.configMu.Lock()
	existing := map[string]bool{}
	for _, d := range s.config.Domains {
		existing[strings.ToLower(d.Host)] = true
	}

	webRoot := s.config.Global.WebRoot
	if webRoot == "" {
		webRoot = "/var/www"
	}

	// Dry-run: just figure out what would happen without touching state.
	if req.DryRun {
		for _, host := range hostnames {
			if existing[host] {
				skipped = append(skipped, host)
			} else {
				added = append(added, host)
			}
		}
		s.configMu.Unlock()
		jsonResponse(w, map[string]any{
			"added":   added,
			"skipped": skipped,
			"total":   len(hostnames),
			"dry_run": true,
		})
		return
	}

	for _, host := range hostnames {
		if existing[host] {
			skipped = append(skipped, host)
			continue
		}
		root := req.DefaultRoot
		if root == "" {
			root = filepath.Join(webRoot, host, "public_html")
		} else {
			root = strings.ReplaceAll(root, "{host}", host)
		}
		d := config.Domain{
			Host: host,
			Type: req.DefaultType,
			Root: root,
			SSL:  config.SSLConfig{Mode: "auto"},
		}
		if d.Type == "php" {
			d.PHP.IndexFiles = []string{"index.php", "index.html"}
			d.Htaccess = config.HtaccessConfig{Mode: "import"}
			d.Security.WAF.Enabled = true
			d.Security.BlockedPaths = []string{".git", ".env", "wp-config.php"}
		}
		if d.Type != "redirect" {
			d.Cache.Enabled = true
			d.Cache.TTL = 3600
		}
		// Best-effort web root creation.
		if root != "" {
			if err := os.MkdirAll(root, 0755); err != nil {
				s.logger.Warn("import: web root create failed", "domain", host, "error", err)
			}
		}
		s.config.Domains = append(s.config.Domains, d)
		existing[host] = true
		added = append(added, host)
	}
	s.configMu.Unlock()

	if len(added) > 0 {
		s.notifyDomainChange()
	}

	s.RecordAudit("cloudflare.zones.import",
		fmt.Sprintf("zone: %s, added: %d, skipped: %d", zoneID, len(added), len(skipped)),
		requestIP(r), true)

	jsonResponse(w, map[string]any{
		"added":   added,
		"skipped": skipped,
		"total":   len(hostnames),
	})
}

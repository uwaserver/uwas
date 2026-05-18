package admin

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/uwaserver/uwas/internal/admin/dashboard"
)

// registerRoutes wires every admin API endpoint onto s.mux. The work is split
// into themed sub-registrars so adding a new endpoint lands in an obvious
// place, and duplicate-path bugs become obvious at review time. Keep each
// sub-registrar ≤ ~30 routes.
//
// Order is informational only — net/http's ServeMux is path-keyed.
func (s *Server) registerRoutes() {
	s.registerCoreRoutes()
	s.registerDomainRoutes()
	s.registerCertRoutes()
	s.registerAuthRoutes()
	s.registerSettingsRoutes()
	s.registerPHPRoutes()
	s.registerAppRoutes()
	s.registerDatabaseRoutes()
	s.registerDNSAndCloudflareRoutes()
	s.registerSystemAdminRoutes()
	s.registerHostingRoutes()
	s.registerObservabilityRoutes()
	s.registerMigrationRoutes()
	s.registerDashboardUI()
}

// registerCoreRoutes covers health, system info, stats, config snapshots,
// reload, and the prometheus metrics endpoint.
func (s *Server) registerCoreRoutes() {
	s.mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	s.mux.HandleFunc("GET /api/v1/system", s.handleSystem)
	s.mux.HandleFunc("GET /api/v1/features", s.handleFeatures)
	s.mux.HandleFunc("GET /api/v1/stats", s.handleStats)
	s.mux.HandleFunc("GET /api/v1/stats/domains", s.handleStatsDomains)
	s.mux.HandleFunc("GET /api/v1/config", s.handleConfig)
	s.mux.HandleFunc("POST /api/v1/reload", s.handleReload)
	s.mux.HandleFunc("POST /api/v1/cache/purge", s.handleCachePurge)
	s.mux.HandleFunc("GET /api/v1/cache/stats", s.handleCacheStats)
	s.mux.HandleFunc("GET /api/v1/security/stats", s.handleSecurityStats)
	s.mux.HandleFunc("GET /api/v1/security/blocked", s.handleSecurityBlocked)
	s.mux.Handle("GET /api/v1/metrics", s.metrics.Handler())
	s.mux.HandleFunc("GET /api/v1/config/raw", s.handleConfigRawGet)
	s.mux.HandleFunc("PUT /api/v1/config/raw", s.handleConfigRawPut)
	s.mux.HandleFunc("GET /api/v1/config/export", s.handleConfigExport)
}

// registerDomainRoutes covers domain CRUD, raw YAML, bulk import, detail,
// debug, health, and the public-facing /unknown-domains views.
func (s *Server) registerDomainRoutes() {
	s.mux.HandleFunc("GET /api/v1/domains", s.handleDomains)
	s.mux.HandleFunc("POST /api/v1/domains", s.handleAddDomain)
	s.mux.HandleFunc("DELETE /api/v1/domains/{host}", s.handleDeleteDomain)
	s.mux.HandleFunc("PUT /api/v1/domains/{host}", s.handleUpdateDomain)
	s.mux.HandleFunc("GET /api/v1/domains/{host}", s.handleDomainDetail)
	s.mux.HandleFunc("GET /api/v1/domains/{host}/debug", s.handleDomainDebug)
	s.mux.HandleFunc("GET /api/v1/domains/health", s.handleDomainHealth)
	s.mux.HandleFunc("POST /api/v1/domains/import", s.handleBulkDomainImport)
	s.mux.HandleFunc("GET /api/v1/config/domains/{host}/raw", s.handleDomainRawGet)
	s.mux.HandleFunc("PUT /api/v1/config/domains/{host}/raw", s.handleDomainRawPut)

	// Unknown-host tracker (lives logically with domains).
	s.mux.HandleFunc("GET /api/v1/unknown-domains", s.handleUnknownDomainsList)
	s.mux.HandleFunc("POST /api/v1/unknown-domains/{host}/alias", s.handleUnknownDomainsAlias)
	s.mux.HandleFunc("POST /api/v1/unknown-domains/{host}/block", s.handleUnknownDomainsBlock)
	s.mux.HandleFunc("POST /api/v1/unknown-domains/{host}/unblock", s.handleUnknownDomainsUnblock)
	s.mux.HandleFunc("DELETE /api/v1/unknown-domains/{host}", s.handleUnknownDomainsDismiss)
}

// registerCertRoutes wires TLS cert listing, renewal, and upload.
func (s *Server) registerCertRoutes() {
	s.mux.HandleFunc("GET /api/v1/certs", s.handleCerts)
	s.mux.HandleFunc("POST /api/v1/certs/{host}/renew", s.handleCertRenew)
	s.mux.HandleFunc("POST /api/v1/certs/{host}/upload", s.handleCertUpload)
}

// registerAuthRoutes covers login/logout, 2FA, recovery codes, ticket-based
// SSE/WebSocket auth, and user CRUD for multi-user mode.
func (s *Server) registerAuthRoutes() {
	// Session auth
	s.mux.HandleFunc("POST /api/v1/auth/login", s.handleLogin)
	s.mux.HandleFunc("POST /api/v1/auth/logout", s.handleLogout)
	s.mux.HandleFunc("GET /api/v1/auth/me", s.handleAuthMe)
	s.mux.HandleFunc("POST /api/v1/auth/ticket", s.handleAuthTicket)

	// 2FA
	s.mux.HandleFunc("GET /api/v1/auth/2fa/status", s.handle2FAStatus)
	s.mux.HandleFunc("POST /api/v1/auth/2fa/setup", s.handle2FASetup)
	s.mux.HandleFunc("POST /api/v1/auth/2fa/verify", s.handle2FAVerify)
	s.mux.HandleFunc("POST /api/v1/auth/2fa/disable", s.handle2FADisable)
	s.mux.HandleFunc("POST /api/v1/auth/2fa/recovery-codes", s.handleGenRecoveryCodes)
	s.mux.HandleFunc("POST /api/v1/auth/2fa/recover", s.handleUseRecoveryCode)

	// User management (admin/reseller)
	s.mux.HandleFunc("GET /api/v1/auth/users", s.handleUserListAuth)
	s.mux.HandleFunc("POST /api/v1/auth/users", s.handleUserCreateAuth)
	s.mux.HandleFunc("GET /api/v1/auth/users/{username}", s.handleUserGetAuth)
	s.mux.HandleFunc("PUT /api/v1/auth/users/{username}", s.handleUserUpdateAuth)
	s.mux.HandleFunc("DELETE /api/v1/auth/users/{username}", s.handleUserDeleteAuth)
	s.mux.HandleFunc("POST /api/v1/auth/users/{username}/apikey", s.handleUserRegenerateAPIKeyAuth)
	s.mux.HandleFunc("POST /api/v1/auth/users/{username}/password", s.handleUserChangePasswordAuth)
}

// registerSettingsRoutes covers the structured settings API, notification
// preferences, white-label branding, and notify test.
func (s *Server) registerSettingsRoutes() {
	s.mux.HandleFunc("GET /api/v1/settings", s.handleSettingsGet)
	s.mux.HandleFunc("PUT /api/v1/settings", s.handleSettingsPut)
	s.mux.HandleFunc("GET /api/v1/settings/notifications", s.handleNotifyPrefsGet)
	s.mux.HandleFunc("PUT /api/v1/settings/notifications", s.handleNotifyPrefsPut)
	s.mux.HandleFunc("GET /api/v1/settings/branding", s.handleBrandingGet)
	s.mux.HandleFunc("PUT /api/v1/settings/branding", s.handleBrandingPut)
	s.mux.HandleFunc("POST /api/v1/notify/test", s.handleNotifyTest)
}

// registerPHPRoutes covers the global PHP manager and per-domain PHP instances.
func (s *Server) registerPHPRoutes() {
	// Global PHP manager
	s.mux.HandleFunc("GET /api/v1/php", s.handlePHPList)
	s.mux.HandleFunc("GET /api/v1/php/install-info", s.handlePHPInstallInfo)
	s.mux.HandleFunc("POST /api/v1/php/install", s.handlePHPInstall)
	s.mux.HandleFunc("GET /api/v1/php/install/status", s.handlePHPInstallStatus)
	s.mux.HandleFunc("GET /api/v1/php/{version}/config", s.handlePHPConfig)
	s.mux.HandleFunc("PUT /api/v1/php/{version}/config", s.handlePHPConfigUpdate)
	s.mux.HandleFunc("GET /api/v1/php/{version}/config/raw", s.handlePHPConfigRawGet)
	s.mux.HandleFunc("PUT /api/v1/php/{version}/config/raw", s.handlePHPConfigRawPut)
	s.mux.HandleFunc("GET /api/v1/php/{version}/extensions", s.handlePHPExtensions)
	s.mux.HandleFunc("POST /api/v1/php/{version}/enable", s.handlePHPEnable)
	s.mux.HandleFunc("POST /api/v1/php/{version}/disable", s.handlePHPDisable)
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
}

// registerAppRoutes covers app process management (Node.js, Python,
// Ruby, Go, Docker), the deploy pipeline, the web terminal, and the
// install task queue. Apps are first-class objects keyed by name —
// the pre-v0.6 domain-keyed surface was removed.
func (s *Server) registerAppRoutes() {
	// Apps CRUD + lifecycle.
	s.mux.HandleFunc("GET /api/v1/apps", s.handleAppsList)
	s.mux.HandleFunc("POST /api/v1/apps", s.handleAppCreate)
	s.mux.HandleFunc("GET /api/v1/apps/{name}", s.handleAppGet)
	s.mux.HandleFunc("PUT /api/v1/apps/{name}", s.handleAppUpdate)
	s.mux.HandleFunc("DELETE /api/v1/apps/{name}", s.handleAppDelete)
	s.mux.HandleFunc("POST /api/v1/apps/{name}/start", s.handleAppStart)
	s.mux.HandleFunc("POST /api/v1/apps/{name}/stop", s.handleAppStop)
	s.mux.HandleFunc("POST /api/v1/apps/{name}/restart", s.handleAppRestart)
	s.mux.HandleFunc("GET /api/v1/apps/{name}/logs", s.handleAppLogs)
	s.mux.HandleFunc("GET /api/v1/apps/{name}/stats", s.handleAppStats)
	s.mux.HandleFunc("POST /api/v1/apps/{name}/deploy", s.handleAppDeploy)
	s.mux.HandleFunc("POST /api/v1/apps/{name}/webhook", s.handleAppWebhook)
	s.mux.HandleFunc("GET /api/v1/apps/{name}/webhook-status", s.handleAppWebhookStatus)

	// Web terminal (WebSocket → PTY) — requires admin + pin for security.
	s.mux.HandleFunc("GET /api/v1/terminal", func(w http.ResponseWriter, r *http.Request) {
		if !s.requireAdmin(w, r) || !s.requirePin(w, r) {
			return
		}
		s.terminalHandler().ServeHTTP(w, r)
	})

	// Install task queue (apt/dpkg/etc. serialised)
	s.mux.HandleFunc("GET /api/v1/tasks", s.handleTaskList)
	s.mux.HandleFunc("GET /api/v1/tasks/{id}", s.handleTaskGet)
}

// registerDatabaseRoutes covers system MySQL/MariaDB management, Docker DB
// containers, and the SQL explorer.
func (s *Server) registerDatabaseRoutes() {
	// System database (apt-managed)
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
	s.mux.HandleFunc("POST /api/v1/database/start", s.handleDBStart)
	s.mux.HandleFunc("POST /api/v1/database/stop", s.handleDBStop)
	s.mux.HandleFunc("POST /api/v1/database/restart", s.handleDBRestart)

	// Docker DB containers
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

	// SQL explorer (browser-side SQL editor)
	s.mux.HandleFunc("GET /api/v1/database/explore/{db}/tables", s.handleDBExploreTables)
	s.mux.HandleFunc("GET /api/v1/database/explore/{db}/tables/{table}", s.handleDBExploreColumns)
	s.mux.HandleFunc("POST /api/v1/database/explore/{db}/query", s.handleDBExploreQuery)
}

// registerDNSAndCloudflareRoutes covers DNS record CRUD and the full
// Cloudflare integration (zones, tunnels, cloudflared lifecycle).
func (s *Server) registerDNSAndCloudflareRoutes() {
	// DNS
	s.mux.HandleFunc("GET /api/v1/dns/{domain}", s.handleDNSCheck)
	s.mux.HandleFunc("GET /api/v1/dns/{domain}/records", s.handleDNSRecords)
	s.mux.HandleFunc("POST /api/v1/dns/{domain}/records", s.handleDNSRecordCreate)
	s.mux.HandleFunc("PUT /api/v1/dns/{domain}/records/{id}", s.handleDNSRecordUpdate)
	s.mux.HandleFunc("DELETE /api/v1/dns/{domain}/records/{id}", s.handleDNSRecordDelete)
	s.mux.HandleFunc("POST /api/v1/dns/{domain}/sync", s.handleDNSSync)

	// Cloudflare
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
	s.mux.HandleFunc("POST /api/v1/cloudflare/zones/{id}/import", s.handleCloudflareZoneImport)
}

// registerSystemAdminRoutes covers OS-level admin: systemd services, the
// firewall, package installer, doctor, self-update, SSH keys, IPs/resources.
func (s *Server) registerSystemAdminRoutes() {
	// Services
	s.mux.HandleFunc("GET /api/v1/services", s.handleServicesList)
	s.mux.HandleFunc("POST /api/v1/services/{name}/start", s.handleServiceStart)
	s.mux.HandleFunc("POST /api/v1/services/{name}/stop", s.handleServiceStop)
	s.mux.HandleFunc("POST /api/v1/services/{name}/restart", s.handleServiceRestart)

	// Firewall
	s.mux.HandleFunc("GET /api/v1/firewall", s.handleFirewallStatus)
	s.mux.HandleFunc("POST /api/v1/firewall/allow", s.handleFirewallAllow)
	s.mux.HandleFunc("POST /api/v1/firewall/deny", s.handleFirewallDeny)
	s.mux.HandleFunc("DELETE /api/v1/firewall/{number}", s.handleFirewallDelete)
	s.mux.HandleFunc("POST /api/v1/firewall/enable", s.handleFirewallEnable)
	s.mux.HandleFunc("POST /api/v1/firewall/disable", s.handleFirewallDisable)

	// Doctor + self-update + packages
	s.mux.HandleFunc("GET /api/v1/doctor", s.handleDoctor)
	s.mux.HandleFunc("POST /api/v1/doctor/fix", s.handleDoctorFix)
	s.mux.HandleFunc("GET /api/v1/system/update-check", s.handleUpdateCheck)
	s.mux.HandleFunc("POST /api/v1/system/update", s.handleUpdate)
	s.mux.HandleFunc("GET /api/v1/packages", s.handlePackageList)
	s.mux.HandleFunc("POST /api/v1/packages/install", s.handlePackageInstall)

	// SSH keys + system resources/IPs
	s.mux.HandleFunc("GET /api/v1/users/{domain}/ssh-keys", s.handleSSHKeyList)
	s.mux.HandleFunc("POST /api/v1/users/{domain}/ssh-keys", s.handleSSHKeyAdd)
	s.mux.HandleFunc("DELETE /api/v1/users/{domain}/ssh-keys", s.handleSSHKeyDelete)
	s.mux.HandleFunc("GET /api/v1/system/resources", s.handleSystemResources)
	s.mux.HandleFunc("GET /api/v1/system/ips", s.handleServerIPs)
}

// registerHostingRoutes covers the per-domain hosting features: file
// manager, cron, WordPress, SFTP user provisioning.
func (s *Server) registerHostingRoutes() {
	// File manager
	s.mux.HandleFunc("GET /api/v1/files/{domain}/list", s.handleFileList)
	s.mux.HandleFunc("GET /api/v1/files/{domain}/read", s.handleFileRead)
	s.mux.HandleFunc("PUT /api/v1/files/{domain}/write", s.handleFileWrite)
	s.mux.HandleFunc("DELETE /api/v1/files/{domain}/delete", s.handleFileDelete)
	s.mux.HandleFunc("POST /api/v1/files/{domain}/mkdir", s.handleFileMkdir)
	s.mux.HandleFunc("POST /api/v1/files/{domain}/upload", s.handleFileUpload)
	s.mux.HandleFunc("GET /api/v1/files/{domain}/disk-usage", s.handleDiskUsage)

	// Cron
	s.mux.HandleFunc("GET /api/v1/cron", s.handleCronList)
	s.mux.HandleFunc("POST /api/v1/cron", s.handleCronAdd)
	s.mux.HandleFunc("DELETE /api/v1/cron", s.handleCronDelete)

	// WordPress
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

	// SFTP user management
	s.mux.HandleFunc("GET /api/v1/users", s.handleUserList)
	s.mux.HandleFunc("POST /api/v1/users", s.handleUserCreate)
	s.mux.HandleFunc("DELETE /api/v1/users/{domain}", s.handleUserDelete)
}

// registerObservabilityRoutes covers logs (ring buffer + SSE), audit log,
// monitoring + alerts, bandwidth, cron monitor, webhooks, MCP, backups.
func (s *Server) registerObservabilityRoutes() {
	// Logs
	s.mux.HandleFunc("GET /api/v1/logs", s.handleLogs)
	s.mux.HandleFunc("GET /api/v1/sse/stats", s.handleSSEStats)
	s.mux.HandleFunc("GET /api/v1/sse/logs", s.handleSSELogs)

	// Audit + monitor + alerts
	s.mux.HandleFunc("GET /api/v1/audit", s.handleAudit)
	s.mux.HandleFunc("GET /api/v1/monitor", s.handleMonitor)
	s.mux.HandleFunc("GET /api/v1/alerts", s.handleAlerts)

	// Bandwidth + cron monitor
	s.mux.HandleFunc("GET /api/v1/bandwidth", s.handleBandwidthList)
	s.mux.HandleFunc("GET /api/v1/bandwidth/{host}", s.handleBandwidthGet)
	s.mux.HandleFunc("POST /api/v1/bandwidth/{host}/reset", s.handleBandwidthReset)
	s.mux.HandleFunc("GET /api/v1/cron/monitor", s.handleCronMonitorList)
	s.mux.HandleFunc("GET /api/v1/cron/monitor/{host}", s.handleCronMonitorDomain)
	s.mux.HandleFunc("POST /api/v1/cron/execute", s.handleCronExecute)

	// Webhooks + MCP
	s.mux.HandleFunc("GET /api/v1/webhooks", s.handleWebhookList)
	s.mux.HandleFunc("POST /api/v1/webhooks", s.handleWebhookCreate)
	s.mux.HandleFunc("DELETE /api/v1/webhooks/{id}", s.handleWebhookDelete)
	s.mux.HandleFunc("POST /api/v1/webhooks/test", s.handleWebhookTest)
	s.mux.HandleFunc("GET /api/v1/mcp/tools", s.handleMCPTools)
	s.mux.HandleFunc("POST /api/v1/mcp/call", s.handleMCPCall)

	// Backups
	s.mux.HandleFunc("GET /api/v1/backups", s.handleBackupList)
	s.mux.HandleFunc("POST /api/v1/backups", s.handleBackupCreate)
	s.mux.HandleFunc("POST /api/v1/backups/restore", s.handleBackupRestore)
	s.mux.HandleFunc("POST /api/v1/backups/domain", s.handleBackupDomain)
	s.mux.HandleFunc("DELETE /api/v1/backups/{name}", s.handleBackupDelete)
	s.mux.HandleFunc("GET /api/v1/backups/schedule", s.handleBackupScheduleGet)
	s.mux.HandleFunc("PUT /api/v1/backups/schedule", s.handleBackupSchedulePut)
}

// registerMigrationRoutes covers Nginx/Apache/cPanel migration and clone.
func (s *Server) registerMigrationRoutes() {
	s.mux.HandleFunc("POST /api/v1/migrate", s.handleMigrate)
	s.mux.HandleFunc("POST /api/v1/migrate/cpanel", s.handleMigrateCPanel)
	s.mux.HandleFunc("POST /api/v1/clone", s.handleClone)
}

// registerDashboardUI mounts the embedded React SPA under /_uwas/dashboard/.
// SPA fallback serves index.html for unmatched routes so client-side routing
// works on deep links and refreshes.
func (s *Server) registerDashboardUI() {
	distFS, err := fs.Sub(dashboard.Assets, "dist")
	if err != nil {
		return
	}
	s.mux.Handle("/_uwas/dashboard/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/_uwas/dashboard/")
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

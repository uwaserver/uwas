package admin

import (
	crand "crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/notify"
	"gopkg.in/yaml.v3"
)

// ============ Notifications ============

func (s *Server) handleNotifyTest(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var ch notify.Channel
	if err := json.NewDecoder(r.Body).Decode(&ch); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	ch.Enabled = true
	msg := notify.Message{
		Level:  "info",
		Title:  "UWAS Test Notification",
		Body:   "This is a test notification from your UWAS server.",
		Source: "uwas_test",
	}
	if err := notify.Send(ch, msg); err != nil {
		jsonError(w, "send failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "sent"})
}

// ── 2FA Recovery Codes ─────────────────────────────────────────────

func (s *Server) handleGenRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	codes := make([]string, 8)
	for i := range codes {
		b := make([]byte, 4)
		if _, err := crand.Read(b); err != nil {
			jsonError(w, "entropy failure", http.StatusInternalServerError)
			return
		}
		codes[i] = fmt.Sprintf("%x", b)
	}
	// Store hashed codes in config
	s.configMu.Lock()
	s.config.Global.Admin.RecoveryCodes = codes
	s.configMu.Unlock()
	s.persistConfig()
	s.recordAuditR(r, "2fa.recovery_codes.generated", "", true)
	jsonResponse(w, map[string]any{"codes": codes, "count": len(codes)})
}

func (s *Server) handleUseRecoveryCode(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	s.configMu.Lock()
	found := false
	for i, c := range s.config.Global.Admin.RecoveryCodes {
		if subtle.ConstantTimeCompare([]byte(c), []byte(req.Code)) == 1 {
			// Remove used code
			s.config.Global.Admin.RecoveryCodes = append(
				s.config.Global.Admin.RecoveryCodes[:i],
				s.config.Global.Admin.RecoveryCodes[i+1:]...,
			)
			found = true
			break
		}
	}
	s.configMu.Unlock()
	if !found {
		jsonError(w, "invalid recovery code", http.StatusUnauthorized)
		return
	}
	s.persistConfig()
	s.recordAuditR(r, "2fa.recovery_code.used", "", true)
	jsonResponse(w, map[string]string{"status": "ok"})
}

// ── Notification Preferences ───────────────────────────────────────

func (s *Server) handleNotifyPrefsGet(w http.ResponseWriter, r *http.Request) {
	s.configMu.RLock()
	prefs := map[string]any{
		"alerting": s.config.Global.Alerting,
		"webhooks": s.config.Global.Webhooks,
	}
	s.configMu.RUnlock()
	jsonResponse(w, prefs)
}

func (s *Server) handleNotifyPrefsPut(w http.ResponseWriter, r *http.Request) {
	// Admin-only: webhook URLs and alerting recipients are sensitive system
	// state. Without this guard any authenticated user could redirect alerts
	// to an attacker-controlled endpoint and silently disable notifications.
	if !s.requireAdmin(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Alerting config.AlertingConfig  `json:"alerting"`
		Webhooks []config.WebhookConfig `json:"webhooks"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	s.configMu.Lock()
	s.config.Global.Alerting = req.Alerting
	s.config.Global.Webhooks = req.Webhooks
	s.configMu.Unlock()
	s.persistConfig()
	s.recordAuditR(r, "settings.notifications", "updated", true)
	jsonResponse(w, map[string]string{"status": "saved"})
}

// ── White-Label Branding ───────────────────────────────────────────

func (s *Server) handleBrandingGet(w http.ResponseWriter, r *http.Request) {
	s.configMu.RLock()
	branding := s.config.Global.Admin.Branding
	s.configMu.RUnlock()
	jsonResponse(w, branding)
}

func (s *Server) handleBrandingPut(w http.ResponseWriter, r *http.Request) {
	// Admin-only: branding controls the admin UI chrome and may include
	// image URLs and inline HTML/CSS rendered into other users' sessions —
	// an unauthenticated branding write is a stored-XSS / phishing pivot.
	if !s.requireAdmin(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var branding config.BrandingConfig
	if err := json.NewDecoder(r.Body).Decode(&branding); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	s.configMu.Lock()
	s.config.Global.Admin.Branding = branding
	s.configMu.Unlock()
	s.persistConfig()
	s.recordAuditR(r, "settings.branding", "updated", true)
	jsonResponse(w, map[string]string{"status": "saved"})
}

// --- Settings / raw config editor (moved from api.go) ---

// handleConfigExport returns the current configuration as a YAML file download.
func (s *Server) handleConfigExport(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	// Build a sanitized copy: strip every secret-bearing field. The export
	// gets shared, committed to git, attached to support tickets, etc., so
	// it must not contain anything that grants access to the system or to
	// third-party services.
	//
	// Note on aliasing: `*s.config` shallow-copies, so slices/maps still
	// point at the live config. Direct field assignments on copied structs
	// are safe; mutations through slice or map elements are not — those
	// must go through a deep copy (see Webhooks and Domains below).
	s.configMu.RLock()
	export := *s.config
	s.configMu.RUnlock()

	// Admin
	export.Global.Admin.APIKey = ""
	export.Global.Admin.PinCode = ""
	export.Global.Admin.TOTPSecret = ""
	export.Global.Admin.TLSKey = ""
	export.Global.Admin.RecoveryCodes = nil
	export.Global.Admin.OAuth.GoogleSecret = ""
	export.Global.Admin.OAuth.GitHubSecret = ""

	// ACME
	export.Global.ACME.DNSCredentials = nil

	// Cache
	export.Global.Cache.PurgeKey = ""
	export.Global.Cache.Redis.Password = ""

	// Alerting (Slack URL embeds the auth token in its path)
	export.Global.Alerting.SlackURL = ""
	export.Global.Alerting.TelegramToken = ""

	// Backup providers
	export.Global.Backup.S3.AccessKey = ""
	export.Global.Backup.S3.SecretKey = ""
	export.Global.Backup.SFTP.Password = ""

	// Webhooks slice — deep copy before zeroing Secret so we don't mutate
	// the live config. The shallow `export := *s.config` shares the
	// underlying array.
	if len(export.Global.Webhooks) > 0 {
		webhooks := make([]config.WebhookConfig, len(export.Global.Webhooks))
		copy(webhooks, export.Global.Webhooks)
		for i := range webhooks {
			webhooks[i].Secret = ""
		}
		export.Global.Webhooks = webhooks
	}

	// Per-domain secrets (PHP env vars frequently hold DB passwords / API keys)
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

// handleConfigRawGet returns the raw YAML content of the main config file.
// Secrets (api_key, pin_code, totp_secret) are masked with asterisks.
func (s *Server) handleConfigRawGet(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.configPath == "" {
		jsonError(w, "config path not set", http.StatusNotImplemented)
		return
	}

	data, err := os.ReadFile(s.configPath)
	if err != nil {
		jsonError(w, "failed to read config: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Mask secrets in raw YAML before sending to dashboard. The Settings
	// page parses dynamic credential fields directly out of this payload
	// (DNS provider tokens, OAuth client secrets, alerting tokens, etc.),
	// so anything matched here arrives masked at the browser.
	//
	// Masking is exact-key prefix match — keep this list aligned with the
	// fields rendered as `type: 'secret'` in web/dashboard/src/pages/Settings.tsx.
	content := string(data)
	for _, key := range []string{
		"api_key", "pin_code", "totp_secret", "secret_key", "password",
		"secret_access_key", // Route53 DNS credentials
		"api_token",         // Cloudflare / DigitalOcean DNS, generic
		"client_secret",     // OAuth google/github
		"telegram_token",    // alerting bot token
		"slack_url",         // Slack incoming-webhook URL has the auth token in the path
		"purge_key",         // cache purge key
	} {
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
		s.recordAuditR(r, "config.raw_put", "invalid JSON body", false)
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	data := []byte(req.Content)

	// Validate YAML syntax.
	var probe config.Config
	if err := yaml.Unmarshal(data, &probe); err != nil {
		s.recordAuditR(r, "config.raw_put", "invalid YAML", false)
		jsonError(w, "invalid YAML: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Validate config semantics before persisting.
	if err := config.Validate(&probe); err != nil {
		s.recordAuditR(r, "config.raw_put", "validation failed", false)
		jsonError(w, "validation failed: "+err.Error(), http.StatusBadRequest)
		return
	}

	auditDetail := fmt.Sprintf("bytes: %d, domains: %d", len(data), len(probe.Domains))

	// Crash-safe write: unique temp file + fsync + rename.
	s.persistMu.Lock()
	writeErr := atomicWriteFile(s.configPath, data, 0600)
	s.persistMu.Unlock()
	if writeErr != nil {
		s.logger.Error("config raw put: write failed", "error", writeErr)
		s.recordAuditR(r, "config.raw_put", auditDetail+" (write failed)", false)
		jsonError(w, "failed to save configuration", http.StatusInternalServerError)
		return
	}

	// Trigger reload if available.
	if s.reloadFn != nil {
		if err := s.reloadFn(); err != nil {
			// File is already written, report reload failure.
			s.recordAuditR(r, "config.raw_put", auditDetail+" (reload failed after persist)", false)
			jsonError(w, "config saved but reload failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	s.recordAuditR(r, "config.raw_put", auditDetail, true)
	jsonResponse(w, map[string]string{"status": "saved"})
}

// handleSettingsGet returns all global config fields as flat key-value pairs.
func (s *Server) handleSettingsGet(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
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
		jsonError(w, "invalid JSON", http.StatusBadRequest)
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

	s.ensureAuthManagerFromConfig()
	s.persistConfig()
	s.recordAuditR(r, "settings.update", fmt.Sprintf("%d fields", len(updates)), true)
	jsonResponse(w, map[string]any{"status": "saved", "updated": len(updates)})
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

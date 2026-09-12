// Package settings provides admin API handlers for global server settings,
// notification preferences, white-label branding, 2FA recovery codes,
// config export/import, and the raw YAML config editor.
package settings

import (
	crand "crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/notify"
	"gopkg.in/yaml.v3"
)

// Deps is the interface the sub-package needs from the admin Server.
type Deps interface {
	// Auth
	RequireAdmin(w http.ResponseWriter, r *http.Request) bool
	RequirePin(w http.ResponseWriter, r *http.Request) bool
	// Audit
	RecordAudit(r *http.Request, action, detail string, success bool)
	// Logging
	LogError(msg string, args ...any)
	// Config access
	ConfigPtr() *config.Config
	LockConfig()
	UnlockConfig()
	RLockConfig()
	RUnlockConfig()
	PersistConfig()
	ConfigPath() string
	// Auth manager init
	EnsureAuthManagerFromConfig()
	// Atomic file write
	AtomicWriteFile(path string, data []byte, perm os.FileMode) error
	// Persist serialization mutex
	LockPersist()
	UnlockPersist()
	// Reload
	Reload() error
	// Config parsing helpers
	ToInt(v any) int
	ParseDur(s string) config.Duration
	ByteSizeStr(b config.ByteSize) string
	ParseBS(s string) config.ByteSize
}

// Handler holds settings admin API handlers.
type Handler struct {
	deps Deps
}

// New creates a settings Handler.
func New(deps Deps) *Handler {
	return &Handler{deps: deps}
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

// ── Notifications ──

func (h *Handler) NotifyTest(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
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
		Level: "info", Title: "UWAS Test Notification",
		Body: "This is a test notification from your UWAS server.", Source: "uwas_test",
	}
	if err := notify.Send(ch, msg); err != nil {
		jsonError(w, "send failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "sent"})
}

// ── 2FA Recovery Codes ──

func hashRecoveryCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

// HashRecoveryCode is the exported version for backward-compat wrappers.
func HashRecoveryCode(code string) string { return hashRecoveryCode(code) }

func (h *Handler) GenRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	codes := make([]string, 8)
	hashed := make([]string, 8)
	for i := range codes {
		b := make([]byte, 8)
		if _, err := crand.Read(b); err != nil {
			jsonError(w, "entropy failure", http.StatusInternalServerError)
			return
		}
		codes[i] = fmt.Sprintf("%x", b)
		hashed[i] = hashRecoveryCode(codes[i])
	}
	h.deps.LockConfig()
	h.deps.ConfigPtr().Global.Admin.RecoveryCodes = hashed
	h.deps.UnlockConfig()
	h.deps.PersistConfig()
	h.deps.RecordAudit(r, "2fa.recovery_codes.generated", "", true)
	jsonResponse(w, map[string]any{"codes": codes, "count": len(codes)})
}

func (h *Handler) UseRecoveryCode(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	sum := hashRecoveryCode(req.Code)
	h.deps.LockConfig()
	found := false
	codes := h.deps.ConfigPtr().Global.Admin.RecoveryCodes
	for i, c := range codes {
		if subtle.ConstantTimeCompare([]byte(c), []byte(sum)) == 1 {
			h.deps.ConfigPtr().Global.Admin.RecoveryCodes = append(codes[:i], codes[i+1:]...)
			found = true
			break
		}
	}
	h.deps.UnlockConfig()
	if !found {
		jsonError(w, "invalid recovery code", http.StatusUnauthorized)
		return
	}
	h.deps.PersistConfig()
	h.deps.RecordAudit(r, "2fa.recovery_code.used", "", true)
	jsonResponse(w, map[string]string{"status": "ok"})
}

// ── Notification Preferences ──

func (h *Handler) NotifyPrefsGet(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	h.deps.RLockConfig()
	prefs := map[string]any{
		"alerting": h.deps.ConfigPtr().Global.Alerting,
		"webhooks": h.deps.ConfigPtr().Global.Webhooks,
	}
	h.deps.RUnlockConfig()
	jsonResponse(w, prefs)
}

func (h *Handler) NotifyPrefsPut(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
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
	h.deps.LockConfig()
	h.deps.ConfigPtr().Global.Alerting = req.Alerting
	h.deps.ConfigPtr().Global.Webhooks = req.Webhooks
	h.deps.UnlockConfig()
	h.deps.PersistConfig()
	h.deps.RecordAudit(r, "settings.notifications", "updated", true)
	jsonResponse(w, map[string]string{"status": "saved"})
}

// ── White-Label Branding ──

func (h *Handler) BrandingGet(w http.ResponseWriter, r *http.Request) {
	h.deps.RLockConfig()
	branding := h.deps.ConfigPtr().Global.Admin.Branding
	h.deps.RUnlockConfig()
	jsonResponse(w, branding)
}

func (h *Handler) BrandingPut(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var branding config.BrandingConfig
	if err := json.NewDecoder(r.Body).Decode(&branding); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	h.deps.LockConfig()
	h.deps.ConfigPtr().Global.Admin.Branding = branding
	h.deps.UnlockConfig()
	h.deps.PersistConfig()
	h.deps.RecordAudit(r, "settings.branding", "updated", true)
	jsonResponse(w, map[string]string{"status": "saved"})
}

// ── Config Export ──

func (h *Handler) ConfigExport(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	h.deps.RLockConfig()
	export := *h.deps.ConfigPtr()
	h.deps.RUnlockConfig()

	export.Global.Admin.APIKey = ""
	export.Global.Admin.PinCode = ""
	export.Global.Admin.TOTPSecret = ""
	export.Global.Admin.TLSKey = ""
	export.Global.Admin.RecoveryCodes = nil
	export.Global.Admin.OAuth.GoogleSecret = ""
	export.Global.Admin.OAuth.GitHubSecret = ""
	export.Global.ACME.DNSCredentials = nil
	export.Global.Cache.PurgeKey = ""
	export.Global.Cache.Redis.Password = ""
	export.Global.Alerting.SlackURL = ""
	export.Global.Alerting.TelegramToken = ""
	export.Global.Backup.S3.AccessKey = ""
	export.Global.Backup.S3.SecretKey = ""
	export.Global.Backup.SFTP.Password = ""

	if len(export.Global.Webhooks) > 0 {
		webhooks := make([]config.WebhookConfig, len(export.Global.Webhooks))
		copy(webhooks, export.Global.Webhooks)
		for i := range webhooks {
			webhooks[i].Secret = ""
		}
		export.Global.Webhooks = webhooks
	}
	sanitized := make([]config.Domain, len(export.Domains))
	copy(sanitized, export.Domains)
	for i := range sanitized {
		sanitized[i].PHP.Env = nil
		// BasicAuth.Users is a map[string]string; copy() does a shallow copy so
		// sanitized and export share the same map. Setting it to nil here redacts
		// the exported YAML without affecting the original config in memory.
		sanitized[i].BasicAuth.Users = nil
		for j := range sanitized[i].Locations {
			sanitized[i].Locations[j].BasicAuth.Users = nil
		}
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

// ── Raw Config Editor ──

func (h *Handler) ConfigRawGet(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	configPath := h.deps.ConfigPath()
	if configPath == "" {
		jsonError(w, "config path not set", http.StatusNotImplemented)
		return
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		jsonError(w, "failed to read config: "+err.Error(), http.StatusInternalServerError)
		return
	}
	content := string(data)
	for _, key := range []string{
		"api_key", "pin_code", "totp_secret", "secret_key", "password",
		"secret_access_key", "api_token", "client_secret",
		"google_client_secret", "github_client_secret",
		"tls_key", "telegram_token", "slack_url", "purge_key",
	} {
		content = maskYAMLValue(content, key)
	}
	content = maskYAMLListValue(content, "recovery_codes")
	jsonResponse(w, map[string]string{"content": content})
}

func (h *Handler) ConfigRawPut(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) || !h.deps.RequirePin(w, r) {
		return
	}
	configPath := h.deps.ConfigPath()
	if configPath == "" {
		jsonError(w, "config path not set", http.StatusNotImplemented)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.deps.RecordAudit(r, "config.raw_put", "invalid JSON body", false)
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	// Restore any masked secret before parsing/writing. ConfigRawGet shows
	// secrets as "********"; without this, a save writes that mask straight over
	// the real secret. Unmask from the current on-disk file so untouched
	// secrets survive and only values the operator actually changed take effect.
	if original, rerr := os.ReadFile(configPath); rerr == nil {
		oldContent := string(original)
		for _, key := range []string{
			"api_key", "pin_code", "totp_secret", "secret_key", "password",
			"secret_access_key", "api_token", "client_secret",
			"google_client_secret", "github_client_secret",
			"tls_key", "telegram_token", "slack_url", "purge_key",
		} {
			req.Content = unmaskYAMLValue(req.Content, oldContent, key)
		}
		req.Content = unmaskYAMLListValue(req.Content, oldContent, "recovery_codes")
	}

	data := []byte(req.Content)
	var probe config.Config
	if err := yaml.Unmarshal(data, &probe); err != nil {
		h.deps.RecordAudit(r, "config.raw_put", "invalid YAML", false)
		jsonError(w, "invalid YAML: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := config.ValidateWithDefaults(&probe); err != nil {
		h.deps.RecordAudit(r, "config.raw_put", "validation failed", false)
		jsonError(w, "validation failed: "+err.Error(), http.StatusBadRequest)
		return
	}
	auditDetail := fmt.Sprintf("bytes: %d, domains: %d", len(data), len(probe.Domains))
	h.deps.LockPersist()
	writeErr := h.deps.AtomicWriteFile(configPath, data, 0600)
	h.deps.UnlockPersist()
	if writeErr != nil {
		h.deps.LogError("config raw put: write failed", "error", writeErr)
		h.deps.RecordAudit(r, "config.raw_put", auditDetail+" (write failed)", false)
		jsonError(w, "failed to save configuration", http.StatusInternalServerError)
		return
	}
	if err := h.deps.Reload(); err != nil {
		h.deps.RecordAudit(r, "config.raw_put", auditDetail+" (reload failed after persist)", false)
		jsonError(w, "config saved but reload failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	h.deps.RecordAudit(r, "config.raw_put", auditDetail, true)
	jsonResponse(w, map[string]string{"status": "saved"})
}

// ── Settings Get/Put ──

func (h *Handler) SettingsGet(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	h.deps.RLockConfig()
	g := h.deps.ConfigPtr().Global
	h.deps.RUnlockConfig()

	result := map[string]any{
		"global.http_listen":               g.HTTPListen,
		"global.https_listen":              g.HTTPSListen,
		"global.http3":                     g.HTTP3Enabled,
		"global.worker_count":              g.WorkerCount,
		"global.max_connections":           g.MaxConnections,
		"global.pid_file":                  g.PIDFile,
		"global.web_root":                  g.WebRoot,
		"global.log_level":                 g.LogLevel,
		"global.log_format":                g.LogFormat,
		"global.access_log.enabled":        g.AccessLog.RequestLogEnabled(),
		"global.timeouts.read":             g.Timeouts.Read.String(),
		"global.timeouts.read_header":      g.Timeouts.ReadHeader.String(),
		"global.timeouts.write":            g.Timeouts.Write.String(),
		"global.timeouts.idle":             g.Timeouts.Idle.String(),
		"global.timeouts.shutdown_grace":   g.Timeouts.ShutdownGrace.String(),
		"global.timeouts.max_header_bytes": g.Timeouts.MaxHeaderBytes,
		"global.admin.enabled":             g.Admin.Enabled,
		"global.admin.listen":              g.Admin.Listen,
		"global.admin.api_key":             maskSecret(g.Admin.APIKey),
		"global.users.enabled":             g.Users.Enabled,
		"global.users.allow_reseller":      g.Users.AllowReseller,
		"global.users.session_ttl":         g.Users.SessionTTL,
		"global.rate_limit.requests":       g.RateLimit.Requests,
		"global.rate_limit.window":         g.RateLimit.Window.String(),
		"global.trusted_proxies":           strings.Join(g.TrustedProxies, "\n"),
		"global.mcp.enabled":               g.MCP.Enabled,
		"global.acme.email":                g.ACME.Email,
		"global.acme.ca_url":               g.ACME.CAURL,
		"global.acme.storage":              g.ACME.Storage,
		"global.acme.dns_provider":         g.ACME.DNSProvider,
		"global.cache.enabled":             g.Cache.Enabled,
		"global.cache.memory_limit":        h.deps.ByteSizeStr(g.Cache.MemoryLimit),
		"global.cache.disk_path":           g.Cache.DiskPath,
		"global.cache.default_ttl":         g.Cache.DefaultTTL,
		"global.alerting.enabled":          g.Alerting.Enabled,
		"global.alerting.webhook_url":      g.Alerting.WebhookURL,
		"global.alerting.slack_url":        maskSecret(g.Alerting.SlackURL),
		"global.alerting.telegram_token":   maskSecret(g.Alerting.TelegramToken),
		"global.alerting.telegram_chat_id": g.Alerting.TelegramChatID,
		"global.backup.enabled":            g.Backup.Enabled,
		"global.backup.provider":           g.Backup.Provider,
		"global.backup.schedule":           g.Backup.Schedule,
		"global.backup.keep":               g.Backup.Keep,
		"global.backup.local.path":         g.Backup.Local.Path,
		"global.backup.s3.endpoint":        g.Backup.S3.Endpoint,
		"global.backup.s3.bucket":          g.Backup.S3.Bucket,
		"global.backup.s3.region":          g.Backup.S3.Region,
		"global.backup.sftp.host":          g.Backup.SFTP.Host,
		"global.backup.sftp.port":          g.Backup.SFTP.Port,
		"global.backup.sftp.user":          g.Backup.SFTP.User,

		"global.autoblock.enabled":            g.AutoBlock.Enabled,
		"global.autoblock.dry_run":            g.AutoBlock.DryRun,
		"global.autoblock.firewall_sync":      g.AutoBlock.FirewallSync,
		"global.autoblock.feed_rate_hits":     g.AutoBlock.FeedRateHits == nil || *g.AutoBlock.FeedRateHits,
		"global.autoblock.window":             g.AutoBlock.Window.String(),
		"global.autoblock.max_connections":    intOrDefault(g.AutoBlock.MaxConnections, 600),
		"global.autoblock.max_aborts":         g.AutoBlock.MaxAborts,
		"global.autoblock.max_concurrent":     intOrDefault(g.AutoBlock.MaxConcurrent, 150),
		"global.autoblock.max_waf_hits":       g.AutoBlock.MaxWAFHits,
		"global.autoblock.max_rate_hits":      g.AutoBlock.MaxRateHits,
		"global.autoblock.max_not_found":      g.AutoBlock.MaxNotFound,
		"global.autoblock.block_duration":     g.AutoBlock.BlockDuration.String(),
		"global.autoblock.max_block_duration": g.AutoBlock.MaxBlockDuration.String(),
		"global.autoblock.escalate":           g.AutoBlock.Escalate,
		"global.autoblock.state_path":         g.AutoBlock.StatePath,
		"global.autoblock.whitelist":          strings.Join(g.AutoBlock.Whitelist, "\n"),

		"global.watchdog.enabled":      g.Watchdog.Enabled,
		"global.watchdog.interval":     g.Watchdog.Interval.String(),
		"global.watchdog.timeout":      g.Watchdog.Timeout.String(),
		"global.watchdog.failures":     g.Watchdog.Failures,
		"global.watchdog.self_restart": g.Watchdog.SelfRestart,
	}
	jsonResponse(w, result)
}

func (h *Handler) SettingsPut(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var updates map[string]any
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	h.deps.LockConfig()
	g := &h.deps.ConfigPtr().Global
	for key, val := range updates {
		sv := fmt.Sprintf("%v", val)
		switch key {
		case "global.http_listen":
			g.HTTPListen = sv
		case "global.https_listen":
			g.HTTPSListen = sv
		case "global.http3":
			g.HTTP3Enabled = sv == "true"
		case "global.worker_count":
			g.WorkerCount = sv
		case "global.max_connections":
			g.MaxConnections = h.deps.ToInt(val)
		case "global.pid_file":
			g.PIDFile = sv
		case "global.web_root":
			g.WebRoot = sv
		case "global.log_level":
			g.LogLevel = sv
		case "global.log_format":
			g.LogFormat = sv
		case "global.access_log.enabled":
			// Stored as *bool so an absent block still means on; the panel
			// always sends an explicit value, so take the pointer either way.
			g.AccessLog.Enabled = config.BoolPtr(sv == "true")
		case "global.timeouts.read":
			g.Timeouts.Read = h.deps.ParseDur(sv)
		case "global.timeouts.read_header":
			g.Timeouts.ReadHeader = h.deps.ParseDur(sv)
		case "global.timeouts.write":
			g.Timeouts.Write = h.deps.ParseDur(sv)
		case "global.timeouts.idle":
			g.Timeouts.Idle = h.deps.ParseDur(sv)
		case "global.timeouts.shutdown_grace":
			g.Timeouts.ShutdownGrace = h.deps.ParseDur(sv)
		case "global.timeouts.max_header_bytes":
			g.Timeouts.MaxHeaderBytes = h.deps.ToInt(val)
		case "global.admin.enabled":
			g.Admin.Enabled = sv == "true"
		case "global.admin.listen":
			g.Admin.Listen = sv
		case "global.admin.api_key":
			g.Admin.APIKey = sv
		case "global.users.enabled":
			g.Users.Enabled = sv == "true"
		case "global.users.allow_reseller":
			g.Users.AllowReseller = sv == "true"
		case "global.users.session_ttl":
			g.Users.SessionTTL = h.deps.ToInt(val)
		case "global.rate_limit.requests":
			g.RateLimit.Requests = h.deps.ToInt(val)
		case "global.rate_limit.window":
			g.RateLimit.Window = h.deps.ParseDur(sv)
		case "global.trusted_proxies":
			g.TrustedProxies = splitListField(sv)
		case "global.mcp.enabled":
			g.MCP.Enabled = sv == "true"
		case "global.mcp.listen":
			g.MCP.Listen = sv
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
		case "global.cache.enabled":
			g.Cache.Enabled = sv == "true"
		case "global.cache.memory_limit":
			g.Cache.MemoryLimit = h.deps.ParseBS(sv)
		case "global.cache.disk_path":
			g.Cache.DiskPath = sv
		case "global.cache.disk_limit":
			g.Cache.DiskLimit = h.deps.ParseBS(sv)
		case "global.cache.default_ttl":
			g.Cache.DefaultTTL = h.deps.ToInt(val)
		case "global.cache.grace_ttl":
			g.Cache.GraceTTL = h.deps.ToInt(val)
		case "global.cache.stale_while_revalidate":
			g.Cache.StaleWhileRevalidate = sv == "true"
		case "global.cache.purge_key":
			g.Cache.PurgeKey = sv
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
		case "global.backup.enabled":
			g.Backup.Enabled = sv == "true"
		case "global.backup.provider":
			g.Backup.Provider = sv
		case "global.backup.schedule":
			g.Backup.Schedule = sv
		case "global.backup.keep":
			g.Backup.Keep = h.deps.ToInt(val)
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
			g.Backup.SFTP.Port = h.deps.ToInt(val)
		case "global.backup.sftp.user":
			g.Backup.SFTP.User = sv
		case "global.backup.sftp.key_file":
			g.Backup.SFTP.KeyFile = sv
		case "global.backup.sftp.password":
			g.Backup.SFTP.Password = sv
		case "global.backup.sftp.remote_path":
			g.Backup.SFTP.RemotePath = sv
		case "global.alerting.email_smtp_host":
			g.Alerting.EmailSMTP = sv
		case "global.alerting.email_from":
			g.Alerting.EmailFrom = sv
		case "global.alerting.email_to":
			g.Alerting.EmailTo = sv

		case "global.autoblock.enabled":
			g.AutoBlock.Enabled = sv == "true"
		case "global.autoblock.dry_run":
			g.AutoBlock.DryRun = sv == "true"
		case "global.autoblock.firewall_sync":
			g.AutoBlock.FirewallSync = sv == "true"
		case "global.autoblock.feed_rate_hits":
			g.AutoBlock.FeedRateHits = config.BoolPtr(sv == "true")
		case "global.autoblock.window":
			g.AutoBlock.Window = h.deps.ParseDur(sv)
		case "global.autoblock.max_connections":
			g.AutoBlock.MaxConnections = intPtrVal(h.deps.ToInt(val))
		case "global.autoblock.max_aborts":
			g.AutoBlock.MaxAborts = h.deps.ToInt(val)
		case "global.autoblock.max_concurrent":
			g.AutoBlock.MaxConcurrent = intPtrVal(h.deps.ToInt(val))
		case "global.autoblock.max_waf_hits":
			g.AutoBlock.MaxWAFHits = h.deps.ToInt(val)
		case "global.autoblock.max_rate_hits":
			g.AutoBlock.MaxRateHits = h.deps.ToInt(val)
		case "global.autoblock.max_not_found":
			g.AutoBlock.MaxNotFound = h.deps.ToInt(val)
		case "global.autoblock.block_duration":
			g.AutoBlock.BlockDuration = h.deps.ParseDur(sv)
		case "global.autoblock.max_block_duration":
			g.AutoBlock.MaxBlockDuration = h.deps.ParseDur(sv)
		case "global.autoblock.escalate":
			g.AutoBlock.Escalate = sv == "true"
		case "global.autoblock.state_path":
			g.AutoBlock.StatePath = sv
		case "global.autoblock.whitelist":
			g.AutoBlock.Whitelist = splitListField(sv)

		case "global.watchdog.enabled":
			g.Watchdog.Enabled = sv == "true"
		case "global.watchdog.interval":
			g.Watchdog.Interval = h.deps.ParseDur(sv)
		case "global.watchdog.timeout":
			g.Watchdog.Timeout = h.deps.ParseDur(sv)
		case "global.watchdog.failures":
			g.Watchdog.Failures = h.deps.ToInt(val)
		case "global.watchdog.self_restart":
			g.Watchdog.SelfRestart = sv == "true"
		}
	}
	h.deps.UnlockConfig()
	h.deps.EnsureAuthManagerFromConfig()
	h.deps.PersistConfig()
	h.deps.RecordAudit(r, "settings.update", fmt.Sprintf("%d fields", len(updates)), true)
	jsonResponse(w, map[string]any{"status": "saved", "updated": len(updates)})
}

// ── YAML masking helpers ──

const secretMask = `"********"`

// unmaskYAMLValue is the inverse of maskYAMLValue for the raw config editor.
// The editor shows secrets as "********"; on save, any secret line still
// holding that mask must be restored from the on-disk value instead of being
// written literally (which would overwrite the real secret with "********").
// A line the operator actually changed keeps its new value. Matching is by
// occurrence order for the key, so duplicate keys (e.g. `password` under s3,
// sftp, redis) each restore from their corresponding original — as long as the
// secret lines are not reordered, which editing values never does.
func unmaskYAMLValue(newContent, oldContent, key string) string {
	var origs []string
	for _, line := range strings.Split(oldContent, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), key+":") {
			idx := strings.Index(line, key+":")
			origs = append(origs, strings.TrimSpace(line[idx+len(key)+1:]))
		}
	}
	occ := 0
	var out strings.Builder
	for _, line := range strings.Split(newContent, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), key+":") {
			idx := strings.Index(line, key+":")
			val := strings.TrimSpace(line[idx+len(key)+1:])
			if (val == secretMask || val == "********") && occ < len(origs) {
				out.WriteString(line[:idx] + key + ": " + origs[occ])
			} else {
				out.WriteString(line)
			}
			occ++
			out.WriteByte('\n')
			continue
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return strings.TrimSuffix(out.String(), "\n")
}

// unmaskYAMLListValue restores masked list items (e.g. recovery_codes) from the
// on-disk list, by position, when the submitted item still holds the mask.
func unmaskYAMLListValue(newContent, oldContent, key string) string {
	// Collect original list items for key, in order.
	collect := func(content string) []string {
		var items []string
		inList := false
		keyIndent := 0
		for _, line := range strings.Split(content, "\n") {
			trimmed := strings.TrimSpace(line)
			if !inList {
				if strings.HasPrefix(trimmed, key+":") {
					idx := strings.Index(line, key+":")
					if strings.TrimSpace(line[idx+len(key)+1:]) == "" {
						inList = true
						keyIndent = idx
					}
				}
				continue
			}
			indent := len(line) - len(strings.TrimLeft(line, " \t"))
			if trimmed != "" && indent > keyIndent && strings.HasPrefix(trimmed, "-") {
				items = append(items, strings.TrimSpace(strings.TrimPrefix(trimmed, "-")))
				continue
			}
			inList = false
		}
		return items
	}
	origs := collect(oldContent)

	occ := 0
	inList := false
	keyIndent := 0
	var out strings.Builder
	for _, line := range strings.Split(newContent, "\n") {
		trimmed := strings.TrimSpace(line)
		if !inList {
			if strings.HasPrefix(trimmed, key+":") && strings.TrimSpace(line[strings.Index(line, key+":")+len(key)+1:]) == "" {
				inList = true
				keyIndent = strings.Index(line, key+":")
			}
			out.WriteString(line)
			out.WriteByte('\n')
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if trimmed != "" && indent > keyIndent && strings.HasPrefix(trimmed, "-") {
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
			if (val == secretMask || val == "********") && occ < len(origs) {
				out.WriteString(line[:indent] + "- " + origs[occ])
			} else {
				out.WriteString(line)
			}
			occ++
			out.WriteByte('\n')
			continue
		}
		inList = false
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return strings.TrimSuffix(out.String(), "\n")
}

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

func maskYAMLListValue(content, key string) string {
	var result strings.Builder
	inList := false
	keyIndent := 0
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if !inList {
			if strings.HasPrefix(trimmed, key+":") {
				idx := strings.Index(line, key+":")
				rest := strings.TrimSpace(line[idx+len(key)+1:])
				if rest == "" {
					inList = true
					keyIndent = idx
					result.WriteString(line)
				} else {
					result.WriteString(line[:idx] + key + `: "********"`)
				}
				result.WriteByte('\n')
				continue
			}
			result.WriteString(line)
			result.WriteByte('\n')
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if trimmed != "" && indent > keyIndent && strings.HasPrefix(trimmed, "-") {
			result.WriteString(line[:indent] + `- "********"`)
			result.WriteByte('\n')
			continue
		}
		inList = false
		result.WriteString(line)
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

// intOrDefault returns *p or def when p is nil (autoblock pointer thresholds:
// nil means "use the default"; an explicit 0 means "disabled").
func intOrDefault(p *int, def int) int {
	if p == nil {
		return def
	}
	return *p
}

// intPtrVal boxes an int for the optional autoblock threshold fields.
func intPtrVal(v int) *int { return &v }

// splitListField turns a panel textarea (one entry per line, optional commas)
// into a string slice for YAML list fields like trusted_proxies / whitelist.
func splitListField(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		for _, part := range strings.Split(line, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

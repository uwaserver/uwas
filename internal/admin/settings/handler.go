// Package settings provides admin API handlers for global server settings,
// notification preferences, white-label branding, 2FA recovery codes,
// config export/import, and the raw YAML config editor.
package settings

import (
	"bytes"
	crand "crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/uwaserver/uwas/internal/backup"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/notify"
	"gopkg.in/yaml.v3"
)

// Deps is the interface the sub-package needs from the admin Server.
type Deps interface {
	// Auth
	RequireAdmin(w http.ResponseWriter, r *http.Request) bool
	RequirePin(w http.ResponseWriter, r *http.Request) bool
	// TOTP step-up for MFA lifecycle endpoints under the /2fa/ middleware exemption
	GetTOTPSecret() string
	ValidateTOTPCode(code string) bool
	// RecordAuthFailure counts a failed second-factor attempt toward the
	// request IP's throttle (these routes bypass the middleware's TOTP gate).
	RecordAuthFailure(r *http.Request)
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
	PersistConfig() error
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
	// Middleware exempts /api/v1/auth/2fa/* from X-TOTP-Code; require step-up here
	// when TOTP is enabled so a first-factor-only session cannot rotate backup codes.
	if secret := h.deps.GetTOTPSecret(); secret != "" {
		code := r.Header.Get("X-TOTP-Code")
		if code == "" {
			var body struct {
				Code string `json:"code"`
			}
			// Body is optional; ignore decode errors and fall through to reject.
			_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body)
			code = body.Code
		}
		if code == "" || !h.deps.ValidateTOTPCode(code) {
			if code != "" {
				h.deps.RecordAuthFailure(r)
			}
			w.Header().Set("X-2FA-Required", "true")
			jsonError(w, "2fa_required", http.StatusForbidden)
			return
		}
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
	if err := h.deps.PersistConfig(); err != nil {
		h.deps.RecordAudit(r, "2fa.recovery_codes.generated", err.Error(), false)
		jsonError(w, "failed to persist recovery codes", http.StatusInternalServerError)
		return
	}
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
			// Build a new slice: persistConfig marshals a snapshot of the old
			// slice header after RUnlock, so splicing in place would rewrite it.
			remaining := make([]string, 0, len(codes)-1)
			remaining = append(remaining, codes[:i]...)
			h.deps.ConfigPtr().Global.Admin.RecoveryCodes = append(remaining, codes[i+1:]...)
			found = true
			break
		}
	}
	if found {
		// Successful recovery must clear TOTP so the operator can regain panel
		// access and re-enroll (disable still requires a live authenticator code).
		h.deps.ConfigPtr().Global.Admin.TOTPSecret = ""
	}
	h.deps.UnlockConfig()
	if !found {
		h.deps.RecordAuthFailure(r)
		jsonError(w, "invalid recovery code", http.StatusUnauthorized)
		return
	}
	if err := h.deps.PersistConfig(); err != nil {
		h.deps.RecordAudit(r, "2fa.recovery_code.used", err.Error(), false)
		jsonError(w, "failed to persist recovery code change", http.StatusInternalServerError)
		return
	}
	h.deps.RecordAudit(r, "2fa.recovery_code.used", "TOTP cleared", true)
	jsonResponse(w, map[string]string{"status": "ok", "2fa": "disabled"})
}

// ── Notification Preferences ──

func (h *Handler) NotifyPrefsGet(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	h.deps.RLockConfig()
	alerting := h.deps.ConfigPtr().Global.Alerting
	webhooks := make([]config.WebhookConfig, len(h.deps.ConfigPtr().Global.Webhooks))
	copy(webhooks, h.deps.ConfigPtr().Global.Webhooks)
	h.deps.RUnlockConfig()
	// Mask secrets like SettingsGet and the webhook list do; NotifyPrefsPut
	// restores masked values from the live config.
	alerting.WebhookURL = maskSecret(alerting.WebhookURL)
	alerting.SlackURL = maskSecret(alerting.SlackURL)
	alerting.TelegramToken = maskSecret(alerting.TelegramToken)
	for i := range webhooks {
		webhooks[i].Secret = maskSecret(webhooks[i].Secret)
		webhooks[i].Headers = MaskWebhookHeaders(webhooks[i].Headers)
	}
	jsonResponse(w, map[string]any{
		"alerting": alerting,
		"webhooks": webhooks,
	})
}

func (h *Handler) NotifyPrefsPut(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Alerting *config.AlertingConfig `json:"alerting"`
		Webhooks []config.WebhookConfig `json:"webhooks"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	// Same limits as the webhook create endpoint: a negative timeout puts the
	// delivery dialer's deadline in the past, so every delivery would fail.
	for _, wh := range req.Webhooks {
		if wh.Retry < 0 {
			jsonError(w, "retry must not be negative", http.StatusBadRequest)
			return
		}
		if wh.Timeout.Duration < 0 {
			jsonError(w, "timeout must not be negative", http.StatusBadRequest)
			return
		}
		// An event name nothing is ever fired under never matches (F13802).
		if err := config.ValidateWebhookEvents(wh.Events); err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	// An omitted (or null) field leaves that part of the config unchanged;
	// send "webhooks": [] to remove every webhook.
	h.deps.LockConfig()
	g := &h.deps.ConfigPtr().Global
	if req.Webhooks != nil {
		// Masked secrets (as returned by NotifyPrefsGet) are restored from the
		// live webhook with the same URL: the n-th submitted entry for a URL
		// maps to the n-th live entry for it. A masked secret with no such
		// webhook cannot be restored and is rejected.
		seen := map[string]int{}
		for i := range req.Webhooks {
			n := seen[req.Webhooks[i].URL]
			seen[req.Webhooks[i].URL]++
			if err := restoreMaskedHeaders(&req.Webhooks[i], g.Webhooks, n); err != nil {
				h.deps.UnlockConfig()
				jsonError(w, err.Error(), http.StatusBadRequest)
				return
			}
			if !isMaskedSecretValue(req.Webhooks[i].Secret) {
				continue
			}
			restored := false
			for _, old := range g.Webhooks {
				if old.URL != req.Webhooks[i].URL {
					continue
				}
				if n == 0 {
					req.Webhooks[i].Secret = old.Secret
					restored = true
					break
				}
				n--
			}
			if !restored {
				h.deps.UnlockConfig()
				jsonError(w, "masked secret for unknown webhook: "+req.Webhooks[i].URL, http.StatusBadRequest)
				return
			}
		}
	}
	if req.Alerting != nil {
		if isMaskedSecretValue(req.Alerting.WebhookURL) {
			req.Alerting.WebhookURL = g.Alerting.WebhookURL
		}
		if isMaskedSecretValue(req.Alerting.SlackURL) {
			req.Alerting.SlackURL = g.Alerting.SlackURL
		}
		if isMaskedSecretValue(req.Alerting.TelegramToken) {
			req.Alerting.TelegramToken = g.Alerting.TelegramToken
		}
		g.Alerting = *req.Alerting
	}
	if req.Webhooks != nil {
		g.Webhooks = req.Webhooks
	}
	h.deps.UnlockConfig()
	// The live webhook manager keeps its own copy of the webhook list; without
	// this, a webhook removed or disabled here kept receiving every event until
	// the next reload.
	if req.Webhooks != nil {
		if n, ok := h.deps.(interface{ WebhooksChanged() }); ok {
			n.WebhooksChanged()
		}
	}
	if err := h.deps.PersistConfig(); err != nil {
		h.deps.RecordAudit(r, "settings.notifications", err.Error(), false)
		jsonError(w, "failed to persist settings", http.StatusInternalServerError)
		return
	}
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
	if err := h.deps.PersistConfig(); err != nil {
		h.deps.RecordAudit(r, "settings.branding", err.Error(), false)
		jsonError(w, "failed to persist settings", http.StatusInternalServerError)
		return
	}
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
	export.Global.Alerting.WebhookURL = ""
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
			// Header values are credentials (Authorization, API keys); the
			// map is shared with the live config, so drop it on the copy.
			webhooks[i].Headers = nil
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
		// Locations is a shared slice and BasicAuth a shared pointer: copy
		// both before redacting, or the live config loses its users. Not
		// every location has basic auth.
		sanitized[i].Locations = append([]config.LocationConfig(nil), sanitized[i].Locations...)
		for j := range sanitized[i].Locations {
			if ba := sanitized[i].Locations[j].BasicAuth; ba != nil {
				redacted := *ba
				redacted.Users = nil
				sanitized[i].Locations[j].BasicAuth = &redacted
			}
		}
	}
	export.Domains = sanitized
	// Unlock only after the copies: the shallow export still shares slices
	// with the live config until here.
	h.deps.RUnlockConfig()

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

// rawSecretKeys are the YAML keys the raw config editor shows masked and
// restores on save. token, access_key and webhook_secret are credential keys
// the DNS providers, the S3 backup and the per-domain deploy webhook read
// (F9500); the same list drives both directions so they cannot drift.
var rawSecretKeys = []string{
	"api_key", "pin_code", "totp_secret", "secret_key", "password",
	"secret_access_key", "api_token", "client_secret",
	"google_client_secret", "github_client_secret",
	"tls_key", "telegram_token", "slack_url", "purge_key", "secret",
	"token", "access_key", "webhook_secret",
	// An incoming-webhook URL carries its credential in the path or query, like
	// slack_url (F9820).
	"webhook_url",
}

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
	for _, key := range rawSecretKeys {
		content = maskYAMLValue(content, key)
	}
	content = maskYAMLListValue(content, "recovery_codes")
	content = maskWebhookHeaderValues(content)
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
	original, rerr := os.ReadFile(configPath)
	if rerr == nil {
		oldContent := string(original)
		for _, key := range rawSecretKeys {
			req.Content = unmaskYAMLValue(req.Content, oldContent, key)
		}
		req.Content = unmaskYAMLListValue(req.Content, oldContent, "recovery_codes")
		req.Content = unmaskWebhookHeaderValues(req.Content, oldContent)
	}

	data := []byte(req.Content)
	var probe config.Config
	if err := decodeYAMLStrict(data, &probe); err != nil {
		h.deps.RecordAudit(r, "config.raw_put", "invalid YAML", false)
		jsonError(w, "invalid YAML: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := config.ValidateWithDefaults(&probe); err != nil {
		h.deps.RecordAudit(r, "config.raw_put", "validation failed", false)
		jsonError(w, "validation failed: "+err.Error(), http.StatusBadRequest)
		return
	}
	// Reload applies the key to the running server and Start refuses a public
	// admin API with no credentials or a placeholder key: refuse the file when
	// the running configuration is acceptable and the new one is not (F16400).
	if x, ok := h.deps.(interface {
		AdminExposureError(addr, apiKey string, multiUser bool) error
	}); ok {
		h.deps.RLockConfig()
		cur := h.deps.ConfigPtr().Global
		h.deps.RUnlockConfig()
		if xerr := x.AdminExposureError(probe.Global.Admin.Listen, probe.Global.Admin.APIKey, probe.Global.Users.Enabled); xerr != nil &&
			x.AdminExposureError(cur.Admin.Listen, cur.Admin.APIKey, cur.Users.Enabled) == nil {
			h.deps.RecordAudit(r, "config.raw_put", "admin exposure refused", false)
			jsonError(w, "refused: "+xerr.Error(), http.StatusBadRequest)
			return
		}
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
		// The probe above reads only this file; Load also merges domains_dir
		// and includes, so a file can pass it and still fail here. Reload
		// refuses before applying anything, so the running server still has
		// the old configuration: put the old file back instead of leaving one
		// the next start or reload cannot load.
		if rerr == nil {
			h.deps.LockPersist()
			restoreErr := h.deps.AtomicWriteFile(configPath, original, 0600)
			h.deps.UnlockPersist()
			if restoreErr != nil {
				h.deps.LogError("config raw put: restore after failed reload", "error", restoreErr)
				h.deps.RecordAudit(r, "config.raw_put", auditDetail+" (reload failed, restore failed)", false)
				jsonError(w, "config saved but reload failed and the previous file could not be restored: "+err.Error(), http.StatusInternalServerError)
				return
			}
			h.deps.RecordAudit(r, "config.raw_put", auditDetail+" (reload failed, previous file restored)", false)
			jsonError(w, "reload failed, previous configuration restored: "+err.Error(), http.StatusInternalServerError)
			return
		}
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
		"global.alerting.webhook_url":      maskSecret(g.Alerting.WebhookURL),
		"global.alerting.slack_url":        maskSecret(g.Alerting.SlackURL),
		"global.alerting.telegram_token":   maskSecret(g.Alerting.TelegramToken),
		"global.alerting.telegram_chat_id": g.Alerting.TelegramChatID,
		"global.backup.enabled":            g.Backup.Enabled,
		"global.backup.provider":           g.Backup.Provider,
		"global.backup.schedule":           backupScheduleShown(g.Backup),
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

		"global.admin.branding.name":          g.Admin.Branding.Name,
		"global.admin.branding.logo_url":      g.Admin.Branding.LogoURL,
		"global.admin.branding.favicon_url":   g.Admin.Branding.FaviconURL,
		"global.admin.branding.primary_color": g.Admin.Branding.PrimaryColor,
		"global.admin.branding.footer_text":   g.Admin.Branding.FooterText,

		"global.admin.oauth.enabled":              g.Admin.OAuth.Enabled,
		"global.admin.oauth.google_client_id":     g.Admin.OAuth.GoogleID,
		"global.admin.oauth.google_client_secret": maskSecret(g.Admin.OAuth.GoogleSecret),
		"global.admin.oauth.github_client_id":     g.Admin.OAuth.GitHubID,
		"global.admin.oauth.github_client_secret": maskSecret(g.Admin.OAuth.GitHubSecret),
		"global.admin.oauth.allowed_emails":       strings.Join(g.Admin.OAuth.AllowedEmails, ", "),

		"global.acme.dns_credentials.api_token":         maskSecret(g.ACME.DNSCredentials["api_token"]),
		"global.acme.dns_credentials.api_key":           maskSecret(g.ACME.DNSCredentials["api_key"]),
		"global.acme.dns_credentials.access_key_id":     maskSecret(g.ACME.DNSCredentials["access_key_id"]),
		"global.acme.dns_credentials.secret_access_key": maskSecret(g.ACME.DNSCredentials["secret_access_key"]),

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
	cfg := h.deps.ConfigPtr()
	prev := cfg.Global
	g := &cfg.Global
	var unknown, unreadable []string
	for key, val := range updates {
		// Durations, sizes, integers and booleans are read the way the loader
		// reads them, or refused: the helpers below used to turn whatever they
		// did not understand into 0 or false and report the change as saved
		// (F14000-F14003).
		if kind := settingKinds[key]; kind != 0 {
			nv, ok := normalizeSettingValue(kind, val)
			if !ok {
				unreadable = append(unreadable, key)
				continue
			}
			val = nv
		}
		sv := settingString(val)
		// SettingsGet returns masked secrets (****last4). Never persist those
		// back — a Save that includes an untouched secret field would otherwise
		// replace the real credential and lock the operator out (issue #43).
		if isSettingsSecretKey(key) && isMaskedSecretValue(sv) {
			continue
		}
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
			// A cron expression and an interval are alternatives; setting one
			// clears the other so the one just saved is the one that runs.
			if len(strings.Fields(sv)) == 5 {
				g.Backup.Cron, g.Backup.Schedule = sv, ""
			} else {
				g.Backup.Schedule, g.Backup.Cron = sv, ""
			}
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

		// The settings page sends these too (F13700). They used to fall
		// through the switch and be reported as saved.
		case "global.admin.branding.name":
			g.Admin.Branding.Name = sv
		case "global.admin.branding.logo_url":
			g.Admin.Branding.LogoURL = sv
		case "global.admin.branding.favicon_url":
			g.Admin.Branding.FaviconURL = sv
		case "global.admin.branding.primary_color":
			g.Admin.Branding.PrimaryColor = sv
		case "global.admin.branding.footer_text":
			g.Admin.Branding.FooterText = sv
		case "global.admin.oauth.enabled":
			g.Admin.OAuth.Enabled = sv == "true"
		case "global.admin.oauth.google_client_id":
			g.Admin.OAuth.GoogleID = sv
		case "global.admin.oauth.google_client_secret":
			g.Admin.OAuth.GoogleSecret = sv
		case "global.admin.oauth.github_client_id":
			g.Admin.OAuth.GitHubID = sv
		case "global.admin.oauth.github_client_secret":
			g.Admin.OAuth.GitHubSecret = sv
		case "global.admin.oauth.allowed_emails":
			g.Admin.OAuth.AllowedEmails = splitListField(sv)
		case "global.acme.dns_credentials.api_token":
			g.ACME.DNSCredentials = withDNSCredential(g.ACME.DNSCredentials, "api_token", sv)
		case "global.acme.dns_credentials.api_key":
			g.ACME.DNSCredentials = withDNSCredential(g.ACME.DNSCredentials, "api_key", sv)
		case "global.acme.dns_credentials.access_key_id":
			g.ACME.DNSCredentials = withDNSCredential(g.ACME.DNSCredentials, "access_key_id", sv)
		case "global.acme.dns_credentials.secret_access_key":
			g.ACME.DNSCredentials = withDNSCredential(g.ACME.DNSCredentials, "secret_access_key", sv)

		default:
			unknown = append(unknown, key)
		}
	}
	// A key the switch does not know used to be counted as saved while
	// changing nothing, so a misspelled "global.rate_limt.requests" left the
	// operator believing a limit was set. Refuse the whole request instead of
	// applying the keys that happened to be spelled right.
	if len(unknown) > 0 || len(unreadable) > 0 {
		cfg.Global = prev
		h.deps.UnlockConfig()
		sort.Strings(unknown)
		sort.Strings(unreadable)
		var parts []string
		if len(unknown) > 0 {
			parts = append(parts, "unknown settings keys: "+strings.Join(unknown, ", "))
		}
		if len(unreadable) > 0 {
			parts = append(parts, "unreadable values for: "+strings.Join(unreadable, ", "))
		}
		msg := strings.Join(parts, "; ")
		h.deps.RecordAudit(r, "settings.update", msg, false)
		jsonError(w, msg, http.StatusBadRequest)
		return
	}
	// Refuse a change config.Load would reject: once persisted it would stop
	// the next start or reload. The switch above only assigns fields, so
	// restoring the struct copy undoes it. An already-invalid config is left
	// to save as before.
	if verr := settingsConfigError(cfg); verr != nil {
		next := cfg.Global
		cfg.Global = prev
		if settingsConfigError(cfg) == nil {
			h.deps.UnlockConfig()
			h.deps.RecordAudit(r, "settings.update", "validation failed", false)
			jsonError(w, "invalid settings: "+verr.Error(), http.StatusBadRequest)
			return
		}
		cfg.Global = next
	}
	// Start refuses to bind the admin API on a public address without
	// credentials or behind a well-known placeholder key. The key is rotated
	// on the running server at once, so a change that reaches that state
	// would leave a public API open or guessable now and stop the admin API
	// from coming up after the next restart. Refuse it when the state before
	// the change was acceptable (F16400).
	if x, ok := h.deps.(interface {
		AdminExposureError(addr, apiKey string, multiUser bool) error
	}); ok {
		if xerr := x.AdminExposureError(g.Admin.Listen, g.Admin.APIKey, g.Users.Enabled); xerr != nil &&
			x.AdminExposureError(prev.Admin.Listen, prev.Admin.APIKey, prev.Users.Enabled) == nil {
			cfg.Global = prev
			h.deps.UnlockConfig()
			h.deps.RecordAudit(r, "settings.update", "admin exposure refused", false)
			jsonError(w, "refused: "+xerr.Error(), http.StatusBadRequest)
			return
		}
	}
	h.deps.UnlockConfig()
	h.deps.EnsureAuthManagerFromConfig()
	if err := h.deps.PersistConfig(); err != nil {
		h.deps.RecordAudit(r, "settings.update", err.Error(), false)
		jsonError(w, "failed to persist settings", http.StatusInternalServerError)
		return
	}
	h.deps.RecordAudit(r, "settings.update", fmt.Sprintf("%d fields", len(updates)), true)
	jsonResponse(w, map[string]any{"status": "saved", "updated": len(updates)})
}

type settingKind int

const (
	kindDuration settingKind = iota + 1
	kindByteSize
	kindInt
	kindBool
	kindBackupSchedule
)

// settingKinds lists the scalar settings whose value the PUT switch hands to a
// parser that cannot report a bad value.
var settingKinds = map[string]settingKind{
	"global.timeouts.read":                kindDuration,
	"global.timeouts.read_header":         kindDuration,
	"global.timeouts.write":               kindDuration,
	"global.timeouts.idle":                kindDuration,
	"global.timeouts.shutdown_grace":      kindDuration,
	"global.rate_limit.window":            kindDuration,
	"global.autoblock.window":             kindDuration,
	"global.autoblock.block_duration":     kindDuration,
	"global.autoblock.max_block_duration": kindDuration,
	"global.watchdog.interval":            kindDuration,
	"global.watchdog.timeout":             kindDuration,

	"global.backup.schedule": kindBackupSchedule,

	"global.cache.memory_limit": kindByteSize,
	"global.cache.disk_limit":   kindByteSize,

	"global.max_connections":           kindInt,
	"global.timeouts.max_header_bytes": kindInt,
	"global.users.session_ttl":         kindInt,
	"global.rate_limit.requests":       kindInt,
	"global.cache.default_ttl":         kindInt,
	"global.cache.grace_ttl":           kindInt,
	"global.backup.keep":               kindInt,
	"global.backup.sftp.port":          kindInt,
	"global.autoblock.max_connections": kindInt,
	"global.autoblock.max_aborts":      kindInt,
	"global.autoblock.max_concurrent":  kindInt,
	"global.autoblock.max_waf_hits":    kindInt,
	"global.autoblock.max_rate_hits":   kindInt,
	"global.autoblock.max_not_found":   kindInt,
	"global.watchdog.failures":         kindInt,

	"global.http3":                        kindBool,
	"global.access_log.enabled":           kindBool,
	"global.admin.enabled":                kindBool,
	"global.admin.oauth.enabled":          kindBool,
	"global.users.enabled":                kindBool,
	"global.users.allow_reseller":         kindBool,
	"global.mcp.enabled":                  kindBool,
	"global.acme.on_demand":               kindBool,
	"global.cache.enabled":                kindBool,
	"global.cache.stale_while_revalidate": kindBool,
	"global.alerting.enabled":             kindBool,
	"global.backup.enabled":               kindBool,
	"global.autoblock.enabled":            kindBool,
	"global.autoblock.dry_run":            kindBool,
	"global.autoblock.firewall_sync":      kindBool,
	"global.autoblock.feed_rate_hits":     kindBool,
	"global.autoblock.escalate":           kindBool,
	"global.watchdog.enabled":             kindBool,
	"global.watchdog.self_restart":        kindBool,
}

// settingString renders a JSON value as text. A float64 is printed without an
// exponent: %v writes 268435456 as 2.68435456e+08, which no size parser reads.
func settingString(val any) string {
	if f, ok := val.(float64); ok {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return fmt.Sprintf("%v", val)
}

// normalizeSettingValue returns val in the form the switch's parser reads
// correctly, or false when val is not a value the setting can hold.
func normalizeSettingValue(kind settingKind, val any) (any, bool) {
	switch kind {
	case kindDuration:
		d, ok := settingDuration(val)
		if !ok {
			return nil, false
		}
		return d.String(), true
	case kindByteSize:
		n, ok := settingByteSize(val)
		if !ok {
			return nil, false
		}
		return strconv.FormatInt(n, 10), true
	case kindInt:
		n, ok := settingInt(val)
		if !ok {
			return nil, false
		}
		return n, true
	case kindBool:
		b, ok := settingBool(val)
		if !ok {
			return nil, false
		}
		return b, true
	case kindBackupSchedule:
		v, ok := settingBackupSchedule(val)
		if !ok {
			return nil, false
		}
		return v, true
	}
	return val, true
}

// settingBackupSchedule accepts what the scheduler can run: a five-field cron
// expression, a positive Go duration, or the "<N>d" day shorthand the backup
// page shows (stored as hours, as the schedule endpoint stores it). Empty
// switches the schedule off.
func settingBackupSchedule(val any) (string, bool) {
	v, ok := val.(string)
	if !ok {
		return "", false
	}
	s := strings.TrimSpace(v)
	if s == "" {
		return "", true
	}
	if len(strings.Fields(s)) == 5 {
		return s, backup.ValidCronExpression(s)
	}
	if d, err := time.ParseDuration(s); err == nil {
		return s, d > 0
	}
	if n, found := strings.CutSuffix(s, "d"); found && n != "" {
		if days, err := strconv.Atoi(n); err == nil && days > 0 && days <= 3650 {
			return (time.Duration(days) * 24 * time.Hour).String(), true
		}
	}
	return "", false
}

// backupScheduleShown is the schedule that actually runs: the cron expression
// when one is set (startup prefers it), else the interval.
func backupScheduleShown(b config.BackupConfig) string {
	if b.Cron != "" {
		return b.Cron
	}
	return b.Schedule
}

// settingDuration reads a duration the way config.Duration does: a Go duration
// string, or a bare number of seconds. An empty string clears the setting.
func settingDuration(val any) (time.Duration, bool) {
	switch v := val.(type) {
	case float64:
		return secondsToDuration(v)
	case int:
		return secondsToDuration(float64(v))
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return 0, true
		}
		if d, err := time.ParseDuration(s); err == nil {
			return d, true
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return secondsToDuration(f)
		}
	}
	return 0, false
}

func secondsToDuration(secs float64) (time.Duration, bool) {
	n := secs * float64(time.Second)
	if math.IsNaN(n) || n >= 1<<63 || n < -1<<63 {
		return 0, false
	}
	return time.Duration(n), true
}

// settingByteSize reads a size the way config.ByteSize does: a size string
// ("256MB", "1.5GB") or a bare number of bytes. An empty string clears it.
func settingByteSize(val any) (int64, bool) {
	switch v := val.(type) {
	case float64:
		if v != math.Trunc(v) || v < 0 || v >= 1<<63 {
			return 0, false
		}
		return int64(v), true
	case int:
		return int64(v), v >= 0
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return 0, true
		}
		var b config.ByteSize
		if err := json.Unmarshal([]byte(strconv.Quote(s)), &b); err != nil || b < 0 {
			return 0, false
		}
		return int64(b), true
	}
	return 0, false
}

// settingInt accepts a whole number, or text holding one; empty clears.
func settingInt(val any) (int, bool) {
	switch v := val.(type) {
	case float64:
		if v != math.Trunc(v) || v < math.MinInt32 || v > math.MaxInt32 {
			return 0, false
		}
		return int(v), true
	case int:
		return v, true
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return 0, true
		}
		n, err := strconv.Atoi(s)
		return n, err == nil
	}
	return 0, false
}

// settingBool accepts a JSON boolean or the text "true" or "false" in any case.
// Anything else used to read as false, which switches a protection off.
func settingBool(val any) (bool, bool) {
	switch v := val.(type) {
	case bool:
		return v, true
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true":
			return true, true
		case "false", "":
			return false, true
		}
	}
	return false, false
}

// decodeYAMLStrict parses operator-written YAML and refuses a key the target
// has no field for. yaml.Unmarshal drops such a key, and so does config.Load
// on every start, so a mistyped "blocked_path" or "log_levl" was saved and
// reloaded as if it were applied while the setting never existed (F13702). An
// empty document is accepted, as yaml.Unmarshal accepts it.
func decodeYAMLStrict(data []byte, out any) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// settingsConfigError reports whether cfg would fail config.Load's validation.
// It checks a YAML round-trip copy so applying defaults never touches cfg.
func settingsConfigError(cfg *config.Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	var probe config.Config
	if err := yaml.Unmarshal(data, &probe); err != nil {
		return err
	}
	return config.ValidateWithDefaults(&probe)
}

// ── YAML masking helpers ──

const secretMask = `"********"`

// unmaskYAMLValue is the inverse of maskYAMLValue for the raw config editor.
// The editor shows secrets as "********"; on save, any secret line still
// holding that mask must be restored from the on-disk value instead of being
// written literally (which would overwrite the real secret with "********").
// A line the operator actually changed keeps its new value. Matching is by
// parent-key path plus occurrence order within that path, so duplicate keys
// (e.g. `password` under s3, sftp, redis) each restore from their own section,
// and adding a secret line in one section cannot shift another section's.
func unmaskYAMLValue(newContent, oldContent, key string) string {
	origs := map[string][]string{}
	oldLines := strings.Split(oldContent, "\n")
	oldPaths := yamlParentPaths(oldLines)
	for i, line := range oldLines {
		if strings.HasPrefix(strings.TrimSpace(line), key+":") {
			idx := strings.Index(line, key+":")
			origs[oldPaths[i]] = append(origs[oldPaths[i]], strings.TrimSpace(line[idx+len(key)+1:]))
		}
	}
	occ := map[string]int{}
	newLines := strings.Split(newContent, "\n")
	newPaths := yamlParentPaths(newLines)
	var out strings.Builder
	for i, line := range newLines {
		if strings.HasPrefix(strings.TrimSpace(line), key+":") {
			idx := strings.Index(line, key+":")
			val := strings.TrimSpace(line[idx+len(key)+1:])
			p := newPaths[i]
			if (val == secretMask || val == "********") && occ[p] < len(origs[p]) {
				out.WriteString(line[:idx] + key + ": " + origs[p][occ[p]])
			} else {
				out.WriteString(line)
			}
			occ[p]++
			out.WriteByte('\n')
			continue
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return strings.TrimSuffix(out.String(), "\n")
}

// yamlParentPaths returns, for each line, the dot-joined keys of the mappings
// enclosing it, derived from indentation ("-" marks a list item). Blank and
// comment lines get "".
func yamlParentPaths(lines []string) []string {
	type level struct {
		indent int
		key    string
	}
	var stack []level
	paths := make([]string, len(lines))
	for i, line := range lines {
		content := strings.TrimLeft(line, " \t")
		if content == "" || strings.HasPrefix(content, "#") {
			continue
		}
		indent := len(line) - len(content)
		pop := func() {
			for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
				stack = stack[:len(stack)-1]
			}
		}
		for strings.HasPrefix(content, "- ") || content == "-" {
			pop()
			stack = append(stack, level{indent, "-"})
			rest := strings.TrimLeft(content[1:], " ")
			indent += len(content) - len(rest)
			content = rest
		}
		pop()
		keys := make([]string, len(stack))
		for j, l := range stack {
			keys[j] = l.key
		}
		paths[i] = strings.Join(keys, ".")
		if k, _, ok := strings.Cut(content, ":"); ok {
			stack = append(stack, level{indent, k})
		}
	}
	return paths
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
			if trimmed != "" && indent >= keyIndent && strings.HasPrefix(trimmed, "-") {
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
		if trimmed != "" && indent >= keyIndent && strings.HasPrefix(trimmed, "-") {
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

// MaskWebhookHeaders returns a copy of a webhook's custom headers with every
// value masked: the values are credentials (Authorization, API keys) and the
// returned map must not alias the live one (F10100).
func MaskWebhookHeaders(h map[string]string) map[string]string {
	if h == nil {
		return nil
	}
	out := make(map[string]string, len(h))
	for k, v := range h {
		if v == "" {
			out[k] = ""
			continue
		}
		out[k] = "********"
	}
	return out
}

// restoreMaskedHeaders puts the live header values back where the submitted
// webhook still carries the mask. The n-th submitted entry for a URL maps to
// the n-th live entry for it, as for the secret; a masked value with no live
// counterpart cannot be restored and is rejected.
func restoreMaskedHeaders(wh *config.WebhookConfig, live []config.WebhookConfig, n int) error {
	masked := false
	for _, v := range wh.Headers {
		if isMaskedSecretValue(v) {
			masked = true
			break
		}
	}
	if !masked {
		return nil
	}
	var old *config.WebhookConfig
	for i := range live {
		if live[i].URL != wh.URL {
			continue
		}
		if n == 0 {
			old = &live[i]
			break
		}
		n--
	}
	hdrs := make(map[string]string, len(wh.Headers))
	for k, v := range wh.Headers {
		if isMaskedSecretValue(v) {
			ov, ok := "", false
			if old != nil {
				ov, ok = old.Headers[k]
			}
			if !ok {
				return fmt.Errorf("masked header %q for unknown webhook: %s", k, wh.URL)
			}
			v = ov
		}
		hdrs[k] = v
	}
	wh.Headers = hdrs
	return nil
}

// webhookHeadersPath is the yamlParentPaths path of a webhook's header map
// entries in the config file.
const webhookHeadersPath = "global.webhooks.-.headers"

// maskWebhookHeaderValues masks every value of a webhook's custom headers in
// the raw config text (F10100). The header names are arbitrary, so they cannot
// go through the fixed rawSecretKeys list.
func maskWebhookHeaderValues(content string) string {
	lines := strings.Split(content, "\n")
	paths := yamlParentPaths(lines)
	for i, line := range lines {
		if paths[i] != webhookHeadersPath {
			continue
		}
		if key, val, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(val) != "" {
			lines[i] = key + `: "********"`
		}
	}
	return strings.Join(lines, "\n")
}

// unmaskWebhookHeaderValues restores masked webhook header values from the
// on-disk config: the n-th masked occurrence of a header name takes the n-th
// value that name has in the old file.
func unmaskWebhookHeaderValues(newContent, oldContent string) string {
	origs := map[string][]string{}
	oldLines := strings.Split(oldContent, "\n")
	oldPaths := yamlParentPaths(oldLines)
	for i, line := range oldLines {
		if oldPaths[i] != webhookHeadersPath {
			continue
		}
		if key, val, ok := strings.Cut(line, ":"); ok {
			k := strings.TrimSpace(key)
			origs[k] = append(origs[k], strings.TrimSpace(val))
		}
	}
	occ := map[string]int{}
	newLines := strings.Split(newContent, "\n")
	newPaths := yamlParentPaths(newLines)
	for i, line := range newLines {
		if newPaths[i] != webhookHeadersPath {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k := strings.TrimSpace(key)
		if strings.TrimSpace(val) == secretMask || strings.TrimSpace(val) == "********" {
			if occ[k] < len(origs[k]) {
				newLines[i] = key + ": " + origs[k][occ[k]]
			}
		}
		occ[k]++
	}
	return strings.Join(newLines, "\n")
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
		if trimmed != "" && indent >= keyIndent && strings.HasPrefix(trimmed, "-") {
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

// withDNSCredential returns a copy of creds with name set to value (removed
// when value is empty). It copies because the caller holds a pre-update copy of
// the config for rollback and a shared map would be changed under it.
func withDNSCredential(creds map[string]string, name, value string) map[string]string {
	out := make(map[string]string, len(creds)+1)
	for k, v := range creds {
		out[k] = v
	}
	if value == "" {
		delete(out, name)
	} else {
		out[name] = value
	}
	return out
}

// isSettingsSecretKey reports panel setting keys that SettingsGet returns masked.
func isSettingsSecretKey(key string) bool {
	switch key {
	case "global.admin.api_key",
		"global.admin.pin_code",
		"global.cache.purge_key",
		"global.alerting.webhook_url",
		"global.alerting.slack_url",
		"global.alerting.telegram_token",
		"global.backup.s3.access_key",
		"global.backup.s3.secret_key",
		"global.backup.sftp.password",
		"global.acme.dns_credentials.api_token",
		"global.acme.dns_credentials.api_key",
		"global.acme.dns_credentials.access_key_id",
		"global.acme.dns_credentials.secret_access_key":
		return true
	default:
		return false
	}
}

// isMaskedSecretValue matches maskSecret output and the raw-editor "********" mask.
func isMaskedSecretValue(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if s == "********" || s == `"********"` {
		return true
	}
	if s == "****" {
		return true
	}
	return strings.HasPrefix(s, "****") && len(s) >= 4 && len(s) <= 8
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

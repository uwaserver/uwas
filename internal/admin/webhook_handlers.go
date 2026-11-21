package admin

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/uwaserver/uwas/internal/admin/settings"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/webhook"
)

// redactWebhookURL keeps the scheme and host of a webhook URL and drops the
// path, query and userinfo, where an incoming-webhook URL carries its
// credential. The audit log is readable by every admin and kept on disk
// (F10101).
func redactWebhookURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "[redacted URL]"
	}
	out := u.Scheme + "://" + u.Host
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.User != nil {
		out += "/****"
	}
	return out
}

// testWebhookPushHook, when a test sets it, runs just before a webhook
// create/delete publishes its list to the live manager.
var testWebhookPushHook func()

// --- Webhook Handlers ---

func (s *Server) handleWebhookList(w http.ResponseWriter, r *http.Request) {
	// Outbound webhook destinations, headers, and retry policy expose global
	// infrastructure details even when individual secrets are masked.
	if !s.requireAdmin(w, r) {
		return
	}
	s.configMu.RLock()
	webhooks := s.config.Global.Webhooks
	s.configMu.RUnlock()

	// Mask secrets
	result := make([]any, len(webhooks))
	for i, wh := range webhooks {
		result[i] = map[string]any{
			"url":     wh.URL,
			"events":  wh.Events,
			"headers": settings.MaskWebhookHeaders(wh.Headers),
			"secret":  maskSecret(wh.Secret),
			"retry":   wh.Retry,
			"timeout": wh.Timeout.Duration.Seconds(),
			"enabled": wh.Enabled,
		}
	}

	limit, offset := parsePagination(r)
	items, total := paginateSlice(result, limit, offset)
	jsonResponse(w, map[string]any{
		"items":  items,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

func (s *Server) handleWebhookCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req config.WebhookConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	if req.URL == "" {
		jsonError(w, "URL is required", http.StatusBadRequest)
		return
	}

	// Validate URL
	if !strings.HasPrefix(req.URL, "http://") && !strings.HasPrefix(req.URL, "https://") {
		jsonError(w, "URL must start with http:// or https://", http.StatusBadRequest)
		return
	}

	// SSRF check: block private IPs, localhost, cloud metadata endpoints
	if err := config.IsWebhookURLSafe(req.URL); err != nil {
		jsonError(w, "URL is not allowed: "+err.Error(), http.StatusBadRequest)
		return
	}

	// A subscription naming an event that is never fired is dead on arrival,
	// and config validation refuses to load it (F13802).
	if err := config.ValidateWebhookEvents(req.Events); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Reject custom headers containing CRLF to prevent HTTP header injection.
	// Go's net/http sanitizes header keys automatically but NOT values.
	for k, v := range req.Headers {
		if strings.ContainsAny(k, "\r\n") || strings.ContainsAny(v, "\r\n") {
			jsonError(w, "header keys and values must not contain CR or LF", http.StatusBadRequest)
			return
		}
	}

	// Negative limits are never meaningful: a negative timeout puts the
	// delivery dialer's deadline in the past, so every delivery would fail.
	if req.Retry < 0 {
		jsonError(w, "retry must not be negative", http.StatusBadRequest)
		return
	}
	if req.Timeout.Duration < 0 {
		jsonError(w, "timeout must not be negative", http.StatusBadRequest)
		return
	}

	// Set defaults
	if req.Retry == 0 {
		req.Retry = 3
	}
	if req.Timeout.Duration == 0 {
		req.Timeout.Duration = 30 * time.Second
	}
	req.Enabled = true

	s.configMu.Lock()
	s.config.Global.Webhooks = append(s.config.Global.Webhooks, req)
	webhooks := make([]config.WebhookConfig, len(s.config.Global.Webhooks))
	copy(webhooks, s.config.Global.Webhooks)
	// Publish under the config lock, as WebhooksChanged does: published after
	// unlocking, a slower request's older list overwrote a newer one, so a
	// created webhook never went live or a deleted one came back (F65900).
	s.publishWebhooksLocked(webhooks)
	s.configMu.Unlock()

	// Without this the webhook vanished on the next restart (F2021).
	if err := s.persistConfig(); err != nil {
		s.recordAuditR(r, "webhook.create", redactWebhookURL(req.URL)+": "+err.Error(), false)
		jsonError(w, "webhook applied but could not be persisted: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.recordAuditR(r, "webhook.create", redactWebhookURL(req.URL), true)
	jsonResponse(w, map[string]any{"success": true})
}

func (s *Server) handleWebhookDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if !s.requirePin(w, r) {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		jsonError(w, "webhook ID required", http.StatusBadRequest)
		return
	}

	idx, err := strconv.Atoi(id)
	if err != nil || idx < 0 {
		jsonError(w, "invalid webhook ID", http.StatusBadRequest)
		return
	}

	s.configMu.Lock()
	if idx >= len(s.config.Global.Webhooks) {
		s.configMu.Unlock()
		jsonError(w, "webhook not found", http.StatusNotFound)
		return
	}

	// Build a fresh slice instead of splicing in place: readers such as
	// handleWebhookList iterate a snapshot of the old slice after unlocking.
	old := s.config.Global.Webhooks
	whURL := old[idx].URL
	webhooks := make([]config.WebhookConfig, 0, len(old)-1)
	webhooks = append(webhooks, old[:idx]...)
	webhooks = append(webhooks, old[idx+1:]...)
	s.config.Global.Webhooks = webhooks
	s.publishWebhooksLocked(webhooks) // F65900
	s.configMu.Unlock()

	if err := s.persistConfig(); err != nil {
		s.recordAuditR(r, "webhook.delete", redactWebhookURL(whURL)+": "+err.Error(), false)
		jsonError(w, "webhook removed but could not be persisted: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.recordAuditR(r, "webhook.delete", redactWebhookURL(whURL), true)
	jsonResponse(w, map[string]any{"success": true})
}

func (s *Server) handleWebhookTest(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	if req.URL == "" {
		jsonError(w, "URL is required", http.StatusBadRequest)
		return
	}

	// SSRF check: block private IPs, localhost, cloud metadata endpoints
	if err := config.IsWebhookURLSafe(req.URL); err != nil {
		jsonError(w, "URL is not allowed: "+err.Error(), http.StatusBadRequest)
		return
	}

	if s.webhookMgr == nil {
		jsonError(w, "webhook manager not initialized", http.StatusServiceUnavailable)
		return
	}

	// Fire a test event to the specific URL
	s.webhookMgr.FireTo(req.URL, webhook.EventTest, map[string]any{
		"message": "This is a test event from UWAS",
		"time":    time.Now().UTC(),
	})

	s.recordAuditR(r, "webhook.test", redactWebhookURL(req.URL), true)
	jsonResponse(w, map[string]any{"success": true, "message": "Test event fired to " + req.URL})
}

// toWebhookConfigs converts config.WebhookConfig to webhook.WebhookConfig.
// publishWebhooksLocked pushes webhooks to the live manager; the caller holds
// configMu, so publishes follow the order of the config changes (F65900).
func (s *Server) publishWebhooksLocked(webhooks []config.WebhookConfig) {
	if s.webhookMgr == nil {
		return
	}
	if testWebhookPushHook != nil {
		testWebhookPushHook()
	}
	s.webhookMgr.UpdateWebhooks(toWebhookConfigs(webhooks))
}

func toWebhookConfigs(cfgs []config.WebhookConfig) []webhook.WebhookConfig {
	result := make([]webhook.WebhookConfig, len(cfgs))
	for i, cfg := range cfgs {
		events := make([]webhook.EventType, len(cfg.Events))
		for j, e := range cfg.Events {
			events[j] = webhook.EventType(e)
		}
		result[i] = webhook.WebhookConfig{
			URL:      cfg.URL,
			Events:   events,
			Headers:  cfg.Headers,
			Secret:   cfg.Secret,
			RetryMax: cfg.Retry,
			Timeout:  cfg.Timeout.Duration,
			Enabled:  cfg.Enabled,
		}
	}
	return result
}

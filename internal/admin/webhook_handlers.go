
package admin

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/webhook"
)

// --- Webhook Handlers ---

func (s *Server) handleWebhookList(w http.ResponseWriter, r *http.Request) {
	s.configMu.RLock()
	webhooks := s.config.Global.Webhooks
	s.configMu.RUnlock()

	// Mask secrets
	result := make([]any, len(webhooks))
	for i, wh := range webhooks {
		result[i] = map[string]any{
			"url":     wh.URL,
			"events":  wh.Events,
			"headers": wh.Headers,
			"secret":  maskSecret(wh.Secret),
			"retry":   wh.Retry,
			"timeout": wh.Timeout.Duration.Seconds(),
			"enabled": wh.Enabled,
		}
	}

	jsonResponse(w, result)
}

func (s *Server) handleWebhookCreate(w http.ResponseWriter, r *http.Request) {
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
	s.configMu.Unlock()

	// Update webhook manager
	if s.webhookMgr != nil {
		s.webhookMgr.UpdateWebhooks(toWebhookConfigs(s.config.Global.Webhooks))
	}

	s.RecordAudit("webhook.create", req.URL, requestIP(r), true)
	jsonResponse(w, map[string]any{"success": true})
}

func (s *Server) handleWebhookDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requirePin(w, r) { return }
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

	url := s.config.Global.Webhooks[idx].URL
	s.config.Global.Webhooks = append(s.config.Global.Webhooks[:idx], s.config.Global.Webhooks[idx+1:]...)
	s.configMu.Unlock()

	// Update webhook manager
	if s.webhookMgr != nil {
		s.webhookMgr.UpdateWebhooks(toWebhookConfigs(s.config.Global.Webhooks))
	}

	s.RecordAudit("webhook.delete", url, requestIP(r), true)
	jsonResponse(w, map[string]any{"success": true})
}

func (s *Server) handleWebhookTest(w http.ResponseWriter, r *http.Request) {
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

	if s.webhookMgr == nil {
		jsonError(w, "webhook manager not initialized", http.StatusServiceUnavailable)
		return
	}

	// Fire a test event to the specific URL
	s.webhookMgr.FireTo(req.URL, webhook.EventTest, map[string]any{
		"message": "This is a test event from UWAS",
		"time":    time.Now().UTC(),
	})

	s.RecordAudit("webhook.test", req.URL, requestIP(r), true)
	jsonResponse(w, map[string]any{"success": true, "message": "Test event fired to " + req.URL})
}

// toWebhookConfigs converts config.WebhookConfig to webhook.WebhookConfig.
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

package admin

import (
	crand "crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/notify"
)

// ============ Notifications ============

func (s *Server) handleNotifyTest(w http.ResponseWriter, r *http.Request) {
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

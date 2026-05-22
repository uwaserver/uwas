package admin

import (
	crand "crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/notify"
)

// recoveryCodeBcryptCost is below bcrypt.DefaultCost to keep recovery
// verification responsive (DefaultCost = 10 → ~70ms × up to 8 codes
// per attempt = ~560ms worst case). The codes themselves carry 32 bits
// of entropy, so the goal of the hash is to defeat offline cracking of
// a leaked config file, not to slow an online brute force (which is
// already infeasible at 4.3B possibilities per code and rate-limited
// by the admin middleware). Cost 8 keeps online verify under ~150ms
// total while still requiring ~256 bcrypt operations per code per
// guess offline.
const recoveryCodeBcryptCost = 8

// recoveryCodeLooksHashed reports whether s appears to be a bcrypt
// hash (starts with $2a$ / $2b$ / $2y$ and is at least 60 chars).
// Used so we can transparently migrate from older plaintext storage.
func recoveryCodeLooksHashed(s string) bool {
	if len(s) < 60 {
		return false
	}
	return strings.HasPrefix(s, "$2a$") ||
		strings.HasPrefix(s, "$2b$") ||
		strings.HasPrefix(s, "$2y$")
}

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
	hashes := make([]string, 8)
	for i := range codes {
		b := make([]byte, 4)
		if _, err := crand.Read(b); err != nil {
			jsonError(w, "entropy failure", http.StatusInternalServerError)
			return
		}
		codes[i] = fmt.Sprintf("%x", b)
		hashed, err := bcrypt.GenerateFromPassword([]byte(codes[i]), recoveryCodeBcryptCost)
		if err != nil {
			jsonError(w, "hash failure", http.StatusInternalServerError)
			return
		}
		hashes[i] = string(hashed)
	}
	// Persist only the hashes; the cleartext codes are shown to the
	// user once in the response and then discarded.
	s.configMu.Lock()
	s.config.Global.Admin.RecoveryCodes = hashes
	s.configMu.Unlock()
	s.persistConfig()
	s.recordAuditR(r, "2fa.recovery_codes.generated", "", true)
	// Discourage caching of the cleartext code list. The response is
	// already auth-gated, but defense-in-depth against intermediate
	// proxies and disk caches.
	w.Header().Set("Cache-Control", "no-store, no-cache, max-age=0, private")
	w.Header().Set("Pragma", "no-cache")
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
	// Length-validate the supplied code before doing any hash work.
	// Generated codes are 8 hex chars; accept 4–64 to cover any future
	// length change without re-tuning. Rejecting obviously malformed
	// input early avoids exposing bcrypt's timing surface to
	// unauthenticated junk.
	if l := len(req.Code); l < 4 || l > 64 {
		jsonError(w, "invalid recovery code", http.StatusUnauthorized)
		return
	}

	// Iterate stored codes under the lock. We always compare against
	// every entry — never short-circuit on first match — so the verify
	// time does not depend on which code matched (or whether any
	// matched at all).
	s.configMu.Lock()
	stored := s.config.Global.Admin.RecoveryCodes
	matchIdx := -1
	for i, c := range stored {
		var eq int
		if recoveryCodeLooksHashed(c) {
			if err := bcrypt.CompareHashAndPassword([]byte(c), []byte(req.Code)); err == nil {
				eq = 1
			}
		} else {
			// Legacy plaintext code from pre-hash deployments. Compare
			// constant-time and treat as a successful single-use entry
			// if it matches.
			eq = subtle.ConstantTimeCompare([]byte(c), []byte(req.Code))
		}
		if eq == 1 && matchIdx == -1 {
			matchIdx = i
		}
	}
	if matchIdx >= 0 {
		// Remove the used code so it cannot be replayed.
		s.config.Global.Admin.RecoveryCodes = append(
			stored[:matchIdx],
			stored[matchIdx+1:]...,
		)
	}
	s.configMu.Unlock()
	if matchIdx < 0 {
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

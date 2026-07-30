// Package backup provides admin API handlers for backup management:
// list, create, domain backup, restore, delete, schedule get/put.
package backup

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/uwaserver/uwas/internal/backup"
	"github.com/uwaserver/uwas/internal/webhook"
)

// Deps is the interface the sub-package needs from the admin Server.
type Deps interface {
	RequireAdmin(w http.ResponseWriter, r *http.Request) bool
	RequirePin(w http.ResponseWriter, r *http.Request) bool
	RecordAudit(r *http.Request, action, detail string, success bool)
	ParsePagination(r *http.Request) (limit, offset int)
	BackupManager() *backup.BackupManager
	WebhookFire(event webhook.EventType, payload map[string]any)
	// Config domain root lookup
	DomainRoot(domain string) (root string, found bool)
}

// Handler holds backup admin API handlers.
type Handler struct {
	deps Deps
}

// New creates a backup Handler.
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

// PaginatedResponse wraps a list response with pagination metadata.
type PaginatedResponse[T any] struct {
	Items  []T `json:"items"`
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

func paginate[T any](items []T, limit, offset int) ([]T, int) {
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

// List returns all backups with pagination.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	mgr := h.deps.BackupManager()
	if mgr == nil {
		jsonError(w, "backup not enabled", http.StatusNotImplemented)
		return
	}
	backups := mgr.ListBackups()
	if backups == nil {
		backups = make([]backup.BackupInfo, 0)
	}
	limit, offset := h.deps.ParsePagination(r)
	backups, total := paginate(backups, limit, offset)
	jsonResponse(w, PaginatedResponse[backup.BackupInfo]{
		Items: backups, Total: total, Limit: limit, Offset: offset,
	})
}

// Create triggers a full server backup.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	mgr := h.deps.BackupManager()
	if mgr == nil {
		h.deps.RecordAudit(r, "backup.create", "backup not enabled", false)
		jsonError(w, "backup not enabled", http.StatusNotImplemented)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	var req struct {
		Provider string `json:"provider"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Provider == "" {
		req.Provider = "local"
	}

	info, err := mgr.CreateBackup(req.Provider)
	if err != nil {
		h.deps.RecordAudit(r, "backup.create", "provider: "+req.Provider+", error: "+err.Error(), false)
		h.deps.WebhookFire(webhook.EventBackupFailed, map[string]any{
			"provider": req.Provider,
			"error":    err.Error(),
		})
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.deps.RecordAudit(r, "backup.create", "provider: "+req.Provider, true)
	h.deps.WebhookFire(webhook.EventBackupCompleted, map[string]any{
		"provider": req.Provider,
		"name":     info.Name,
		"size":     info.Size,
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(info)
}

// DomainBackup creates a single-domain backup (files + database).
func (h *Handler) DomainBackup(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	mgr := h.deps.BackupManager()
	if mgr == nil {
		jsonError(w, "backup not enabled", http.StatusNotImplemented)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Domain   string `json:"domain"`
		Provider string `json:"provider"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Domain == "" {
		jsonError(w, "domain is required", http.StatusBadRequest)
		return
	}
	if req.Provider == "" {
		req.Provider = "local"
	}

	webRoot, found := h.deps.DomainRoot(req.Domain)
	if !found {
		h.deps.RecordAudit(r, "backup.domain", req.Domain+": unknown domain", false)
		jsonError(w, "unknown domain: "+req.Domain, http.StatusNotFound)
		return
	}

	// Try to detect DB name from wp-config.php
	var dbName string
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

	info, err := mgr.CreateDomainBackup(req.Domain, webRoot, dbName, req.Provider)
	if err != nil {
		h.deps.RecordAudit(r, "backup.domain", req.Domain+": "+err.Error(), false)
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.deps.RecordAudit(r, "backup.domain", req.Domain, true)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(info)
}

// Restore restores a backup archive. Requires PIN confirmation.
func (h *Handler) Restore(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) || !h.deps.RequirePin(w, r) {
		return
	}
	mgr := h.deps.BackupManager()
	if mgr == nil {
		h.deps.RecordAudit(r, "backup.restore", "backup not enabled", false)
		jsonError(w, "backup not enabled", http.StatusNotImplemented)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	var req struct {
		Name     string `json:"name"`
		Provider string `json:"provider"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		jsonError(w, "name is required", http.StatusBadRequest)
		return
	}
	if req.Provider == "" {
		req.Provider = "local"
	}

	if err := mgr.RestoreBackup(req.Name, req.Provider); err != nil {
		h.deps.RecordAudit(r, "backup.restore", "name: "+req.Name+", error: "+err.Error(), false)
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.deps.RecordAudit(r, "backup.restore", "name: "+req.Name, true)
	jsonResponse(w, map[string]string{"status": "restored", "name": req.Name})
}

// Delete removes a backup archive. Requires PIN confirmation.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) || !h.deps.RequirePin(w, r) {
		return
	}
	mgr := h.deps.BackupManager()
	if mgr == nil {
		h.deps.RecordAudit(r, "backup.delete", "backup not enabled", false)
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

	if err := mgr.DeleteBackup(name, provider); err != nil {
		h.deps.RecordAudit(r, "backup.delete", "name: "+name+", error: "+err.Error(), false)
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.deps.RecordAudit(r, "backup.delete", "name: "+name, true)
	jsonResponse(w, map[string]string{"status": "deleted", "name": name})
}

// ScheduleGet returns the current backup schedule configuration.
func (h *Handler) ScheduleGet(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	mgr := h.deps.BackupManager()
	if mgr == nil {
		jsonError(w, "backup not enabled", http.StatusNotImplemented)
		return
	}
	jsonResponse(w, mgr.ScheduleDetail())
}

// SchedulePut updates the backup schedule (interval, enabled, keep count).
func (h *Handler) SchedulePut(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	mgr := h.deps.BackupManager()
	if mgr == nil {
		h.deps.RecordAudit(r, "backup.schedule", "backup not enabled", false)
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
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if req.Keep > 0 {
		mgr.SetKeepCount(req.Keep)
	}

	if req.Enabled != nil && !*req.Enabled {
		mgr.ScheduleBackup(0)
		h.deps.RecordAudit(r, "backup.schedule", "disabled", true)
		jsonResponse(w, mgr.ScheduleDetail())
		return
	}

	if req.Interval == "" {
		jsonError(w, "interval is required", http.StatusBadRequest)
		return
	}
	d, err := time.ParseDuration(req.Interval)
	if err != nil {
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

	mgr.ScheduleBackup(d)
	h.deps.RecordAudit(r, "backup.schedule", "interval: "+d.String(), true)
	jsonResponse(w, mgr.ScheduleDetail())
}

// Ensure fmt is used (for potential future error wrapping).
var _ = fmt.Sprintf

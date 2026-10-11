// Package backup provides admin API handlers for backup management:
// list, create, domain backup, restore, delete, schedule get/put.
package backup

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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


// jsonEncode writes v as JSON to w, logging on write failure so truncated
// responses never silently corrupt client state.
func jsonEncode(w http.ResponseWriter, v any) {
	if err := json.NewEncoder(w).Encode(v); err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] admin/backup: JSON write failed: %v\n", err)
	}
}
func jsonResponse(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	jsonEncode(w, data)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	jsonEncode(w, map[string]string{"error": msg})
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
	jsonEncode(w, info)
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
	data, err := readWPConfig(wpConfig)
	if err != nil {
		h.deps.RecordAudit(r, "backup.domain", req.Domain+": "+err.Error(), false)
		jsonError(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	if data != nil {
		var unresolved bool
		if dbName, unresolved = wpConfigDBName(string(data)); unresolved {
			h.deps.RecordAudit(r, "backup.domain", req.Domain+": DB_NAME in wp-config.php is not a string literal", false)
			jsonError(w, "cannot determine DB_NAME from wp-config.php (not a string literal); database would be missing from the backup", http.StatusUnprocessableEntity)
			return
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
	jsonEncode(w, info)
}

// maxWPConfigSize bounds the wp-config.php read; real files are a few KiB.
const maxWPConfigSize = 1 << 20

// readWPConfig reads the tenant-controlled wp-config.php without following a
// symlink (which would dump another site's database into this archive) and
// without opening anything but a regular file (a FIFO blocks open forever).
// A missing file returns nil, nil; other open/read errors are ignored as before.
func readWPConfig(path string) ([]byte, error) {
	li, err := os.Lstat(path)
	if err != nil {
		return nil, nil
	}
	if !li.Mode().IsRegular() {
		return nil, fmt.Errorf("wp-config.php is not a regular file; refusing to read it")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !os.SameFile(li, fi) {
		return nil, fmt.Errorf("wp-config.php changed while being read")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxWPConfigSize+1))
	if err != nil {
		return nil, nil
	}
	if len(data) > maxWPConfigSize {
		return nil, fmt.Errorf("wp-config.php is larger than %d bytes", maxWPConfigSize)
	}
	return data, nil
}

var (
	wpBlockCommentRe = regexp.MustCompile(`(?s)/\*.*?\*/`)
	wpLineCommentRe  = regexp.MustCompile(`(?m)^\s*(//|#).*$`)
	wpDBNameDefRe    = regexp.MustCompile(`define\s*\(\s*['"]DB_NAME['"]\s*,`)
	wpDBNameRe       = regexp.MustCompile(`define\s*\(\s*['"]DB_NAME['"]\s*,\s*(?:'([^']*)'|"([^"]*)")\s*\)`)
)

// wpConfigDBName returns the DB_NAME PHP would use: the first define() outside
// comments, with either quote style. unresolved reports a live DB_NAME define
// whose value is not a string literal (e.g. getenv()), which can't be resolved.
func wpConfigDBName(src string) (name string, unresolved bool) {
	src = wpBlockCommentRe.ReplaceAllString(src, "")
	src = wpLineCommentRe.ReplaceAllString(src, "")
	loc := wpDBNameDefRe.FindStringIndex(src)
	if loc == nil {
		return "", false
	}
	m := wpDBNameRe.FindStringSubmatchIndex(src)
	if m == nil || m[0] != loc[0] {
		return "", true
	}
	return src[max(m[2], m[4]):max(m[3], m[5])], false
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

	if req.Enabled != nil && !*req.Enabled {
		if req.Keep > 0 {
			mgr.SetKeepCount(req.Keep)
		}
		mgr.ScheduleBackup(0)
		if !h.persistSchedule(w, r, "", req.Keep) {
			return
		}
		h.deps.RecordAudit(r, "backup.schedule", "disabled", true)
		jsonResponse(w, mgr.ScheduleDetail())
		return
	}

	if req.Interval == "" {
		jsonError(w, "interval is required", http.StatusBadRequest)
		return
	}
	d, err := parseScheduleInterval(req.Interval)
	if err != nil {
		jsonError(w, "invalid interval: "+err.Error(), http.StatusBadRequest)
		return
	}
	if d < time.Minute {
		jsonError(w, "interval must be at least 1m", http.StatusBadRequest)
		return
	}

	// Apply only after validation so a rejected request changes nothing.
	if req.Keep > 0 {
		mgr.SetKeepCount(req.Keep)
	}
	mgr.ScheduleBackup(d)
	if !h.persistSchedule(w, r, d.String(), req.Keep) {
		return
	}
	h.deps.RecordAudit(r, "backup.schedule", "interval: "+d.String(), true)
	jsonResponse(w, mgr.ScheduleDetail())
}

// schedulePersister is implemented by Deps that can write the schedule back to
// the config file. It is optional so existing Deps implementations keep working.
type schedulePersister interface {
	PersistBackupSchedule(schedule string, keep int) error
}

// persistSchedule writes the applied schedule to the config so it survives a
// restart (F2022). It reports false after answering the request with an error.
func (h *Handler) persistSchedule(w http.ResponseWriter, r *http.Request, schedule string, keep int) bool {
	p, ok := h.deps.(schedulePersister)
	if !ok {
		return true
	}
	if err := p.PersistBackupSchedule(schedule, keep); err != nil {
		h.deps.RecordAudit(r, "backup.schedule", "persist failed: "+err.Error(), false)
		jsonError(w, "schedule applied but could not be persisted: "+err.Error(), http.StatusInternalServerError)
		return false
	}
	return true
}

// parseScheduleInterval accepts a Go duration or the "<N>d" day shorthand that
// ScheduleGet itself reports (e.g. "1d" for 24h), so GET → PUT round-trips.
func parseScheduleInterval(s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err == nil {
		return d, nil
	}
	if n, ok := strings.CutSuffix(s, "d"); ok && n != "" {
		if days, convErr := strconv.Atoi(n); convErr == nil && days > 0 && days <= 3650 {
			return time.Duration(days) * 24 * time.Hour, nil
		}
	}
	return 0, err
}

// Ensure fmt is used (for potential future error wrapping).
var _ = fmt.Sprintf

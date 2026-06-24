package admin

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/uwaserver/uwas/internal/backup"
	"github.com/uwaserver/uwas/internal/webhook"
)

func (s *Server) SetBackupManager(m *backup.BackupManager) { s.backupMgr = m }

func (s *Server) handleBackupList(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.backupMgr == nil {
		jsonError(w, "backup not enabled", http.StatusNotImplemented)
		return
	}
	backups := s.backupMgr.ListBackups()
	if backups == nil {
		backups = make([]backup.BackupInfo, 0)
	}
	limit, offset := parsePagination(r)
	backups, total := paginateSlice(backups, limit, offset)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"items":  backups,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

func (s *Server) handleBackupCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.backupMgr == nil {
		s.recordAuditR(r, "backup.create", "backup not enabled", false)
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

	info, err := s.backupMgr.CreateBackup(req.Provider)
	if err != nil {
		s.recordAuditR(r, "backup.create", "provider: "+req.Provider+", error: "+err.Error(), false)
		if s.webhookMgr != nil {
			s.webhookMgr.Fire(webhook.EventBackupFailed, map[string]any{
				"provider": req.Provider,
				"error":    err.Error(),
			})
		}
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.recordAuditR(r, "backup.create", "provider: "+req.Provider, true)
	if s.webhookMgr != nil {
		s.webhookMgr.Fire(webhook.EventBackupCompleted, map[string]any{
			"provider": req.Provider,
			"name":     info.Name,
			"size":     info.Size,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(info)
}

func (s *Server) handleBackupDomain(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.backupMgr == nil {
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

	// Find domain root and DB name from config
	var webRoot, dbName string
	s.configMu.RLock()
	for _, d := range s.config.Domains {
		if d.Host == req.Domain {
			webRoot = d.Root
			break
		}
	}
	s.configMu.RUnlock()

	// Try to detect DB name from wp-config.php
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

	info, err := s.backupMgr.CreateDomainBackup(req.Domain, webRoot, dbName, req.Provider)
	if err != nil {
		s.recordAuditR(r, "backup.domain", req.Domain+": "+err.Error(), false)
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.recordAuditR(r, "backup.domain", req.Domain, true)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(info)
}

func (s *Server) handleBackupRestore(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) || !s.requirePin(w, r) {
		return
	}
	if s.backupMgr == nil {
		s.recordAuditR(r, "backup.restore", "backup not enabled", false)
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

	if err := s.backupMgr.RestoreBackup(req.Name, req.Provider); err != nil {
		s.recordAuditR(r, "backup.restore", "name: "+req.Name+", error: "+err.Error(), false)
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.recordAuditR(r, "backup.restore", "name: "+req.Name, true)
	jsonResponse(w, map[string]string{"status": "restored", "name": req.Name})
}

func (s *Server) handleBackupDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) || !s.requirePin(w, r) {
		return
	}
	if s.backupMgr == nil {
		s.recordAuditR(r, "backup.delete", "backup not enabled", false)
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

	if err := s.backupMgr.DeleteBackup(name, provider); err != nil {
		s.recordAuditR(r, "backup.delete", "name: "+name+", error: "+err.Error(), false)
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.recordAuditR(r, "backup.delete", "name: "+name, true)
	jsonResponse(w, map[string]string{"status": "deleted", "name": name})
}

func (s *Server) handleBackupScheduleGet(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.backupMgr == nil {
		jsonError(w, "backup not enabled", http.StatusNotImplemented)
		return
	}
	jsonResponse(w, s.backupMgr.ScheduleDetail())
}

func (s *Server) handleBackupSchedulePut(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.backupMgr == nil {
		s.recordAuditR(r, "backup.schedule", "backup not enabled", false)
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

	// Update keep count if provided
	if req.Keep > 0 {
		s.backupMgr.SetKeepCount(req.Keep)
	}

	if req.Enabled != nil && !*req.Enabled {
		s.backupMgr.ScheduleBackup(0)
		s.recordAuditR(r, "backup.schedule", "disabled", true)
		jsonResponse(w, s.backupMgr.ScheduleDetail())
		return
	}

	if req.Interval == "" {
		jsonError(w, "interval is required", http.StatusBadRequest)
		return
	}
	d, err := time.ParseDuration(req.Interval)
	if err != nil {
		// Try common formats: "24h", "7d"
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

	s.backupMgr.ScheduleBackup(d)
	s.recordAuditR(r, "backup.schedule", "interval: "+d.String(), true)
	jsonResponse(w, s.backupMgr.ScheduleDetail())
}

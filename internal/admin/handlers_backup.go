package admin

import (
	"net/http"

	backupadmin "github.com/uwaserver/uwas/internal/admin/backup"
	"github.com/uwaserver/uwas/internal/backup"
	"github.com/uwaserver/uwas/internal/webhook"
)

// backupDeps adapts admin.Server to the backup.Deps interface.
type backupDeps struct {
	s *Server
}

func (d *backupDeps) RequireAdmin(w http.ResponseWriter, r *http.Request) bool {
	return d.s.requireAdmin(w, r)
}
func (d *backupDeps) RequirePin(w http.ResponseWriter, r *http.Request) bool {
	return d.s.requirePin(w, r)
}
func (d *backupDeps) RecordAudit(r *http.Request, action, detail string, success bool) {
	d.s.recordAuditR(r, action, detail, success)
}
func (d *backupDeps) ParsePagination(r *http.Request) (limit, offset int) {
	return parsePagination(r)
}
func (d *backupDeps) BackupManager() *backup.BackupManager { return d.s.backupMgr }
func (d *backupDeps) WebhookFire(event webhook.EventType, payload map[string]any) {
	if d.s.webhookMgr != nil {
		d.s.webhookMgr.Fire(event, payload)
	}
}
func (d *backupDeps) DomainRoot(domain string) (root string, found bool) {
	d.s.configMu.RLock()
	defer d.s.configMu.RUnlock()
	for _, d := range d.s.config.Domains {
		if d.Host == domain {
			return d.Root, true
		}
	}
	return "", false
}

// backupHandler holds the backup admin handler instance.
var backupHandler *backupadmin.Handler

func (s *Server) initBackupHandler() {
	backupHandler = backupadmin.New(&backupDeps{s: s})
}

// SetBackupManager wires the backup manager.
func (s *Server) SetBackupManager(m *backup.BackupManager) { s.backupMgr = m }

// ── Thin wrappers ──

func (s *Server) handleBackupList(w http.ResponseWriter, r *http.Request)         { backupHandler.List(w, r) }
func (s *Server) handleBackupCreate(w http.ResponseWriter, r *http.Request)        { backupHandler.Create(w, r) }
func (s *Server) handleBackupDomain(w http.ResponseWriter, r *http.Request)        { backupHandler.DomainBackup(w, r) }
func (s *Server) handleBackupRestore(w http.ResponseWriter, r *http.Request)       { backupHandler.Restore(w, r) }
func (s *Server) handleBackupDelete(w http.ResponseWriter, r *http.Request)        { backupHandler.Delete(w, r) }
func (s *Server) handleBackupScheduleGet(w http.ResponseWriter, r *http.Request)   { backupHandler.ScheduleGet(w, r) }
func (s *Server) handleBackupSchedulePut(w http.ResponseWriter, r *http.Request)   { backupHandler.SchedulePut(w, r) }

// Compile-time check.
var _ backupadmin.Deps = (*backupDeps)(nil)

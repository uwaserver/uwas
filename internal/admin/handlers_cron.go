package admin

import (
	"encoding/json"
	"net/http"

	"github.com/uwaserver/uwas/internal/auth"
	"github.com/uwaserver/uwas/internal/cronjob"
)

// SetCronMonitor sets the cron job monitor for execution tracking.
func (s *Server) SetCronMonitor(m *cronjob.Monitor) { s.cronMonitor = m }

func (s *Server) handleCronMonitorList(w http.ResponseWriter, r *http.Request) {
	if s.cronMonitor == nil {
		jsonResponse(w, []any{})
		return
	}
	statuses := s.cronMonitor.GetAllStatus()
	// Per-domain scoping: a non-admin must not see other tenants' job
	// commands/output. Admins (and single-key mode) pass everything.
	filtered := make([]cronjob.JobStatus, 0, len(statuses))
	for _, st := range statuses {
		if s.canAccessDomain(r, st.Domain) {
			filtered = append(filtered, st)
		}
	}
	jsonResponse(w, filtered)
}

func (s *Server) handleCronMonitorDomain(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")
	if !s.requireDomainAccess(w, r, host, "cron.read") {
		return
	}
	if s.cronMonitor == nil {
		jsonError(w, "cron monitor not initialized", http.StatusServiceUnavailable)
		return
	}
	statuses := s.cronMonitor.GetDomainStatus(host)
	if statuses == nil {
		statuses = []cronjob.JobStatus{}
	}
	jsonResponse(w, statuses)
}

func (s *Server) handleCronExecute(w http.ResponseWriter, r *http.Request) {
	// Admin-only: cron execute runs arbitrary shell commands
	if s.authMgr != nil {
		user, ok := auth.UserFromContext(r.Context())
		if ok && user.Role != auth.RoleAdmin {
			jsonError(w, "forbidden: admin only", http.StatusForbidden)
			return
		}
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Domain   string `json:"domain"`
		Schedule string `json:"schedule"`
		Command  string `json:"command"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Domain == "" || req.Command == "" {
		jsonError(w, "domain and command are required", http.StatusBadRequest)
		return
	}
	if s.cronMonitor == nil {
		jsonError(w, "cron monitor not initialized", http.StatusServiceUnavailable)
		return
	}

	// Execute the job asynchronously and return the record
	record := s.cronMonitor.Execute(req.Domain, req.Schedule, req.Command)

	s.recordAuditR(r, "cron.execute", req.Domain+": "+req.Command, record.Success)

	w.Header().Set("Content-Type", "application/json")
	if record.Success {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusOK) // Still 200, but success=false in body
	}
	json.NewEncoder(w).Encode(record)
}

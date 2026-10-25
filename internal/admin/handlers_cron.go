package admin

import (
	"encoding/json"
	"net/http"

	"github.com/uwaserver/uwas/internal/apps"
	"github.com/uwaserver/uwas/internal/auth"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/cronjob"
	"github.com/uwaserver/uwas/internal/domainroot"
)

// SetCronMonitor sets the cron job monitor for execution tracking.
func (s *Server) SetCronMonitor(m *cronjob.Monitor) {
	s.cronMonitor = m
	if m != nil {
		m.SetRootResolver(s.cronDomainRoot)
	}
}

// cronDomainRoot returns the configured root of the domain that serves host
// (main host or alias, any spelling), so a manual cron run starts where the
// site's files are.
func (s *Server) cronDomainRoot(host string) string {
	s.configMu.RLock()
	idx := s.certHostDomain(host)
	var dom config.Domain
	if idx >= 0 {
		dom = s.config.Domains[idx]
	}
	s.configMu.RUnlock()
	if idx < 0 {
		return ""
	}
	var store *apps.Store
	var instances []apps.Instance
	if mgr := s.appsMgr; mgr != nil {
		store = mgr.Store()
		instances = mgr.Instances()
	}
	root, err := domainroot.ForDomainWithApps(dom, store, instances)
	if err != nil {
		return dom.Root
	}
	return root
}

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
	// Cron execute runs arbitrary shell commands. requirePermission alone is
	// insufficient: in single-key mode (authMgr == nil) it returns true without
	// checking the request context (VULN-035). Add an explicit auth check first.
	if s.getAuthMgr() == nil {
		if _, ok := auth.UserFromContext(r.Context()); !ok {
			jsonError(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	} else {
		if !s.requirePermission(w, r, auth.PermSystemConfig) {
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
	if !s.requireDomainAccess(w, r, req.Domain, "cron.execute") {
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
	jsonEncode(w, record)
}

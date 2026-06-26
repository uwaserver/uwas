package admin

import (
	"net/http"

	"github.com/uwaserver/uwas/internal/bandwidth"
)

// SetBandwidthManager sets the bandwidth manager for bandwidth monitoring and limits.
func (s *Server) SetBandwidthManager(m *bandwidth.Manager) { s.bwMgr = m }

func (s *Server) handleBandwidthList(w http.ResponseWriter, r *http.Request) {
	if s.bwMgr == nil {
		jsonResponse(w, []any{})
		return
	}
	statuses := s.bwMgr.GetAllStatus()
	// Per-domain scoping: non-admins only see their own domains' usage.
	filtered := make([]bandwidth.Status, 0, len(statuses))
	for _, st := range statuses {
		if s.canAccessDomain(r, st.Host) {
			filtered = append(filtered, st)
		}
	}
	jsonResponse(w, filtered)
}

func (s *Server) handleBandwidthGet(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")
	if !s.requireDomainAccess(w, r, host, "bandwidth.read") {
		return
	}
	if s.bwMgr == nil {
		jsonError(w, "bandwidth manager not initialized", http.StatusServiceUnavailable)
		return
	}
	status := s.bwMgr.GetStatus(host)
	if status == nil {
		jsonError(w, "domain not found or bandwidth not enabled", http.StatusNotFound)
		return
	}
	jsonResponse(w, status)
}

func (s *Server) handleBandwidthReset(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")
	if !s.requireDomainAccess(w, r, host, "bandwidth.reset") {
		return
	}
	if s.bwMgr == nil {
		jsonError(w, "bandwidth manager not initialized", http.StatusServiceUnavailable)
		return
	}
	s.bwMgr.Reset(host)
	s.recordAuditR(r, "bandwidth.reset", host, true)
	jsonResponse(w, map[string]string{"status": "reset", "host": host})
}

package admin

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/uwaserver/uwas/internal/autoblock"
)

// SetAutoBlocker wires the autoblocker into the /api/v1/autoblock endpoints.
func (s *Server) SetAutoBlocker(b *autoblock.Blocker) { s.autoblocker = b }

func (s *Server) handleAutoBlockStatus(w http.ResponseWriter, r *http.Request) {
	if s.autoblocker == nil {
		jsonResponse(w, map[string]any{"enabled": false, "active_blocks": 0})
		return
	}
	st := s.autoblocker.Stats()
	st["blocks"] = s.autoblocker.List()
	jsonResponse(w, st)
}

// handleAutoBlockAdd blocks a source IP by hand. duration accepts a Go
// duration string; "permanent" (or a negative value) never expires.
func (s *Server) handleAutoBlockAdd(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.autoblocker == nil {
		jsonError(w, "autoblock not enabled", http.StatusNotImplemented)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		IP       string `json:"ip"`
		Reason   string `json:"reason"`
		Duration string `json:"duration"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.IP == "" {
		jsonError(w, "ip is required", http.StatusBadRequest)
		return
	}

	var dur time.Duration
	switch req.Duration {
	case "":
		// zero means "use the configured base duration"
	case "permanent", "forever":
		dur = -1
	default:
		d, err := time.ParseDuration(req.Duration)
		if err != nil {
			jsonError(w, "invalid duration: "+err.Error(), http.StatusBadRequest)
			return
		}
		dur = d
	}

	if err := s.autoblocker.Block(req.IP, req.Reason, dur); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.recordAuditR(r, "autoblock.add", req.IP, true)
	jsonResponse(w, map[string]any{"blocked": req.IP})
}

func (s *Server) handleAutoBlockDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.autoblocker == nil {
		jsonError(w, "autoblock not enabled", http.StatusNotImplemented)
		return
	}
	ip := r.PathValue("ip")
	if ip == "" {
		jsonError(w, "ip is required", http.StatusBadRequest)
		return
	}
	if err := s.autoblocker.Unblock(ip); err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	s.recordAuditR(r, "autoblock.remove", ip, true)
	jsonResponse(w, map[string]any{"unblocked": ip})
}

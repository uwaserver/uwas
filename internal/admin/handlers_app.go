package admin

import (
	"net/http"

	"github.com/uwaserver/uwas/internal/terminal"
)

func (s *Server) handleAppList(w http.ResponseWriter, r *http.Request) {
	if s.appMgr == nil {
		jsonResponse(w, []any{})
		return
	}
	jsonResponse(w, s.appMgr.Instances())
}

func (s *Server) handleAppGet(w http.ResponseWriter, r *http.Request) {
	if s.appMgr == nil {
		jsonError(w, "app manager not enabled", http.StatusNotImplemented)
		return
	}
	domain := r.PathValue("domain")
	inst := s.appMgr.Get(domain)
	if inst == nil {
		jsonError(w, "app not found: "+domain, http.StatusNotFound)
		return
	}
	jsonResponse(w, inst)
}

func (s *Server) handleAppStart(w http.ResponseWriter, r *http.Request) {
	if s.appMgr == nil {
		jsonError(w, "app manager not enabled", http.StatusNotImplemented)
		return
	}
	domain := r.PathValue("domain")
	if err := s.appMgr.Start(domain); err != nil {
		jsonError(w, err.Error(), http.StatusConflict)
		return
	}
	s.RecordAudit("app.start", domain, requestIP(r), true)
	jsonResponse(w, map[string]string{"status": "started", "domain": domain})
}

func (s *Server) handleAppStop(w http.ResponseWriter, r *http.Request) {
	if s.appMgr == nil {
		jsonError(w, "app manager not enabled", http.StatusNotImplemented)
		return
	}
	domain := r.PathValue("domain")
	if err := s.appMgr.Stop(domain); err != nil {
		jsonError(w, err.Error(), http.StatusConflict)
		return
	}
	s.RecordAudit("app.stop", domain, requestIP(r), true)
	jsonResponse(w, map[string]string{"status": "stopped", "domain": domain})
}

func (s *Server) handleAppRestart(w http.ResponseWriter, r *http.Request) {
	if s.appMgr == nil {
		jsonError(w, "app manager not enabled", http.StatusNotImplemented)
		return
	}
	domain := r.PathValue("domain")
	if err := s.appMgr.Restart(domain); err != nil {
		jsonError(w, err.Error(), http.StatusConflict)
		return
	}
	s.RecordAudit("app.restart", domain, requestIP(r), true)
	jsonResponse(w, map[string]string{"status": "restarted", "domain": domain})
}

// terminalHandler returns the web terminal HTTP handler.
func (s *Server) terminalHandler() http.Handler {
	return terminal.New(s.logger)
}

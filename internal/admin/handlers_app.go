package admin

import (
	"encoding/json"
	"net/http"

	"github.com/uwaserver/uwas/internal/deploy"
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

// --- Deploy handlers ---

func (s *Server) handleDeploy(w http.ResponseWriter, r *http.Request) {
	if s.deployMgr == nil {
		jsonError(w, "deploy manager not enabled", http.StatusNotImplemented)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req deploy.DeployRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	domain := r.PathValue("domain")
	req.Domain = domain

	root := s.domainRoot(domain)
	if root == "" {
		jsonError(w, "domain not found", http.StatusNotFound)
		return
	}

	s.RecordAudit("deploy.start", domain+" git:"+req.GitURL, requestIP(r), true)

	// Deploy in background, restart app on completion
	s.deployMgr.Deploy(req, root, func(err error) {
		if err == nil && s.appMgr != nil {
			_ = s.appMgr.Restart(domain)
		}
	})

	jsonResponse(w, map[string]string{"status": "deploying", "domain": domain})
}

func (s *Server) handleDeployStatus(w http.ResponseWriter, r *http.Request) {
	if s.deployMgr == nil {
		jsonError(w, "deploy manager not enabled", http.StatusNotImplemented)
		return
	}
	domain := r.PathValue("domain")
	status := s.deployMgr.Status(domain)
	if status == nil {
		jsonError(w, "no deployment found", http.StatusNotFound)
		return
	}
	jsonResponse(w, status)
}

func (s *Server) handleDeployList(w http.ResponseWriter, r *http.Request) {
	if s.deployMgr == nil {
		jsonResponse(w, []any{})
		return
	}
	jsonResponse(w, s.deployMgr.AllStatuses())
}

// handleDeployWebhook handles GitHub/GitLab push webhooks for auto-deploy.
func (s *Server) handleDeployWebhook(w http.ResponseWriter, r *http.Request) {
	if s.deployMgr == nil {
		jsonError(w, "deploy manager not enabled", http.StatusNotImplemented)
		return
	}
	domain := r.PathValue("domain")
	root := s.domainRoot(domain)
	if root == "" {
		jsonError(w, "domain not found", http.StatusNotFound)
		return
	}

	// Simple: any POST to this endpoint triggers a deploy (git pull + build + restart)
	req := deploy.DeployRequest{Domain: domain}
	s.RecordAudit("deploy.webhook", domain, requestIP(r), true)

	s.deployMgr.Deploy(req, root, func(err error) {
		if err == nil && s.appMgr != nil {
			_ = s.appMgr.Restart(domain)
		}
	})

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"deploying"}`))
}

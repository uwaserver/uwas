package admin

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

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

func (s *Server) handleAppStats(w http.ResponseWriter, r *http.Request) {
	if s.appMgr == nil {
		jsonError(w, "app manager not enabled", http.StatusNotImplemented)
		return
	}
	domain := r.PathValue("domain")
	stats := s.appMgr.Stats(domain)
	if stats == nil {
		jsonError(w, "app not found: "+domain, http.StatusNotFound)
		return
	}
	jsonResponse(w, stats)
}

// --- App config + logs handlers ---

func (s *Server) handleAppEnvUpdate(w http.ResponseWriter, r *http.Request) {
	if s.appMgr == nil {
		jsonError(w, "app manager not enabled", http.StatusNotImplemented)
		return
	}
	domain := r.PathValue("domain")
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Env     map[string]string `json:"env"`
		Command string            `json:"command,omitempty"`
		Port    int               `json:"port,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Update domain config
	s.configMu.Lock()
	for i := range s.config.Domains {
		if s.config.Domains[i].Host == domain {
			if req.Env != nil {
				s.config.Domains[i].App.Env = req.Env
			}
			if req.Command != "" {
				s.config.Domains[i].App.Command = req.Command
			}
			if req.Port > 0 {
				s.config.Domains[i].App.Port = req.Port
			}
			break
		}
	}
	s.configMu.Unlock()
	s.persistConfig()

	s.RecordAudit("app.env.update", domain, requestIP(r), true)
	jsonResponse(w, map[string]string{"status": "updated", "domain": domain})
}

func (s *Server) handleAppLogs(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root := s.domainRoot(domain)
	if root == "" {
		jsonError(w, "domain not found", http.StatusNotFound)
		return
	}

	logPath := filepath.Join(filepath.Dir(root), "logs", "app.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		jsonResponse(w, map[string]string{"log": "", "error": "no log file"})
		return
	}
	// Return last 100KB
	if len(data) > 100*1024 {
		data = data[len(data)-100*1024:]
	}
	jsonResponse(w, map[string]string{"log": string(data)})
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
// Public endpoint (no auth middleware) — uses webhook secret for verification.
// GitHub: X-Hub-Signature-256 header with HMAC-SHA256
// GitLab: X-Gitlab-Token header with plain secret
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

	// Read body for signature verification
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		jsonError(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Verify webhook secret (from domain's pin_code or global admin API key)
	s.configMu.RLock()
	secret := s.config.Global.Admin.APIKey // use API key as webhook secret
	s.configMu.RUnlock()

	if secret != "" {
		// GitHub: X-Hub-Signature-256 = sha256=HMAC
		if sig := r.Header.Get("X-Hub-Signature-256"); sig != "" {
			mac := hmac.New(sha256.New, []byte(secret))
			mac.Write(body)
			expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
			if !hmac.Equal([]byte(sig), []byte(expected)) {
				jsonError(w, "invalid webhook signature", http.StatusForbidden)
				return
			}
		} else if tok := r.Header.Get("X-Gitlab-Token"); tok != "" {
			// GitLab: X-Gitlab-Token = plain secret
			if tok != secret {
				jsonError(w, "invalid webhook token", http.StatusForbidden)
				return
			}
		} else {
			// No signature — check query param ?secret=
			if qs := r.URL.Query().Get("secret"); qs == "" || qs != secret {
				jsonError(w, "webhook secret required", http.StatusForbidden)
				return
			}
		}
	}

	// Check if this is a push event (GitHub sends X-GitHub-Event header)
	event := r.Header.Get("X-GitHub-Event")
	if event != "" && event != "push" && event != "ping" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ignored", "event": event})
		return
	}
	// Respond to ping
	if event == "ping" {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"pong"}`))
		return
	}

	// Extract branch from payload (optional — defaults to configured branch)
	var branch string
	if len(body) > 0 {
		var payload struct {
			Ref string `json:"ref"` // "refs/heads/main"
		}
		if json.Unmarshal(body, &payload) == nil && strings.HasPrefix(payload.Ref, "refs/heads/") {
			branch = strings.TrimPrefix(payload.Ref, "refs/heads/")
		}
	}

	req := deploy.DeployRequest{Domain: domain}
	if branch != "" {
		req.GitBranch = branch
	}
	s.RecordAudit("deploy.webhook", domain+" branch:"+branch, requestIP(r), true)

	s.deployMgr.Deploy(req, root, func(err error) {
		if err == nil && s.appMgr != nil {
			_ = s.appMgr.Restart(domain)
		}
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "deploying", "branch": branch})
}

package admin

import (
	"fmt"
	"net/http"
	"time"

	appsadmin "github.com/uwaserver/uwas/internal/admin/apps"
	"github.com/uwaserver/uwas/internal/apps"
)

// appsDeps adapts admin.Server to the apps.Deps interface.
type appsDeps struct {
	s *Server
}

func (d *appsDeps) RequireAdmin(w http.ResponseWriter, r *http.Request) bool {
	return d.s.requireAdmin(w, r)
}

func (d *appsDeps) RequirePin(w http.ResponseWriter, r *http.Request) bool {
	return d.s.requirePin(w, r)
}

func (d *appsDeps) LogInfo(msg string, args ...any)  { d.s.logger.Info(msg, args...) }
func (d *appsDeps) LogWarn(msg string, args ...any)  { d.s.logger.Warn(msg, args...) }
func (d *appsDeps) LogError(msg string, args ...any) { d.s.logger.Error(msg, args...) }

func (d *appsDeps) RecordAudit(r *http.Request, action, detail string, success bool) {
	d.s.recordAuditR(r, action, detail, success)
}

func (d *appsDeps) ParsePagination(r *http.Request) (limit, offset int) {
	return parsePagination(r)
}

func (d *appsDeps) AppsManager() *apps.Manager { return d.s.appsMgr }

func (d *appsDeps) Reload() error {
	if d.s.reloadFn == nil {
		return nil
	}
	return d.s.reloadFn()
}

func (d *appsDeps) ConfigPath() string { return d.s.configPath }

func (d *appsDeps) ValidateDeployConfig(def *apps.App) error {
	return validateDeployConfig(def)
}

// appsHandler holds the apps admin handler instance.
// Set in initAppsHandler(), called from New().
var appsHandler *appsadmin.Handler

func (s *Server) initAppsHandler() {
	appsHandler = appsadmin.New(&appsDeps{s: s})
}

// Override the constant from constants.go for the sub-package.
// listeningProbeTimeout is 3s — same as constants.go.
var _ = 3 * time.Second

// ── Thin wrappers for CRUD + lifecycle ──
// The deploy/git/webhook/keys handlers stay in their original files
// until the deploy pipeline is refactored into a standalone package.

func (s *Server) handleAppsList(w http.ResponseWriter, r *http.Request)   { appsHandler.List(w, r) }
func (s *Server) handleAppGet(w http.ResponseWriter, r *http.Request)     { appsHandler.Get(w, r) }
func (s *Server) handleAppCreate(w http.ResponseWriter, r *http.Request)  { appsHandler.Create(w, r) }
func (s *Server) handleAppUpdate(w http.ResponseWriter, r *http.Request)  { appsHandler.Update(w, r) }
func (s *Server) handleAppDelete(w http.ResponseWriter, r *http.Request)  { appsHandler.Delete(w, r) }
func (s *Server) handleAppStart(w http.ResponseWriter, r *http.Request)   { appsHandler.Start(w, r) }
func (s *Server) handleAppStop(w http.ResponseWriter, r *http.Request)    { appsHandler.Stop(w, r) }
func (s *Server) handleAppRestart(w http.ResponseWriter, r *http.Request) { appsHandler.Restart(w, r) }
func (s *Server) handleAppStats(w http.ResponseWriter, r *http.Request)   { appsHandler.Stats(w, r) }
func (s *Server) handleAppLogs(w http.ResponseWriter, r *http.Request)    { appsHandler.Logs(w, r) }

// Compile-time check.
var _ appsadmin.Deps = (*appsDeps)(nil)

// ── Helpers retained for deploy/webhook handlers still in admin ──

var blockedEnvVars = map[string]bool{
	"PATH": true, "LD_PRELOAD": true, "LD_LIBRARY_PATH": true, "LD_AUDIT": true,
	"LD_PROFILE": true, "SHELL": true, "IFS": true, "ENV": true, "BASH_ENV": true,
	"PS4": true, "PROMPT_COMMAND": true, "HOME": true, "USER": true, "LOGNAME": true,
}

func validEnvName(name string) bool {
	if name == "" {
		return false
	}
	for i, c := range name {
		if i == 0 {
			if c != '_' && (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') {
				return false
			}
		} else if c != '_' && (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

func validateAppEnvMap(env map[string]string) error {
	for k := range env {
		if blockedEnvVars[k] {
			return fmt.Errorf("env var %s is reserved", k)
		}
		if !validEnvName(k) {
			return fmt.Errorf("invalid env name: %s", k)
		}
	}
	return nil
}

func appDefinitionForResponse(a *apps.App) *apps.App {
	if a == nil {
		return nil
	}
	out := *a
	out.Deploy.GitToken = ""
	return &out
}

func (s *Server) maybeReloadForApps() {
	if s.reloadFn == nil {
		return
	}
	if err := s.reloadFn(); err != nil && s.logger != nil {
		s.logger.Warn("post-app-change reload failed", "error", err)
	}
}

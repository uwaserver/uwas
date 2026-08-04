// Package apps provides admin API handlers for standalone application
// management: CRUD, lifecycle (start/stop/restart), stats, logs,
// git deploy, webhook-triggered deploy, deploy keys, and deploy history.
//
// Extracted from the admin package following the Deps-interface pattern
// documented in docs/admin-subpackaging.md.
package apps

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/uwaserver/uwas/internal/apps"
)

// ── Deps interface ──

// Deps is the interface the sub-package needs from the admin Server.
type Deps interface {
	// Auth
	RequireAdmin(w http.ResponseWriter, r *http.Request) bool
	RequirePin(w http.ResponseWriter, r *http.Request) bool
	// Logging
	LogInfo(msg string, args ...any)
	LogWarn(msg string, args ...any)
	LogError(msg string, args ...any)
	// Audit
	RecordAudit(r *http.Request, action, detail string, success bool)
	// Pagination
	ParsePagination(r *http.Request) (limit, offset int)
	// Apps manager (read at call time)
	AppsManager() *apps.Manager
	// Reload
	Reload() error
	// Config path (for deploy history persistence root)
	ConfigPath() string
	// Deploy config validation (delegates to admin's git validation helpers)
	ValidateDeployConfig(def *apps.App) error
}

// Handler holds apps admin API handlers.
type Handler struct {
	deps Deps
}

// New creates an apps Handler.
func New(deps Deps) *Handler {
	return &Handler{deps: deps}
}

// ── Constants ──

const listeningProbeTimeout = 3e9 // 3 seconds as int64 nanoseconds

// ── Helpers ──

func jsonResponse(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// blockedEnvVars are system-critical environment variables apps must not override.
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

func paginateSlice[T any](items []T, limit, offset int) ([]T, int) {
	total := len(items)
	if offset >= total {
		return []T{}, total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return items[offset:end], total
}

// maybeReload triggers a config reload so proxy pools re-resolve.
func (h *Handler) maybeReload() {
	if err := h.deps.Reload(); err != nil {
		h.deps.LogWarn("post-app-change reload failed", "error", err)
	}
}

// ── CRUD handlers ──

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	mgr := h.deps.AppsManager()
	if mgr == nil {
		jsonResponse(w, []any{})
		return
	}
	instances := mgr.Instances()
	out := make([]any, 0, len(instances))
	for _, inst := range instances {
		out = append(out, inst)
	}
	limit, offset := h.deps.ParsePagination(r)
	items, total := paginateSlice(out, limit, offset)
	jsonResponse(w, map[string]any{
		"items": items, "total": total, "limit": limit, "offset": offset,
	})
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	mgr := h.deps.AppsManager()
	if mgr == nil {
		jsonError(w, "apps manager not enabled", http.StatusNotImplemented)
		return
	}
	name := r.PathValue("name")
	def, err := mgr.Store().Get(name)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if def == nil {
		jsonError(w, "app not found: "+name, http.StatusNotFound)
		return
	}
	jsonResponse(w, map[string]any{
		"app":      appDefinitionForResponse(def),
		"instance": mgr.Get(name),
	})
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	mgr := h.deps.AppsManager()
	if mgr == nil {
		jsonError(w, "apps manager not enabled", http.StatusNotImplemented)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var a apps.App
	if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	a.Name = apps.SanitizeName(a.Name)

	if mgr.Store().Exists(a.Name) {
		jsonError(w, "app already exists: "+a.Name, http.StatusConflict)
		return
	}

	if a.WorkDir == "" {
		a.WorkDir = mgr.Store().DefaultWorkDir(a.Name)
	}
	if err := a.Validate(); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	scaffolded := false
	if a.Deploy.GitURL == "" {
		var err error
		scaffolded, err = apps.ScaffoldDemo(&a)
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	if err := mgr.Register(&a); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	h.deps.RecordAudit(r, "app.create", a.Name, true)

	def, _ := mgr.Store().Get(a.Name)

	startMode := r.URL.Query().Get("start")
	wantStart := startMode != "false" && !a.Disabled

	result := map[string]any{
		"app":        appDefinitionForResponse(def),
		"started":    false,
		"scaffolded": scaffolded,
	}

	if wantStart {
		if err := mgr.Start(a.Name); err != nil {
			result["start_error"] = err.Error()
		} else {
			result["started"] = true
			if err := mgr.WaitListening(a.Name, listenTimeout()); err != nil {
				result["listening"] = false
				result["listening_warning"] = err.Error()
			} else {
				result["listening"] = true
			}
		}
	}

	h.maybeReload()

	w.WriteHeader(http.StatusCreated)
	jsonResponse(w, result)
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	mgr := h.deps.AppsManager()
	if mgr == nil {
		jsonError(w, "apps manager not enabled", http.StatusNotImplemented)
		return
	}

	name := r.PathValue("name")
	existing, err := mgr.Store().Get(name)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if existing == nil {
		jsonError(w, "app not found: "+name, http.StatusNotFound)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var patch apps.App
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	hasDeployPatch := patch.Deploy.GitURL != "" ||
		patch.Deploy.GitBranch != "" ||
		patch.Deploy.BuildCmd != "" ||
		patch.Deploy.HealthPath != "" ||
		patch.Deploy.SSHKeyPath != "" ||
		patch.Deploy.GitToken != "" ||
		patch.Deploy.WebhookSecret != "" ||
		patch.Deploy.BranchFilter != ""
	hasOperationalPatch := patch.Description != "" ||
		patch.Runtime != "" ||
		patch.Command != "" ||
		patch.WorkDir != "" ||
		patch.Port > 0 ||
		patch.Ports != nil ||
		patch.Env != nil ||
		patch.Docker.Image != "" ||
		patch.Docker.ContainerPort > 0 ||
		patch.Docker.Volumes != nil ||
		patch.Docker.ExtraArgs != nil ||
		patch.Docker.Build.Context != ""

	if patch.Name != "" && patch.Name != name {
		jsonError(w, "renaming via PUT is not supported — delete and recreate", http.StatusBadRequest)
		return
	}
	if patch.Description != "" {
		existing.Description = patch.Description
	}
	if patch.Runtime != "" {
		existing.Runtime = patch.Runtime
	}
	if patch.Command != "" {
		existing.Command = patch.Command
	}
	if patch.WorkDir != "" {
		existing.WorkDir = patch.WorkDir
	}
	if patch.Port > 0 {
		existing.Port = patch.Port
	}
	if patch.Ports != nil {
		existing.Ports = patch.Ports
	}
	if patch.Env != nil {
		if err := validateAppEnvMap(patch.Env); err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		existing.Env = patch.Env
	}
	if patch.Deploy.GitURL != "" {
		existing.Deploy.GitURL = patch.Deploy.GitURL
	}
	if patch.Deploy.GitBranch != "" {
		existing.Deploy.GitBranch = patch.Deploy.GitBranch
	}
	if patch.Deploy.BuildCmd != "" {
		existing.Deploy.BuildCmd = patch.Deploy.BuildCmd
	}
	if patch.Deploy.HealthPath != "" {
		existing.Deploy.HealthPath = patch.Deploy.HealthPath
	}
	if patch.Deploy.SSHKeyPath != "" {
		existing.Deploy.SSHKeyPath = patch.Deploy.SSHKeyPath
	}
	if patch.Deploy.GitToken != "" {
		existing.Deploy.GitToken = patch.Deploy.GitToken
	}
	if patch.Deploy.WebhookSecret != "" {
		existing.Deploy.WebhookSecret = patch.Deploy.WebhookSecret
	}
	if patch.Deploy.BranchFilter != "" {
		existing.Deploy.BranchFilter = patch.Deploy.BranchFilter
	}
	if hasDeployPatch {
		if err := h.deps.ValidateDeployConfig(existing); err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
	}

	if patch.Runtime == apps.RuntimeDocker {
		if patch.Docker.Image != "" {
			existing.Docker.Image = patch.Docker.Image
		}
		if patch.Docker.ContainerPort > 0 {
			existing.Docker.ContainerPort = patch.Docker.ContainerPort
		}
		if patch.Docker.Volumes != nil {
			existing.Docker.Volumes = patch.Docker.Volumes
		}
		if patch.Docker.ExtraArgs != nil {
			existing.Docker.ExtraArgs = patch.Docker.ExtraArgs
		}
		if patch.Docker.Build.Context != "" {
			existing.Docker.Build = patch.Docker.Build
		}
	}
	if hasDeployPatch && !hasOperationalPatch {
		if err := mgr.Store().Save(existing); err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		h.deps.RecordAudit(r, "app.deploy_config", name, true)
		def, _ := mgr.Store().Get(name)
		running := false
		if inst := mgr.Get(name); inst != nil {
			running = inst.Running
		}
		jsonResponse(w, map[string]any{
			"app":       appDefinitionForResponse(def),
			"started":   running,
			"listening": running,
		})
		return
	}

	existing.AutoRestart = patch.AutoRestart
	existing.Disabled = patch.Disabled

	_ = mgr.Stop(name)

	if err := mgr.Register(existing); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	result := map[string]any{
		"started": false,
	}
	if !existing.Disabled {
		if err := mgr.Start(name); err != nil {
			result["start_error"] = err.Error()
		} else {
			result["started"] = true
			if err := mgr.WaitListening(name, listenTimeout()); err != nil {
				result["listening"] = false
				result["listening_warning"] = err.Error()
			} else {
				result["listening"] = true
			}
		}
	} else {
		result["started"] = false
		result["start_error"] = "app is disabled — clear the disabled flag and click Start to launch"
	}

	h.deps.RecordAudit(r, "app.update", name, true)
	h.maybeReload()
	def, _ := mgr.Store().Get(name)
	result["app"] = appDefinitionForResponse(def)
	jsonResponse(w, result)
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) || !h.deps.RequirePin(w, r) {
		return
	}
	mgr := h.deps.AppsManager()
	if mgr == nil {
		jsonError(w, "apps manager not enabled", http.StatusNotImplemented)
		return
	}
	name := r.PathValue("name")
	if !mgr.Store().Exists(name) {
		jsonError(w, "app not found: "+name, http.StatusNotFound)
		return
	}
	if err := mgr.Unregister(name); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.deps.RecordAudit(r, "app.delete", name, true)
	h.maybeReload()
	jsonResponse(w, map[string]string{"status": "deleted", "name": name})
}

func (h *Handler) Start(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	mgr := h.deps.AppsManager()
	if mgr == nil {
		jsonError(w, "apps manager not enabled", http.StatusNotImplemented)
		return
	}
	name := r.PathValue("name")
	existing, err := mgr.Store().Get(name)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if existing == nil {
		jsonError(w, "app not found: "+name, http.StatusNotFound)
		return
	}
	if existing.Disabled {
		existing.Disabled = false
		if err := mgr.Register(existing); err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
	}

	if err := mgr.Start(name); err != nil {
		jsonError(w, err.Error(), http.StatusConflict)
		return
	}
	h.deps.RecordAudit(r, "app.start", name, true)
	h.maybeReload()

	resp := map[string]any{"status": "started", "name": name}
	if err := mgr.WaitListening(name, listenTimeout()); err != nil {
		resp["listening"] = false
		resp["listening_warning"] = err.Error()
	} else {
		resp["listening"] = true
	}
	jsonResponse(w, resp)
}

func (h *Handler) Stop(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	mgr := h.deps.AppsManager()
	if mgr == nil {
		jsonError(w, "apps manager not enabled", http.StatusNotImplemented)
		return
	}
	name := r.PathValue("name")
	existing, err := mgr.Store().Get(name)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if existing == nil {
		jsonError(w, "app not found: "+name, http.StatusNotFound)
		return
	}

	if err := mgr.Stop(name); err != nil {
		if strings.Contains(err.Error(), "not running") ||
			strings.Contains(err.Error(), "not registered") {
			jsonResponse(w, map[string]string{"status": "already stopped", "name": name})
			return
		}
		jsonError(w, err.Error(), http.StatusConflict)
		return
	}

	if !existing.Disabled {
		existing.Disabled = true
		_ = mgr.Store().Save(existing)
	}

	h.deps.RecordAudit(r, "app.stop", name, true)
	h.maybeReload()
	jsonResponse(w, map[string]string{"status": "stopped", "name": name})
}

func (h *Handler) Restart(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	mgr := h.deps.AppsManager()
	if mgr == nil {
		jsonError(w, "apps manager not enabled", http.StatusNotImplemented)
		return
	}
	name := r.PathValue("name")
	existing, err := mgr.Store().Get(name)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if existing == nil {
		jsonError(w, "app not found: "+name, http.StatusNotFound)
		return
	}
	if existing.Disabled {
		existing.Disabled = false
		_ = mgr.Store().Save(existing)
	}
	if err := mgr.Restart(name); err != nil {
		jsonError(w, err.Error(), http.StatusConflict)
		return
	}
	h.deps.RecordAudit(r, "app.restart", name, true)
	h.maybeReload()

	resp := map[string]any{"status": "restarted", "name": name}
	if err := mgr.WaitListening(name, listenTimeout()); err != nil {
		resp["listening"] = false
		resp["listening_warning"] = err.Error()
	} else {
		resp["listening"] = true
	}
	jsonResponse(w, resp)
}

func (h *Handler) Stats(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	mgr := h.deps.AppsManager()
	if mgr == nil {
		jsonError(w, "apps manager not enabled", http.StatusNotImplemented)
		return
	}
	name := r.PathValue("name")
	stats := mgr.Stats(name)
	if stats == nil {
		jsonError(w, "app not found: "+name, http.StatusNotFound)
		return
	}
	jsonResponse(w, stats)
}

func (h *Handler) Logs(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	mgr := h.deps.AppsManager()
	if mgr == nil {
		jsonError(w, "apps manager not enabled", http.StatusNotImplemented)
		return
	}
	name := r.PathValue("name")
	def, err := mgr.Store().Get(name)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if def == nil {
		jsonError(w, "app not found: "+name, http.StatusNotFound)
		return
	}

	logPath := filepath.Join(filepath.Dir(def.WorkDir), "logs", name+".log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		buildLogPath := filepath.Join(filepath.Dir(def.WorkDir), "logs", name+"-build.log")
		if bdata, berr := os.ReadFile(buildLogPath); berr == nil {
			if len(bdata) > 100*1024 {
				bdata = bdata[len(bdata)-100*1024:]
			}
			jsonResponse(w, map[string]string{
				"log":  string(bdata),
				"kind": "build",
			})
			return
		}
		jsonResponse(w, map[string]string{"log": "", "kind": "runtime"})
		return
	}
	if len(data) > 100*1024 {
		data = data[len(data)-100*1024:]
	}
	jsonResponse(w, map[string]string{"log": string(data), "kind": "runtime"})
}

// listenTimeout returns the probe timeout as a duration.
func listenTimeout() time.Duration {
	return time.Duration(listeningProbeTimeout)
}

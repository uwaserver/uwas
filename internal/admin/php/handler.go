// Package php provides admin API handlers for PHP-FPM lifecycle management.
// Extracted from the admin package following the same pattern as
// internal/admin/database (refactor.md A17).
package php

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/uwaserver/uwas/internal/auth"
	"github.com/uwaserver/uwas/internal/phpmanager"
	"github.com/uwaserver/uwas/internal/respond"
)

// Deps is the interface the sub-package needs from the admin Server.
type Deps interface {
	// Auth
	RequireAdmin(w http.ResponseWriter, r *http.Request) bool
	RequireDomainAccess(w http.ResponseWriter, r *http.Request, domain, action string) bool
	CanManageDomain(r *http.Request, domain string) bool
	// Logging
	LogInfo(msg string, args ...any)
	LogWarn(msg string, args ...any)
	LogError(msg string, args ...any)
	// Audit
	RecordAudit(r *http.Request, action, detail string, success bool)
	// Pagination
	ParsePagination(r *http.Request) (limit, offset int)
	// Task queue
	TaskActive() *TaskInfo
	TaskSubmit(category, name, action string, fn func(appendOutput func(string)) error) *TaskInfo
	TaskActiveByType(typ string) *TaskInfo
	TaskLatestByType(typ string) *TaskInfo
	// Config access
	DomainRoot(domain string) string
	SetDomainFPMAddress(domain, addr string)
	PersistConfig()
	NotifyDomainChange()
	PersistDomainPHPOverrides(domain string)
	// PHP manager (read at call time so direct s.phpMgr assignments work)
	PHPManager() *phpmanager.Manager
	// PHP install (test seam)
	PhpRunInstall(distro string) (string, error)
}

// TaskInfo is a minimal description of a background task.
type TaskInfo struct {
	ID     string
	Name   string
	Type   string
	Status string
	Output string
	Error  string
}

// Handler holds PHP admin API handlers.
type Handler struct {
	deps Deps
}

// New creates a PHP Handler.
func New(deps Deps) *Handler {
	return &Handler{deps: deps}
}

// ── Helpers ──

func jsonResponse(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	respond.Error(w, code, msg)
}

// validPHPVersion reports whether s is a bare PHP version of the form N.N.
func validPHPVersion(s string) bool {
	dot := false
	before, after := 0, 0
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9':
			if dot {
				after++
			} else {
				before++
			}
		case c == '.' && !dot:
			dot = true
		default:
			return false
		}
	}
	return dot && before > 0 && after > 0
}

// ── Handlers ──

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	if h.deps.PHPManager() == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	statuses := h.deps.PHPManager().Status()
	limit, offset := h.deps.ParsePagination(r)
	items, total := paginateSlice(statuses, limit, offset)
	jsonResponse(w, map[string]any{
		"items": items, "total": total, "limit": limit, "offset": offset,
	})
}

func (h *Handler) InstallInfo(w http.ResponseWriter, r *http.Request) {
	version := r.URL.Query().Get("version")
	if version == "" {
		version = "8.3"
	}
	jsonResponse(w, phpmanager.GetInstallInfo(version))
}

func (h *Handler) Install(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Version == "" {
		req.Version = "8.3"
	}
	if !validPHPVersion(req.Version) {
		jsonError(w, "invalid PHP version (expected N.N, e.g. 8.3)", http.StatusBadRequest)
		return
	}
	if active := h.deps.TaskActive(); active != nil {
		jsonError(w, fmt.Sprintf("another installation in progress: %s (%s)", active.Name, active.ID), http.StatusConflict)
		return
	}
	info := phpmanager.GetInstallInfo(req.Version)
	h.deps.LogInfo("starting PHP install", "version", req.Version, "distro", info.Distro)
	task := h.deps.TaskSubmit("php", req.Version, "install", func(appendOutput func(string)) error {
		output, err := h.deps.PhpRunInstall(info.Distro)
		appendOutput(output)
		if err != nil {
			h.deps.LogError("PHP install failed", "version", req.Version, "error", err)
			return err
		}
		h.deps.LogInfo("PHP install complete", "version", req.Version)
		return nil
	})
	jsonResponse(w, map[string]string{
		"status":  "started",
		"task_id": task.ID,
		"version": req.Version,
		"distro":  info.Distro,
	})
}

func (h *Handler) InstallStatus(w http.ResponseWriter, r *http.Request) {
	if t := h.deps.TaskActiveByType("php"); t != nil {
		jsonResponse(w, map[string]any{
			"status": t.Status, "output": t.Output, "error": t.Error,
			"task_id": t.ID, "version": t.Name,
		})
		return
	}
	if t := h.deps.TaskLatestByType("php"); t != nil {
		jsonResponse(w, map[string]any{
			"status": t.Status, "output": t.Output, "error": t.Error,
			"task_id": t.ID, "version": t.Name,
		})
		return
	}
	jsonResponse(w, map[string]string{"status": "idle"})
}

func (h *Handler) Config(w http.ResponseWriter, r *http.Request) {
	if h.deps.PHPManager() == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	version := r.PathValue("version")
	cfg, err := h.deps.PHPManager().GetConfig(version)
	if err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	jsonResponse(w, cfg)
}

func (h *Handler) ConfigUpdate(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	if h.deps.PHPManager() == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	version := r.PathValue("version")
	var req struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Key == "" {
		jsonError(w, "key is required", http.StatusBadRequest)
		return
	}
	if err := h.deps.PHPManager().SetConfig(version, req.Key, req.Value); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	restarted := false
	if err := h.deps.PHPManager().RestartFPM(version); err == nil {
		restarted = true
	}
	jsonResponse(w, map[string]any{"status": "updated", "key": req.Key, "value": req.Value, "restarted": restarted})
}

func (h *Handler) Extensions(w http.ResponseWriter, r *http.Request) {
	if h.deps.PHPManager() == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	version := r.PathValue("version")
	exts, err := h.deps.PHPManager().GetExtensions(version)
	if err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	jsonResponse(w, exts)
}

func (h *Handler) Start(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	if h.deps.PHPManager() == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	version := r.PathValue("version")
	var req struct {
		ListenAddr string `json:"listen_addr"`
	}
	if r.Body != nil {
		json.NewDecoder(r.Body).Decode(&req)
	}
	if req.ListenAddr == "" {
		req.ListenAddr = "127.0.0.1:9000"
	}
	if err := h.deps.PHPManager().StartFPM(version, req.ListenAddr); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "started", "version": version, "listen": req.ListenAddr})
}

func (h *Handler) Stop(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	if h.deps.PHPManager() == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	version := r.PathValue("version")
	if err := h.deps.PHPManager().StopFPM(version); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "stopped", "version": version})
}

func (h *Handler) Restart(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	if h.deps.PHPManager() == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	version := r.PathValue("version")
	if err := h.deps.PHPManager().RestartFPM(version); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "restarted", "version": version})
}

func (h *Handler) ConfigRawGet(w http.ResponseWriter, r *http.Request) {
	if h.deps.PHPManager() == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	version := r.PathValue("version")
	content, err := h.deps.PHPManager().GetConfigRaw(version)
	if err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	jsonResponse(w, map[string]string{"content": content, "version": version})
}

func (h *Handler) ConfigRawPut(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	if h.deps.PHPManager() == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	version := r.PathValue("version")
	var req struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if err := h.deps.PHPManager().SetConfigRaw(version, req.Content); err != nil {
		h.deps.RecordAudit(r, "php.config_raw_put", fmt.Sprintf("version: %s", version), false)
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.deps.LogInfo("PHP config raw updated", "version", version)
	restarted := false
	if err := h.deps.PHPManager().RestartFPM(version); err == nil {
		restarted = true
	}
	h.deps.RecordAudit(r, "php.config_raw_put", fmt.Sprintf("version: %s, bytes: %d, restarted: %t", version, len(req.Content), restarted), true)
	jsonResponse(w, map[string]any{"status": "saved", "version": version, "restarted": restarted})
}

func (h *Handler) Enable(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	if h.deps.PHPManager() == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	version := r.PathValue("version")
	h.deps.PHPManager().EnableVersion(version)
	h.deps.LogInfo("PHP version enabled", "version", version)
	h.deps.RecordAudit(r, "php.enable", fmt.Sprintf("version: %s", version), true)
	jsonResponse(w, map[string]string{"status": "enabled", "version": version})
}

func (h *Handler) Disable(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	if h.deps.PHPManager() == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	version := r.PathValue("version")
	if err := h.deps.PHPManager().DisableVersion(version); err != nil {
		h.deps.RecordAudit(r, "php.disable", fmt.Sprintf("version: %s", version), false)
		jsonError(w, err.Error(), http.StatusConflict)
		return
	}
	h.deps.LogInfo("PHP version disabled", "version", version)
	h.deps.RecordAudit(r, "php.disable", fmt.Sprintf("version: %s", version), true)
	jsonResponse(w, map[string]string{"status": "disabled", "version": version})
}

// ── Per-domain PHP ──

func (h *Handler) DomainsList(w http.ResponseWriter, r *http.Request) {
	if h.deps.PHPManager() == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	jsonResponse(w, h.deps.PHPManager().GetDomainInstances())
}

func (h *Handler) DomainAssign(w http.ResponseWriter, r *http.Request) {
	if h.deps.PHPManager() == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Domain  string `json:"domain"`
		Version string `json:"version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Domain == "" {
		jsonError(w, "domain is required", http.StatusBadRequest)
		return
	}
	if req.Version == "" {
		jsonError(w, "version is required", http.StatusBadRequest)
		return
	}
	if !h.deps.RequireDomainAccess(w, r, req.Domain, "php.assign") {
		return
	}
	domRoot := h.deps.DomainRoot(req.Domain)
	dp, err := h.deps.PHPManager().AssignDomainWithRoot(req.Domain, req.Version, domRoot)
	if err != nil {
		jsonError(w, err.Error(), http.StatusConflict)
		return
	}
	h.deps.SetDomainFPMAddress(req.Domain, dp.ListenAddr)
	h.deps.PersistConfig()
	h.deps.NotifyDomainChange()
	if err := h.deps.PHPManager().StartDomain(req.Domain); err != nil {
		h.deps.LogWarn("PHP start after assign failed", "domain", req.Domain, "error", err)
	}
	h.deps.RecordAudit(r, "php.assign", req.Domain+": PHP "+req.Version+" → "+dp.ListenAddr, true)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(dp)
}

func (h *Handler) DomainUnassign(w http.ResponseWriter, r *http.Request) {
	if h.deps.PHPManager() == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	domain := r.PathValue("domain")
	if !h.deps.RequireDomainAccess(w, r, domain, "php.unassign") {
		return
	}
	h.deps.PHPManager().UnassignDomain(domain)
	jsonResponse(w, map[string]string{"status": "unassigned", "domain": domain})
}

func (h *Handler) DomainStart(w http.ResponseWriter, r *http.Request) {
	if h.deps.PHPManager() == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	domain := r.PathValue("domain")
	if !h.deps.CanManageDomain(r, domain) {
		h.deps.RecordAudit(r, "php.domain_start", "domain: "+domain+" (forbidden)", false)
		jsonError(w, "forbidden: cannot manage this domain", http.StatusForbidden)
		return
	}
	if err := h.deps.PHPManager().StartDomain(domain); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "started", "domain": domain})
}

func (h *Handler) DomainStop(w http.ResponseWriter, r *http.Request) {
	if h.deps.PHPManager() == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	domain := r.PathValue("domain")
	if !h.deps.CanManageDomain(r, domain) {
		h.deps.RecordAudit(r, "php.domain_stop", "domain: "+domain+" (forbidden)", false)
		jsonError(w, "forbidden: cannot manage this domain", http.StatusForbidden)
		return
	}
	if err := h.deps.PHPManager().StopDomain(domain); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "stopped", "domain": domain})
}

func (h *Handler) DomainConfigGet(w http.ResponseWriter, r *http.Request) {
	if h.deps.PHPManager() == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	domain := r.PathValue("domain")
	if !h.deps.CanManageDomain(r, domain) {
		h.deps.RecordAudit(r, "php.domain_config_get", "domain: "+domain+" (forbidden)", false)
		jsonError(w, "forbidden: cannot manage this domain", http.StatusForbidden)
		return
	}
	cfg := h.deps.PHPManager().GetDomainConfig(domain)
	if cfg == nil {
		jsonError(w, "domain not found or no PHP assignment", http.StatusNotFound)
		return
	}
	jsonResponse(w, cfg)
}

func (h *Handler) DomainConfigPut(w http.ResponseWriter, r *http.Request) {
	if h.deps.PHPManager() == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	domain := r.PathValue("domain")
	if !h.deps.CanManageDomain(r, domain) {
		h.deps.RecordAudit(r, "php.domain_config_put", "domain: "+domain+" (forbidden)", false)
		jsonError(w, "forbidden: cannot manage this domain", http.StatusForbidden)
		return
	}
	var req struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Key == "" {
		jsonError(w, "key is required", http.StatusBadRequest)
		return
	}
	if err := h.deps.PHPManager().SetDomainConfig(domain, req.Key, req.Value); err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	h.deps.PersistDomainPHPOverrides(domain)
	restarted := false
	if err := h.deps.PHPManager().RestartDomain(domain); err == nil {
		restarted = true
	}
	jsonResponse(w, map[string]any{"status": "updated", "domain": domain, "key": req.Key, "value": req.Value, "restarted": restarted})
}

// paginateSlice returns a paginated slice and the total count.
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

// AuthRoleAdmin is re-exported so the sub-package doesn't need to import auth
// directly for the role comparison. But we do import auth for UserFromContext
// which is needed by CanManageDomain in the Deps adapter.
var _ = auth.RoleAdmin // keep import alive for CanManageDomain adapter

// Package wordpress provides admin API handlers for WordPress site
// management: install, detect, update core/plugins, fix permissions,
// security hardening, database optimization, and user management.
package wordpress

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	wp "github.com/uwaserver/uwas/internal/wordpress"
)

// Deps is the interface the sub-package needs from the admin Server.
type Deps interface {
	// Auth
	RequireDomainAccess(w http.ResponseWriter, r *http.Request, domain, action string) bool
	CanAccessDomain(r *http.Request, domain string) bool
	// Domain root resolution (returns root path + ok)
	AuthorizedDomainRoot(w http.ResponseWriter, r *http.Request, domain, action string) (string, bool)
	// Config access
	DomainRoot(domain string) string
	GlobalWebRoot() string
	Domains() []wp.DomainInfo
	// Logging
	LogInfo(msg string, args ...any)
	// Audit
	RecordAudit(r *http.Request, action, detail string, success bool)
}

// Handler holds WordPress admin API handlers.
type Handler struct {
	deps   Deps
	mu     sync.Mutex
	result *wp.InstallResult
}

// New creates a WordPress Handler.
func New(deps Deps) *Handler { return &Handler{deps: deps} }

// Package-level install state (shared across requests).
var (
	// InstallMu is exported for admin adapter test compat.
	InstallMu sync.Mutex
	// InstallResult is exported for admin adapter test compat.
	InstallResult *wp.InstallResult
)

func jsonResponse(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// ── Handlers ──

func (h *Handler) Install(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req wp.InstallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Domain == "" {
		jsonError(w, "domain is required", http.StatusBadRequest)
		return
	}
	if !h.deps.RequireDomainAccess(w, r, req.Domain, "wordpress.install") {
		return
	}

	if req.WebRoot == "" {
		req.WebRoot = h.deps.DomainRoot(req.Domain)
		if req.WebRoot == "" {
			webRoot := h.deps.GlobalWebRoot()
			if webRoot != "" {
				req.WebRoot = filepath.Join(webRoot, req.Domain, "public_html")
			}
		}
	}

	if req.WebRoot != "" && wp.IsWordPress(req.WebRoot) {
		jsonError(w, "WordPress is already installed at "+req.WebRoot+". Use the Sites tab to manage it.", http.StatusConflict)
		return
	}

	h.mu.Lock()
	if h.result != nil && h.result.Status == "running" {
		h.mu.Unlock()
		jsonError(w, "WordPress install already in progress", http.StatusConflict)
		return
	}
	h.result = &wp.InstallResult{Status: "running", Domain: req.Domain}
	h.mu.Unlock()

	h.deps.LogInfo("starting WordPress install", "domain", req.Domain)

	go func() {
		result := wp.Install(req)
		h.mu.Lock()
		h.result = &result
		h.mu.Unlock()
	}()

	jsonResponse(w, map[string]string{"status": "started", "domain": req.Domain})
}

func (h *Handler) InstallStatus(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	result := h.result
	h.mu.Unlock()
	if result == nil {
		jsonResponse(w, map[string]string{"status": "idle"})
		return
	}
	jsonResponse(w, result)
}

func (h *Handler) Sites(w http.ResponseWriter, r *http.Request) {
	domains := h.deps.Domains()
	sites := wp.DetectSites(domains)
	if sites == nil {
		sites = []wp.SiteInfo{}
	}
	jsonResponse(w, map[string]any{
		"items":  sites,
		"total":  len(sites),
		"limit":  50,
		"offset": 0,
	})
}

func (h *Handler) SiteDetail(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root, ok := h.deps.AuthorizedDomainRoot(w, r, domain, "wordpress.detail")
	if !ok {
		return
	}
	if !wp.IsWordPress(root) {
		jsonError(w, "not a WordPress site", http.StatusBadRequest)
		return
	}
	sites := wp.DetectSites([]wp.DomainInfo{{Host: domain, WebRoot: root}})
	if len(sites) == 0 {
		jsonError(w, "WordPress not detected", http.StatusNotFound)
		return
	}
	wp.EnrichSite(&sites[0])
	jsonResponse(w, sites[0])
}

func (h *Handler) UpdateCore(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root, ok := h.deps.AuthorizedDomainRoot(w, r, domain, "wordpress.update_core")
	if !ok {
		return
	}
	if !wp.IsWordPress(root) {
		jsonError(w, "not a WordPress site", http.StatusBadRequest)
		return
	}
	out, err := wp.UpdateCore(root)
	if err != nil {
		jsonError(w, "update failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "updated", "output": out})
}

func (h *Handler) UpdatePlugins(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root, ok := h.deps.AuthorizedDomainRoot(w, r, domain, "wordpress.update_plugins")
	if !ok {
		return
	}
	out, err := wp.UpdateAllPlugins(root)
	if err != nil {
		jsonError(w, "update failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "updated", "output": out})
}

func (h *Handler) PluginAction(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	action := r.PathValue("action")
	plugin := r.PathValue("plugin")
	root, ok := h.deps.AuthorizedDomainRoot(w, r, domain, "wordpress.plugin")
	if !ok {
		return
	}
	var out string
	var err error
	switch action {
	case "activate":
		out, err = wp.ActivatePlugin(root, plugin)
	case "deactivate":
		out, err = wp.DeactivatePlugin(root, plugin)
	case "delete":
		out, err = wp.DeletePlugin(root, plugin)
	case "update":
		out, err = wp.UpdatePlugin(root, plugin)
	default:
		jsonError(w, "invalid action: "+action, http.StatusBadRequest)
		return
	}
	if err != nil {
		jsonError(w, action+" failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": action + "d", "output": out})
}

func (h *Handler) FixPermissions(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root, ok := h.deps.AuthorizedDomainRoot(w, r, domain, "wordpress.fix_permissions")
	if !ok {
		return
	}
	out, err := wp.FixPermissions(root)
	if err != nil {
		jsonError(w, "fix failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "fixed", "output": out})
}

func (h *Handler) Reinstall(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root, ok := h.deps.AuthorizedDomainRoot(w, r, domain, "wordpress.reinstall")
	if !ok {
		return
	}
	if !wp.IsWordPress(root) {
		jsonError(w, "not a WordPress site", http.StatusBadRequest)
		return
	}
	out, err := wp.ReinstallWordPress(root)
	if err != nil {
		jsonError(w, "reinstall failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "reinstalled", "output": out})
}

func (h *Handler) ToggleDebug(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root, ok := h.deps.AuthorizedDomainRoot(w, r, domain, "wordpress.debug")
	if !ok {
		return
	}
	if !wp.IsWordPress(root) {
		jsonError(w, "not a WordPress site", http.StatusBadRequest)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Enable bool `json:"enable"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := wp.SetDebugMode(root, req.Enable); err != nil {
		jsonError(w, "toggle debug: "+err.Error(), http.StatusInternalServerError)
		return
	}
	h.deps.RecordAudit(r, "wordpress.debug", fmt.Sprintf("domain: %s, enable: %v", domain, req.Enable), true)
	jsonResponse(w, map[string]any{"status": "ok", "debug": req.Enable})
}

func (h *Handler) ErrorLog(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root, ok := h.deps.AuthorizedDomainRoot(w, r, domain, "wordpress.error_log")
	if !ok {
		return
	}
	logPath := filepath.Join(root, "wp-content", "debug.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			jsonResponse(w, map[string]any{"log": "", "message": "No debug.log file — enable WP_DEBUG first, then reproduce the error"})
			return
		}
		jsonError(w, "read error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	content := string(data)
	if len(content) > 100*1024 {
		content = content[len(content)-100*1024:]
	}
	jsonResponse(w, map[string]any{"log": content, "size": len(data)})
}

func (h *Handler) Users(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root, ok := h.deps.AuthorizedDomainRoot(w, r, domain, "wordpress.users")
	if !ok {
		return
	}
	users, err := wp.ListUsers(root)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, users)
}

func (h *Handler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root, ok := h.deps.AuthorizedDomainRoot(w, r, domain, "wordpress.change_password")
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Username == "" || req.Password == "" {
		jsonError(w, "username and password required", http.StatusBadRequest)
		return
	}
	if len(req.Password) < 8 {
		jsonError(w, "password must be at least 8 characters", http.StatusBadRequest)
		return
	}
	if err := wp.ChangeUserPassword(root, req.Username, req.Password); err != nil {
		jsonError(w, "password change failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	h.deps.RecordAudit(r, "wordpress.change_password", domain+":"+req.Username, true)
	jsonResponse(w, map[string]string{"status": "ok"})
}

func (h *Handler) SecurityStatus(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root, ok := h.deps.AuthorizedDomainRoot(w, r, domain, "wordpress.security")
	if !ok {
		return
	}
	status := wp.GetSecurityStatus(root)
	jsonResponse(w, status)
}

func (h *Handler) Harden(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root, ok := h.deps.AuthorizedDomainRoot(w, r, domain, "wordpress.harden")
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var opts wp.HardenOptions
	if err := json.NewDecoder(r.Body).Decode(&opts); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	output, err := wp.Harden(root, opts)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.deps.RecordAudit(r, "wordpress.harden", domain, true)
	jsonResponse(w, map[string]string{"status": "ok", "output": output})
}

func (h *Handler) OptimizeDB(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root, ok := h.deps.AuthorizedDomainRoot(w, r, domain, "wordpress.optimize_db")
	if !ok {
		return
	}
	result, err := wp.OptimizeDatabase(root)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.deps.RecordAudit(r, "wordpress.optimize_db", domain, true)
	jsonResponse(w, result)
}

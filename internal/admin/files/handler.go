// Package files provides admin API handlers for the file manager
// (list, read, write, delete, mkdir, upload, disk usage) and cron job
// management (list, add, delete).
package files

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/uwaserver/uwas/internal/apps"
	"github.com/uwaserver/uwas/internal/auth"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/cronjob"
	"github.com/uwaserver/uwas/internal/domainroot"
	"github.com/uwaserver/uwas/internal/filemanager"
)

// Deps is the interface the sub-package needs from the admin Server.
type Deps interface {
	RequireAdmin(w http.ResponseWriter, r *http.Request) bool
	RequirePin(w http.ResponseWriter, r *http.Request) bool
	RequireDomainAccess(w http.ResponseWriter, r *http.Request, domain, action string) bool
	CanAccessDomain(r *http.Request, domain string) bool
	RecordAudit(r *http.Request, action, detail string, success bool)
	ParsePagination(r *http.Request) (limit, offset int)
	LogInfo(msg string, args ...any)
	// Config access
	Domains() []config.Domain
	WebRoot() string
	// Apps manager (may be nil)
	AppsManager() *apps.Manager
	// Auth manager presence (nil = single-user, everyone is admin)
	AuthEnabled() bool
}

// Handler holds file manager and cron admin API handlers.
type Handler struct {
	deps Deps
}

// New creates a files Handler.
func New(deps Deps) *Handler {
	return &Handler{deps: deps}
}

func jsonResponse(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// PaginatedResponse wraps a list response with pagination metadata.
type PaginatedResponse[T any] struct {
	Items  []T `json:"items"`
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

func paginate[T any](items []T, limit, offset int) ([]T, int) {
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

// ── Types ──

// FileWorkspace describes a browsable filesystem root in the file manager.
type FileWorkspace struct {
	ID      string   `json:"id"`
	Label   string   `json:"label"`
	Kind    string   `json:"kind"`
	Root    string   `json:"root,omitempty"`
	Host    string   `json:"host,omitempty"`
	AppName string   `json:"app_name,omitempty"`
	Runtime string   `json:"runtime,omitempty"`
	Domains []string `json:"domains,omitempty"`
}

// ── Domain root resolution ──

func appFileTargetName(target string) (string, bool) {
	target = strings.TrimSpace(target)
	for _, prefix := range []string{"app:", "apps://"} {
		if strings.HasPrefix(strings.ToLower(target), prefix) {
			name := strings.TrimSpace(target[len(prefix):])
			if idx := strings.IndexAny(name, "/?#"); idx >= 0 {
				name = name[:idx]
			}
			return name, name != ""
		}
	}
	return "", false
}

func (h *Handler) domainRootForFiles(domain string) (string, error) {
	if appName, ok := appFileTargetName(domain); ok {
		return h.appRootForFiles(appName)
	}

	domains := h.deps.Domains()
	var found *config.Domain
	for _, d := range domains {
		if d.Host == domain {
			dd := d
			found = &dd
			break
		}
	}
	webRoot := h.deps.WebRoot()

	if found != nil {
		var store *apps.Store
		var instances []apps.Instance
		if mgr := h.deps.AppsManager(); mgr != nil {
			store = mgr.Store()
			instances = mgr.Instances()
		}
		return domainroot.ForDomainWithApps(*found, store, instances)
	}
	return domainroot.Fallback(webRoot, domain), nil
}

func (h *Handler) appRootForFiles(name string) (string, error) {
	mgr := h.deps.AppsManager()
	if mgr == nil {
		return "", fmt.Errorf("app %q root unavailable: apps manager is not initialized", name)
	}
	app, err := mgr.Store().Get(name)
	if err != nil {
		return "", fmt.Errorf("app %q root unavailable: %w", name, err)
	}
	if app == nil {
		return "", fmt.Errorf("app %q root unavailable: app not found", name)
	}
	if strings.TrimSpace(app.WorkDir) == "" {
		return "", fmt.Errorf("app %q root unavailable: work_dir is empty", name)
	}
	return app.WorkDir, nil
}

func (h *Handler) authorizedDomainRoot(w http.ResponseWriter, r *http.Request, domain, action string) (string, bool) {
	if _, isApp := appFileTargetName(domain); isApp {
		if h.deps.AuthEnabled() && !h.deps.RequireAdmin(w, r) {
			if action != "" {
				h.deps.RecordAudit(r, action, "app: "+domain+" (forbidden)", false)
			}
			return "", false
		}
	} else if !h.deps.RequireDomainAccess(w, r, domain, action) {
		return "", false
	}
	root, err := h.domainRootForFiles(domain)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return "", false
	}
	if root == "" {
		jsonError(w, "domain not found", http.StatusNotFound)
		return "", false
	}
	return root, true
}

// ── Handlers ──

func (h *Handler) Workspaces(w http.ResponseWriter, r *http.Request) {
	domains := h.deps.Domains()
	webRoot := h.deps.WebRoot()

	var instances []apps.Instance
	var storedApps []*apps.App
	if mgr := h.deps.AppsManager(); mgr != nil {
		instances = mgr.Instances()
		if loaded, _, err := mgr.Store().Load(); err == nil {
			storedApps = loaded
		}
	}

	appLinks := make(map[string][]string)
	items := make([]FileWorkspace, 0, len(domains)+len(storedApps)+len(instances))
	for _, d := range domains {
		if !h.deps.CanAccessDomain(r, d.Host) {
			continue
		}
		if appName, ok := domainroot.AppName(d); ok {
			appLinks[appName] = append(appLinks[appName], d.Host)
			continue
		}
		if appName, ok := domainroot.LocalAppName(d, instances); ok {
			appLinks[appName] = append(appLinks[appName], d.Host)
			continue
		}
		if config.DomainType(d.Type) == config.DomainTypeRedirect {
			continue
		}
		root := strings.TrimSpace(d.Root)
		if root == "" {
			root = domainroot.Fallback(webRoot, d.Host)
		}
		if root == "" {
			continue
		}
		items = append(items, FileWorkspace{
			ID: d.Host, Label: d.Host, Kind: "domain", Root: root, Host: d.Host,
		})
	}

	canSeeApps := !h.deps.AuthEnabled()
	if !canSeeApps {
		if user, ok := auth.UserFromContext(r.Context()); ok && user.Role == auth.RoleAdmin {
			canSeeApps = true
		}
	}
	if canSeeApps {
		appWorkspaces := make(map[string]FileWorkspace, len(storedApps)+len(instances))
		for _, app := range storedApps {
			if app == nil || strings.TrimSpace(app.WorkDir) == "" {
				continue
			}
			appWorkspaces[app.Name] = FileWorkspace{
				ID: "app:" + app.Name, Label: app.Name, Kind: "application",
				Root: app.WorkDir, AppName: app.Name, Runtime: string(app.Runtime),
			}
		}
		for _, inst := range instances {
			if strings.TrimSpace(inst.WorkDir) == "" {
				continue
			}
			appWorkspaces[inst.Name] = FileWorkspace{
				ID: "app:" + inst.Name, Label: inst.Name, Kind: "application",
				Root: inst.WorkDir, AppName: inst.Name, Runtime: string(inst.Runtime),
			}
		}
		for name, item := range appWorkspaces {
			ds := append([]string(nil), appLinks[name]...)
			sort.Strings(ds)
			item.Domains = ds
			items = append(items, item)
		}
	}

	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Kind != items[j].Kind {
			return items[i].Kind == "domain"
		}
		return strings.ToLower(items[i].Label) < strings.ToLower(items[j].Label)
	})
	limit, offset := h.deps.ParsePagination(r)
	paged, total := paginate(items, limit, offset)
	jsonResponse(w, PaginatedResponse[FileWorkspace]{
		Items: paged, Total: total, Limit: limit, Offset: offset,
	})
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root, ok := h.authorizedDomainRoot(w, r, domain, "file.list")
	if !ok {
		return
	}
	os.MkdirAll(root, 0755)
	path := r.URL.Query().Get("path")
	if path == "" {
		path = "."
	}
	entries, err := filemanager.List(root, path)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if entries == nil {
		entries = []filemanager.Entry{}
	}
	limit, offset := h.deps.ParsePagination(r)
	items, total := paginate(entries, limit, offset)
	jsonResponse(w, PaginatedResponse[filemanager.Entry]{
		Items: items, Total: total, Limit: limit, Offset: offset,
	})
}

func (h *Handler) Read(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root, ok := h.authorizedDomainRoot(w, r, domain, "file.read")
	if !ok {
		return
	}
	path := r.URL.Query().Get("path")
	data, err := filemanager.ReadFile(root, path)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	lowerPath := strings.ToLower(path)
	if strings.HasSuffix(lowerPath, ".png") ||
		strings.HasSuffix(lowerPath, ".jpg") ||
		strings.HasSuffix(lowerPath, ".jpeg") ||
		strings.HasSuffix(lowerPath, ".gif") ||
		strings.HasSuffix(lowerPath, ".webp") ||
		strings.HasSuffix(lowerPath, ".svg") ||
		strings.HasSuffix(lowerPath, ".ico") {

		contentType := "application/octet-stream"
		switch {
		case strings.HasSuffix(lowerPath, ".png"):
			contentType = "image/png"
		case strings.HasSuffix(lowerPath, ".jpg") || strings.HasSuffix(lowerPath, ".jpeg"):
			contentType = "image/jpeg"
		case strings.HasSuffix(lowerPath, ".gif"):
			contentType = "image/gif"
		case strings.HasSuffix(lowerPath, ".webp"):
			contentType = "image/webp"
		case strings.HasSuffix(lowerPath, ".svg"):
			contentType = "image/svg+xml"
		case strings.HasSuffix(lowerPath, ".ico"):
			contentType = "image/x-icon"
		}

		w.Header().Set("Content-Type", contentType)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if strings.HasSuffix(lowerPath, ".svg") {
			w.Header().Set("Content-Disposition", "attachment")
			w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
		}
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
		w.Write(data)
		return
	}

	jsonResponse(w, map[string]string{"content": string(data), "path": path})
}

func (h *Handler) Write(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root, ok := h.authorizedDomainRoot(w, r, domain, "file.write")
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := filemanager.WriteFile(root, req.Path, []byte(req.Content)); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	jsonResponse(w, map[string]string{"status": "written", "path": req.Path})
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root, ok := h.authorizedDomainRoot(w, r, domain, "file.delete")
	if !ok {
		return
	}
	path := r.URL.Query().Get("path")
	if err := filemanager.Delete(root, path); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	jsonResponse(w, map[string]string{"status": "deleted", "path": path})
}

func (h *Handler) Mkdir(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root, ok := h.authorizedDomainRoot(w, r, domain, "file.mkdir")
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := filemanager.CreateDir(root, req.Path); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	jsonResponse(w, map[string]string{"status": "created", "path": req.Path})
}

func (h *Handler) Upload(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root, ok := h.authorizedDomainRoot(w, r, domain, "file.upload")
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 100<<20)
	if err := r.ParseMultipartForm(100 << 20); err != nil {
		jsonError(w, "parse form: "+err.Error(), http.StatusBadRequest)
		return
	}
	dir := r.FormValue("path")
	if dir == "" {
		dir = "."
	}

	const maxFileSize = 50 << 20
	var uploaded []string
	for _, fHeaders := range r.MultipartForm.File {
		for _, fh := range fHeaders {
			if fh.Size > maxFileSize {
				jsonError(w, fmt.Sprintf("file %q exceeds maximum size of %d MB", fh.Filename, maxFileSize>>20), http.StatusBadRequest)
				return
			}
			src, err := fh.Open()
			if err != nil {
				continue
			}
			relPath := filepath.Join(dir, filepath.Base(fh.Filename))
			_, err = filemanager.SaveUpload(root, relPath, src)
			src.Close()
			if err == nil {
				uploaded = append(uploaded, relPath)
			}
		}
	}
	jsonResponse(w, map[string]any{"status": "uploaded", "files": uploaded})
}

func (h *Handler) DiskUsage(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root, ok := h.authorizedDomainRoot(w, r, domain, "file.disk_usage")
	if !ok {
		return
	}
	bytes, err := filemanager.DiskUsage(root)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]any{
		"domain": domain, "bytes": bytes, "human": formatBytes(bytes), "root": root,
	})
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// ── Cron handlers ──

func (h *Handler) CronList(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	jobs, err := cronjob.List()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if jobs == nil {
		jobs = []cronjob.Job{}
	}
	limit, offset := h.deps.ParsePagination(r)
	items, total := paginate(jobs, limit, offset)
	jsonResponse(w, PaginatedResponse[cronjob.Job]{
		Items: items, Total: total, Limit: limit, Offset: offset,
	})
}

func (h *Handler) CronAdd(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var job cronjob.Job
	if err := json.NewDecoder(r.Body).Decode(&job); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if job.Schedule == "" || job.Command == "" {
		jsonError(w, "schedule and command are required", http.StatusBadRequest)
		return
	}
	if err := cronjob.Add(job); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.deps.LogInfo("cron job added", "schedule", job.Schedule, "command", job.Command)
	jsonResponse(w, map[string]string{"status": "added"})
}

func (h *Handler) CronDelete(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) || !h.deps.RequirePin(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Schedule string `json:"schedule"`
		Command  string `json:"command"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := cronjob.Remove(req.Schedule, req.Command); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "removed"})
}

// ── Exported helpers for admin package compat ──

// AppFileTargetName is exported for the admin adapter.
func AppFileTargetName(target string) (string, bool) { return appFileTargetName(target) }

// AppSFTPIdentity generates an SFTP identity from an app name.
func AppSFTPIdentity(appName string) string {
	appName = strings.TrimSpace(strings.ToLower(appName))
	appName = strings.ReplaceAll(appName, "_", "--u--")
	return "app-" + appName + ".uwas.local"
}

// AppSFTPTargetName resolves an SFTP target to an app name.
func AppSFTPTargetName(target string) (string, bool) {
	target = strings.TrimSpace(target)
	if appName, ok := appFileTargetName(target); ok {
		return appName, true
	}
	lower := strings.ToLower(target)
	if strings.HasPrefix(lower, "app-") && strings.HasSuffix(lower, ".uwas.local") {
		name := strings.TrimSuffix(strings.TrimPrefix(lower, "app-"), ".uwas.local")
		name = strings.ReplaceAll(name, "--u--", "_")
		if name != "" {
			return name, true
		}
	}
	return "", false
}

// FormatBytes is exported for the admin adapter.
func FormatBytes(b int64) string { return formatBytes(b) }

// ResolveDomainRoot resolves a domain or app: prefix to a filesystem root.
// Exported for the admin adapter's domainRootForFiles method.
func ResolveDomainRoot(deps Deps, domain string) (string, error) {
	h := &Handler{deps: deps}
	return h.domainRootForFiles(domain)
}

// ResolveSiteUserRoot resolves an SFTP/terminal domain or app identity to a root.
// Exported for the admin adapter's siteUserRoot method.
func ResolveSiteUserRoot(deps Deps, domain string) (string, error) {
	if appName, ok := AppSFTPTargetName(domain); ok {
		h := &Handler{deps: deps}
		return h.appRootForFiles(appName)
	}
	h := &Handler{deps: deps}
	root, err := h.domainRootForFiles(domain)
	if err != nil {
		return "", err
	}
	if root != "" {
		return root, nil
	}
	return domainrootFallback("/var/www", domain), nil
}

func domainrootFallback(webRoot, host string) string {
	// Inline implementation to avoid importing domainroot for one function.
	// Matches domainroot.Fallback logic.
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	return webRoot + "/" + host
}

// AuthorizedDomainRoot checks permissions and returns the domain root.
// Exported for the admin adapter.
func (h *Handler) AuthorizedDomainRoot(w http.ResponseWriter, r *http.Request, domain, action string) (string, bool) {
	return h.authorizedDomainRoot(w, r, domain, action)
}

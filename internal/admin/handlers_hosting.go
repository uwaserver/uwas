package admin

import (
	crand "crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/uwaserver/uwas/internal/build"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/cronjob"
	"github.com/uwaserver/uwas/internal/database"
	"github.com/uwaserver/uwas/internal/dnschecker"
	"github.com/uwaserver/uwas/internal/dnsmanager"
	"github.com/uwaserver/uwas/internal/doctor"
	"github.com/uwaserver/uwas/internal/filemanager"
	"github.com/uwaserver/uwas/internal/firewall"
	"github.com/uwaserver/uwas/internal/middleware"
	"github.com/uwaserver/uwas/internal/migrate"
	"github.com/uwaserver/uwas/internal/notify"
	"github.com/uwaserver/uwas/internal/selfupdate"
	"github.com/uwaserver/uwas/internal/serverip"
	"github.com/uwaserver/uwas/internal/services"
	"github.com/uwaserver/uwas/internal/siteuser"
	"github.com/uwaserver/uwas/internal/wordpress"
)

// ============ WordPress ============

var (
	wpInstallMu     sync.Mutex
	wpInstallResult *wordpress.InstallResult
)

func (s *Server) handleWPInstall(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req wordpress.InstallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Domain == "" {
		jsonError(w, "domain is required", http.StatusBadRequest)
		return
	}
	if !s.requireDomainAccess(w, r, req.Domain, "wordpress.install") {
		return
	}

	// Resolve web root from config
	if req.WebRoot == "" {
		s.configMu.RLock()
		for _, d := range s.config.Domains {
			if d.Host == req.Domain {
				req.WebRoot = d.Root
				break
			}
		}
		if req.WebRoot == "" && s.config.Global.WebRoot != "" {
			req.WebRoot = filepath.Join(s.config.Global.WebRoot, req.Domain, "public_html")
		}
		s.configMu.RUnlock()
	}

	// Prevent duplicate install on existing WordPress site
	if req.WebRoot != "" && wordpress.IsWordPress(req.WebRoot) {
		jsonError(w, "WordPress is already installed at "+req.WebRoot+". Use the Sites tab to manage it.", http.StatusConflict)
		return
	}

	wpInstallMu.Lock()
	if wpInstallResult != nil && wpInstallResult.Status == "running" {
		wpInstallMu.Unlock()
		jsonError(w, "WordPress install already in progress", http.StatusConflict)
		return
	}
	wpInstallResult = &wordpress.InstallResult{Status: "running", Domain: req.Domain}
	wpInstallMu.Unlock()

	s.logger.Info("starting WordPress install", "domain", req.Domain)

	go func() {
		result := wordpress.Install(req)
		wpInstallMu.Lock()
		wpInstallResult = &result
		wpInstallMu.Unlock()
		s.logger.Info("WordPress install finished", "domain", req.Domain, "status", result.Status)
	}()

	jsonResponse(w, map[string]string{"status": "started", "domain": req.Domain})
}

func (s *Server) handleWPInstallStatus(w http.ResponseWriter, r *http.Request) {
	wpInstallMu.Lock()
	result := wpInstallResult
	wpInstallMu.Unlock()
	if result == nil {
		jsonResponse(w, map[string]string{"status": "idle"})
		return
	}
	jsonResponse(w, result)
}

// handleWPSites detects all WordPress installations across configured domains.
func (s *Server) handleWPSites(w http.ResponseWriter, r *http.Request) {
	s.configMu.RLock()
	var domains []wordpress.DomainInfo
	for _, d := range s.config.Domains {
		if !s.canAccessDomain(r, d.Host) {
			continue
		}
		domains = append(domains, wordpress.DomainInfo{Host: d.Host, WebRoot: d.Root})
	}
	s.configMu.RUnlock()

	sites := wordpress.DetectSites(domains)
	if sites == nil {
		sites = []wordpress.SiteInfo{}
	}
	jsonResponse(w, sites)
}

// handleWPSiteDetail returns enriched info (plugins, themes via wp-cli) for a single site.
func (s *Server) handleWPSiteDetail(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root, ok := s.authorizedDomainRoot(w, r, domain, "wordpress.detail")
	if !ok {
		return
	}
	if !wordpress.IsWordPress(root) {
		jsonError(w, "not a WordPress site", http.StatusBadRequest)
		return
	}
	// Quick detect first
	sites := wordpress.DetectSites([]wordpress.DomainInfo{{Host: domain, WebRoot: root}})
	if len(sites) == 0 {
		jsonError(w, "WordPress not detected", http.StatusNotFound)
		return
	}
	// Enrich with wp-cli (slow but on-demand)
	wordpress.EnrichSite(&sites[0])
	jsonResponse(w, sites[0])
}

// handleWPUpdateCore triggers WP core update via WP-CLI.
func (s *Server) handleWPUpdateCore(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root, ok := s.authorizedDomainRoot(w, r, domain, "wordpress.update_core")
	if !ok {
		return
	}
	if !wordpress.IsWordPress(root) {
		jsonError(w, "not a WordPress site", http.StatusBadRequest)
		return
	}
	out, err := wordpress.UpdateCore(root)
	if err != nil {
		jsonError(w, "update failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "updated", "output": out})
}

// handleWPUpdatePlugins updates all plugins via WP-CLI.
func (s *Server) handleWPUpdatePlugins(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root, ok := s.authorizedDomainRoot(w, r, domain, "wordpress.update_plugins")
	if !ok {
		return
	}
	out, err := wordpress.UpdateAllPlugins(root)
	if err != nil {
		jsonError(w, "update failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "updated", "output": out})
}

// handleWPPluginAction activates, deactivates, or deletes a plugin.
func (s *Server) handleWPPluginAction(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	action := r.PathValue("action")
	plugin := r.PathValue("plugin")
	root, ok := s.authorizedDomainRoot(w, r, domain, "wordpress.plugin")
	if !ok {
		return
	}
	var out string
	var err error
	switch action {
	case "activate":
		out, err = wordpress.ActivatePlugin(root, plugin)
	case "deactivate":
		out, err = wordpress.DeactivatePlugin(root, plugin)
	case "delete":
		out, err = wordpress.DeletePlugin(root, plugin)
	case "update":
		out, err = wordpress.UpdatePlugin(root, plugin)
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

// handleWPFixPermissions fixes file permissions for a WordPress site.
func (s *Server) handleWPFixPermissions(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root, ok := s.authorizedDomainRoot(w, r, domain, "wordpress.fix_permissions")
	if !ok {
		return
	}
	out, err := wordpress.FixPermissions(root)
	if err != nil {
		jsonError(w, "fix failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "fixed", "output": out})
}

// handleWPReinstall re-downloads WordPress core files without touching wp-content or DB.
func (s *Server) handleWPReinstall(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root, ok := s.authorizedDomainRoot(w, r, domain, "wordpress.reinstall")
	if !ok {
		return
	}
	if !wordpress.IsWordPress(root) {
		jsonError(w, "not a WordPress site", http.StatusBadRequest)
		return
	}
	out, err := wordpress.ReinstallWordPress(root)
	if err != nil {
		jsonError(w, "reinstall failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "reinstalled", "output": out})
}

// handleWPToggleDebug enables or disables WP_DEBUG + display_errors + error logging.
func (s *Server) handleWPToggleDebug(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root, ok := s.authorizedDomainRoot(w, r, domain, "wordpress.debug")
	if !ok {
		return
	}
	if !wordpress.IsWordPress(root) {
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

	if err := wordpress.SetDebugMode(root, req.Enable); err != nil {
		jsonError(w, "toggle debug: "+err.Error(), http.StatusInternalServerError)
		return
	}

	s.RecordAudit("wordpress.debug", fmt.Sprintf("domain: %s, enable: %v", domain, req.Enable), requestIP(r), true)
	jsonResponse(w, map[string]any{"status": "ok", "debug": req.Enable})
}

// handleWPErrorLog reads the WordPress debug.log file.
func (s *Server) handleWPErrorLog(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root, ok := s.authorizedDomainRoot(w, r, domain, "wordpress.error_log")
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

	// Return last 100KB max
	content := string(data)
	if len(content) > 100*1024 {
		content = content[len(content)-100*1024:]
	}
	jsonResponse(w, map[string]any{"log": content, "size": len(data)})
}

func (s *Server) handleWPUsers(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root, ok := s.authorizedDomainRoot(w, r, domain, "wordpress.users")
	if !ok {
		return
	}
	users, err := wordpress.ListUsers(root)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, users)
}

func (s *Server) handleWPChangePassword(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root, ok := s.authorizedDomainRoot(w, r, domain, "wordpress.change_password")
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
	if err := wordpress.ChangeUserPassword(root, req.Username, req.Password); err != nil {
		jsonError(w, "password change failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.RecordAudit("wordpress.change_password", domain+":"+req.Username, requestIP(r), true)
	jsonResponse(w, map[string]string{"status": "ok"})
}

func (s *Server) handleWPSecurityStatus(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root, ok := s.authorizedDomainRoot(w, r, domain, "wordpress.security")
	if !ok {
		return
	}
	status := wordpress.GetSecurityStatus(root)
	jsonResponse(w, status)
}

func (s *Server) handleWPHarden(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root, ok := s.authorizedDomainRoot(w, r, domain, "wordpress.harden")
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var opts wordpress.HardenOptions
	if err := json.NewDecoder(r.Body).Decode(&opts); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	output, err := wordpress.Harden(root, opts)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.RecordAudit("wordpress.harden", domain, requestIP(r), true)
	jsonResponse(w, map[string]string{"status": "ok", "output": output})
}

func (s *Server) handleWPOptimizeDB(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root, ok := s.authorizedDomainRoot(w, r, domain, "wordpress.optimize_db")
	if !ok {
		return
	}
	result, err := wordpress.OptimizeDatabase(root)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.RecordAudit("wordpress.optimize_db", domain, requestIP(r), true)
	jsonResponse(w, result)
}

// ============ File Manager ============

func (s *Server) domainRoot(domain string) string {
	s.configMu.RLock()
	defer s.configMu.RUnlock()
	for _, d := range s.config.Domains {
		if d.Host == domain {
			return d.Root
		}
	}
	if s.config.Global.WebRoot != "" {
		return filepath.Join(s.config.Global.WebRoot, domain, "public_html")
	}
	return ""
}

func (s *Server) authorizedDomainRoot(w http.ResponseWriter, r *http.Request, domain, action string) (string, bool) {
	if !s.requireDomainAccess(w, r, domain, action) {
		return "", false
	}
	root := s.domainRoot(domain)
	if root == "" {
		jsonError(w, "domain not found", http.StatusNotFound)
		return "", false
	}
	return root, true
}

func (s *Server) handleFileList(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root, ok := s.authorizedDomainRoot(w, r, domain, "file.list")
	if !ok {
		return
	}
	// Auto-create root if it doesn't exist
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
	jsonResponse(w, entries)
}

func (s *Server) handleFileRead(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root, ok := s.authorizedDomainRoot(w, r, domain, "file.read")
	if !ok {
		return
	}
	path := r.URL.Query().Get("path")
	data, err := filemanager.ReadFile(root, path)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Check if file is an image - serve binary with proper content type
	lowerPath := strings.ToLower(path)
	if strings.HasSuffix(lowerPath, ".png") ||
		strings.HasSuffix(lowerPath, ".jpg") ||
		strings.HasSuffix(lowerPath, ".jpeg") ||
		strings.HasSuffix(lowerPath, ".gif") ||
		strings.HasSuffix(lowerPath, ".webp") ||
		strings.HasSuffix(lowerPath, ".svg") ||
		strings.HasSuffix(lowerPath, ".ico") {

		// Set appropriate content type
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
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
		w.Write(data)
		return
	}

	// For text files, return as JSON
	jsonResponse(w, map[string]string{"content": string(data), "path": path})
}

func (s *Server) handleFileWrite(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root, ok := s.authorizedDomainRoot(w, r, domain, "file.write")
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

func (s *Server) handleFileDelete(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root, ok := s.authorizedDomainRoot(w, r, domain, "file.delete")
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

func (s *Server) handleFileMkdir(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root, ok := s.authorizedDomainRoot(w, r, domain, "file.mkdir")
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

func (s *Server) handleFileUpload(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root, ok := s.authorizedDomainRoot(w, r, domain, "file.upload")
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 100<<20) // 100MB total request limit
	if err := r.ParseMultipartForm(100 << 20); err != nil {
		jsonError(w, "parse form: "+err.Error(), http.StatusBadRequest)
		return
	}
	dir := r.FormValue("path")
	if dir == "" {
		dir = "."
	}

	// Enforce per-file size limit (50MB per file)
	const maxFileSize = 50 << 20

	var uploaded []string
	for _, fHeaders := range r.MultipartForm.File {
		for _, fh := range fHeaders {
			// Reject files that exceed per-file limit
			if fh.Size > maxFileSize {
				jsonError(w, fmt.Sprintf("file %q exceeds maximum size of %d MB", fh.Filename, maxFileSize>>20), http.StatusBadRequest)
				return
			}
			src, err := fh.Open()
			if err != nil {
				continue
			}
			// Use filepath.Base to strip directory components from uploaded filename
			// (prevents path traversal via filenames like "../../etc/cron.d/x")
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

func (s *Server) handleDiskUsage(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root, ok := s.authorizedDomainRoot(w, r, domain, "file.disk_usage")
	if !ok {
		return
	}
	bytes, err := filemanager.DiskUsage(root)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]any{
		"domain": domain,
		"bytes":  bytes,
		"human":  formatBytes(bytes),
		"root":   root,
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

// ============ Cron Jobs ============

func (s *Server) handleCronList(w http.ResponseWriter, r *http.Request) {
	jobs, err := cronjob.List()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if jobs == nil {
		jobs = []cronjob.Job{}
	}
	jsonResponse(w, jobs)
}

func (s *Server) handleCronAdd(w http.ResponseWriter, r *http.Request) {
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
	s.logger.Info("cron job added", "schedule", job.Schedule, "command", job.Command)
	jsonResponse(w, map[string]string{"status": "added"})
}

func (s *Server) handleCronDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requirePin(w, r) {
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

// ============ Firewall ============

func (s *Server) handleFirewallStatus(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, firewall.GetStatus())
}

func (s *Server) handleFirewallAllow(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Port  string `json:"port"`
		Proto string `json:"proto"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Port == "" {
		jsonError(w, "port is required", http.StatusBadRequest)
		return
	}
	if err := firewall.AllowPort(req.Port, req.Proto); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.logger.Info("firewall allow", "port", req.Port, "proto", req.Proto)
	jsonResponse(w, map[string]string{"status": "allowed", "port": req.Port})
}

func (s *Server) handleFirewallDeny(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Port  string `json:"port"`
		Proto string `json:"proto"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Port == "" {
		jsonError(w, "port is required", http.StatusBadRequest)
		return
	}
	if err := firewall.DenyPort(req.Port, req.Proto); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.logger.Info("firewall deny", "port", req.Port, "proto", req.Proto)
	jsonResponse(w, map[string]string{"status": "denied", "port": req.Port})
}

func (s *Server) handleFirewallDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requirePin(w, r) {
		return
	}
	numStr := r.PathValue("number")
	var num int
	fmt.Sscanf(numStr, "%d", &num)
	if num <= 0 {
		jsonError(w, "invalid rule number", http.StatusBadRequest)
		return
	}
	if err := firewall.DeleteRule(num); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "deleted"})
}

func (s *Server) handleFirewallEnable(w http.ResponseWriter, r *http.Request) {
	if err := firewall.Enable(); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "enabled"})
}

func (s *Server) handleFirewallDisable(w http.ResponseWriter, r *http.Request) {
	if !s.requirePin(w, r) {
		return
	}
	if err := firewall.Disable(); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "disabled"})
}

// ============ SSH Keys ============

func (s *Server) handleSSHKeyList(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	webRoot := "/var/www"
	s.configMu.RLock()
	if s.config.Global.WebRoot != "" {
		webRoot = s.config.Global.WebRoot
	}
	s.configMu.RUnlock()

	keys := siteuser.ListSSHKeys(webRoot, domain)
	if keys == nil {
		keys = []string{}
	}
	jsonResponse(w, keys)
}

func (s *Server) handleSSHKeyAdd(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		PublicKey string `json:"public_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if !strings.HasPrefix(req.PublicKey, "ssh-") {
		jsonError(w, "invalid SSH public key (must start with ssh-)", http.StatusBadRequest)
		return
	}

	webRoot := "/var/www"
	s.configMu.RLock()
	if s.config.Global.WebRoot != "" {
		webRoot = s.config.Global.WebRoot
	}
	s.configMu.RUnlock()

	if err := siteuser.AddSSHKey(webRoot, domain, req.PublicKey); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.logger.Info("SSH key added", "domain", domain)
	jsonResponse(w, map[string]string{"status": "added"})
}

func (s *Server) handleSSHKeyDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requirePin(w, r) {
		return
	}
	domain := r.PathValue("domain")
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Fingerprint string `json:"fingerprint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	webRoot := "/var/www"
	s.configMu.RLock()
	if s.config.Global.WebRoot != "" {
		webRoot = s.config.Global.WebRoot
	}
	s.configMu.RUnlock()

	if err := siteuser.RemoveSSHKey(webRoot, domain, req.Fingerprint); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "removed"})
}

// ============ Self-Update ============

func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	info, err := selfupdate.CheckUpdate(build.Version)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, info)
}

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.requirePin(w, r) {
		return
	}
	info, err := selfupdate.CheckUpdate(build.Version)
	if err != nil {
		jsonError(w, "check failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if !info.UpdateAvail {
		jsonResponse(w, map[string]string{"status": "up-to-date", "version": info.CurrentVersion})
		return
	}
	if err := selfupdate.Update(info.DownloadURL); err != nil {
		jsonError(w, "update failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.logger.Info("UWAS updated", "from", info.CurrentVersion, "to", info.LatestVersion)
	jsonResponse(w, map[string]string{
		"status":  "updated",
		"from":    info.CurrentVersion,
		"to":      info.LatestVersion,
		"message": "Restarting UWAS...",
	})

	// Auto-restart after response is sent
	go func() {
		time.Sleep(500 * time.Millisecond) // let response flush
		selfupdate.RestartSelf()           // tries systemctl restart uwas, falls back to re-exec
	}()
}

// ============ Database ============

func (s *Server) handleDBStatus(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, database.GetStatus())
}

func (s *Server) handleDBList(w http.ResponseWriter, r *http.Request) {
	// Check if MySQL is available before querying
	st := database.GetStatus()
	if !st.Installed || !st.Running {
		jsonResponse(w, []database.DBInfo{})
		return
	}
	dbs, err := database.ListDatabases()
	if err != nil {
		// Don't error — just return empty list with a log
		s.logger.Debug("database list failed", "error", err)
		jsonResponse(w, []database.DBInfo{})
		return
	}
	if dbs == nil {
		dbs = []database.DBInfo{}
	}
	jsonResponse(w, dbs)
}

func (s *Server) handleDBCreate(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Name     string `json:"name"`
		User     string `json:"user"`
		Password string `json:"password"`
		Host     string `json:"host"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		jsonError(w, "name is required", http.StatusBadRequest)
		return
	}
	result, err := database.CreateDatabase(req.Name, req.User, req.Password, req.Host)
	if err != nil {
		jsonError(w, "create database: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.logger.Info("database created", "name", result.Name, "user", result.User)
	jsonResponse(w, result)
}

func (s *Server) handleDBDrop(w http.ResponseWriter, r *http.Request) {
	if !s.requirePin(w, r) {
		return
	}
	name := r.PathValue("name")
	if err := database.DropDatabase(name, name, "localhost"); err != nil {
		jsonError(w, "drop database: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.logger.Info("database dropped", "name", name)
	jsonResponse(w, map[string]string{"status": "dropped", "name": name})
}

func (s *Server) handleDBInstall(w http.ResponseWriter, r *http.Request) {
	st := database.GetStatus()
	if st.Installed {
		jsonResponse(w, map[string]string{"status": "already_installed", "version": st.Version})
		return
	}

	// Check if any install task is already running
	if active := s.taskMgr.Active(); active != nil {
		jsonError(w, fmt.Sprintf("another installation in progress: %s (%s)", active.Name, active.ID), http.StatusConflict)
		return
	}

	task := s.taskMgr.Submit("database", "MariaDB", "install", func(appendOutput func(string)) error {
		output, err := database.InstallMySQL()
		appendOutput(output)
		if err != nil {
			s.logger.Error("database install failed", "error", err)
			return err
		}
		s.logger.Info("database install complete")
		return nil
	})

	jsonResponse(w, map[string]string{"status": "installing", "task_id": task.ID})
}

func (s *Server) handleDBUsers(w http.ResponseWriter, r *http.Request) {
	users, err := database.ListUsers()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if users == nil {
		users = []database.DBUser{}
	}
	jsonResponse(w, users)
}

func (s *Server) handleDBChangePassword(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		User     string `json:"user"`
		Host     string `json:"host"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.User == "" || req.Password == "" {
		jsonError(w, "user and password required", http.StatusBadRequest)
		return
	}
	if err := database.ChangePassword(req.User, req.Host, req.Password); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.RecordAudit("database.password_change", "user: "+req.User, requestIP(r), true)
	jsonResponse(w, map[string]string{"status": "changed"})
}

func (s *Server) handleDBExport(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	data, err := database.ExportDatabase(name)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/sql")
	safeName := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '_'
	}, name)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.sql"`, safeName))
	w.Write(data)
}

func (s *Server) handleDBImport(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	r.Body = http.MaxBytesReader(w, r.Body, 256<<20) // 256MB max
	data, err := io.ReadAll(r.Body)
	if err != nil {
		jsonError(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := database.ImportDatabase(name, data); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.RecordAudit("database.import", "db: "+name, requestIP(r), true)
	jsonResponse(w, map[string]string{"status": "imported", "database": name})
}

func (s *Server) handleDBUninstall(w http.ResponseWriter, r *http.Request) {
	if !s.requirePin(w, r) {
		return
	}
	ip := requestIP(r)
	out, err := database.UninstallService()
	if err != nil {
		s.RecordAudit("database.uninstall", "error: "+err.Error(), ip, false)
		jsonError(w, err.Error()+"\n"+out, http.StatusInternalServerError)
		return
	}
	s.RecordAudit("database.uninstall", "success", ip, true)
	s.logger.Info("MySQL/MariaDB uninstalled")
	jsonResponse(w, map[string]string{"status": "uninstalled", "output": out})
}

func (s *Server) handleDBRepair(w http.ResponseWriter, r *http.Request) {
	ip := requestIP(r)
	out, err := database.RepairService()
	if err != nil {
		s.RecordAudit("database.repair", "error: "+err.Error(), ip, false)
		jsonError(w, err.Error()+"\n"+out, http.StatusInternalServerError)
		return
	}
	s.RecordAudit("database.repair", "success", ip, true)
	s.logger.Info("MySQL/MariaDB repaired")
	jsonResponse(w, map[string]string{"status": "repaired", "output": out})
}

func (s *Server) handleDBForceUninstall(w http.ResponseWriter, r *http.Request) {
	if !s.requirePin(w, r) {
		return
	}
	ip := requestIP(r)
	out, err := database.ForceUninstall()
	if err != nil {
		s.RecordAudit("database.force_uninstall", "error: "+err.Error(), ip, false)
		jsonError(w, err.Error()+"\n"+out, http.StatusInternalServerError)
		return
	}
	s.RecordAudit("database.force_uninstall", "success", ip, true)
	s.logger.Info("MySQL/MariaDB force uninstalled")
	jsonResponse(w, map[string]string{"status": "force_uninstalled", "output": out})
}

func (s *Server) handleDBDiagnose(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, database.DiagnoseService())
}

// ============ Docker Database Containers ============

func (s *Server) handleDockerDBList(w http.ResponseWriter, r *http.Request) {
	if !database.DockerAvailable() {
		jsonResponse(w, map[string]any{"docker": false, "containers": []any{}})
		return
	}
	containers, err := database.ListDockerDBs()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if containers == nil {
		containers = []database.DockerDBContainer{}
	}
	jsonResponse(w, map[string]any{
		"docker":     true,
		"version":    database.DockerVersion(),
		"containers": containers,
	})
}

func (s *Server) handleDockerDBCreate(w http.ResponseWriter, r *http.Request) {
	if !database.DockerAvailable() {
		jsonError(w, "Docker is not installed or not running", http.StatusServiceUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Engine   string `json:"engine"`    // mariadb, mysql, postgresql
		Name     string `json:"name"`      // container suffix
		Port     int    `json:"port"`      // host port
		RootPass string `json:"root_pass"` // root/admin password
		DataDir  string `json:"data_dir"`  // optional persistent volume
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Name == "" || req.Engine == "" || req.Port == 0 || req.RootPass == "" {
		jsonError(w, "name, engine, port, and root_pass are required", http.StatusBadRequest)
		return
	}

	engine := database.DockerDBEngine(req.Engine)
	container, err := database.CreateDockerDB(engine, req.Name, req.Port, req.RootPass, req.DataDir)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.RecordAudit("docker_db.create", fmt.Sprintf("engine: %s, name: %s, port: %d", req.Engine, req.Name, req.Port), requestIP(r), true)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(container)
}

func (s *Server) handleDockerDBStart(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := database.StartDockerDB(name); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "started"})
}

func (s *Server) handleDockerDBStop(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := database.StopDockerDB(name); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "stopped"})
}

func (s *Server) handleDockerDBRemove(w http.ResponseWriter, r *http.Request) {
	if !s.requirePin(w, r) {
		return
	}
	name := r.PathValue("name")
	if err := database.RemoveDockerDB(name); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.RecordAudit("docker_db.remove", "name: "+name, requestIP(r), true)
	jsonResponse(w, map[string]string{"status": "removed"})
}

func (s *Server) handleDockerDBListDatabases(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	dbs, err := database.DockerDBListDatabases(name)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if dbs == nil {
		dbs = []database.DBInfo{}
	}
	jsonResponse(w, dbs)
}

func (s *Server) handleDockerDBCreateDatabase(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		DBName   string `json:"name"`
		User     string `json:"user"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.DBName == "" {
		jsonError(w, "database name required", http.StatusBadRequest)
		return
	}
	result, err := database.DockerDBCreateDatabase(name, req.DBName, req.User, req.Password)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.RecordAudit("docker_db.create_database", name+"/"+req.DBName, requestIP(r), true)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(result)
}

func (s *Server) handleDockerDBDropDatabase(w http.ResponseWriter, r *http.Request) {
	if !s.requirePin(w, r) {
		return
	}
	name := r.PathValue("name")
	db := r.PathValue("db")
	if err := database.DockerDBDropDatabase(name, db); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.RecordAudit("docker_db.drop_database", name+"/"+db, requestIP(r), true)
	jsonResponse(w, map[string]string{"status": "dropped"})
}

func (s *Server) handleDockerDBExport(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	db := r.PathValue("db")
	dump, err := database.DockerDBExport(name, db)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/sql")
	safeName := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '_'
	}, name+"_"+db)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.sql"`, safeName))
	w.Write([]byte(dump))
}

func (s *Server) handleDockerDBImport(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	db := r.PathValue("db")
	r.Body = http.MaxBytesReader(w, r.Body, 100<<20) // 100MB max
	data, err := io.ReadAll(r.Body)
	if err != nil {
		jsonError(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := database.DockerDBImport(name, db, string(data)); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.RecordAudit("docker_db.import", name+"/"+db, requestIP(r), true)
	jsonResponse(w, map[string]string{"status": "imported"})
}

// ============ DNS Checker ============

func (s *Server) handleDNSCheck(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	if domain == "" {
		jsonError(w, "domain required", http.StatusBadRequest)
		return
	}
	result := dnschecker.Check(domain)
	jsonResponse(w, result)
}

// ============ System Services ============

func (s *Server) handleServicesList(w http.ResponseWriter, r *http.Request) {
	svcs := services.ListServices()
	if svcs == nil {
		svcs = []services.Service{}
	}
	jsonResponse(w, svcs)
}

func (s *Server) handleServiceStart(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := services.StartService(name); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.logger.Info("service started", "name", name)
	jsonResponse(w, map[string]string{"status": "started", "name": name})
}

func (s *Server) handleServiceStop(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := services.StopService(name); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.logger.Info("service stopped", "name", name)
	jsonResponse(w, map[string]string{"status": "stopped", "name": name})
}

func (s *Server) handleServiceRestart(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := services.RestartService(name); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.logger.Info("service restarted", "name", name)
	jsonResponse(w, map[string]string{"status": "restarted", "name": name})
}

// ============ Database Service Control ============

func (s *Server) handleDBStart(w http.ResponseWriter, r *http.Request) {
	if err := database.StartService(); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.logger.Info("MySQL/MariaDB started")
	jsonResponse(w, map[string]string{"status": "started"})
}

func (s *Server) handleDBStop(w http.ResponseWriter, r *http.Request) {
	if err := database.StopService(); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.logger.Info("MySQL/MariaDB stopped")
	jsonResponse(w, map[string]string{"status": "stopped"})
}

func (s *Server) handleDBRestart(w http.ResponseWriter, r *http.Request) {
	if err := database.RestartService(); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.logger.Info("MySQL/MariaDB restarted")
	jsonResponse(w, map[string]string{"status": "restarted"})
}

// ============ Notifications ============

func (s *Server) handleNotifyTest(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var ch notify.Channel
	if err := json.NewDecoder(r.Body).Decode(&ch); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	ch.Enabled = true
	msg := notify.Message{
		Level:  "info",
		Title:  "UWAS Test Notification",
		Body:   "This is a test notification from your UWAS server.",
		Source: "uwas_test",
	}
	if err := notify.Send(ch, msg); err != nil {
		jsonError(w, "send failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "sent"})
}

// ============ DNS Records Management ============

func (s *Server) getDNSProvider() dnsmanager.Provider {
	s.configMu.RLock()
	provider := s.config.Global.ACME.DNSProvider
	creds := s.config.Global.ACME.DNSCredentials
	s.configMu.RUnlock()

	if creds == nil {
		return nil
	}

	token := creds["api_token"]
	if token == "" {
		token = creds["token"]
	}

	switch provider {
	case "cloudflare":
		if token == "" {
			return nil
		}
		return dnsmanager.NewCloudflare(token)
	case "hetzner":
		if token == "" {
			return nil
		}
		return dnsmanager.NewHetzner(token)
	case "digitalocean":
		if token == "" {
			return nil
		}
		return dnsmanager.NewDigitalOcean(token)
	case "route53":
		accessKey := creds["access_key"]
		secretKey := creds["secret_key"]
		region := creds["region"]
		if accessKey == "" || secretKey == "" {
			return nil
		}
		if region == "" {
			region = "us-east-1"
		}
		return dnsmanager.NewRoute53(accessKey, secretKey, region)
	default:
		return nil
	}
}

func (s *Server) handleDNSRecords(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	cf := s.getDNSProvider()
	if cf == nil {
		jsonError(w, "DNS provider not configured — set dns_provider and credentials in Settings → ACME", http.StatusNotImplemented)
		return
	}
	zone, err := cf.FindZoneByDomain(domain)
	if err != nil {
		jsonError(w, "zone not found: "+err.Error(), http.StatusNotFound)
		return
	}
	records, err := cf.ListRecords(zone.ID)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]any{"zone_id": zone.ID, "zone": zone.Name, "records": records})
}

func (s *Server) handleDNSRecordCreate(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	cf := s.getDNSProvider()
	if cf == nil {
		jsonError(w, "DNS provider not configured", http.StatusNotImplemented)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var rec dnsmanager.Record
	if err := json.NewDecoder(r.Body).Decode(&rec); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	zone, err := cf.FindZoneByDomain(domain)
	if err != nil {
		jsonError(w, "zone not found: "+err.Error(), http.StatusNotFound)
		return
	}
	created, err := cf.CreateRecord(zone.ID, rec)
	if err != nil {
		jsonError(w, "create record: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.logger.Info("DNS record created", "domain", domain, "type", rec.Type, "name", rec.Name, "content", rec.Content)
	jsonResponse(w, created)
}

func (s *Server) handleDNSRecordDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requirePin(w, r) {
		return
	}
	domain := r.PathValue("domain")
	recordID := r.PathValue("id")
	cf := s.getDNSProvider()
	if cf == nil {
		jsonError(w, "DNS provider not configured", http.StatusNotImplemented)
		return
	}
	zone, err := cf.FindZoneByDomain(domain)
	if err != nil {
		jsonError(w, "zone not found: "+err.Error(), http.StatusNotFound)
		return
	}
	if err := cf.DeleteRecord(zone.ID, recordID); err != nil {
		jsonError(w, "delete record: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.logger.Info("DNS record deleted", "domain", domain, "record_id", recordID)
	jsonResponse(w, map[string]string{"status": "deleted"})
}

func (s *Server) handleDNSRecordUpdate(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	recordID := r.PathValue("id")
	prov := s.getDNSProvider()
	if prov == nil {
		jsonError(w, "DNS provider not configured", http.StatusNotImplemented)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var rec dnsmanager.Record
	if err := json.NewDecoder(r.Body).Decode(&rec); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	zone, err := prov.FindZoneByDomain(domain)
	if err != nil {
		jsonError(w, "zone not found: "+err.Error(), http.StatusNotFound)
		return
	}
	updated, err := prov.UpdateRecord(zone.ID, recordID, rec)
	if err != nil {
		jsonError(w, "update record: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.logger.Info("DNS record updated", "domain", domain, "record_id", recordID, "type", rec.Type, "content", rec.Content)
	jsonResponse(w, updated)
}

func (s *Server) handleDNSSync(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	cf := s.getDNSProvider()
	if cf == nil {
		jsonError(w, "DNS provider not configured", http.StatusNotImplemented)
		return
	}

	// Get server's public IP
	ip := serverip.PublicIP()
	if ip == "" {
		jsonError(w, "could not detect server IP", http.StatusInternalServerError)
		return
	}

	// Find zone and sync A record
	zone, err := cf.FindZoneByDomain(domain)
	if err != nil {
		jsonError(w, "zone not found: "+err.Error(), http.StatusNotFound)
		return
	}
	records, err := cf.ListRecords(zone.ID)
	if err != nil {
		jsonError(w, "list records: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// Find existing A record or create new one
	found := false
	for _, rec := range records {
		if rec.Type == "A" && (rec.Name == domain || rec.Name == "@") {
			if rec.Content != ip {
				rec.Content = ip
				if _, err := cf.UpdateRecord(zone.ID, rec.ID, rec); err != nil {
					jsonError(w, "update A record: "+err.Error(), http.StatusInternalServerError)
					return
				}
			}
			found = true
			break
		}
	}
	if !found {
		if _, err := cf.CreateRecord(zone.ID, dnsmanager.Record{Type: "A", Name: domain, Content: ip, TTL: 1}); err != nil {
			jsonError(w, "create A record: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	s.logger.Info("DNS synced", "domain", domain, "ip", ip)
	jsonResponse(w, map[string]string{"status": "synced", "domain": domain, "ip": ip})
}

// ============ Domain Debug ============

func (s *Server) handleDomainDebug(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")

	result := map[string]any{"host": host}

	// Config lookup
	s.configMu.RLock()
	var domainCfg *config.Domain
	for i := range s.config.Domains {
		if s.config.Domains[i].Host == host {
			domainCfg = &s.config.Domains[i]
			break
		}
	}
	webRoot := s.config.Global.WebRoot
	s.configMu.RUnlock()

	if domainCfg == nil {
		result["error"] = "domain not found in config"
		result["configured"] = false
		jsonResponse(w, result)
		return
	}

	result["configured"] = true
	result["type"] = domainCfg.Type
	result["root"] = domainCfg.Root
	result["ssl_mode"] = domainCfg.SSL.Mode
	result["php_fpm_address"] = domainCfg.PHP.FPMAddress
	result["web_root_global"] = webRoot

	// Check if root directory exists
	if domainCfg.Root != "" {
		if info, err := os.Stat(domainCfg.Root); err != nil {
			result["root_exists"] = false
			result["root_error"] = err.Error()
		} else {
			result["root_exists"] = true
			result["root_is_dir"] = info.IsDir()
			// List files in root
			entries, _ := os.ReadDir(domainCfg.Root)
			var files []string
			for _, e := range entries {
				files = append(files, e.Name())
			}
			result["root_files"] = files
		}
	} else {
		result["root_exists"] = false
		result["root_error"] = "root is empty"
	}

	// Config match check
	result["in_config"] = true

	// PHP status
	if domainCfg.Type == "php" && s.phpMgr != nil {
		instances := s.phpMgr.GetDomainInstances()
		for _, inst := range instances {
			if inst.Domain == host {
				result["php_assigned"] = true
				result["php_version"] = inst.Version
				result["php_listen"] = inst.ListenAddr
				result["php_running"] = inst.Running
				result["php_pid"] = inst.PID
				break
			}
		}
		if result["php_assigned"] == nil {
			result["php_assigned"] = false
		}
	}

	// SSL/cert status
	if s.tlsMgr != nil {
		if certInfo := s.tlsMgr.CertStatus(host); certInfo != nil {
			result["cert_active"] = true
			result["cert_issuer"] = certInfo.Issuer
			result["cert_days_left"] = certInfo.DaysLeft
		} else {
			result["cert_active"] = false
		}
	} else {
		result["cert_active"] = false
	}

	jsonResponse(w, result)
}

// ============ System Resources ============

func (s *Server) handleSystemResources(w http.ResponseWriter, r *http.Request) {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	result := map[string]any{
		"cpus":            runtime.NumCPU(),
		"goroutines":      runtime.NumGoroutine(),
		"memory_alloc_mb": float64(memStats.Alloc) / 1024 / 1024,
		"memory_sys_mb":   float64(memStats.Sys) / 1024 / 1024,
		"gc_cycles":       memStats.NumGC,
	}

	// Disk usage of web root
	s.configMu.RLock()
	webRoot := s.config.Global.WebRoot
	s.configMu.RUnlock()

	if webRoot != "" {
		if du, err := filemanager.DiskUsage(webRoot); err == nil {
			result["disk_used_bytes"] = du
			result["disk_used_mb"] = float64(du) / 1024 / 1024
		}
	}

	jsonResponse(w, result)
}

// ============ Domain Health ============

func (s *Server) handleDomainHealth(w http.ResponseWriter, r *http.Request) {
	s.configMu.RLock()
	domains := make([]config.Domain, len(s.config.Domains))
	copy(domains, s.config.Domains)
	s.configMu.RUnlock()

	type healthResult struct {
		Host   string `json:"host"`
		Status string `json:"status"` // "up", "down", "error"
		Code   int    `json:"code"`
		Ms     int64  `json:"ms"`
		Error  string `json:"error,omitempty"`
	}

	results := make([]healthResult, len(domains))
	client := &http.Client{
		Timeout:   5 * time.Second,
		Transport: &http.Transport{DisableKeepAlives: true},
	}

	var wg sync.WaitGroup
	for i, d := range domains {
		wg.Add(1)
		go func(idx int, dom config.Domain) {
			defer wg.Done()
			hr := healthResult{Host: dom.Host}
			scheme := "http"
			if dom.SSL.Mode == "auto" || dom.SSL.Mode == "manual" {
				scheme = "https"
			}
			url := fmt.Sprintf("%s://%s/", scheme, dom.Host)

			start := time.Now()
			resp, err := client.Get(url)
			hr.Ms = time.Since(start).Milliseconds()

			if err != nil {
				hr.Status = "down"
				hr.Error = err.Error()
			} else {
				resp.Body.Close()
				hr.Code = resp.StatusCode
				if resp.StatusCode >= 200 && resp.StatusCode < 400 {
					hr.Status = "up"
				} else {
					hr.Status = "error"
				}
			}
			results[idx] = hr
		}(i, d)
	}
	wg.Wait()

	jsonResponse(w, results)
}

func (s *Server) handleServerIPs(w http.ResponseWriter, r *http.Request) {
	ips := serverip.DetectAll()
	pub := serverip.PublicIP()
	jsonResponse(w, map[string]any{
		"ips":       ips,
		"public_ip": pub,
	})
}

// ============ Security Stats ============

// SetSecurityStats sets the security stats tracker for the API.
func (s *Server) SetSecurityStats(st *middleware.SecurityStats) { s.securityStats = st }

func (s *Server) handleSecurityStats(w http.ResponseWriter, r *http.Request) {
	if s.securityStats == nil {
		jsonResponse(w, map[string]any{
			"waf_blocked": 0, "bot_blocked": 0, "rate_blocked": 0,
			"hotlink_blocked": 0, "total_blocked": 0,
		})
		return
	}
	jsonResponse(w, s.securityStats.Snapshot())
}

func (s *Server) handleSecurityBlocked(w http.ResponseWriter, r *http.Request) {
	if s.securityStats == nil {
		jsonResponse(w, []any{})
		return
	}
	jsonResponse(w, s.securityStats.RecentBlocked())
}

// ── Doctor ─────────────────────────────────────────────────

func (s *Server) handleDoctor(w http.ResponseWriter, r *http.Request) {
	s.configMu.RLock()
	webRoot := s.config.Global.WebRoot
	s.configMu.RUnlock()

	report := doctor.Run(doctor.Options{
		ConfigPath: s.configPath,
		WebRoot:    webRoot,
		AutoFix:    false,
	})
	jsonResponse(w, report)
}

func (s *Server) handleDoctorFix(w http.ResponseWriter, r *http.Request) {
	s.configMu.RLock()
	webRoot := s.config.Global.WebRoot
	s.configMu.RUnlock()

	report := doctor.Run(doctor.Options{
		ConfigPath: s.configPath,
		WebRoot:    webRoot,
		AutoFix:    true,
	})

	ip := requestIP(r)
	fixed := 0
	for _, c := range report.Checks {
		if c.Status == "fixed" {
			fixed++
		}
	}
	s.RecordAudit("doctor.fix", fmt.Sprintf("%d issues fixed", fixed), ip, true)
	jsonResponse(w, report)
}

// ============ Package Installer ============

// PackageInfo describes an installable system package.
type PackageInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Installed   bool   `json:"installed"`
	Version     string `json:"version,omitempty"`
	Category    string `json:"category"`
	Required    bool   `json:"required"`          // true if UWAS needs this
	UsedBy      string `json:"used_by,omitempty"` // what uses it: "WordPress", "Image Optimization", etc
	Warning     string `json:"warning,omitempty"` // uninstall warning
	CanRemove   bool   `json:"can_remove"`        // false if critical dependency
}

type knownPkg struct {
	id, name, description, category string
	required                        bool
	usedBy                          string
	warning                         string // shown before uninstall
	canRemove                       bool
	binaries                        []string
	aptPkgs                         []string
	aptRemove                       []string // packages to remove (may differ from install)
}

var knownPackages = []knownPkg{
	// ── Core (UWAS needs these) ──
	{"mariadb", "MariaDB", "Database for WordPress and web apps", "Database",
		true, "WordPress, Database page", "ALL databases will be destroyed! Back up first.", true,
		[]string{"mariadbd", "mysqld"}, []string{"mariadb-server", "mariadb-client"}, []string{"mariadb-server", "mariadb-client"}},

	// ── PHP (managed separately via PHP page) ──

	// ── Docker ──
	{"docker", "Docker", "Container runtime for dockerized databases (MariaDB, MySQL, PostgreSQL)", "Database",
		false, "Database page (Docker containers)", "All Docker containers will remain, only Docker engine removed.", true,
		[]string{"docker"}, []string{"docker.io"}, []string{"docker.io"}},

	// ── Image Optimization ──
	{"webp", "WebP Tools", "Convert images to WebP (smaller, faster loading)", "Performance",
		false, "Image Optimization (per-domain)", "", true,
		[]string{"cwebp"}, []string{"webp"}, []string{"webp"}},
	{"avif", "AVIF Tools", "Convert images to AVIF (next-gen format)", "Performance",
		false, "Image Optimization (per-domain)", "", true,
		[]string{"avifenc"}, []string{"libavif-bin"}, []string{"libavif-bin"}},

	// ── Security ──
	{"ufw", "UFW Firewall", "Manage firewall rules from dashboard", "Security",
		true, "Firewall page", "All firewall rules will be removed!", true,
		[]string{"ufw"}, []string{"ufw"}, []string{"ufw"}},
	{"fail2ban", "Fail2Ban", "Auto-block brute-force attacks on SSH/HTTP", "Security",
		false, "SSH + admin panel protection", "", true,
		[]string{"fail2ban-client"}, []string{"fail2ban"}, []string{"fail2ban"}},

	// ── WordPress ──
	{"wp-cli", "WP-CLI", "Manage WordPress from dashboard (updates, plugins, themes)", "WordPress",
		false, "WordPress Sites page (plugin/theme management)", "", true,
		[]string{"wp"}, nil, nil},

	// ── Email ──
	{"postfix", "Postfix", "Send emails from your server (SMTP)", "Email",
		false, "WordPress email sending, contact forms", "Server will not be able to send emails!", true,
		[]string{"postfix"}, []string{"postfix"}, []string{"postfix"}},

	// ── Utilities (required by UWAS internals) ──
	{"curl", "cURL", "HTTP client (used for ACME, health checks, WP-CLI)", "Required",
		true, "SSL certificates, health monitoring", "", false,
		[]string{"curl"}, []string{"curl"}, nil},
	{"unzip", "Unzip", "Extract archives (used for WordPress install)", "Required",
		true, "WordPress installer", "", false,
		[]string{"unzip"}, []string{"unzip"}, nil},
}

func (s *Server) handlePackageList(w http.ResponseWriter, r *http.Request) {
	pkgs := make([]PackageInfo, 0, len(knownPackages))
	for _, kp := range knownPackages {
		pi := PackageInfo{
			ID:          kp.id,
			Name:        kp.name,
			Description: kp.description,
			Category:    kp.category,
			Required:    kp.required,
			UsedBy:      kp.usedBy,
			Warning:     kp.warning,
			CanRemove:   kp.canRemove,
		}
		for _, bin := range kp.binaries {
			if p, err := exec.LookPath(bin); err == nil {
				pi.Installed = true
				if out, err := exec.Command(p, "--version").CombinedOutput(); err == nil {
					lines := strings.SplitN(string(out), "\n", 2)
					if len(lines) > 0 {
						v := strings.TrimSpace(lines[0])
						if len(v) > 60 {
							v = v[:60]
						}
						pi.Version = v
					}
				}
				break
			}
		}
		pkgs = append(pkgs, pi)
	}
	jsonResponse(w, pkgs)
}

// Package installation is managed by the global task manager (install.Manager).

func findPkg(id string) *knownPkg {
	for i := range knownPackages {
		if knownPackages[i].id == id {
			return &knownPackages[i]
		}
	}
	return nil
}

func (s *Server) handlePackageInstall(w http.ResponseWriter, r *http.Request) {
	ip := requestIP(r)
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		ID     string `json:"id"`
		Action string `json:"action"` // "install" (default) or "remove"
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Action == "" {
		req.Action = "install"
	}

	pkg := findPkg(req.ID)
	if pkg == nil {
		jsonError(w, "unknown package: "+req.ID, http.StatusBadRequest)
		return
	}

	// Uninstall validation
	if req.Action == "remove" {
		if !pkg.canRemove {
			jsonError(w, pkg.name+" is required by UWAS and cannot be removed", http.StatusForbidden)
			return
		}
		if len(pkg.aptRemove) == 0 && pkg.id != "wp-cli" {
			jsonError(w, "no removal method for "+pkg.name, http.StatusBadRequest)
			return
		}
	}

	// Check if any install task is already running
	if active := s.taskMgr.Active(); active != nil {
		jsonError(w, fmt.Sprintf("another installation in progress: %s (%s)", active.Name, active.ID), http.StatusConflict)
		return
	}

	action := req.Action
	s.RecordAudit("package."+action, pkg.name, ip, true)

	pkgName := pkg.name
	pkgID := pkg.id
	aptPkgs := pkg.aptPkgs
	aptRemove := pkg.aptRemove

	task := s.taskMgr.Submit("package", pkgName, action, func(appendOutput func(string)) error {
		var cmd *exec.Cmd

		if action == "remove" {
			if pkgID == "wp-cli" {
				cmd = exec.Command("rm", "-f", "/usr/local/bin/wp")
			} else {
				exec.Command("systemctl", "stop", pkgID).Run()
				args := append([]string{"remove", "-y", "--purge"}, aptRemove...)
				cmd = exec.Command("apt", args...)
			}
		} else {
			if pkgID == "wp-cli" {
				cmd = exec.Command("bash", "-c", "curl -fsSL -o /usr/local/bin/wp https://raw.githubusercontent.com/wp-cli/builds/gh-pages/phar/wp-cli.phar && chmod +x /usr/local/bin/wp")
			} else if len(aptPkgs) > 0 {
				args := append([]string{"install", "-y"}, aptPkgs...)
				cmd = exec.Command("apt", args...)
			} else {
				return fmt.Errorf("no install method for %s", pkgName)
			}
		}

		cmd.Env = append(os.Environ(),
			"DEBIAN_FRONTEND=noninteractive",
			"NEEDRESTART_MODE=a",
			"APT_LISTCHANGES_FRONTEND=none",
			"DEBIAN_PRIORITY=critical",
		)
		out, err := cmd.CombinedOutput()
		appendOutput(string(out))
		if err != nil {
			s.logger.Error("package "+action+" failed", "package", pkgName, "error", err)
			return err
		}
		s.logger.Info("package "+action+" complete", "package", pkgName)
		return nil
	})

	jsonResponse(w, map[string]string{"status": action + "ing", "package": pkgName, "task_id": task.ID})
}

// ============ Site Migration + Clone ============

func (s *Server) handleMigrate(w http.ResponseWriter, r *http.Request) {
	ip := requestIP(r)
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req migrate.MigrateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.SourceHost == "" || req.Domain == "" {
		jsonError(w, "source_host and domain required", http.StatusBadRequest)
		return
	}
	if req.LocalRoot == "" {
		req.LocalRoot = s.domainRoot(req.Domain)
	}
	if req.LocalRoot == "" {
		jsonError(w, "domain not found or no web root", http.StatusBadRequest)
		return
	}
	s.RecordAudit("migrate.start", req.SourceHost+" → "+req.Domain, ip, true)
	result := migrate.Migrate(req)
	jsonResponse(w, result)
}

// validateCloneRequest validates the clone request and returns the parsed request or error.
func (s *Server) validateCloneRequest(r *http.Request) (migrate.CloneRequest, error) {
	var req migrate.CloneRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return req, fmt.Errorf("invalid JSON: %w", err)
	}
	if req.SourceDomain == "" || req.TargetDomain == "" {
		return req, fmt.Errorf("source_domain and target_domain required")
	}
	return req, nil
}

// resolveClonePaths resolves source and target root paths for cloning.
func (s *Server) resolveClonePaths(req *migrate.CloneRequest) error {
	if req.SourceRoot == "" {
		req.SourceRoot = s.domainRoot(req.SourceDomain)
	}
	if req.SourceRoot == "" {
		return fmt.Errorf("source domain not found")
	}
	if req.TargetRoot == "" {
		s.configMu.RLock()
		webRoot := s.config.Global.WebRoot
		s.configMu.RUnlock()
		if webRoot == "" {
			webRoot = "/var/www"
		}
		req.TargetRoot = filepath.Join(webRoot, req.TargetDomain, "public_html")
	}
	return nil
}

// detectWordPressDB auto-detects database credentials from wp-config.php.
func detectWordPressDB(req *migrate.CloneRequest) {
	if req.SourceDB != "" {
		return
	}
	wpCfg := filepath.Join(req.SourceRoot, "wp-config.php")
	data, err := os.ReadFile(wpCfg)
	if err != nil {
		return
	}
	content := string(data)
	for _, line := range strings.Split(content, "\n") {
		if strings.Contains(line, "DB_NAME") {
			parts := strings.Split(line, "'")
			if len(parts) >= 4 {
				req.SourceDB = parts[3]
			}
		}
		if strings.Contains(line, "DB_USER") && req.DBUser == "" {
			parts := strings.Split(line, "'")
			if len(parts) >= 4 {
				req.DBUser = parts[3]
			}
		}
		if strings.Contains(line, "DB_PASSWORD") && req.DBPass == "" {
			parts := strings.Split(line, "'")
			if len(parts) >= 4 {
				req.DBPass = parts[3]
			}
		}
	}
}

// autoCreateDomainForClone creates domain config after successful clone.
func (s *Server) autoCreateDomainForClone(req *migrate.CloneRequest, result *migrate.CloneResult) {
	if result.Status != "done" && result.Status != "completed" {
		return
	}

	s.configMu.Lock()
	defer s.configMu.Unlock()

	// Find source domain config to copy settings
	var sourceCfg *config.Domain
	for i := range s.config.Domains {
		if s.config.Domains[i].Host == req.SourceDomain {
			sourceCfg = &s.config.Domains[i]
			break
		}
	}

	// Check target doesn't already exist
	for _, d := range s.config.Domains {
		if d.Host == req.TargetDomain {
			return
		}
	}

	newDomain := config.Domain{
		Host:     req.TargetDomain,
		Root:     req.TargetRoot,
		Type:     "php",
		SSL:      config.SSLConfig{Mode: "auto"},
		Htaccess: config.HtaccessConfig{Mode: "import"},
	}
	// Copy settings from source if available
	if sourceCfg != nil {
		newDomain.Type = sourceCfg.Type
		newDomain.PHP = sourceCfg.PHP
		newDomain.Cache = sourceCfg.Cache
		newDomain.Security = sourceCfg.Security
	}
	s.config.Domains = append(s.config.Domains, newDomain)
	s.persistConfig()
	s.notifyDomainChange()
	s.logger.Info("clone: auto-created domain", "domain", req.TargetDomain, "root", req.TargetRoot)
}

func (s *Server) handleClone(w http.ResponseWriter, r *http.Request) {
	ip := requestIP(r)
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	req, err := s.validateCloneRequest(r)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := s.resolveClonePaths(&req); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	detectWordPressDB(&req)

	s.RecordAudit("clone.start", req.SourceDomain+" → "+req.TargetDomain, ip, true)
	result := migrate.Clone(req)

	s.autoCreateDomainForClone(&req, result)

	jsonResponse(w, result)
}

// saveUploadedFile saves uploaded file to temp location and returns path.
func saveUploadedFile(r *http.Request, fieldName string) (string, *multipart.FileHeader, error) {
	file, header, err := r.FormFile(fieldName)
	if err != nil {
		return "", nil, fmt.Errorf("backup file required")
	}
	defer file.Close()

	tmp, err := os.CreateTemp("", "uwas-cpanel-upload-*.tar.gz")
	if err != nil {
		return "", nil, fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, file); err != nil {
		tmp.Close()
		return "", nil, fmt.Errorf("save upload: %w", err)
	}
	tmp.Close()
	return tmp.Name(), header, nil
}

// createDomainsFromMigration creates domain configs from migration result.
func (s *Server) createDomainsFromMigration(result *migrate.CPanelResult, webRoot string) []string {
	s.configMu.Lock()
	defer s.configMu.Unlock()

	existingHosts := map[string]bool{}
	for _, d := range s.config.Domains {
		existingHosts[d.Host] = true
	}

	var added []string
	for _, dom := range result.Domains {
		if dom.Domain == "" || dom.Domain == "unknown" || existingHosts[dom.Domain] {
			continue
		}
		newDomain := config.Domain{
			Host: dom.Domain,
			Type: "php",
			Root: filepath.Join(webRoot, dom.Domain, "public_html"),
			SSL:  config.SSLConfig{Mode: "auto"},
		}
		s.config.Domains = append(s.config.Domains, newDomain)
		added = append(added, dom.Domain)
	}

	return added
}

// handleMigrateCPanel imports a cPanel backup archive (cpmove-*.tar.gz).
// Expects multipart upload with "backup" file field.
func (s *Server) handleMigrateCPanel(w http.ResponseWriter, r *http.Request) {
	ip := requestIP(r)

	r.Body = http.MaxBytesReader(w, r.Body, 10<<30) // 10GB max backup
	if err := r.ParseMultipartForm(256 << 20); err != nil {
		jsonError(w, "parse form: "+err.Error(), http.StatusBadRequest)
		return
	}

	tmpPath, header, err := saveUploadedFile(r, "backup")
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.configMu.RLock()
	webRoot := s.config.Global.WebRoot
	s.configMu.RUnlock()
	if webRoot == "" {
		webRoot = "/var/www"
	}

	importDB := r.FormValue("import_db") == "true"
	s.RecordAudit("migrate.cpanel", header.Filename, ip, true)

	result, err := migrate.ImportCPanelBackup(tmpPath, webRoot, importDB)
	if err != nil {
		jsonError(w, "import failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	added := s.createDomainsFromMigration(result, webRoot)
	if len(added) > 0 {
		s.persistConfig()
		s.notifyDomainChange()
		s.RecordAudit("migrate.cpanel.domains", strings.Join(added, ", "), ip, true)
	}

	jsonResponse(w, map[string]any{
		"status":        "imported",
		"user":          result.User,
		"domains":       result.Domains,
		"databases":     result.Databases,
		"ssl_certs":     result.SSLCerts,
		"files_count":   result.FilesCount,
		"domains_added": added,
		"errors":        result.Errors,
	})
}

// ── Database Explorer ──────────────────────────────────────────────

func (s *Server) handleDBExploreTables(w http.ResponseWriter, r *http.Request) {
	db := r.PathValue("db")
	if db == "" {
		jsonError(w, "database name required", http.StatusBadRequest)
		return
	}
	if !database.ValidDBIdentifier(db) {
		jsonError(w, "invalid database name", http.StatusBadRequest)
		return
	}
	sql := fmt.Sprintf("SELECT TABLE_NAME, TABLE_ROWS, DATA_LENGTH, INDEX_LENGTH, ENGINE, TABLE_COLLATION FROM information_schema.TABLES WHERE TABLE_SCHEMA = '%s' ORDER BY TABLE_NAME", database.EscapeSQL(db))
	out, err := database.RunSQL(sql)
	if err != nil {
		jsonError(w, "query failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// Parse tab-separated output into JSON
	var tables []map[string]string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) >= 6 {
			tables = append(tables, map[string]string{
				"name":       fields[0],
				"rows":       fields[1],
				"data_size":  fields[2],
				"index_size": fields[3],
				"engine":     fields[4],
				"collation":  fields[5],
			})
		}
	}
	jsonResponse(w, tables)
}

func (s *Server) handleDBExploreColumns(w http.ResponseWriter, r *http.Request) {
	db := r.PathValue("db")
	table := r.PathValue("table")
	if !database.ValidDBIdentifier(db) || !database.ValidDBIdentifier(table) {
		jsonError(w, "invalid name", http.StatusBadRequest)
		return
	}
	sql := fmt.Sprintf("SELECT COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE, COLUMN_KEY, COLUMN_DEFAULT, EXTRA FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = '%s' AND TABLE_NAME = '%s' ORDER BY ORDINAL_POSITION", database.EscapeSQL(db), database.EscapeSQL(table))
	out, err := database.RunSQL(sql)
	if err != nil {
		jsonError(w, "query failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	var columns []map[string]string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) >= 6 {
			columns = append(columns, map[string]string{
				"name":     fields[0],
				"type":     fields[1],
				"nullable": fields[2],
				"key":      fields[3],
				"default":  fields[4],
				"extra":    fields[5],
			})
		}
	}
	jsonResponse(w, columns)
}

func (s *Server) handleDBExploreQuery(w http.ResponseWriter, r *http.Request) {
	db := r.PathValue("db")
	if !database.ValidDBIdentifier(db) {
		jsonError(w, "invalid database name", http.StatusBadRequest)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		SQL   string `json:"sql"`
		Limit int    `json:"limit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.SQL == "" {
		jsonError(w, "sql required", http.StatusBadRequest)
		return
	}
	// Safety: only allow read-only statements (allowlist approach).
	// Strip leading comments and whitespace to prevent comment-based bypass.
	trimmed := strings.TrimSpace(req.SQL)
	for strings.HasPrefix(trimmed, "/*") {
		if end := strings.Index(trimmed, "*/"); end >= 0 {
			trimmed = strings.TrimSpace(trimmed[end+2:])
		} else {
			break
		}
	}
	for strings.HasPrefix(trimmed, "--") || strings.HasPrefix(trimmed, "#") {
		if nl := strings.IndexByte(trimmed, '\n'); nl >= 0 {
			trimmed = strings.TrimSpace(trimmed[nl+1:])
		} else {
			trimmed = ""
		}
	}
	upper := strings.ToUpper(trimmed)
	// Block multi-statement queries (semicolons).
	if strings.Contains(req.SQL, ";") {
		jsonError(w, "multi-statement queries not allowed", http.StatusForbidden)
		return
	}
	// Only allow SELECT, SHOW, DESCRIBE, EXPLAIN.
	if !strings.HasPrefix(upper, "SELECT") && !strings.HasPrefix(upper, "SHOW") &&
		!strings.HasPrefix(upper, "DESCRIBE") && !strings.HasPrefix(upper, "DESC ") &&
		!strings.HasPrefix(upper, "EXPLAIN") {
		jsonError(w, "only SELECT, SHOW, DESCRIBE, EXPLAIN are allowed in explorer", http.StatusForbidden)
		return
	}
	// Add LIMIT if SELECT and no LIMIT present
	if strings.HasPrefix(upper, "SELECT") && !strings.Contains(upper, "LIMIT") {
		limit := req.Limit
		if limit <= 0 || limit > 1000 {
			limit = 100
		}
		req.SQL = req.SQL + fmt.Sprintf(" LIMIT %d", limit)
	}
	// Use specific database
	fullSQL := fmt.Sprintf("USE %s;\n%s", database.BacktickID(db), req.SQL)
	out, err := database.RunSQL(fullSQL)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Parse tab-separated into rows
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) == 0 {
		jsonResponse(w, map[string]any{"columns": []string{}, "rows": [][]string{}, "affected": out})
		return
	}
	headers := strings.Split(lines[0], "\t")
	var rows [][]string
	for _, line := range lines[1:] {
		if line != "" {
			rows = append(rows, strings.Split(line, "\t"))
		}
	}
	jsonResponse(w, map[string]any{
		"columns": headers,
		"rows":    rows,
		"count":   len(rows),
	})
}

// ── SSL Certificate Upload ─────────────────────────────────────────

func (s *Server) handleCertUpload(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Cert  string `json:"cert"`
		Key   string `json:"key"`
		Chain string `json:"chain"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Cert == "" || req.Key == "" {
		jsonError(w, "cert and key required (PEM format)", http.StatusBadRequest)
		return
	}

	if strings.ContainsAny(host, `/\.`) || strings.Contains(host, "..") {
		jsonError(w, "invalid hostname", http.StatusBadRequest)
		return
	}
	certDir := filepath.Join("/var/lib/uwas/certs", host)
	os.MkdirAll(certDir, 0700)
	if err := os.WriteFile(filepath.Join(certDir, "cert.pem"), []byte(req.Cert), 0600); err != nil {
		jsonError(w, "write cert: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := os.WriteFile(filepath.Join(certDir, "key.pem"), []byte(req.Key), 0600); err != nil {
		jsonError(w, "write key: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if req.Chain != "" {
		os.WriteFile(filepath.Join(certDir, "chain.pem"), []byte(req.Chain), 0600)
	}

	// Update domain SSL mode to manual
	s.configMu.Lock()
	for i, d := range s.config.Domains {
		if d.Host == host {
			s.config.Domains[i].SSL.Mode = "manual"
			s.config.Domains[i].SSL.Cert = filepath.Join(certDir, "cert.pem")
			s.config.Domains[i].SSL.Key = filepath.Join(certDir, "key.pem")
			break
		}
	}
	s.configMu.Unlock()
	s.persistConfig()
	s.notifyDomainChange()

	s.RecordAudit("cert.upload", host, requestIP(r), true)
	jsonResponse(w, map[string]string{"status": "uploaded", "host": host})
}

// ── Bulk Domain Import ─────────────────────────────────────────────

func (s *Server) handleBulkDomainImport(w http.ResponseWriter, r *http.Request) {
	ip := requestIP(r)
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Domains []struct {
			Host string `json:"host"`
			Type string `json:"type"`
			Root string `json:"root"`
			SSL  string `json:"ssl"`
		} `json:"domains"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	s.configMu.Lock()
	existing := map[string]bool{}
	for _, d := range s.config.Domains {
		existing[d.Host] = true
	}

	var added, skipped []string
	webRoot := s.config.Global.WebRoot
	if webRoot == "" {
		webRoot = "/var/www"
	}
	for _, d := range req.Domains {
		if d.Host == "" || existing[d.Host] {
			skipped = append(skipped, d.Host)
			continue
		}
		dtype := d.Type
		if dtype == "" {
			dtype = "static"
		}
		sslMode := d.SSL
		if sslMode == "" {
			sslMode = "auto"
		}
		root := d.Root
		if root == "" {
			root = filepath.Join(webRoot, d.Host, "public_html")
		}
		s.config.Domains = append(s.config.Domains, config.Domain{
			Host: d.Host, Type: dtype, Root: root,
			SSL: config.SSLConfig{Mode: sslMode},
		})
		added = append(added, d.Host)
		existing[d.Host] = true
	}
	s.configMu.Unlock()

	if len(added) > 0 {
		s.persistConfig()
		s.notifyDomainChange()
		s.RecordAudit("domain.bulk_import", fmt.Sprintf("%d added", len(added)), ip, true)
	}
	jsonResponse(w, map[string]any{"added": added, "skipped": skipped})
}

// ── 2FA Recovery Codes ─────────────────────────────────────────────

func (s *Server) handleGenRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	codes := make([]string, 8)
	for i := range codes {
		b := make([]byte, 4)
		if _, err := crand.Read(b); err != nil {
			jsonError(w, "entropy failure", http.StatusInternalServerError)
			return
		}
		codes[i] = fmt.Sprintf("%x", b)
	}
	// Store hashed codes in config
	s.configMu.Lock()
	s.config.Global.Admin.RecoveryCodes = codes
	s.configMu.Unlock()
	s.persistConfig()
	s.RecordAudit("2fa.recovery_codes.generated", "", requestIP(r), true)
	jsonResponse(w, map[string]any{"codes": codes, "count": len(codes)})
}

func (s *Server) handleUseRecoveryCode(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	s.configMu.Lock()
	found := false
	for i, c := range s.config.Global.Admin.RecoveryCodes {
		if subtle.ConstantTimeCompare([]byte(c), []byte(req.Code)) == 1 {
			// Remove used code
			s.config.Global.Admin.RecoveryCodes = append(
				s.config.Global.Admin.RecoveryCodes[:i],
				s.config.Global.Admin.RecoveryCodes[i+1:]...,
			)
			found = true
			break
		}
	}
	s.configMu.Unlock()
	if !found {
		jsonError(w, "invalid recovery code", http.StatusUnauthorized)
		return
	}
	s.persistConfig()
	s.RecordAudit("2fa.recovery_code.used", "", requestIP(r), true)
	jsonResponse(w, map[string]string{"status": "ok"})
}

// ── Notification Preferences ───────────────────────────────────────

func (s *Server) handleNotifyPrefsGet(w http.ResponseWriter, r *http.Request) {
	s.configMu.RLock()
	prefs := map[string]any{
		"alerting": s.config.Global.Alerting,
		"webhooks": s.config.Global.Webhooks,
	}
	s.configMu.RUnlock()
	jsonResponse(w, prefs)
}

func (s *Server) handleNotifyPrefsPut(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Alerting config.AlertingConfig  `json:"alerting"`
		Webhooks []config.WebhookConfig `json:"webhooks"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	s.configMu.Lock()
	s.config.Global.Alerting = req.Alerting
	s.config.Global.Webhooks = req.Webhooks
	s.configMu.Unlock()
	s.persistConfig()
	s.RecordAudit("settings.notifications", "updated", requestIP(r), true)
	jsonResponse(w, map[string]string{"status": "saved"})
}

// ── White-Label Branding ───────────────────────────────────────────

func (s *Server) handleBrandingGet(w http.ResponseWriter, r *http.Request) {
	s.configMu.RLock()
	branding := s.config.Global.Admin.Branding
	s.configMu.RUnlock()
	jsonResponse(w, branding)
}

func (s *Server) handleBrandingPut(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var branding config.BrandingConfig
	if err := json.NewDecoder(r.Body).Decode(&branding); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	s.configMu.Lock()
	s.config.Global.Admin.Branding = branding
	s.configMu.Unlock()
	s.persistConfig()
	s.RecordAudit("settings.branding", "updated", requestIP(r), true)
	jsonResponse(w, map[string]string{"status": "saved"})
}

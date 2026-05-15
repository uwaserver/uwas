package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"

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
	limit, offset := parsePagination(r)
	items, total := paginateSlice(sites, limit, offset)
	jsonResponse(w, map[string]any{
		"items":  items,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
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

	s.recordAuditR(r, "wordpress.debug", fmt.Sprintf("domain: %s, enable: %v", domain, req.Enable), true)
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
	s.recordAuditR(r, "wordpress.change_password", domain+":"+req.Username, true)
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
	s.recordAuditR(r, "wordpress.harden", domain, true)
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
	s.recordAuditR(r, "wordpress.optimize_db", domain, true)
	jsonResponse(w, result)
}

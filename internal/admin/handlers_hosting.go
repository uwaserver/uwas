package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/uwaserver/uwas/internal/build"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/doctor"
	"github.com/uwaserver/uwas/internal/cronjob"
	"github.com/uwaserver/uwas/internal/database"
	"github.com/uwaserver/uwas/internal/dnschecker"
	"github.com/uwaserver/uwas/internal/dnsmanager"
	"github.com/uwaserver/uwas/internal/filemanager"
	"github.com/uwaserver/uwas/internal/firewall"
	"github.com/uwaserver/uwas/internal/middleware"
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
		domains = append(domains, wordpress.DomainInfo{Host: d.Host, WebRoot: d.Root})
	}
	s.configMu.RUnlock()

	sites := wordpress.DetectSites(domains)
	if sites == nil {
		sites = []wordpress.SiteInfo{}
	}
	jsonResponse(w, sites)
}

// handleWPUpdateCore triggers WP core update via WP-CLI.
func (s *Server) handleWPUpdateCore(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root := s.domainRoot(domain)
	if root == "" {
		jsonError(w, "domain not found", http.StatusNotFound)
		return
	}
	if !wordpress.IsWordPress(root) {
		jsonError(w, "not a WordPress site", http.StatusBadRequest)
		return
	}
	out, err := wordpress.UpdateCore(root)
	if err != nil {
		jsonError(w, "update failed: "+out, http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "updated", "output": out})
}

// handleWPUpdatePlugins updates all plugins via WP-CLI.
func (s *Server) handleWPUpdatePlugins(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root := s.domainRoot(domain)
	if root == "" {
		jsonError(w, "domain not found", http.StatusNotFound)
		return
	}
	out, err := wordpress.UpdateAllPlugins(root)
	if err != nil {
		jsonError(w, "update failed: "+out, http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "updated", "output": out})
}

// handleWPPluginAction activates, deactivates, or deletes a plugin.
func (s *Server) handleWPPluginAction(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	action := r.PathValue("action")
	plugin := r.PathValue("plugin")
	root := s.domainRoot(domain)
	if root == "" {
		jsonError(w, "domain not found", http.StatusNotFound)
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
		jsonError(w, action+" failed: "+out, http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": action + "d", "output": out})
}

// handleWPFixPermissions fixes file permissions for a WordPress site.
func (s *Server) handleWPFixPermissions(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root := s.domainRoot(domain)
	if root == "" {
		jsonError(w, "domain not found", http.StatusNotFound)
		return
	}
	out, err := wordpress.FixPermissions(root)
	if err != nil {
		jsonError(w, "fix failed: "+out, http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "fixed", "output": out})
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

func (s *Server) handleFileList(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root := s.domainRoot(domain)
	if root == "" {
		jsonError(w, fmt.Sprintf("domain %q not configured or has no web root", domain), http.StatusNotFound)
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
	root := s.domainRoot(domain)
	if root == "" {
		jsonError(w, "domain not found", http.StatusNotFound)
		return
	}
	path := r.URL.Query().Get("path")
	data, err := filemanager.ReadFile(root, path)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	jsonResponse(w, map[string]string{"content": string(data), "path": path})
}

func (s *Server) handleFileWrite(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root := s.domainRoot(domain)
	if root == "" {
		jsonError(w, "domain not found", http.StatusNotFound)
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
	root := s.domainRoot(domain)
	if root == "" {
		jsonError(w, "domain not found", http.StatusNotFound)
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
	root := s.domainRoot(domain)
	if root == "" {
		jsonError(w, "domain not found", http.StatusNotFound)
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
	root := s.domainRoot(domain)
	if root == "" {
		jsonError(w, "domain not found", http.StatusNotFound)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 100<<20) // 100MB limit
	if err := r.ParseMultipartForm(100 << 20); err != nil {
		jsonError(w, "parse form: "+err.Error(), http.StatusBadRequest)
		return
	}
	dir := r.FormValue("path")
	if dir == "" {
		dir = "."
	}

	var uploaded []string
	for _, fHeaders := range r.MultipartForm.File {
		for _, fh := range fHeaders {
			src, err := fh.Open()
			if err != nil {
				continue
			}
			relPath := filepath.Join(dir, fh.Filename)
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
	root := s.domainRoot(domain)
	if root == "" {
		jsonError(w, "domain not found", http.StatusNotFound)
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
		// Try systemctl restart first (cleanest)
		if err := exec.Command("systemctl", "restart", "uwas").Run(); err != nil {
			// Fallback: send SIGHUP to self for graceful reload
			if p, err := os.FindProcess(os.Getpid()); err == nil {
				p.Signal(syscall.SIGHUP)
			}
		}
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
	output, err := database.InstallMySQL()
	if err != nil {
		jsonError(w, "install failed: "+err.Error()+"\n"+output, http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "installed"})
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

func (s *Server) getDNSProvider() *dnsmanager.CloudflareProvider {
	s.configMu.RLock()
	provider := s.config.Global.ACME.DNSProvider
	creds := s.config.Global.ACME.DNSCredentials
	s.configMu.RUnlock()

	if provider != "cloudflare" || creds == nil {
		return nil
	}
	token := creds["api_token"]
	if token == "" {
		token = creds["token"]
	}
	if token == "" {
		return nil
	}
	return dnsmanager.NewCloudflare(token)
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

	if err := cf.SyncDomainToIP(domain, ip); err != nil {
		jsonError(w, "sync failed: "+err.Error(), http.StatusInternalServerError)
		return
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
	client := &http.Client{Timeout: 5 * time.Second}

	for i, d := range domains {
		hr := healthResult{Host: d.Host}
		scheme := "http"
		if d.SSL.Mode == "auto" || d.SSL.Mode == "manual" {
			scheme = "https"
		}
		url := fmt.Sprintf("%s://%s/", scheme, d.Host)

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
		results[i] = hr
	}

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
	InstallCmd  string `json:"install_cmd,omitempty"`
}

var knownPackages = []struct {
	id          string
	name        string
	description string
	category    string
	binaries    []string // check if installed via which/command -v
	aptPkgs     []string // apt install packages
	dnfPkgs     []string // dnf install packages
}{
	{"mariadb", "MariaDB", "MySQL-compatible database server", "Database", []string{"mariadbd", "mysqld"}, []string{"mariadb-server", "mariadb-client"}, []string{"mariadb-server"}},
	{"redis", "Redis", "In-memory cache and message broker", "Database", []string{"redis-server"}, []string{"redis-server"}, []string{"redis"}},
	{"webp", "WebP Tools", "Image conversion to WebP format (cwebp)", "Image", []string{"cwebp"}, []string{"webp"}, []string{"libwebp-tools"}},
	{"avif", "AVIF Tools", "Image conversion to AVIF format (avifenc)", "Image", []string{"avifenc"}, []string{"libavif-bin"}, []string{"libavif-tools"}},
	{"imagemagick", "ImageMagick", "Image processing suite (convert, identify)", "Image", []string{"convert", "magick"}, []string{"imagemagick"}, []string{"ImageMagick"}},
	{"certbot", "Certbot", "Let's Encrypt certificate tool (standalone)", "SSL", []string{"certbot"}, []string{"certbot"}, []string{"certbot"}},
	{"ufw", "UFW", "Uncomplicated Firewall", "Security", []string{"ufw"}, []string{"ufw"}, []string{"ufw"}},
	{"fail2ban", "Fail2Ban", "Intrusion prevention (brute-force protection)", "Security", []string{"fail2ban-client"}, []string{"fail2ban"}, []string{"fail2ban"}},
	{"git", "Git", "Version control system", "Dev Tools", []string{"git"}, []string{"git"}, []string{"git"}},
	{"wp-cli", "WP-CLI", "WordPress command-line tool", "WordPress", []string{"wp"}, nil, nil}, // special install
	{"docker", "Docker", "Container runtime", "Containers", []string{"docker"}, []string{"docker.io"}, []string{"docker"}},
	{"docker-compose", "Docker Compose", "Multi-container orchestration", "Containers", []string{"docker-compose", "docker"}, []string{"docker-compose"}, []string{"docker-compose"}},
	{"postfix", "Postfix", "Mail Transfer Agent (SMTP server)", "Email", []string{"postfix"}, []string{"postfix"}, []string{"postfix"}},
	{"dovecot", "Dovecot", "IMAP/POP3 mail server", "Email", []string{"dovecot"}, []string{"dovecot-imapd", "dovecot-pop3d"}, []string{"dovecot"}},
	{"rsync", "Rsync", "Fast file synchronization", "Utilities", []string{"rsync"}, []string{"rsync"}, []string{"rsync"}},
	{"htop", "htop", "Interactive process viewer", "Utilities", []string{"htop"}, []string{"htop"}, []string{"htop"}},
	{"curl", "cURL", "HTTP client tool", "Utilities", []string{"curl"}, []string{"curl"}, []string{"curl"}},
	{"unzip", "Unzip", "ZIP archive extraction", "Utilities", []string{"unzip"}, []string{"unzip"}, []string{"unzip"}},
}

func (s *Server) handlePackageList(w http.ResponseWriter, r *http.Request) {
	pkgs := make([]PackageInfo, 0, len(knownPackages))
	for _, kp := range knownPackages {
		pi := PackageInfo{
			ID:          kp.id,
			Name:        kp.name,
			Description: kp.description,
			Category:    kp.category,
		}
		// Check if installed
		for _, bin := range kp.binaries {
			if path, err := exec.LookPath(bin); err == nil {
				pi.Installed = true
				// Try to get version
				if out, err := exec.Command(path, "--version").CombinedOutput(); err == nil {
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
		// Build install command
		if !pi.Installed {
			if len(kp.aptPkgs) > 0 {
				pi.InstallCmd = "apt install -y " + strings.Join(kp.aptPkgs, " ")
			}
			if kp.id == "wp-cli" {
				pi.InstallCmd = "curl -O https://raw.githubusercontent.com/wp-cli/builds/gh-pages/phar/wp-cli.phar && chmod +x wp-cli.phar && mv wp-cli.phar /usr/local/bin/wp"
			}
		}
		pkgs = append(pkgs, pi)
	}
	jsonResponse(w, pkgs)
}

// packageInstallState tracks background package installation.
var (
	pkgInstallMu     sync.Mutex
	pkgInstallStatus struct {
		Package string `json:"package"`
		Status  string `json:"status"` // idle, running, done, error
		Output  string `json:"output"`
		Error   string `json:"error,omitempty"`
	}
)

func (s *Server) handlePackageInstall(w http.ResponseWriter, r *http.Request) {
	ip := requestIP(r)
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	// Find package
	var found *struct {
		id, name, description, category string
		binaries, aptPkgs, dnfPkgs      []string
	}
	for i := range knownPackages {
		if knownPackages[i].id == req.ID {
			kp := knownPackages[i]
			found = &struct {
				id, name, description, category string
				binaries, aptPkgs, dnfPkgs      []string
			}{kp.id, kp.name, kp.description, kp.category, kp.binaries, kp.aptPkgs, kp.dnfPkgs}
			break
		}
	}
	if found == nil {
		jsonError(w, "unknown package: "+req.ID, http.StatusBadRequest)
		return
	}

	pkgInstallMu.Lock()
	if pkgInstallStatus.Status == "running" {
		pkgInstallMu.Unlock()
		jsonError(w, "another installation in progress: "+pkgInstallStatus.Package, http.StatusConflict)
		return
	}
	pkgInstallStatus.Package = found.name
	pkgInstallStatus.Status = "running"
	pkgInstallStatus.Output = ""
	pkgInstallStatus.Error = ""
	pkgInstallMu.Unlock()

	s.RecordAudit("package.install", found.name, ip, true)

	go func() {
		var cmd *exec.Cmd
		if found.id == "wp-cli" {
			cmd = exec.Command("bash", "-c", "curl -fsSL -o /usr/local/bin/wp https://raw.githubusercontent.com/wp-cli/builds/gh-pages/phar/wp-cli.phar && chmod +x /usr/local/bin/wp")
		} else if len(found.aptPkgs) > 0 {
			args := append([]string{"install", "-y"}, found.aptPkgs...)
			cmd = exec.Command("apt", args...)
		} else if len(found.dnfPkgs) > 0 {
			args := append([]string{"install", "-y"}, found.dnfPkgs...)
			cmd = exec.Command("dnf", args...)
		} else {
			pkgInstallMu.Lock()
			pkgInstallStatus.Status = "error"
			pkgInstallStatus.Error = "no install method for " + found.name
			pkgInstallMu.Unlock()
			return
		}

		// Set DEBIAN_FRONTEND to avoid interactive prompts
		cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
		out, err := cmd.CombinedOutput()

		pkgInstallMu.Lock()
		pkgInstallStatus.Output = string(out)
		if err != nil {
			pkgInstallStatus.Status = "error"
			pkgInstallStatus.Error = err.Error()
		} else {
			pkgInstallStatus.Status = "done"
		}
		pkgInstallMu.Unlock()

		s.logger.Info("package installed", "package", found.name, "status", pkgInstallStatus.Status)
	}()

	jsonResponse(w, map[string]string{"status": "installing", "package": found.name})
}

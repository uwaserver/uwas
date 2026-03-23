package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"

	"github.com/uwaserver/uwas/internal/build"
	"github.com/uwaserver/uwas/internal/cronjob"
	"github.com/uwaserver/uwas/internal/filemanager"
	"github.com/uwaserver/uwas/internal/firewall"
	"github.com/uwaserver/uwas/internal/middleware"
	"github.com/uwaserver/uwas/internal/selfupdate"
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
		jsonError(w, "domain not found", http.StatusNotFound)
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		path = "."
	}
	entries, err := filemanager.List(root, path)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
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
	json.NewDecoder(r.Body).Decode(&req)
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
		"domain":     domain,
		"bytes":      bytes,
		"human":      formatBytes(bytes),
		"root":       root,
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
	json.NewDecoder(r.Body).Decode(&req)
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
	json.NewDecoder(r.Body).Decode(&req)
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
	json.NewDecoder(r.Body).Decode(&req)

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
		"message": "Restart UWAS to use the new version",
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

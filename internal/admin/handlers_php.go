package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/uwaserver/uwas/internal/auth"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/phpmanager"
	"gopkg.in/yaml.v3"
)

// SetPHPManager sets the PHP manager for the PHP API endpoints and wires up
// the domain change callback so that starting a per-domain PHP instance
// automatically updates the domain's php.fpm_address in the running config.
func (s *Server) SetPHPManager(m *phpmanager.Manager) {
	s.phpMgr = m

	// Auto-wire: when a domain PHP starts, update the running config.
	m.SetDomainChangeFunc(func(domain, fpmAddr string) {
		s.configMu.Lock()
		for i, d := range s.config.Domains {
			if d.Host == domain {
				s.config.Domains[i].PHP.FPMAddress = fpmAddr
				break
			}
		}
		s.configMu.Unlock()
		s.notifyDomainChange()
	})
}

func (s *Server) handlePHPList(w http.ResponseWriter, r *http.Request) {
	if s.phpMgr == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	statuses := s.phpMgr.Status()
	limit, offset := parsePagination(r)
	items, total := paginateSlice(statuses, limit, offset)
	jsonResponse(w, map[string]any{
		"items":  items,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

func (s *Server) handlePHPInstallInfo(w http.ResponseWriter, r *http.Request) {
	version := r.URL.Query().Get("version")
	if version == "" {
		version = "8.3"
	}
	info := phpmanager.GetInstallInfo(version)
	jsonResponse(w, info)
}

func (s *Server) handlePHPInstall(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
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
	// Constrain the version to N.N before it flows into package names / install
	// commands. exec runs without a shell so this isn't RCE, but it blocks apt
	// argument injection and bogus values. (Defense in depth; admin-gated.)
	if !validPHPVersion(req.Version) {
		jsonError(w, "invalid PHP version (expected N.N, e.g. 8.3)", http.StatusBadRequest)
		return
	}

	// Check if any install task is already running
	if active := s.taskMgr.Active(); active != nil {
		jsonError(w, fmt.Sprintf("another installation in progress: %s (%s)", active.Name, active.ID), http.StatusConflict)
		return
	}

	info := phpmanager.GetInstallInfo(req.Version)
	s.logger.Info("starting PHP install", "version", req.Version, "distro", info.Distro)

	// Task Name carries the bare version (e.g. "8.5") so dashboards rendering
	// "Installing PHP {version}" don't end up with "PHP PHP 8.5".
	task := s.taskMgr.Submit("php", req.Version, "install", s.phpInstallTaskFn(req.Version))

	jsonResponse(w, map[string]string{
		"status":  "started",
		"task_id": task.ID,
		"version": req.Version,
		"distro":  info.Distro,
	})
}

func (s *Server) handlePHPInstallStatus(w http.ResponseWriter, r *http.Request) {
	// Active or recently-finished PHP task. ActiveByType only returns running/queued,
	// so for done/error we fall back to scanning the task list for the latest "php" task.
	if t := s.taskMgr.ActiveByType("php"); t != nil {
		jsonResponse(w, map[string]interface{}{
			"status":  t.Status,
			"output":  t.Output,
			"error":   t.Error,
			"task_id": t.ID,
			"version": t.Name,
		})
		return
	}
	if t := s.taskMgr.LatestByType("php"); t != nil {
		jsonResponse(w, map[string]interface{}{
			"status":  t.Status,
			"output":  t.Output,
			"error":   t.Error,
			"task_id": t.ID,
			"version": t.Name,
		})
		return
	}
	jsonResponse(w, map[string]string{"status": "idle"})
}

func (s *Server) handlePHPConfig(w http.ResponseWriter, r *http.Request) {
	if s.phpMgr == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	version := r.PathValue("version")
	cfg, err := s.phpMgr.GetConfig(version)
	if err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	jsonResponse(w, cfg)
}

func (s *Server) handlePHPConfigUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.phpMgr == nil {
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

	if err := s.phpMgr.SetConfig(version, req.Key, req.Value); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Auto-restart PHP so updated ini takes effect
	restarted := false
	if err := s.phpMgr.RestartFPM(version); err == nil {
		restarted = true
	}
	jsonResponse(w, map[string]any{"status": "updated", "key": req.Key, "value": req.Value, "restarted": restarted})
}

func (s *Server) handlePHPExtensions(w http.ResponseWriter, r *http.Request) {
	if s.phpMgr == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	version := r.PathValue("version")
	exts, err := s.phpMgr.GetExtensions(version)
	if err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	jsonResponse(w, exts)
}

func (s *Server) handlePHPStart(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.phpMgr == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	version := r.PathValue("version")

	var req struct {
		ListenAddr string `json:"listen_addr"`
	}
	if r.Body != nil {
		json.NewDecoder(r.Body).Decode(&req) // optional body
	}
	if req.ListenAddr == "" {
		req.ListenAddr = "127.0.0.1:9000"
	}

	if err := s.phpMgr.StartFPM(version, req.ListenAddr); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "started", "version": version, "listen": req.ListenAddr})
}

func (s *Server) handlePHPStop(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.phpMgr == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	version := r.PathValue("version")

	if err := s.phpMgr.StopFPM(version); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "stopped", "version": version})
}

func (s *Server) handlePHPRestart(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.phpMgr == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	version := r.PathValue("version")

	if err := s.phpMgr.RestartFPM(version); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "restarted", "version": version})
}

func (s *Server) handlePHPConfigRawGet(w http.ResponseWriter, r *http.Request) {
	if s.phpMgr == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	version := r.PathValue("version")
	content, err := s.phpMgr.GetConfigRaw(version)
	if err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	jsonResponse(w, map[string]string{"content": content, "version": version})
}

func (s *Server) handlePHPConfigRawPut(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.phpMgr == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20) // 2MB
	version := r.PathValue("version")
	var req struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if err := s.phpMgr.SetConfigRaw(version, req.Content); err != nil {
		s.recordAuditR(r, "php.config_raw_put", fmt.Sprintf("version: %s", version), false)
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.logger.Info("PHP config raw updated", "version", version)
	// Auto-restart PHP so updated ini takes effect
	restarted := false
	if err := s.phpMgr.RestartFPM(version); err == nil {
		restarted = true
	}
	s.recordAuditR(r, "php.config_raw_put", fmt.Sprintf("version: %s, bytes: %d, restarted: %t", version, len(req.Content), restarted), true)
	jsonResponse(w, map[string]any{"status": "saved", "version": version, "restarted": restarted})
}

func (s *Server) handlePHPEnable(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.phpMgr == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	version := r.PathValue("version")
	s.phpMgr.EnableVersion(version)
	s.logger.Info("PHP version enabled", "version", version)
	s.recordAuditR(r, "php.enable", fmt.Sprintf("version: %s", version), true)
	jsonResponse(w, map[string]string{"status": "enabled", "version": version})
}

func (s *Server) handlePHPDisable(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.phpMgr == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	version := r.PathValue("version")
	if err := s.phpMgr.DisableVersion(version); err != nil {
		s.recordAuditR(r, "php.disable", fmt.Sprintf("version: %s", version), false)
		jsonError(w, err.Error(), http.StatusConflict)
		return
	}
	s.logger.Info("PHP version disabled", "version", version)
	s.recordAuditR(r, "php.disable", fmt.Sprintf("version: %s", version), true)
	jsonResponse(w, map[string]string{"status": "disabled", "version": version})
}

// --- Per-domain PHP endpoints ---

func (s *Server) handlePHPDomainsList(w http.ResponseWriter, r *http.Request) {
	if s.phpMgr == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	jsonResponse(w, s.phpMgr.GetDomainInstances())
}

func (s *Server) handlePHPDomainAssign(w http.ResponseWriter, r *http.Request) {
	if s.phpMgr == nil {
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
	if !s.requireDomainAccess(w, r, req.Domain, "php.assign") {
		return
	}

	// Find domain root from config for open_basedir isolation
	var domRoot string
	s.configMu.RLock()
	for _, dom := range s.config.Domains {
		if dom.Host == req.Domain {
			domRoot = dom.Root
			break
		}
	}
	s.configMu.RUnlock()
	dp, err := s.phpMgr.AssignDomainWithRoot(req.Domain, req.Version, domRoot)
	if err != nil {
		jsonError(w, err.Error(), http.StatusConflict)
		return
	}

	// Persist FPM address to domain config so it survives restart
	s.configMu.Lock()
	for i, dom := range s.config.Domains {
		if dom.Host == req.Domain {
			s.config.Domains[i].PHP.FPMAddress = dp.ListenAddr
			break
		}
	}
	s.configMu.Unlock()
	s.persistConfig()
	s.notifyDomainChange()

	// Start the PHP process
	if err := s.phpMgr.StartDomain(req.Domain); err != nil {
		s.logger.Warn("PHP start after assign failed", "domain", req.Domain, "error", err)
	}

	s.recordAuditR(r, "php.assign", req.Domain+": PHP "+req.Version+" → "+dp.ListenAddr, true)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(dp)
}

func (s *Server) handlePHPDomainUnassign(w http.ResponseWriter, r *http.Request) {
	if s.phpMgr == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	domain := r.PathValue("domain")
	if !s.requireDomainAccess(w, r, domain, "php.unassign") {
		return
	}
	s.phpMgr.UnassignDomain(domain)
	jsonResponse(w, map[string]string{"status": "unassigned", "domain": domain})
}

func (s *Server) handlePHPDomainStart(w http.ResponseWriter, r *http.Request) {
	if s.phpMgr == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	domain := r.PathValue("domain")

	// Check domain permissions for non-admin users
	if s.authMgr != nil {
		if user, ok := auth.UserFromContext(r.Context()); ok && user.Role != auth.RoleAdmin {
			if !s.authMgr.CanManageDomain(user, domain) {
				s.recordAuditR(r, "php.domain_start", "domain: "+domain+" (forbidden)", false)
				jsonError(w, "forbidden: cannot manage this domain", http.StatusForbidden)
				return
			}
		}
	}

	if err := s.phpMgr.StartDomain(domain); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "started", "domain": domain})
}

func (s *Server) handlePHPDomainStop(w http.ResponseWriter, r *http.Request) {
	if s.phpMgr == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	domain := r.PathValue("domain")

	// Check domain permissions for non-admin users
	if s.authMgr != nil {
		if user, ok := auth.UserFromContext(r.Context()); ok && user.Role != auth.RoleAdmin {
			if !s.authMgr.CanManageDomain(user, domain) {
				s.recordAuditR(r, "php.domain_stop", "domain: "+domain+" (forbidden)", false)
				jsonError(w, "forbidden: cannot manage this domain", http.StatusForbidden)
				return
			}
		}
	}

	if err := s.phpMgr.StopDomain(domain); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "stopped", "domain": domain})
}

func (s *Server) handlePHPDomainConfigGet(w http.ResponseWriter, r *http.Request) {
	if s.phpMgr == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	domain := r.PathValue("domain")

	// Check domain permissions for non-admin users
	if s.authMgr != nil {
		if user, ok := auth.UserFromContext(r.Context()); ok && user.Role != auth.RoleAdmin {
			if !s.authMgr.CanManageDomain(user, domain) {
				s.recordAuditR(r, "php.domain_config_get", "domain: "+domain+" (forbidden)", false)
				jsonError(w, "forbidden: cannot manage this domain", http.StatusForbidden)
				return
			}
		}
	}

	cfg := s.phpMgr.GetDomainConfig(domain)
	if cfg == nil {
		jsonError(w, "domain not found or no PHP assignment", http.StatusNotFound)
		return
	}
	jsonResponse(w, cfg)
}

func (s *Server) handlePHPDomainConfigPut(w http.ResponseWriter, r *http.Request) {
	if s.phpMgr == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	domain := r.PathValue("domain")

	// Check domain permissions for non-admin users
	if s.authMgr != nil {
		if user, ok := auth.UserFromContext(r.Context()); ok && user.Role != auth.RoleAdmin {
			if !s.authMgr.CanManageDomain(user, domain) {
				s.recordAuditR(r, "php.domain_config_put", "domain: "+domain+" (forbidden)", false)
				jsonError(w, "forbidden: cannot manage this domain", http.StatusForbidden)
				return
			}
		}
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

	if err := s.phpMgr.SetDomainConfig(domain, req.Key, req.Value); err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}

	// Persist overrides to domain YAML so they survive restarts
	s.persistDomainPHPOverrides(domain)

	// Restart domain PHP so updated config takes effect
	restarted := false
	if err := s.phpMgr.RestartDomain(domain); err == nil {
		restarted = true
	}
	jsonResponse(w, map[string]any{"status": "updated", "domain": domain, "key": req.Key, "value": req.Value, "restarted": restarted})
}

func (s *Server) persistDomainPHPOverrides(domain string) {
	overrides := s.phpMgr.GetDomainConfig(domain)

	path, err := s.domainFilePath(domain)
	if err != nil {
		s.logger.Warn("cannot persist PHP overrides: bad domain path", "domain", domain, "error", err)
		return
	}

	data, err := os.ReadFile(path)
	if err != nil {
		s.logger.Warn("cannot persist PHP overrides: read failed", "domain", domain, "error", err)
		return
	}

	var domCfg config.Domain
	if err := yaml.Unmarshal(data, &domCfg); err != nil {
		s.logger.Warn("cannot persist PHP overrides: parse failed", "domain", domain, "error", err)
		return
	}

	domCfg.PHP.ConfigOverrides = overrides

	out, err := yaml.Marshal(&domCfg)
	if err != nil {
		s.logger.Warn("cannot persist PHP overrides: marshal failed", "domain", domain, "error", err)
		return
	}

	// Crash-safe write: unique temp file + fsync + rename.
	if err := atomicWriteFile(path, out, 0600); err != nil {
		s.logger.Warn("cannot persist PHP overrides: write failed", "domain", domain, "error", err)
		return
	}

	s.logger.Info("persisted PHP config overrides", "domain", domain, "count", len(overrides))
}

// phpRunInstall is a test seam for the PHP install path, which shells out to
// apt / add-apt-repository. TestMain points it at a no-op.
var phpRunInstall = phpmanager.RunInstall

// validPHPVersion reports whether s is a bare PHP version of the form N.N
// (e.g. "8.3"). Used to sanitize the version before it reaches package names
// and install commands.
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

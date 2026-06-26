package admin

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/uwaserver/uwas/internal/auth"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/cronjob"
	"github.com/uwaserver/uwas/internal/pathsafe"
	"github.com/uwaserver/uwas/internal/siteuser"
	"github.com/uwaserver/uwas/internal/webhook"
	"gopkg.in/yaml.v3"
)

func (s *Server) handleDomains(w http.ResponseWriter, r *http.Request) {
	type domainInfo struct {
		Host           string   `json:"host"`
		MainHost       string   `json:"main_host,omitempty"`
		CanonicalHost  string   `json:"canonical_host,omitempty"`
		IP             string   `json:"ip,omitempty"`
		Aliases        []string `json:"aliases"`
		Type           string   `json:"type"`
		SSL            string   `json:"ssl"`
		ForceSSL       bool     `json:"force_ssl"`
		Root           string   `json:"root,omitempty"`
		CloudflareOnly bool     `json:"cloudflare_only"`
	}

	// Get current user for domain filtering
	var allowedDomains map[string]bool
	if s.authMgr != nil {
		if user, ok := auth.UserFromContext(r.Context()); ok {
			if user.Role != auth.RoleAdmin {
				// Resellers and users can only see their assigned domains
				allowedDomains = make(map[string]bool)
				for _, d := range user.Domains {
					allowedDomains[d] = true
				}
			}
		}
	}

	s.configMu.RLock()
	domains := make([]domainInfo, 0)
	seenHosts := make(map[string]struct{})
	for _, d := range s.config.Domains {
		displayHost := canonicalDomainHostname(d.Host)
		if displayHost == "" {
			displayHost = normalizeDomainHostname(d.Host)
		}
		if isImplicitWWWRedirectForDomains(d, s.config.Domains) {
			continue
		}
		if _, ok := seenHosts[displayHost]; ok {
			continue
		}
		seenHosts[displayHost] = struct{}{}
		// Filter domains for non-admin users
		if allowedDomains != nil && !allowedDomains[d.Host] && !allowedDomains[displayHost] {
			continue
		}
		domains = append(domains, domainInfo{
			Host:           displayHost,
			MainHost:       mainDomainHostname(d),
			CanonicalHost:  normalizeCanonicalHostPreference(d.CanonicalHost),
			IP:             d.IP,
			Aliases:        publicDomainAliases(d),
			Type:           d.Type,
			SSL:            d.SSL.Mode,
			ForceSSL:       d.SSL.ForceSSL,
			Root:           d.Root,
			CloudflareOnly: d.Security.CloudflareOnly,
		})
	}
	s.configMu.RUnlock()
	limit, offset := parsePagination(r)
	domains, total := paginateSlice(domains, limit, offset)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"items":  domains,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

// removeDomainFile deletes the YAML file for a host from domains.d/. Called by
// handleDeleteDomain after the in-memory state has been updated; safe because
// the caller has explicitly identified the host to remove.
func (s *Server) removeDomainFile(host string) {
	if s.configPath == "" {
		return
	}
	domainsDir := s.config.DomainsDir
	if domainsDir == "" {
		domainsDir = "domains.d"
	}
	if !filepath.IsAbs(domainsDir) {
		domainsDir = filepath.Join(filepath.Dir(s.configPath), domainsDir)
	}
	clean := strings.ReplaceAll(host, ":", "_")
	clean = filepath.Base(clean)
	for _, ext := range []string{".yaml", ".yml"} {
		path := filepath.Join(domainsDir, clean+ext)
		if err := os.Remove(path); err == nil {
			s.logger.Info("removed domain file", "path", path)
		}
	}
}

func (s *Server) handleAddDomain(w http.ResponseWriter, r *http.Request) {
	// Check domain permissions for non-admin users
	if s.authMgr != nil {
		user, ok := auth.UserFromContext(r.Context())
		if ok && user.Role != auth.RoleAdmin {
			// Query param host is optional; actual JSON body host is validated below.
			if qHost := r.URL.Query().Get("host"); qHost != "" {
				if !s.authMgr.CanManageDomain(user, qHost) {
					s.recordAuditR(r, "domain.create", "domain: "+qHost+" (forbidden)", false)
					jsonError(w, "forbidden: cannot manage this domain", http.StatusForbidden)
					return
				}
			}
		}
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	var d config.Domain
	if err := json.Unmarshal(body, &d); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	aliasOptions, err := parseDomainAliasOptions(body)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	if d.Host == "" {
		jsonError(w, "host is required", http.StatusBadRequest)
		return
	}
	if d.Type == string(config.DomainTypeRedirect) && len(d.Aliases) > 0 {
		jsonError(w, "redirect domains cannot have aliases; create separate redirect domains instead", http.StatusBadRequest)
		return
	}
	if err := validateRequestedDomainAliases(d.Host, d.Aliases); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	normalizeDomainHostnames(&d)
	if err := validateRequestedDomainAliases(d.Host, d.Aliases); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if d.Type == string(config.DomainTypeRedirect) && len(d.Aliases) > 0 {
		jsonError(w, "redirect domains cannot have aliases; create separate redirect domains instead", http.StatusBadRequest)
		return
	}
	redirectAliases := []string(nil)
	explicitRedirectAliases := []string(nil)
	if aliasOptions.redirect {
		explicitRedirectAliases = append(explicitRedirectAliases, d.Aliases...)
		redirectAliases = append(redirectAliases, explicitRedirectAliases...)
		d.Aliases = nil
	}
	applyDomainCanonicalPreference(&d, aliasOptions)
	redirectAliases = uniqueNormalizedHostnames(redirectAliases)
	if err := validateRequestedDomainAliases(d.Host, d.Aliases); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Check domain permissions for resellers
	if s.authMgr != nil {
		user, ok := auth.UserFromContext(r.Context())
		if ok && user.Role != auth.RoleAdmin {
			if !s.authMgr.CanManageDomain(user, d.Host) {
				s.recordAuditR(r, "domain.create", "domain: "+d.Host+" (forbidden)", false)
				jsonError(w, "forbidden: cannot manage this domain", http.StatusForbidden)
				return
			}
		}
	}

	if !isValidHostname(d.Host) {
		jsonError(w, "invalid hostname: must be a valid domain name", http.StatusBadRequest)
		return
	}
	// Mass-assignment protection: non-admin users cannot set sensitive fields.
	if s.authMgr != nil {
		if user, ok := auth.UserFromContext(r.Context()); ok && user.Role != auth.RoleAdmin {
			d.SSL = config.SSLConfig{}
			d.Security = config.SecurityConfig{}
			d.Cache = config.DomainCache{}
			d.Compression = config.CompressionConfig{}
			d.BasicAuth = config.BasicAuthConfig{}
			d.Resources = config.ResourceLimits{}
			d.Aliases = nil
			d.Htaccess = config.HtaccessConfig{}
			d.Locations = nil
			d.PHP.FPMAddress = ""
		}
	}

	if d.Type == "" {
		d.Type = "static"
	}
	if d.SSL.Mode == "" {
		d.SSL.Mode = "auto"
	}
	// Proxy/redirect domains do not serve files, so ignore any legacy/UI root.
	if !domainTypeUsesWebRoot(d.Type) {
		d.Root = ""
	}

	// ── Pre-save validation ──
	if err := validateDomainConfig(&d, s); err != nil {
		jsonError(w, "validation failed: "+err.Error(), http.StatusBadRequest)
		return
	}

	s.configMu.Lock()

	removeImplicitWWWRedirectDomains(&s.config.Domains, d.Host, -1)

	// Check for duplicate hostnames across hosts and aliases. Apex and www are
	// one site identity, so they must never become separate records.
	for _, host := range domainHostnames(d) {
		if conflict := findDomainHostnameConflict(s.config.Domains, -1, host); conflict != "" {
			s.configMu.Unlock()
			s.recordAuditR(r, "domain.create", "domain: "+d.Host+" (duplicate hostname)", false)
			jsonError(w, fmt.Sprintf("hostname %q is already configured on %s", host, conflict), http.StatusConflict)
			return
		}
	}
	for _, alias := range explicitRedirectAliases {
		if conflict := findDomainHostnameConflict(s.config.Domains, -1, alias); conflict != "" {
			s.configMu.Unlock()
			s.recordAuditR(r, "domain.create", "domain: "+d.Host+" (duplicate redirect alias)", false)
			jsonError(w, fmt.Sprintf("alias %q is already configured on %s", alias, conflict), http.StatusConflict)
			return
		}
	}
	// ── Auto-fill defaults based on domain type ──

	if domainTypeUsesWebRoot(d.Type) {
		webRoot := s.config.Global.WebRoot
		if webRoot == "" {
			webRoot = "/var/www"
		}
		if d.Root == "" {
			d.Root = filepath.Join(webRoot, d.Host, "public_html")
		}
	}

	// PHP defaults
	if d.Type == "php" {
		if len(d.PHP.IndexFiles) == 0 {
			d.PHP.IndexFiles = []string{"index.php", "index.html"}
		}
		// FPM address will be set by auto-assign after unlock
		d.Htaccess = config.HtaccessConfig{Mode: "import"}
		if !d.Security.WAF.Enabled {
			d.Security.WAF.Enabled = true
		}
		if len(d.Security.BlockedPaths) == 0 {
			d.Security.BlockedPaths = []string{".git", ".env", "wp-config.php"}
		}
	}

	// Static defaults
	if d.Type == "static" {
		d.Compression = config.CompressionConfig{
			Enabled:    true,
			Algorithms: []string{"gzip", "br"},
		}
	}

	// Cache defaults (all types except redirect)
	if d.Type != "redirect" && !d.Cache.Enabled {
		d.Cache.Enabled = true
		d.Cache.TTL = 3600
	}
	if d.Root != "" {
		// Create web root with parent directory
		parentDir := filepath.Dir(d.Root) // e.g. /var/www/example.com
		if err := os.MkdirAll(d.Root, 0755); err != nil {
			s.logger.Warn("failed to create web root", "path", d.Root, "error", err)
		}
		{
			idx := filepath.Join(d.Root, "index.html")
			if _, err := os.Stat(idx); os.IsNotExist(err) {
				placeholder := fmt.Sprintf(`<!DOCTYPE html>
<html><head><title>%s</title></head>
<body style="font-family:system-ui;display:flex;justify-content:center;align-items:center;min-height:100vh;margin:0;background:#0f172a;color:#e2e8f0">
<div style="text-align:center"><h1>%s</h1><p style="color:#94a3b8">Site is ready. Upload your files via SFTP or place them in:<br><code>%s</code></p></div>
</body></html>`, d.Host, d.Host, d.Root)
				if err := os.WriteFile(idx, []byte(placeholder), 0644); err != nil {
					s.logger.Warn("failed to write placeholder index", "path", idx, "error", err)
				}
			}
		}
		// Set ownership to www-data so PHP/WordPress can write files
		// (.htaccess, wp-content/uploads, cache, etc.)
		if runtime.GOOS == "linux" {
			exec.Command("chown", "-R", "www-data:www-data", parentDir).Run()
			exec.Command("chmod", "-R", "755", parentDir).Run()
			// public_html needs to be writable for WordPress uploads
			exec.Command("chmod", "-R", "775", d.Root).Run()
		}
	}

	// Auto-create .htaccess for PHP domains (WordPress/Laravel friendly)
	if d.Type == "php" && d.Root != "" {
		htaccessPath := filepath.Join(d.Root, ".htaccess")
		if _, err := os.Stat(htaccessPath); os.IsNotExist(err) {
			htContent := "# UWAS — PHP front-controller rewrite\n" +
				"# Works with WordPress, Laravel, Drupal, etc.\n" +
				"<IfModule mod_rewrite.c>\n" +
				"RewriteEngine On\n" +
				"RewriteBase /\n" +
				"RewriteRule ^index\\.php$ - [L]\n" +
				"RewriteCond %{REQUEST_FILENAME} !-f\n" +
				"RewriteCond %{REQUEST_FILENAME} !-d\n" +
				"RewriteRule . /index.php [L]\n" +
				"</IfModule>\n"
			os.WriteFile(htaccessPath, []byte(htContent), 0644)
		}
	}

	// Auto-set per-domain access log path if not configured.
	if d.AccessLog.Path == "" && d.Root != "" {
		logDir := filepath.Join(filepath.Dir(d.Root), "logs")
		os.MkdirAll(logDir, 0755)
		d.AccessLog.Path = filepath.Join(logDir, "access.log")
	}

	// PHP assignment: use user-provided FPM address, or auto-assign.
	if d.Type == "php" && s.phpMgr != nil {
		if d.PHP.FPMAddress != "" {
			// User explicitly provided FPM address — register so it shows in PHP page.
			phpStatus := s.phpMgr.Status()
			if len(phpStatus) == 0 {
				_ = s.phpMgr.Detect()
				phpStatus = s.phpMgr.Status()
			}
			ver := ""
			if len(phpStatus) > 0 {
				ver = phpStatus[0].Version
			}
			s.phpMgr.RegisterExistingDomain(d.Host, ver, d.PHP.FPMAddress, d.Root, d.PHP.ConfigOverrides)
			s.logger.Info("using user-provided PHP address", "domain", d.Host, "address", d.PHP.FPMAddress)
		} else {
			// Auto-assign: prefer FPM socket, fallback to CGI TCP port
			phpStatus := s.phpMgr.Status()
			if len(phpStatus) == 0 {
				_ = s.phpMgr.Detect()
				phpStatus = s.phpMgr.Status()
			}
			if len(phpStatus) > 0 {
				// Pick best version: prefer FPM over CGI
				version := phpStatus[0].Version
				for _, st := range phpStatus {
					if strings.Contains(st.SAPI, "fpm") && st.Running {
						version = st.Version
						break
					}
				}
				if inst, err := s.phpMgr.AssignDomainWithRoot(d.Host, version, d.Root); err == nil {
					d.PHP.FPMAddress = inst.ListenAddr
					if err := s.phpMgr.StartDomain(d.Host); err != nil {
						s.logger.Warn("PHP auto-start failed", "domain", d.Host, "error", err)
					} else {
						s.logger.Info("auto-assigned PHP to domain", "domain", d.Host, "version", version, "listen", inst.ListenAddr)
					}
				} else {
					s.logger.Warn("PHP auto-assign failed", "domain", d.Host, "error", err)
				}
			}
		}
	}

	// Reject type=app — apps are first-class objects now. Operator
	// should create a standalone app via /api/v1/apps then add a
	// type=proxy domain pointing at apps://<name>.
	if d.Type == "app" {
		s.configMu.Unlock()
		jsonError(w,
			"type=app is no longer supported. Create the app via /api/v1/apps then add a type=proxy domain with apps://<name> upstream.",
			http.StatusBadRequest)
		return
	}

	s.config.Domains = append(s.config.Domains, d)
	if len(redirectAliases) > 0 {
		for _, alias := range redirectAliases {
			s.config.Domains = append(s.config.Domains, newCanonicalRedirectAliasDomain(alias, d.Host, aliasOptions.redirectCode, aliasOptions.preservePath))
		}
	}
	s.configMu.Unlock()

	s.recordAuditR(r, "domain.create", "domain: "+d.Host, true)
	s.notifyDomainChange()

	// Fire webhook event
	if s.webhookMgr != nil {
		s.webhookMgr.Fire(webhook.EventDomainAdd, map[string]any{
			"host": d.Host,
			"type": d.Type,
			"root": d.Root,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(d)
}

func (s *Server) handleDeleteDomain(w http.ResponseWriter, r *http.Request) {
	if !s.requirePin(w, r) {
		return
	}
	host := canonicalDomainHostname(r.PathValue("host"))
	cleanup := r.URL.Query().Get("cleanup") == "true"
	confirm := r.URL.Query().Get("confirm") == "true"
	if !confirm {
		jsonError(w, "missing confirmation: add ?confirm=true", http.StatusBadRequest)
		return
	}

	// Protect default/system domains from deletion
	if host == "localhost" || host == "localhost:80" || host == "localhost:443" || host == "127.0.0.1" {
		jsonError(w, "cannot delete default domain: "+host, http.StatusForbidden)
		return
	}

	// Check domain permissions for non-admin users
	if s.authMgr != nil {
		user, ok := auth.UserFromContext(r.Context())
		if ok && user.Role != auth.RoleAdmin {
			if !s.authMgr.CanManageDomain(user, host) {
				s.recordAuditR(r, "domain.delete", "domain: "+host+" (forbidden)", false)
				jsonError(w, "forbidden: cannot manage this domain", http.StatusForbidden)
				return
			}
		}
	}

	s.configMu.Lock()
	found := false
	var domainRoot string
	for i, d := range s.config.Domains {
		if canonicalDomainHostname(d.Host) == host {
			domainRoot = d.Root
			s.config.Domains = append(s.config.Domains[:i], s.config.Domains[i+1:]...)
			found = true
			break
		}
	}
	s.configMu.Unlock()

	// Invalidate cached docroot BEFORE updating config; prevents stale handle on Windows.
	// Without this, domain deletion + re-creation with the same root would silently fail
	// because the deleted domain's os.Root handle is still held.
	if found && domainRoot != "" {
		pathsafe.InvalidateBase(domainRoot)
	}

	if !found {
		s.recordAuditR(r, "domain.delete", "domain: "+host+" (not found)", false)
		jsonError(w, "domain not found", http.StatusNotFound)
		return
	}

	// Explicit YAML file deletion. Previously persistConfig's orphan cleanup
	// did this implicitly, but that scan deleted *any* domains.d/ file not in
	// memory — including ones we hadn't loaded due to a transient failure. We
	// now know exactly which host the operator asked to delete, so remove
	// only that file.
	s.removeDomainFile(host)

	// Cleanup: stop PHP, stop app, remove cron jobs, purge cache, remove SFTP user, delete files
	if cleanup {
		if s.phpMgr != nil {
			s.phpMgr.StopDomain(host)
			s.phpMgr.UnassignDomain(host)
		}
		if s.cache != nil {
			s.cache.PurgeByTag("site:" + host)
		}
		cronjob.RemoveByDomain(host)
		siteuser.DeleteUser(host)
		// Delete web root — only the domain-specific directory.
		// Safety: never delete system dirs, shared roots, or paths too close to /
		if domainRoot != "" {
			// Find the domain-specific directory to delete.
			// Convention: /var/www/{domain}/public_html → delete /var/www/{domain}
			// But if root IS /var/www (no subdirectory), don't delete anything.
			absRoot, _ := filepath.Abs(domainRoot)
			parent := filepath.Dir(absRoot) // e.g. /var/www/domain.com

			protectedPaths := map[string]bool{
				"/": true, "/var": true, "/var/www": true, "/home": true,
				"/etc": true, "/tmp": true, "/root": true, "/opt": true,
				"/usr": true, "/srv": true, "/var/lib": true, "/var/log": true,
			}

			// Only delete if:
			// 1. Parent is a domain-specific dir (not a system/shared path)
			// 2. Parent has at least 3 path components (e.g. /var/www/domain.com)
			// 3. Parent is not the webRoot itself
			webRoot := s.config.Global.WebRoot
			if webRoot == "" {
				webRoot = "/var/www"
			}
			absWebRoot, _ := filepath.Abs(webRoot)

			safe := parent != "" &&
				!protectedPaths[parent] &&
				!protectedPaths[absRoot] &&
				parent != absWebRoot &&
				absRoot != absWebRoot &&
				len(strings.Split(parent, string(filepath.Separator))) >= 4

			if safe {
				os.RemoveAll(parent)
				s.logger.Info("deleted domain files", "domain", host, "path", parent)
			} else if absRoot != absWebRoot && !protectedPaths[absRoot] {
				// Try deleting just the root directory itself (not parent)
				os.RemoveAll(absRoot)
				s.logger.Info("deleted domain root", "domain", host, "path", absRoot)
			} else {
				s.logger.Warn("skipped file deletion (protected path)", "domain", host, "path", parent)
			}
		}
	}

	s.recordAuditR(r, "domain.delete", "domain: "+host, true)
	s.notifyDomainChange()

	// Fire webhook event
	if s.webhookMgr != nil {
		s.webhookMgr.Fire(webhook.EventDomainDelete, map[string]any{
			"host":    host,
			"cleanup": cleanup,
		})
	}

	jsonResponse(w, map[string]string{"status": "deleted", "cleanup": fmt.Sprintf("%v", cleanup)})
}

func (s *Server) handleUpdateDomain(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	host := canonicalDomainHostname(r.PathValue("host"))
	var currentUser *auth.User

	// Check domain permissions for non-admin users
	if s.authMgr != nil {
		user, ok := auth.UserFromContext(r.Context())
		if ok {
			currentUser = user
		}
		if ok && user.Role != auth.RoleAdmin {
			if !s.authMgr.CanManageDomain(user, host) {
				s.recordAuditR(r, "domain.update", "domain: "+host+" (forbidden)", false)
				jsonError(w, "forbidden: cannot manage this domain", http.StatusForbidden)
				return
			}
		}
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 10<<20)) // 10MB limit
	if err != nil {
		if err == io.ErrUnexpectedEOF {
			jsonError(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		jsonError(w, "failed to read request body", http.StatusBadRequest)
		return
	}

	var d config.Domain
	if err := json.Unmarshal(body, &d); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	aliasOptions, err := parseDomainAliasOptions(body)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if len(d.Aliases) > 0 && s.domainTypeForHost(host) == string(config.DomainTypeRedirect) {
		s.recordAuditR(r, "domain.update", "domain: "+host+" (redirect aliases rejected)", false)
		jsonError(w, "redirect domains cannot have aliases; create separate redirect domains instead", http.StatusBadRequest)
		return
	}
	if err := validateRequestedDomainAliases(firstNonEmpty(d.Host, host), d.Aliases); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if d.Host == "" {
		d.Host = host
	}
	normalizeDomainHostnames(&d)
	redirectAliases := []string(nil)
	if aliasOptions.redirect {
		redirectAliases = append(redirectAliases, d.Aliases...)
		d.Aliases = nil
	}
	if d.Host != "" && !isValidHostname(d.Host) {
		s.recordAuditR(r, "domain.update", "domain: "+host+" (invalid hostname)", false)
		jsonError(w, "invalid hostname: must be a valid domain name", http.StatusBadRequest)
		return
	}
	if currentUser != nil && currentUser.Role != auth.RoleAdmin && d.Host != "" && d.Host != host {
		if !s.authMgr.CanManageDomain(currentUser, d.Host) {
			s.recordAuditR(r, "domain.update", "domain: "+host+" (forbidden rename)", false)
			jsonError(w, "forbidden: cannot rename to this domain", http.StatusForbidden)
			return
		}
	}

	var raw map[string]json.RawMessage
	_ = json.Unmarshal(body, &raw)
	_, hasAliases := raw["aliases"]
	_, hasLocations := raw["locations"]
	_, hasBasicAuth := raw["basic_auth"]
	_, hasSecurity := raw["security"]
	_, hasCache := raw["cache"]
	_, hasSSL := raw["ssl"]
	hasSSLForce := false
	if rawSSL, ok := raw["ssl"]; ok {
		var sslRaw map[string]json.RawMessage
		if err := json.Unmarshal(rawSSL, &sslRaw); err == nil {
			_, hasSSLForce = sslRaw["force_ssl"]
		}
	}
	_, hasCompression := raw["compression"]
	_, hasResources := raw["resources"]
	_, hasHtaccess := raw["htaccess"]
	_, hasCanonical := raw["canonical_host"]
	replaceMode := r.URL.Query().Get("replace") == "true"

	// Mass-assignment protection: non-admin users cannot modify sensitive fields.
	if currentUser != nil && currentUser.Role != auth.RoleAdmin {
		sensitive := []string{}
		if hasSSL {
			sensitive = append(sensitive, "ssl")
		}
		if hasSecurity {
			sensitive = append(sensitive, "security")
		}
		if hasCache {
			sensitive = append(sensitive, "cache")
		}
		if hasCompression {
			sensitive = append(sensitive, "compression")
		}
		if hasBasicAuth {
			sensitive = append(sensitive, "basic_auth")
		}
		if hasResources {
			sensitive = append(sensitive, "resources")
		}
		if hasAliases {
			sensitive = append(sensitive, "aliases")
		}
		if hasHtaccess {
			sensitive = append(sensitive, "htaccess")
		}
		if hasLocations {
			sensitive = append(sensitive, "locations")
		}
		if rawPHP, ok := raw["php"]; ok {
			var phpRaw map[string]json.RawMessage
			if err := json.Unmarshal(rawPHP, &phpRaw); err == nil {
				if _, ok := phpRaw["fpm_address"]; ok {
					sensitive = append(sensitive, "php.fpm_address")
				}
			}
		}
		// Structural/security-sensitive top-level fields non-admins may never set.
		// Omitting these enables privilege escalation: root → filesystem escape
		// (the file manager jails to the domain root); type/proxy/redirect → route
		// hijack & SSRF; ip → bind to another address; app → process control;
		// internal_aliases → X-Accel/X-Sendfile path widening; access_log →
		// arbitrary log path; webhook_secret → forged webhook signatures.
		for _, field := range []string{"root", "type", "ip", "proxy", "redirect", "app", "internal_aliases", "access_log", "webhook_secret"} {
			if _, ok := raw[field]; ok {
				sensitive = append(sensitive, field)
			}
		}
		if len(sensitive) > 0 {
			s.recordAuditR(r, "domain.update", "domain: "+host+" (forbidden fields: "+strings.Join(sensitive, ", ")+")", false)
			jsonError(w, "forbidden: non-admin users cannot modify "+strings.Join(sensitive, ", "), http.StatusForbidden)
			return
		}
	}

	// Field-by-field merge moved to config.MergeDomain (refactor.md A23).
	// It is pure and independently unit-tested in config/merge_test.go;
	// this handler stays focused on RBAC, validation, and persistence.
	patchFields := config.DomainPatchFields{
		HasAliases:     hasAliases,
		HasLocations:   hasLocations,
		HasBasicAuth:   hasBasicAuth,
		HasSecurity:    hasSecurity,
		HasCache:       hasCache,
		HasCompression: hasCompression,
		HasHtaccess:    hasHtaccess,
		HasSSL:         hasSSL,
		HasSSLForce:    hasSSLForce,
		HasResources:   hasResources,
		HasCanonical:   hasCanonical,
	}

	s.configMu.Lock()
	found := false
	for i, existing := range s.config.Domains {
		if canonicalDomainHostname(existing.Host) == host {
			merged := config.MergeDomain(existing, d, patchFields, replaceMode)
			normalizeDomainHostnames(&merged)
			if merged.Type == string(config.DomainTypeRedirect) {
				if len(redirectAliases) > 0 {
					s.configMu.Unlock()
					s.recordAuditR(r, "domain.update", "domain: "+host+" (redirect aliases rejected)", false)
					jsonError(w, "redirect domains cannot have aliases; create separate redirect domains instead", http.StatusBadRequest)
					return
				}
				merged.Aliases = nil
			}

			if !isValidHostname(merged.Host) {
				s.configMu.Unlock()
				s.recordAuditR(r, "domain.update", "domain: "+host+" (invalid hostname)", false)
				jsonError(w, "invalid hostname: must be a valid domain name", http.StatusBadRequest)
				return
			}
			if merged.Host != host {
				for j := range s.config.Domains {
					if j != i && strings.EqualFold(s.config.Domains[j].Host, merged.Host) {
						s.configMu.Unlock()
						s.recordAuditR(r, "domain.update", "domain: "+host+" (duplicate rename)", false)
						jsonError(w, "domain already exists", http.StatusConflict)
						return
					}
				}
			}
			for _, candidate := range domainHostnames(merged) {
				if conflict := findDomainHostnameConflictAllowingRedirect(s.config.Domains, i, candidate, merged.Host); conflict != "" {
					s.configMu.Unlock()
					s.recordAuditR(r, "domain.update", "domain: "+host+" (duplicate hostname)", false)
					jsonError(w, fmt.Sprintf("hostname %q is already configured on %s", candidate, conflict), http.StatusConflict)
					return
				}
			}
			for _, alias := range redirectAliases {
				if conflict := findDomainHostnameConflictAllowingRedirect(s.config.Domains, i, alias, merged.Host); conflict != "" {
					s.configMu.Unlock()
					s.recordAuditR(r, "domain.update", "domain: "+host+" (duplicate redirect alias)", false)
					jsonError(w, fmt.Sprintf("alias %q is already configured on %s", alias, conflict), http.StatusConflict)
					return
				}
			}
			if err := validateDomainUpdateConfig(&merged, s); err != nil {
				s.configMu.Unlock()
				s.recordAuditR(r, "domain.update", "domain: "+host+" (validation failed)", false)
				jsonError(w, "validation failed: "+err.Error(), http.StatusBadRequest)
				return
			}

			s.config.Domains[i] = merged
			removeImplicitWWWRedirectDomains(&s.config.Domains, merged.Host, i)
			if len(redirectAliases) > 0 {
				upsertCanonicalRedirectAliasDomains(&s.config.Domains, i, redirectAliases, merged.Host, aliasOptions.redirectCode, aliasOptions.preservePath)
			}
			d = merged // use merged for subsequent operations
			found = true
			break
		}
	}
	s.configMu.Unlock()

	if !found {
		s.recordAuditR(r, "domain.update", "domain: "+host+" (not found)", false)
		jsonError(w, "domain not found", http.StatusNotFound)
		return
	}
	s.recordAuditR(r, "domain.update", "domain: "+host, true)
	if s.webhookMgr != nil {
		s.webhookMgr.Fire(webhook.EventDomainUpdate, map[string]any{
			"host": host,
		})
	}
	s.notifyDomainChange()

	// Auto-assign PHP if domain type changed to php and no assignment exists.
	if d.Type == "php" && s.phpMgr != nil {
		instances := s.phpMgr.GetDomainInstances()
		hasAssign := false
		for _, inst := range instances {
			if inst.Domain == d.Host {
				hasAssign = true
				break
			}
		}
		if !hasAssign {
			phpStatus := s.phpMgr.Status()
			if len(phpStatus) == 0 {
				_ = s.phpMgr.Detect()
				phpStatus = s.phpMgr.Status()
			}
			if len(phpStatus) > 0 {
				version := phpStatus[0].Version
				if inst, err := s.phpMgr.AssignDomainWithRoot(d.Host, version, d.Root); err == nil {
					s.phpMgr.StartDomain(d.Host)
					s.configMu.Lock()
					for i := range s.config.Domains {
						if s.config.Domains[i].Host == d.Host {
							s.config.Domains[i].PHP.FPMAddress = inst.ListenAddr
							break
						}
					}
					s.configMu.Unlock()
					s.persistConfig()
					s.notifyDomainChange() // sync VHost router with new FPM address
					s.logger.Info("auto-assigned PHP on update", "domain", d.Host, "version", version)
				}
			}
		}
	}

	// type=app is no longer a routing decision. An operator who
	// explicitly PUTs type=app via dashboard gets a deprecation error.
	if d.Type == "app" {
		jsonError(w,
			"type=app is no longer supported. Manage apps via /api/v1/apps and route domains to them with type=proxy + apps://<name>.",
			http.StatusBadRequest)
		return
	}

	jsonResponse(w, d)
}

func (s *Server) handleUnknownDomainsList(w http.ResponseWriter, r *http.Request) {
	if s.unknownHT == nil {
		jsonResponse(w, []any{})
		return
	}
	jsonResponse(w, s.unknownHT.List())
}

func (s *Server) handleUnknownDomainsAlias(w http.ResponseWriter, r *http.Request) {
	host := normalizeDomainHostname(r.PathValue("host"))

	if s.authMgr != nil {
		if user, ok := auth.UserFromContext(r.Context()); ok && user.Role != auth.RoleAdmin {
			s.recordAuditR(r, "unknown_domain.alias", "host: "+host+" (forbidden)", false)
			jsonError(w, "forbidden: admin access required", http.StatusForbidden)
			return
		}
	}
	if !isValidHostname(host) {
		jsonError(w, "invalid hostname: must be a valid domain name", http.StatusBadRequest)
		return
	}

	var req struct {
		Domain       string `json:"domain"`
		Mode         string `json:"mode,omitempty"`
		RedirectCode int    `json:"redirect_code,omitempty"`
		PreservePath *bool  `json:"preserve_path,omitempty"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	target := canonicalDomainHostname(req.Domain)
	if target == "" {
		jsonError(w, "domain is required", http.StatusBadRequest)
		return
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	switch mode {
	case "", "alias":
		mode = "redirect"
	case "redirect":
	default:
		jsonError(w, "mode must be redirect", http.StatusBadRequest)
		return
	}
	redirectCode := req.RedirectCode
	if mode == "redirect" && redirectCode == 0 {
		redirectCode = http.StatusMovedPermanently
	}
	if redirectCode != 0 {
		mode = "redirect"
	}
	if mode == "redirect" {
		switch redirectCode {
		case http.StatusMovedPermanently, http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		default:
			jsonError(w, "redirect_code must be 301, 302, 307, or 308", http.StatusBadRequest)
			return
		}
	}

	s.configMu.Lock()
	targetIndex := -1
	for i, d := range s.config.Domains {
		if canonicalDomainHostname(d.Host) == target {
			targetIndex = i
			break
		}
	}
	if targetIndex == -1 {
		s.configMu.Unlock()
		s.recordAuditR(r, "unknown_domain.alias", "host: "+host+" -> "+target+" (target not found)", false)
		jsonError(w, "target domain not found", http.StatusNotFound)
		return
	}

	if canonicalDomainHostname(host) == canonicalDomainHostname(s.config.Domains[targetIndex].Host) {
		s.config.Domains[targetIndex].Aliases = removeDomainAlias(s.config.Domains[targetIndex].Aliases, host)
		s.configMu.Unlock()
		if s.unknownHT != nil {
			s.unknownHT.Unblock(host)
			s.unknownHT.Dismiss(host)
		}
		s.notifyDomainChange()
		jsonResponse(w, map[string]string{"status": "already_primary", "host": host, "domain": target})
		return
	}

	hostIndex := -1
	for i, d := range s.config.Domains {
		if i == targetIndex {
			continue
		}
		if canonicalDomainHostname(d.Host) == canonicalDomainHostname(host) {
			hostIndex = i
			break
		}
		for _, alias := range d.Aliases {
			if canonicalDomainHostname(alias) == canonicalDomainHostname(host) {
				hostIndex = i
				break
			}
		}
		if hostIndex != -1 {
			break
		}
	}
	if hostIndex != -1 && !(mode == "redirect" && strings.EqualFold(s.config.Domains[hostIndex].Type, "redirect") && normalizeDomainHostname(s.config.Domains[hostIndex].Host) == host) {
		conflict := firstNonEmpty(s.config.Domains[hostIndex].Host, host)
		s.configMu.Unlock()
		s.recordAuditR(r, "unknown_domain.alias", "host: "+host+" (duplicate hostname)", false)
		jsonError(w, fmt.Sprintf("hostname %q is already configured on %s", host, conflict), http.StatusConflict)
		return
	}

	if mode == "redirect" {
		preservePath := true
		if req.PreservePath != nil {
			preservePath = *req.PreservePath
		}
		s.config.Domains[targetIndex].Aliases = removeDomainAlias(s.config.Domains[targetIndex].Aliases, host)
		redirectDomain := newCanonicalRedirectAliasDomain(host, s.config.Domains[targetIndex].Host, redirectCode, preservePath)
		if hostIndex != -1 {
			s.config.Domains[hostIndex] = redirectDomain
		} else {
			s.config.Domains = append(s.config.Domains, redirectDomain)
		}
		s.configMu.Unlock()

		if s.unknownHT != nil {
			s.unknownHT.Unblock(host)
			s.unknownHT.Dismiss(host)
		}
		s.recordAuditR(r, "unknown_domain.alias_redirect", fmt.Sprintf("host: %s -> %s (%d)", host, target, redirectCode), true)
		s.notifyDomainChange()
		jsonResponse(w, map[string]any{"status": "redirect", "host": host, "domain": target, "redirect_code": redirectCode})
		return
	}
}

func (s *Server) handleUnknownDomainsBlock(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")

	// Only admins can block unknown domains (global security setting)
	if s.authMgr != nil {
		if user, ok := auth.UserFromContext(r.Context()); ok && user.Role != auth.RoleAdmin {
			s.recordAuditR(r, "unknown_domain.block", "host: "+host+" (forbidden)", false)
			jsonError(w, "forbidden: admin access required", http.StatusForbidden)
			return
		}
	}

	if s.unknownHT == nil {
		jsonError(w, "tracker not available", http.StatusServiceUnavailable)
		return
	}
	s.unknownHT.Block(host)
	s.logger.Info("blocked unknown domain", "domain", host)
	jsonResponse(w, map[string]string{"status": "blocked", "host": host})
}

func (s *Server) handleUnknownDomainsUnblock(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")

	// Only admins can unblock unknown domains (global security setting)
	if s.authMgr != nil {
		if user, ok := auth.UserFromContext(r.Context()); ok && user.Role != auth.RoleAdmin {
			s.recordAuditR(r, "unknown_domain.unblock", "host: "+host+" (forbidden)", false)
			jsonError(w, "forbidden: admin access required", http.StatusForbidden)
			return
		}
	}

	if s.unknownHT == nil {
		jsonError(w, "tracker not available", http.StatusServiceUnavailable)
		return
	}
	s.unknownHT.Unblock(host)
	s.logger.Info("unblocked unknown domain", "domain", host)
	jsonResponse(w, map[string]string{"status": "unblocked", "host": host})
}

func (s *Server) handleUnknownDomainsDismiss(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")

	// Only admins can dismiss unknown domains (global security setting)
	if s.authMgr != nil {
		if user, ok := auth.UserFromContext(r.Context()); ok && user.Role != auth.RoleAdmin {
			s.recordAuditR(r, "unknown_domain.dismiss", "host: "+host+" (forbidden)", false)
			jsonError(w, "forbidden: admin access required", http.StatusForbidden)
			return
		}
	}

	if s.unknownHT == nil {
		jsonError(w, "tracker not available", http.StatusServiceUnavailable)
		return
	}
	s.unknownHT.Dismiss(host)
	jsonResponse(w, map[string]string{"status": "dismissed", "host": host})
}

// --- Domain detail ---

func (s *Server) handleDomainDetail(w http.ResponseWriter, r *http.Request) {
	host := canonicalDomainHostname(r.PathValue("host"))

	// Check domain permissions for non-admin users
	if s.authMgr != nil {
		if user, ok := auth.UserFromContext(r.Context()); ok && user.Role != auth.RoleAdmin {
			if !s.authMgr.CanManageDomain(user, host) {
				s.recordAuditR(r, "domain.read", "domain: "+host+" (forbidden)", false)
				jsonError(w, "forbidden: cannot view this domain", http.StatusForbidden)
				return
			}
		}
	}

	s.configMu.RLock()
	defer s.configMu.RUnlock()

	for _, d := range s.config.Domains {
		if canonicalDomainHostname(d.Host) == host {
			out := d
			normalizeDomainHostnames(&out)
			out.Aliases = publicDomainAliases(out)
			jsonResponse(w, out)
			return
		}
	}
	jsonError(w, "domain not found", http.StatusNotFound)
}

func (s *Server) domainTypeForHost(host string) string {
	host = canonicalDomainHostname(host)
	s.configMu.RLock()
	defer s.configMu.RUnlock()
	for _, d := range s.config.Domains {
		if canonicalDomainHostname(d.Host) == host {
			return d.Type
		}
	}
	return ""
}

// validateDomainConfig performs comprehensive pre-save validation. Delegates
// static checks (type, SSL, basic auth, proxy upstreams, cache, rate-limit)
// to config.ValidateDomain, then adds runtime-dependent checks (PHP
// availability, root-under-webroot).
func validateDomainConfig(d *config.Domain, s *Server) error {
	if err := config.ValidateDomain(d); err != nil {
		return err
	}

	webRoot := "/var/www"
	if s != nil {
		s.configMu.RLock()
		if s.config.Global.WebRoot != "" {
			webRoot = s.config.Global.WebRoot
		}
		s.configMu.RUnlock()
	}

	// PHP type: verify PHP is available (runtime state, can't live in config pkg).
	if d.Type == "php" && s != nil && s.phpMgr != nil {
		phpStatus := s.phpMgr.Status()
		activePHP := 0
		for _, p := range phpStatus {
			if !p.Disabled {
				activePHP++
			}
		}
		if activePHP == 0 {
			return fmt.Errorf("no active PHP versions available — install or enable PHP first")
		}
	}

	// Root path: must be under web root (prevent serving /etc, /root, etc.).
	// Web root is admin-runtime state; lives here, not in config.ValidateDomain.
	if d.Root != "" && d.Type != "redirect" {
		if !pathsafe.IsWithinBase(webRoot, d.Root) || !pathsafe.IsWithinBaseResolved(webRoot, d.Root) {
			return fmt.Errorf("root path must be under %s (got %s)", webRoot, d.Root)
		}
	}

	return nil
}

// validateDomainUpdateConfig is the merge/update variant: same shape checks
// as validateDomainConfig but lenient about cross-field invariants
// (config.ValidateDomainPartial) so PATCH-style updates aren't rejected for
// fields the caller intentionally didn't touch. It also enforces the
// root-under-webroot containment that the create path applies, so an update
// can't relocate a domain's root outside the web root (filesystem escape).
//
// The sole runtime caller (handleUpdateDomain) holds s.configMu while calling
// this, so WebRoot is read directly without re-locking — the RWMutex is not
// reentrant and an RLock here would deadlock.
func validateDomainUpdateConfig(d *config.Domain, s *Server) error {
	if err := config.ValidateDomainPartial(d); err != nil {
		return err
	}

	webRoot := "/var/www"
	if s != nil && s.config.Global.WebRoot != "" {
		webRoot = s.config.Global.WebRoot
	}
	if d.Root != "" && d.Type != "redirect" {
		if !pathsafe.IsWithinBase(webRoot, d.Root) || !pathsafe.IsWithinBaseResolved(webRoot, d.Root) {
			return fmt.Errorf("root path must be under %s (got %s)", webRoot, d.Root)
		}
	}
	return nil
}

func domainTypeUsesWebRoot(domainType string) bool {
	switch domainType {
	case "static", "php":
		return true
	default:
		return false
	}
}

func normalizeDomainHostnames(d *config.Domain) {
	d.Host = canonicalDomainHostname(d.Host)
	if d.Type == string(config.DomainTypeRedirect) {
		d.CanonicalHost = ""
	} else if d.Host != "" {
		d.CanonicalHost = normalizeCanonicalHostPreference(d.CanonicalHost)
	}
	seen := make(map[string]struct{}, len(d.Aliases))
	aliases := make([]string, 0, len(d.Aliases))
	for _, alias := range d.Aliases {
		alias = canonicalDomainHostname(alias)
		if alias == "" || alias == d.Host {
			continue
		}
		if _, ok := seen[alias]; ok {
			continue
		}
		seen[alias] = struct{}{}
		aliases = append(aliases, alias)
	}
	d.Aliases = aliases
}

func normalizeDomainHostname(host string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
}

func canonicalDomainHostname(host string) string {
	host = normalizeDomainHostname(host)
	if host == "" || strings.Contains(host, ":") || strings.HasPrefix(host, "*.") {
		return host
	}
	if strings.HasPrefix(host, "www.") {
		apex := strings.TrimPrefix(host, "www.")
		if apex != "" && strings.Contains(apex, ".") {
			return apex
		}
	}
	return host
}

func implicitWWWHostname(host string) string {
	host = canonicalDomainHostname(host)
	if host == "" || strings.Contains(host, ":") || strings.HasPrefix(host, "*.") || !strings.Contains(host, ".") {
		return ""
	}
	return "www." + host
}

func domainHostnames(d config.Domain) []string {
	seen := make(map[string]struct{}, 2+len(d.Aliases)*2)
	hosts := make([]string, 0, 2+len(d.Aliases)*2)
	for _, host := range append([]string{d.Host}, d.Aliases...) {
		host = canonicalDomainHostname(host)
		if host == "" {
			continue
		}
		candidates := []string{host, implicitWWWHostname(host)}
		if normalizeCanonicalHostPreference(d.CanonicalHost) == "www" && implicitWWWHostname(host) != "" {
			candidates = []string{implicitWWWHostname(host), host}
		}
		for _, candidate := range candidates {
			if candidate == "" {
				continue
			}
			if _, ok := seen[candidate]; ok {
				continue
			}
			seen[candidate] = struct{}{}
			hosts = append(hosts, candidate)
		}
	}
	return hosts
}

func mainDomainHostname(d config.Domain) string {
	host := canonicalDomainHostname(d.Host)
	if host == "" {
		return normalizeDomainHostname(d.Host)
	}
	if d.Type != string(config.DomainTypeRedirect) && normalizeCanonicalHostPreference(d.CanonicalHost) == "www" {
		if www := implicitWWWHostname(host); www != "" {
			return www
		}
	}
	return host
}

func findDomainHostnameConflict(domains []config.Domain, skipIndex int, host string) string {
	host = canonicalDomainHostname(host)
	if host == "" {
		return ""
	}
	for i, d := range domains {
		if i == skipIndex {
			continue
		}
		if canonicalDomainHostname(d.Host) == host {
			return d.Host
		}
		for _, alias := range d.Aliases {
			if canonicalDomainHostname(alias) == host {
				return d.Host
			}
		}
	}
	return ""
}

// isValidHostname is kept as a package-local alias for readability at the
// call sites. config.IsValidHostname is the authoritative implementation.
func isValidHostname(s string) bool { return config.IsValidHostname(s) }

// --- Per-domain raw YAML editor (moved from api.go) ---

func (s *Server) handleDomainRawGet(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")

	// Check domain permissions for non-admin users
	if s.authMgr != nil {
		if user, ok := auth.UserFromContext(r.Context()); ok && user.Role != auth.RoleAdmin {
			if !s.authMgr.CanManageDomain(user, host) {
				s.recordAuditR(r, "domain.read_raw", "domain: "+host+" (forbidden)", false)
				jsonError(w, "forbidden: cannot view this domain", http.StatusForbidden)
				return
			}
		}
	}

	// Try reading from domains.d/ file first
	path, err := s.domainFilePath(host)
	if err == nil {
		data, err := os.ReadFile(path)
		if err == nil {
			jsonResponse(w, map[string]string{"content": string(data)})
			return
		}
	}

	// Fallback: generate YAML from the in-memory config for this domain
	s.configMu.RLock()
	var found *config.Domain
	for i := range s.config.Domains {
		if s.config.Domains[i].Host == host {
			d := s.config.Domains[i]
			found = &d
			break
		}
	}
	s.configMu.RUnlock()

	if found == nil {
		jsonError(w, "domain not found", http.StatusNotFound)
		return
	}

	data, err := yaml.Marshal(found)
	if err != nil {
		jsonError(w, "failed to marshal domain config", http.StatusInternalServerError)
		return
	}

	jsonResponse(w, map[string]string{"content": string(data)})
}

// handleDomainRawPut validates and writes raw YAML content for a single
// domain file in domains.d/, then triggers a reload.
func (s *Server) handleDomainRawPut(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")

	// Check domain permissions for non-admin users
	if s.authMgr != nil {
		if user, ok := auth.UserFromContext(r.Context()); ok && user.Role != auth.RoleAdmin {
			if !s.authMgr.CanManageDomain(user, host) {
				s.recordAuditR(r, "domain.update_raw", "domain: "+host+" (forbidden)", false)
				jsonError(w, "forbidden: cannot modify this domain", http.StatusForbidden)
				return
			}
		}
	}

	path, err := s.domainFilePath(host)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MB limit
	var req struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	data := []byte(req.Content)

	// Validate YAML syntax by parsing as a domain.
	var probe config.Domain
	if err := yaml.Unmarshal(data, &probe); err != nil {
		jsonError(w, "invalid YAML: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Validate domain semantics before persisting.
	tmpCfg := config.Config{
		Global: config.GlobalConfig{
			LogLevel:  "info",
			LogFormat: "json",
			Admin:     config.AdminConfig{Listen: "127.0.0.1:9443"},
			WebRoot:   "/var/www",
		},
		Domains: []config.Domain{probe},
	}
	if err := config.Validate(&tmpCfg); err != nil {
		jsonError(w, "validation failed: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Ensure the domains.d directory exists.
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		s.logger.Error("domain raw put: mkdir failed", "error", err)
		jsonError(w, "failed to save domain configuration", http.StatusInternalServerError)
		return
	}

	// Crash-safe write: unique temp file + fsync + rename.
	s.persistMu.Lock()
	writeErr := atomicWriteFile(path, data, 0600)
	s.persistMu.Unlock()
	if writeErr != nil {
		s.logger.Error("domain raw put: write failed", "error", writeErr)
		jsonError(w, "failed to save domain configuration", http.StatusInternalServerError)
		return
	}

	// Trigger reload if available.
	if s.reloadFn != nil {
		if err := s.reloadFn(); err != nil {
			jsonError(w, "domain saved but reload failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	jsonResponse(w, map[string]string{"status": "saved"})
}

// domainFilePath resolves the on-disk path for a domain's YAML file inside
// the domains.d/ directory adjacent to the main config file.
func (s *Server) domainFilePath(host string) (string, error) {
	if s.configPath == "" {
		return "", fmt.Errorf("config path not set")
	}

	// Reject path traversal characters before any transformation
	if strings.ContainsAny(host, `/\`) || strings.Contains(host, "..") {
		return "", fmt.Errorf("invalid host name")
	}

	// Sanitize host: replace port separator for filesystem safety
	clean := strings.ReplaceAll(host, ":", "_")
	clean = filepath.Base(clean)
	if clean == "." || clean == ".." {
		return "", fmt.Errorf("invalid host name")
	}

	baseDir := filepath.Dir(s.configPath)

	// Use configured domains_dir if present, else default to domains.d/
	s.configMu.RLock()
	domainsDir := s.config.DomainsDir
	s.configMu.RUnlock()

	if domainsDir == "" {
		domainsDir = "domains.d"
	}
	if !filepath.IsAbs(domainsDir) {
		domainsDir = filepath.Join(baseDir, domainsDir)
	}

	return filepath.Join(domainsDir, clean+".yaml"), nil
}

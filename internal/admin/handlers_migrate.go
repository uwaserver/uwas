package admin

import (
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/uwaserver/uwas/internal/auth"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/domainutil"
	"github.com/uwaserver/uwas/internal/migrate"
	"github.com/uwaserver/uwas/internal/pathsafe"
)

// ============ Site Migration + Clone ============

func (s *Server) handleMigrate(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) || !s.requirePin(w, r) {
		return
	}
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
	s.recordAuditR(r, "migrate.start", req.SourceHost+" → "+req.Domain, true)
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
// Caller-supplied relative roots are resolved against the web root (not the
// process working directory) and the target — the destructive destination of
// rsync --delete — is required to stay under the web root. The source is only
// read and may legitimately live outside it (app workdirs).
func (s *Server) resolveClonePaths(req *migrate.CloneRequest) error {
	s.configMu.RLock()
	webRoot := s.config.Global.WebRoot
	s.configMu.RUnlock()
	if webRoot == "" {
		webRoot = "/var/www"
	}
	if req.SourceRoot == "" {
		req.SourceRoot = s.domainRoot(req.SourceDomain)
	}
	if req.SourceRoot == "" {
		return fmt.Errorf("source domain not found")
	}
	if req.TargetRoot == "" {
		req.TargetRoot = filepath.Join(webRoot, req.TargetDomain, "public_html")
	}

	if !filepath.IsAbs(req.SourceRoot) {
		req.SourceRoot = filepath.Join(webRoot, req.SourceRoot)
	}
	if !filepath.IsAbs(req.TargetRoot) {
		req.TargetRoot = filepath.Join(webRoot, req.TargetRoot)
	}
	absWeb, err := filepath.Abs(webRoot)
	if err != nil {
		return fmt.Errorf("resolve web root: %w", err)
	}
	absTarget, err := filepath.Abs(req.TargetRoot)
	if err != nil {
		return fmt.Errorf("resolve target root: %w", err)
	}
	if !pathsafe.IsWithinBase(absWeb, absTarget) {
		return fmt.Errorf("target root %q escapes the web root", req.TargetRoot)
	}
	return nil
}

// detectWordPressDB auto-detects database credentials from wp-config.php.
// It returns an error when DB_NAME is defined but not as a string literal
// (e.g. getenv()): cloning then would copy the files only and leave the
// clone's wp-config pointing at the source site's live database.
func detectWordPressDB(req *migrate.CloneRequest) error {
	if req.SourceDB != "" {
		return nil
	}
	wpCfg := filepath.Join(req.SourceRoot, "wp-config.php")
	data, err := os.ReadFile(wpCfg)
	if err != nil {
		return nil
	}
	src := wpBlockCommentRe.ReplaceAllString(string(data), "")
	src = wpLineCommentRe.ReplaceAllString(src, "")
	name, unresolved := wpConfigDefine(src, "DB_NAME")
	if unresolved {
		return fmt.Errorf("cannot determine DB_NAME from wp-config.php (not a string literal); set source_db explicitly")
	}
	req.SourceDB = name
	if req.DBUser == "" {
		req.DBUser, _ = wpConfigDefine(src, "DB_USER")
	}
	if req.DBPass == "" {
		req.DBPass, _ = wpConfigDefine(src, "DB_PASSWORD")
	}
	return nil
}

var (
	wpBlockCommentRe = regexp.MustCompile(`(?s)/\*.*?\*/`)
	wpLineCommentRe  = regexp.MustCompile(`(?m)^\s*(//|#).*$`)
)

// wpConfigDefine returns the value PHP would use for define(key, ...): the
// first define outside comments (src must already be comment-stripped), with
// either quote style. unresolved reports a define whose value is not a plain
// string literal.
func wpConfigDefine(src, key string) (value string, unresolved bool) {
	k := regexp.QuoteMeta(key)
	loc := regexp.MustCompile(`define\s*\(\s*['"]` + k + `['"]\s*,`).FindStringIndex(src)
	if loc == nil {
		return "", false
	}
	re := regexp.MustCompile(`define\s*\(\s*['"]` + k + `['"]\s*,\s*(?:'([^']*)'|"([^"]*)")\s*\)`)
	m := re.FindStringSubmatchIndex(src)
	if m == nil || m[0] != loc[0] {
		return "", true
	}
	return src[max(m[2], m[4]):max(m[3], m[5])], false
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
	if !s.requireAdmin(w, r) || !s.requirePin(w, r) {
		return
	}
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

	if err := detectWordPressDB(&req); err != nil {
		s.recordAuditR(r, "clone.start", req.SourceDomain+" → "+req.TargetDomain+": "+err.Error(), false)
		jsonError(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}

	s.recordAuditR(r, "clone.start", req.SourceDomain+" → "+req.TargetDomain, true)
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
	if !s.requireAdmin(w, r) || !s.requirePin(w, r) {
		return
	}
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
	s.recordAuditR(r, "migrate.cpanel", header.Filename, true)

	result, err := migrate.ImportCPanelBackup(tmpPath, webRoot, importDB)
	if err != nil {
		jsonError(w, "import failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	added := s.createDomainsFromMigration(result, webRoot)
	if len(added) > 0 {
		s.persistConfig()
		s.notifyDomainChange()
		s.recordAuditR(r, "migrate.cpanel.domains", strings.Join(added, ", "), true)
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

// ── SSL Certificate Upload ─────────────────────────────────────────

func (s *Server) handleCertUpload(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	host := r.PathValue("host")
	// Domain ownership gate: prevents an admin from uploading certs for
	// domains they do not own. Same IDOR fix as handleCertRenew.
	if !s.domainHandler.RequirePermission(w, r, auth.PermDomainUpdate) {
		return
	}
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

	if strings.ContainsAny(host, `/\`) || strings.Contains(host, "..") {
		jsonError(w, "invalid hostname", http.StatusBadRequest)
		return
	}
	certDir := filepath.Join("/var/lib/uwas/certs", host)
	if err := os.MkdirAll(certDir, 0700); err != nil {
		jsonError(w, "failed to create certificate directory", http.StatusInternalServerError)
		return
	}
	// Write cert + key atomically: temp file → fsync → rename. This prevents
	// three failure modes that the previous os.WriteFile pair allowed:
	//   1. A power loss between the two writes left cert.pem present and
	//      key.pem missing, so the next LoadX509KeyPair on boot failed.
	//   2. Without fsync the kernel buffer was free to reorder the rename
	//      ahead of the data flush, leaving both files visible but truncated.
	//   3. notifyDomainChange triggered the TLS manager's reload concurrent
	//      with the writes; if the first WriteFile won the race the manager
	//      saw a half-written pair. Renaming after both files are fully on
	//      disk gives the manager an all-or-nothing view.
	if err := atomicWriteFile(filepath.Join(certDir, "cert.pem"), []byte(req.Cert), 0600); err != nil {
		jsonError(w, "write cert: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := atomicWriteFile(filepath.Join(certDir, "key.pem"), []byte(req.Key), 0600); err != nil {
		jsonError(w, "write key: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if req.Chain != "" {
		// Chain is non-fatal: log but do not fail the upload.
		if err := atomicWriteFile(filepath.Join(certDir, "chain.pem"), []byte(req.Chain), 0600); err != nil {
			s.logger.Warn("cert upload: chain write failed", "domain", host, "error", err)
		}
	}

	// Update domain SSL mode to manual. The mutation happens *after* the
	// files are committed to disk so notifyDomainChange's reload always
	// finds a consistent pair.
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

	s.recordAuditR(r, "cert.upload", host, true)
	jsonResponse(w, map[string]string{"status": "uploaded", "host": host})
}

// ── Bulk Domain Import ─────────────────────────────────────────────

func (s *Server) handleBulkDomainImport(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
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
	// Key by canonical host: apex and www are one domain (config validation
	// rejects them as duplicate hosts), so importing www.x next to x must skip.
	for _, d := range s.config.Domains {
		existing[domainutil.CanonicalDomainHostname(d.Host)] = true
	}

	var added, skipped []string
	webRoot := s.config.Global.WebRoot
	if webRoot == "" {
		webRoot = "/var/www"
	}
	for _, d := range req.Domains {
		host := normalizeDomainHostname(d.Host)
		key := domainutil.CanonicalDomainHostname(host)
		if host == "" || existing[key] {
			skipped = append(skipped, host)
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
			root = filepath.Join(webRoot, host, "public_html")
		}
		domain := config.Domain{
			Host: host, Type: dtype, Root: root,
			SSL: config.SSLConfig{Mode: sslMode},
		}
		s.config.Domains = append(s.config.Domains, domain)
		added = append(added, host)
		existing[key] = true
		// No separate www redirect domain: the router already serves www.<host>
		// for this domain, and NewCanonicalRedirectAliasDomain folds the www
		// alias back to <host>, which produced a self-redirecting duplicate.
	}
	s.configMu.Unlock()

	if len(added) > 0 {
		s.persistConfig()
		s.notifyDomainChange()
		s.recordAuditR(r, "domain.bulk_import", fmt.Sprintf("%d added", len(added)), true)
	}
	jsonResponse(w, map[string]any{"added": added, "skipped": skipped})
}

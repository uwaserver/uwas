package admin

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/uwaserver/uwas/internal/auth"
	domainadmin "github.com/uwaserver/uwas/internal/admin/domain"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/domainutil"
	"github.com/uwaserver/uwas/internal/pathsafe"
	"github.com/uwaserver/uwas/internal/phpmanager"
	"github.com/uwaserver/uwas/internal/webhook"
)

// filepathBase, dirOf, joinPath, isAbs are thin wrappers to avoid importing
// path/filepath in every test file that references handlers_domain.
func filepathBase(p string) string   { return filepath.Base(p) }
func dirOf(p string) string           { return filepath.Dir(p) }
func joinPath(elem ...string) string  { return filepath.Join(elem...) }
func isAbs(p string) bool             { return filepath.IsAbs(p) }

// domainDeps adapts admin.Server to the domain.Deps interface.
type domainDeps struct {
	s *Server
}

func (d *domainDeps) RequirePermission(w http.ResponseWriter, r *http.Request, perm auth.Permission) bool {
	return d.s.requirePermission(w, r, perm)
}

func (d *domainDeps) RequirePin(w http.ResponseWriter, r *http.Request) bool {
	return d.s.requirePin(w, r)
}

func (d *domainDeps) ConfigDomains() []config.Domain {
	d.s.configMu.RLock()
	defer d.s.configMu.RUnlock()
	return d.s.config.Domains
}

func (d *domainDeps) WebRoot() string {
	d.s.configMu.RLock()
	defer d.s.configMu.RUnlock()
	return d.s.config.Global.WebRoot
}

func (d *domainDeps) DomainsDir() string {
	d.s.configMu.RLock()
	defer d.s.configMu.RUnlock()
	return d.s.config.DomainsDir
}

func (d *domainDeps) ConfigPath() string {
	return d.s.configPath
}

func (d *domainDeps) LockConfig()   { d.s.configMu.Lock() }
func (d *domainDeps) UnlockConfig() { d.s.configMu.Unlock() }

func (d *domainDeps) ConfigPtr() *config.Config {
	return d.s.config
}

func (d *domainDeps) PersistConfig() { d.s.persistConfig() }

func (d *domainDeps) DomainFilePath(host string) (string, error) {
	return d.s.domainFilePath(host)
}

func (d *domainDeps) RemoveDomainFile(host string) {
	d.s.removeDomainFile(host)
}

func (d *domainDeps) AtomicWriteFile(path string, data []byte, perm os.FileMode) error {
	return atomicWriteFile(path, data, perm)
}

func (d *domainDeps) PHPManager() *phpmanager.Manager { return d.s.phpMgr }

func (d *domainDeps) CachePurgeByTag(tag string) int {
	if d.s.cache != nil {
		return d.s.cache.PurgeByTag(tag)
	}
	return 0
}

func (d *domainDeps) UnknownHostList() []any {
	if d.s.unknownHT == nil {
		return nil
	}
	entries := d.s.unknownHT.List()
	out := make([]any, len(entries))
	for i, e := range entries {
		out[i] = e
	}
	return out
}

func (d *domainDeps) UnknownHostBlock(host string) {
	if d.s.unknownHT == nil {
		return
	}
	d.s.unknownHT.Block(host)
}

func (d *domainDeps) UnknownHostUnblock(host string) {
	if d.s.unknownHT == nil {
		return
	}
	d.s.unknownHT.Unblock(host)
}

func (d *domainDeps) UnknownHostDismiss(host string) {
	if d.s.unknownHT == nil {
		return
	}
	d.s.unknownHT.Dismiss(host)
}

// UnknownHostAvailable reports whether the tracker is initialized.
func (d *domainDeps) UnknownHostAvailable() bool {
	return d.s.unknownHT != nil
}

func (d *domainDeps) WebhookFire(event webhook.EventType, payload map[string]any) {
	if d.s.webhookMgr != nil {
		d.s.webhookMgr.Fire(event, payload)
	}
}

func (d *domainDeps) LogInfo(msg string, args ...any)  { d.s.logger.Info(msg, args...) }
func (d *domainDeps) LogWarn(msg string, args ...any)  { d.s.logger.Warn(msg, args...) }
func (d *domainDeps) LogError(msg string, args ...any) { d.s.logger.Error(msg, args...) }

func (d *domainDeps) RecordAudit(r *http.Request, action, detail string, success bool) {
	d.s.recordAuditR(r, action, detail, success)
}

func (d *domainDeps) NotifyDomainChange() { d.s.notifyDomainChange() }

func (d *domainDeps) Reload() error {
	if d.s.reloadFn == nil {
		return nil
	}
	return d.s.reloadFn()
}

func (d *domainDeps) UserFromContext(r *http.Request) (*auth.User, bool) {
	return auth.UserFromContext(r.Context())
}

func (d *domainDeps) CanManageDomain(user *auth.User, domain string) bool {
	if d.s.authMgr != nil {
		return d.s.authMgr.CanManageDomain(user, domain)
	}
	return true
}

// ── Thin wrappers ──

func (s *Server) handleDomains(w http.ResponseWriter, r *http.Request)               { s.domainHandler.List(w, r) }
func (s *Server) handleAddDomain(w http.ResponseWriter, r *http.Request)              { s.domainHandler.Add(w, r) }
func (s *Server) handleDeleteDomain(w http.ResponseWriter, r *http.Request)           { s.domainHandler.Delete(w, r) }
func (s *Server) handleUpdateDomain(w http.ResponseWriter, r *http.Request)           { s.domainHandler.Update(w, r) }
func (s *Server) handleDomainDetail(w http.ResponseWriter, r *http.Request)           { s.domainHandler.Detail(w, r) }
func (s *Server) handleUnknownDomainsList(w http.ResponseWriter, r *http.Request)     { s.domainHandler.UnknownList(w, r) }
func (s *Server) handleUnknownDomainsAlias(w http.ResponseWriter, r *http.Request)    { s.domainHandler.UnknownAlias(w, r) }
func (s *Server) handleUnknownDomainsBlock(w http.ResponseWriter, r *http.Request)    { s.domainHandler.UnknownBlock(w, r) }
func (s *Server) handleUnknownDomainsUnblock(w http.ResponseWriter, r *http.Request)  { s.domainHandler.UnknownUnblock(w, r) }
func (s *Server) handleUnknownDomainsDismiss(w http.ResponseWriter, r *http.Request)  { s.domainHandler.UnknownDismiss(w, r) }
func (s *Server) handleDomainRawGet(w http.ResponseWriter, r *http.Request)           { s.domainHandler.RawGet(w, r) }
func (s *Server) handleDomainRawPut(w http.ResponseWriter, r *http.Request)           { s.domainHandler.RawPut(w, r) }

// ── Helpers retained for compat with tests + other files ──

func (s *Server) domainTypeForHost(host string) string {
	host = domainutil.CanonicalDomainHostname(host)
	s.configMu.RLock()
	defer s.configMu.RUnlock()
	for _, d := range s.config.Domains {
		if domainutil.CanonicalDomainHostname(d.Host) == host {
			return d.Type
		}
	}
	return ""
}

func validateDomainConfig(d *config.Domain, s *Server) error {
	if err := config.ValidateDomain(d); err != nil {
		return err
	}
	webRoot := "/var/www"
	s.configMu.RLock()
	if s.config.Global.WebRoot != "" {
		webRoot = s.config.Global.WebRoot
	}
	s.configMu.RUnlock()
	if d.Type == "php" && s.phpMgr != nil {
		phpStatus := s.phpMgr.Status()
		activePHP := 0
		for _, p := range phpStatus {
			if !p.Disabled {
				activePHP++
			}
		}
		if activePHP == 0 {
			return errNoActivePHP
		}
	}
	if d.Root != "" && d.Type != "redirect" {
		if !pathsafe.IsWithinBase(webRoot, d.Root) || !pathsafe.IsWithinBaseResolved(webRoot, d.Root) {
			return errRootNotUnderWebRoot(webRoot, d.Root)
		}
	}
	return nil
}

func validateDomainUpdateConfig(d *config.Domain, s *Server) error {
	if err := config.ValidateDomainPartial(d); err != nil {
		return err
	}
	webRoot := "/var/www"
	if s.config.Global.WebRoot != "" {
		webRoot = s.config.Global.WebRoot
	}
	if d.Root != "" && d.Type != "redirect" {
		if !pathsafe.IsWithinBase(webRoot, d.Root) || !pathsafe.IsWithinBaseResolved(webRoot, d.Root) {
			return errRootNotUnderWebRoot(webRoot, d.Root)
		}
	}
	return nil
}

var errNoActivePHP = newStrErr("no active PHP versions available — install or enable PHP first")

func errRootNotUnderWebRoot(webRoot, root string) error {
	return newStrErr("root path must be under " + webRoot + " (got " + root + ")")
}

type strErr string

func (e strErr) Error() string { return string(e) }

func newStrErr(s string) error { return strErr(s) }

// ── Helper wrappers (for files still referencing admin-local names) ──
func mainDomainHostname(d config.Domain) string { return domainutil.MainDomainHostname(d) }
func domainHostnames(d config.Domain) []string  { return domainutil.DomainHostnames(d) }
func canonicalDomainHostname(host string) string { return domainutil.CanonicalDomainHostname(host) }
func normalizeDomainHostname(host string) string { return domainutil.NormalizeDomainHostname(host) }
func isValidHostname(s string) bool              { return domainutil.IsValidHostname(s) }
func domainTypeUsesWebRoot(t string) bool         { return domainutil.DomainTypeUsesWebRoot(t) }
func normalizeDomainHostnames(d *config.Domain)  { domainutil.NormalizeDomainHostnames(d) }
func findDomainHostnameConflict(domains []config.Domain, skip int, host string) string {
	return domainutil.FindDomainHostnameConflict(domains, skip, host)
}
func implicitWWWHostname(host string) string { return domainutil.ImplicitWWWHostname(host) }

// domainFilePath resolves the on-disk path for a domain's YAML file.
func (s *Server) domainFilePath(host string) (string, error) {
	if s.configPath == "" {
		return "", newStrErr("config path not set")
	}
	if strings.ContainsAny(host, `/\`) || strings.Contains(host, "..") {
		return "", newStrErr("invalid host name")
	}
	clean := strings.ReplaceAll(host, ":", "_")
	clean = filepathBase(clean)
	if clean == "." || clean == ".." {
		return "", newStrErr("invalid host name")
	}
	baseDir := dirOf(s.configPath)
	s.configMu.RLock()
	domainsDir := s.config.DomainsDir
	s.configMu.RUnlock()
	if domainsDir == "" {
		domainsDir = "domains.d"
	}
	if !isAbs(domainsDir) {
		domainsDir = joinPath(baseDir, domainsDir)
	}
	return joinPath(domainsDir, clean+".yaml"), nil
}

func (s *Server) removeDomainFile(host string) {
	if s.configPath == "" {
		return
	}
	s.configMu.RLock()
	domainsDir := s.config.DomainsDir
	s.configMu.RUnlock()
	if domainsDir == "" {
		domainsDir = "domains.d"
	}
	if !isAbs(domainsDir) {
		domainsDir = joinPath(dirOf(s.configPath), domainsDir)
	}
	clean := strings.ReplaceAll(host, ":", "_")
	clean = filepathBase(clean)
	for _, ext := range []string{".yaml", ".yml"} {
		path := joinPath(domainsDir, clean+ext)
		if err := os.Remove(path); err == nil {
			s.logger.Info("removed domain file", "path", path)
		}
	}
}

func (s *Server) initDomainHandler() {
	s.domainHandler = domainadmin.New(&domainDeps{s: s})
}

// Compile-time check.
var _ domainadmin.Deps = (*domainDeps)(nil)

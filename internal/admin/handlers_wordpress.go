package admin

import (
	"net/http"

	wpadmin "github.com/uwaserver/uwas/internal/admin/wordpress"
	"github.com/uwaserver/uwas/internal/wordpress"
)

// wpDeps adapts admin.Server to the wordpress.Deps interface.
type wpDeps struct {
	s *Server
}

func (d *wpDeps) RequireDomainAccess(w http.ResponseWriter, r *http.Request, domain, action string) bool {
	return d.s.requireDomainAccess(w, r, domain, action)
}
func (d *wpDeps) CanAccessDomain(r *http.Request, domain string) bool {
	return d.s.canAccessDomain(r, domain)
}
func (d *wpDeps) AuthorizedDomainRoot(w http.ResponseWriter, r *http.Request, domain, action string) (string, bool) {
	return d.s.authorizedDomainRoot(w, r, domain, action)
}
func (d *wpDeps) DomainRoot(domain string) string {
	d.s.configMu.RLock()
	defer d.s.configMu.RUnlock()
	for _, d := range d.s.config.Domains {
		if d.Host == domain {
			return d.Root
		}
	}
	return ""
}
func (d *wpDeps) GlobalWebRoot() string {
	d.s.configMu.RLock()
	defer d.s.configMu.RUnlock()
	return d.s.config.Global.WebRoot
}
func (d *wpDeps) Domains() []wordpress.DomainInfo {
	d.s.configMu.RLock()
	defer d.s.configMu.RUnlock()
	out := make([]wordpress.DomainInfo, 0, len(d.s.config.Domains))
	for _, d := range d.s.config.Domains {
		out = append(out, wordpress.DomainInfo{Host: d.Host, WebRoot: d.Root})
	}
	return out
}
func (d *wpDeps) LogInfo(msg string, args ...any) { d.s.logger.Info(msg, args...) }
func (d *wpDeps) RecordAudit(r *http.Request, action, detail string, success bool) {
	d.s.recordAuditR(r, action, detail, success)
}

// wpHandler holds the WordPress admin handler instance.
var wpHandler *wpadmin.Handler

func (s *Server) initWPHandler() {
	wpHandler = wpadmin.New(&wpDeps{s: s})
}

// ── Thin wrappers ──

func (s *Server) handleWPInstall(w http.ResponseWriter, r *http.Request) { wpHandler.Install(w, r) }
func (s *Server) handleWPInstallStatus(w http.ResponseWriter, r *http.Request) {
	wpHandler.InstallStatus(w, r)
}
func (s *Server) handleWPSites(w http.ResponseWriter, r *http.Request) { wpHandler.Sites(w, r) }
func (s *Server) handleWPSiteDetail(w http.ResponseWriter, r *http.Request) {
	wpHandler.SiteDetail(w, r)
}
func (s *Server) handleWPUpdateCore(w http.ResponseWriter, r *http.Request) {
	wpHandler.UpdateCore(w, r)
}
func (s *Server) handleWPUpdatePlugins(w http.ResponseWriter, r *http.Request) {
	wpHandler.UpdatePlugins(w, r)
}
func (s *Server) handleWPPluginAction(w http.ResponseWriter, r *http.Request) {
	wpHandler.PluginAction(w, r)
}
func (s *Server) handleWPFixPermissions(w http.ResponseWriter, r *http.Request) {
	wpHandler.FixPermissions(w, r)
}
func (s *Server) handleWPReinstall(w http.ResponseWriter, r *http.Request) { wpHandler.Reinstall(w, r) }
func (s *Server) handleWPToggleDebug(w http.ResponseWriter, r *http.Request) {
	wpHandler.ToggleDebug(w, r)
}
func (s *Server) handleWPErrorLog(w http.ResponseWriter, r *http.Request) { wpHandler.ErrorLog(w, r) }
func (s *Server) handleWPUsers(w http.ResponseWriter, r *http.Request)    { wpHandler.Users(w, r) }
func (s *Server) handleWPChangePassword(w http.ResponseWriter, r *http.Request) {
	wpHandler.ChangePassword(w, r)
}
func (s *Server) handleWPSecurityStatus(w http.ResponseWriter, r *http.Request) {
	wpHandler.SecurityStatus(w, r)
}
func (s *Server) handleWPHarden(w http.ResponseWriter, r *http.Request) { wpHandler.Harden(w, r) }
func (s *Server) handleWPOptimizeDB(w http.ResponseWriter, r *http.Request) {
	wpHandler.OptimizeDB(w, r)
}

// Compile-time check.
var _ wpadmin.Deps = (*wpDeps)(nil)

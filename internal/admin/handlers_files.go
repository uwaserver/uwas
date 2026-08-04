package admin

import (
	"net/http"

	"github.com/uwaserver/uwas/internal/admin/files"
	"github.com/uwaserver/uwas/internal/apps"
	"github.com/uwaserver/uwas/internal/auth"
	"github.com/uwaserver/uwas/internal/config"
)

// filesDeps adapts admin.Server to the files.Deps interface.
type filesDeps struct {
	s *Server
}

func (d *filesDeps) RequireAdmin(w http.ResponseWriter, r *http.Request) bool {
	return d.s.requireAdmin(w, r)
}
func (d *filesDeps) RequirePin(w http.ResponseWriter, r *http.Request) bool {
	return d.s.requirePin(w, r)
}
func (d *filesDeps) RequireDomainAccess(w http.ResponseWriter, r *http.Request, domain, action string) bool {
	return d.s.requireDomainAccess(w, r, domain, action)
}
func (d *filesDeps) CanAccessDomain(r *http.Request, domain string) bool {
	return d.s.canAccessDomain(r, domain)
}
func (d *filesDeps) RecordAudit(r *http.Request, action, detail string, success bool) {
	d.s.recordAuditR(r, action, detail, success)
}
func (d *filesDeps) ParsePagination(r *http.Request) (limit, offset int) {
	return parsePagination(r)
}
func (d *filesDeps) LogInfo(msg string, args ...any) { d.s.logger.Info(msg, args...) }
func (d *filesDeps) Domains() []config.Domain {
	d.s.configMu.RLock()
	defer d.s.configMu.RUnlock()
	return append([]config.Domain(nil), d.s.config.Domains...)
}
func (d *filesDeps) WebRoot() string {
	d.s.configMu.RLock()
	defer d.s.configMu.RUnlock()
	return d.s.config.Global.WebRoot
}
func (d *filesDeps) AppsManager() *apps.Manager { return d.s.appsMgr }
func (d *filesDeps) AuthEnabled() bool {
	return d.s.authMgr != nil
}

// Suppress unused import warnings — auth is used via UserFromContext in the sub-package.
var _ = auth.RoleAdmin

// filesHandler holds the files admin handler instance.
var filesHandler *files.Handler

func (s *Server) initFilesHandler() {
	filesHandler = files.New(&filesDeps{s: s})
}

// ── Thin wrappers ──

func (s *Server) handleFileWorkspaces(w http.ResponseWriter, r *http.Request) {
	filesHandler.Workspaces(w, r)
}
func (s *Server) handleFileList(w http.ResponseWriter, r *http.Request)   { filesHandler.List(w, r) }
func (s *Server) handleFileRead(w http.ResponseWriter, r *http.Request)   { filesHandler.Read(w, r) }
func (s *Server) handleFileWrite(w http.ResponseWriter, r *http.Request)  { filesHandler.Write(w, r) }
func (s *Server) handleFileDelete(w http.ResponseWriter, r *http.Request) { filesHandler.Delete(w, r) }
func (s *Server) handleFileMkdir(w http.ResponseWriter, r *http.Request)  { filesHandler.Mkdir(w, r) }
func (s *Server) handleFileUpload(w http.ResponseWriter, r *http.Request) { filesHandler.Upload(w, r) }
func (s *Server) handleDiskUsage(w http.ResponseWriter, r *http.Request) {
	filesHandler.DiskUsage(w, r)
}
func (s *Server) handleCronList(w http.ResponseWriter, r *http.Request) { filesHandler.CronList(w, r) }
func (s *Server) handleCronAdd(w http.ResponseWriter, r *http.Request)  { filesHandler.CronAdd(w, r) }
func (s *Server) handleCronDelete(w http.ResponseWriter, r *http.Request) {
	filesHandler.CronDelete(w, r)
}

// ── Retained helpers for other admin files ──

func (s *Server) domainRoot(domain string) string {
	root, _ := s.domainRootForFiles(domain)
	return root
}

func (s *Server) domainRootForFiles(domain string) (string, error) {
	// Delegate to the sub-package's resolution
	return files.ResolveDomainRoot(&filesDeps{s: s}, domain)
}

func (s *Server) authorizedDomainRoot(w http.ResponseWriter, r *http.Request, domain, action string) (string, bool) {
	return filesHandler.AuthorizedDomainRoot(w, r, domain, action)
}

func (s *Server) siteUserRoot(domain string) (string, error) {
	return files.ResolveSiteUserRoot(&filesDeps{s: s}, domain)
}

// Compile-time check.
var _ files.Deps = (*filesDeps)(nil)

// Retained helpers for other admin files.
func appSFTPTargetName(target string) (string, bool) { return files.AppSFTPTargetName(target) }
func appSFTPIdentity(appName string) string          { return files.AppSFTPIdentity(appName) }
func formatBytes(b int64) string                     { return files.FormatBytes(b) }

// fileWorkspace is aliased for test compat.
type fileWorkspace = files.FileWorkspace

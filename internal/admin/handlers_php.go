package admin

import (
	"net/http"

	phpadmin "github.com/uwaserver/uwas/internal/admin/php"
	"github.com/uwaserver/uwas/internal/auth"
	"github.com/uwaserver/uwas/internal/phpmanager"
)

// phpDeps adapts admin.Server to the php.Deps interface.
type phpDeps struct {
	s *Server
}

func (d *phpDeps) RequireAdmin(w http.ResponseWriter, r *http.Request) bool {
	return d.s.requireAdmin(w, r)
}

func (d *phpDeps) RequireDomainAccess(w http.ResponseWriter, r *http.Request, domain, action string) bool {
	return d.s.requireDomainAccess(w, r, domain, action)
}

func (d *phpDeps) CanManageDomain(r *http.Request, domain string) bool {
	if d.s.authMgr != nil {
		if user, ok := auth.UserFromContext(r.Context()); ok && user.Role != auth.RoleAdmin {
			return d.s.authMgr.CanManageDomain(user, domain)
		}
	}
	return true
}

func (d *phpDeps) LogInfo(msg string, args ...any)  { d.s.logger.Info(msg, args...) }
func (d *phpDeps) LogWarn(msg string, args ...any)  { d.s.logger.Warn(msg, args...) }
func (d *phpDeps) LogError(msg string, args ...any) { d.s.logger.Error(msg, args...) }

func (d *phpDeps) RecordAudit(r *http.Request, action, detail string, success bool) {
	d.s.recordAuditR(r, action, detail, success)
}

func (d *phpDeps) ParsePagination(r *http.Request) (limit, offset int) {
	return parsePagination(r)
}

func (d *phpDeps) TaskActive() *phpadmin.TaskInfo {
	if active := d.s.taskMgr.Active(); active != nil {
		return &phpadmin.TaskInfo{ID: active.ID, Name: active.Name, Type: active.Type}
	}
	return nil
}

func (d *phpDeps) TaskSubmit(category, name, action string, fn func(appendOutput func(string)) error) *phpadmin.TaskInfo {
	task := d.s.taskMgr.Submit(category, name, action, fn)
	return &phpadmin.TaskInfo{ID: task.ID, Name: task.Name, Type: task.Type}
}

func (d *phpDeps) TaskActiveByType(typ string) *phpadmin.TaskInfo {
	if t := d.s.taskMgr.ActiveByType(typ); t != nil {
		return &phpadmin.TaskInfo{ID: t.ID, Name: t.Name, Type: t.Type, Status: string(t.Status), Output: t.Output, Error: t.Error}
	}
	return nil
}

func (d *phpDeps) TaskLatestByType(typ string) *phpadmin.TaskInfo {
	if t := d.s.taskMgr.LatestByType(typ); t != nil {
		return &phpadmin.TaskInfo{ID: t.ID, Name: t.Name, Type: t.Type, Status: string(t.Status), Output: t.Output, Error: t.Error}
	}
	return nil
}

func (d *phpDeps) DomainRoot(domain string) string {
	d.s.configMu.RLock()
	defer d.s.configMu.RUnlock()
	for _, dom := range d.s.config.Domains {
		if dom.Host == domain {
			return dom.Root
		}
	}
	return ""
}

func (d *phpDeps) SetDomainFPMAddress(domain, addr string) {
	d.s.configMu.Lock()
	for i, dom := range d.s.config.Domains {
		if dom.Host == domain {
			d.s.config.Domains[i].PHP.FPMAddress = addr
			break
		}
	}
	d.s.configMu.Unlock()
}

func (d *phpDeps) PersistConfig()      { d.s.persistConfig() }
func (d *phpDeps) NotifyDomainChange() { d.s.notifyDomainChange() }
func (d *phpDeps) PersistDomainPHPOverrides(domain string) {
	d.s.persistDomainPHPOverrides(domain)
}

func (d *phpDeps) PHPManager() *phpmanager.Manager { return d.s.phpMgr }
func (d *phpDeps) PhpRunInstall(distro string) (string, error) {
	return phpRunInstall(distro)
}

// SetPHPManager sets the PHP manager and initializes the PHP handler.
func (s *Server) SetPHPManager(m *phpmanager.Manager) {
	s.phpMgr = m
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
	s.initPHPHandler()
}

// (phpHandler is now a Server field)

func (s *Server) initPHPHandler() {
	s.phpHandler = phpadmin.New(&phpDeps{s: s})
}

// ── Thin wrappers ──

func (s *Server) handlePHPList(w http.ResponseWriter, r *http.Request) {
	if s.phpHandler == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	s.phpHandler.List(w, r)
}
func (s *Server) handlePHPInstallInfo(w http.ResponseWriter, r *http.Request) {
	s.phpHandler.InstallInfo(w, r)
}
func (s *Server) handlePHPInstall(w http.ResponseWriter, r *http.Request) {
	s.phpHandler.Install(w, r)
}
func (s *Server) handlePHPInstallStatus(w http.ResponseWriter, r *http.Request) {
	if s.phpHandler == nil {
		jsonResponse(w, map[string]string{"status": "idle"})
		return
	}
	s.phpHandler.InstallStatus(w, r)
}
func (s *Server) handlePHPConfig(w http.ResponseWriter, r *http.Request) {
	if s.phpHandler == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	s.phpHandler.Config(w, r)
}
func (s *Server) handlePHPConfigUpdate(w http.ResponseWriter, r *http.Request) {
	if s.phpHandler == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	s.phpHandler.ConfigUpdate(w, r)
}
func (s *Server) handlePHPExtensions(w http.ResponseWriter, r *http.Request) {
	if s.phpHandler == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	s.phpHandler.Extensions(w, r)
}
func (s *Server) handlePHPStart(w http.ResponseWriter, r *http.Request) {
	if s.phpHandler == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	s.phpHandler.Start(w, r)
}
func (s *Server) handlePHPStop(w http.ResponseWriter, r *http.Request) {
	if s.phpHandler == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	s.phpHandler.Stop(w, r)
}
func (s *Server) handlePHPRestart(w http.ResponseWriter, r *http.Request) {
	if s.phpHandler == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	s.phpHandler.Restart(w, r)
}
func (s *Server) handlePHPConfigRawGet(w http.ResponseWriter, r *http.Request) {
	if s.phpHandler == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	s.phpHandler.ConfigRawGet(w, r)
}
func (s *Server) handlePHPConfigRawPut(w http.ResponseWriter, r *http.Request) {
	if s.phpHandler == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	s.phpHandler.ConfigRawPut(w, r)
}
func (s *Server) handlePHPEnable(w http.ResponseWriter, r *http.Request) {
	if s.phpHandler == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	s.phpHandler.Enable(w, r)
}
func (s *Server) handlePHPDisable(w http.ResponseWriter, r *http.Request) {
	if s.phpHandler == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	s.phpHandler.Disable(w, r)
}
func (s *Server) handlePHPDomainsList(w http.ResponseWriter, r *http.Request) {
	if s.phpHandler == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	s.phpHandler.DomainsList(w, r)
}
func (s *Server) handlePHPDomainAssign(w http.ResponseWriter, r *http.Request) {
	if s.phpHandler == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	s.phpHandler.DomainAssign(w, r)
}
func (s *Server) handlePHPDomainUnassign(w http.ResponseWriter, r *http.Request) {
	if s.phpHandler == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	s.phpHandler.DomainUnassign(w, r)
}
func (s *Server) handlePHPDomainStart(w http.ResponseWriter, r *http.Request) {
	if s.phpHandler == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	s.phpHandler.DomainStart(w, r)
}
func (s *Server) handlePHPDomainStop(w http.ResponseWriter, r *http.Request) {
	if s.phpHandler == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	s.phpHandler.DomainStop(w, r)
}
func (s *Server) handlePHPDomainConfigGet(w http.ResponseWriter, r *http.Request) {
	if s.phpHandler == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	s.phpHandler.DomainConfigGet(w, r)
}
func (s *Server) handlePHPDomainConfigPut(w http.ResponseWriter, r *http.Request) {
	if s.phpHandler == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	s.phpHandler.DomainConfigPut(w, r)
}

// phpRunInstall is a test seam for the PHP install path. TestMain points it
// at a no-op so `go test` never invokes real apt commands.
var phpRunInstall = phpmanager.RunInstall

// validPHPVersion reports whether s is a bare PHP version of the form N.N.
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

var _ phpadmin.Deps = (*phpDeps)(nil)

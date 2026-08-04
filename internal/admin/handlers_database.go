package admin

import (
	"net/http"

	dbadmin "github.com/uwaserver/uwas/internal/admin/database"
	"github.com/uwaserver/uwas/internal/database"
	"github.com/uwaserver/uwas/internal/install"
)

// dbDeps adapts admin.Server to the database.Deps interface.
type dbDeps struct {
	s *Server
}

func (d *dbDeps) RequireAdmin(w http.ResponseWriter, r *http.Request) bool {
	return d.s.requireAdmin(w, r)
}

func (d *dbDeps) RequirePin(w http.ResponseWriter, r *http.Request) bool {
	return d.s.requirePin(w, r)
}

func (d *dbDeps) LogInfo(msg string, args ...any) {
	d.s.logger.Info(msg, args...)
}

func (d *dbDeps) LogDebug(msg string, args ...any) {
	d.s.logger.Debug(msg, args...)
}

func (d *dbDeps) LogError(msg string, args ...any) {
	d.s.logger.Error(msg, args...)
}

func (d *dbDeps) RecordAudit(r *http.Request, action, detail string, success bool) {
	d.s.recordAuditR(r, action, detail, success)
}

func (d *dbDeps) ParsePagination(r *http.Request) (limit, offset int) {
	return parsePagination(r)
}

func (d *dbDeps) TaskActive() *dbadmin.TaskInfo {
	if active := d.s.taskMgr.Active(); active != nil {
		return &dbadmin.TaskInfo{ID: active.ID, Name: active.Name, Type: active.Type}
	}
	return nil
}

func (d *dbDeps) TaskSubmit(category, name, action string, fn func(appendOutput func(string)) error) *dbadmin.TaskInfo {
	task := d.s.taskMgr.Submit(category, name, action, fn)
	return &dbadmin.TaskInfo{ID: task.ID, Name: task.Name, Type: task.Type}
}

// (dbHandler is now a Server field)

// Test seams: package-level vars that tests override. These are read
// dynamically by the dbDeps methods below, so test overrides take effect
// immediately without re-syncing.
var (
	databaseStartService   = database.StartService
	databaseStopService    = database.StopService
	databaseRestartService = database.RestartService
	databaseRepairService  = database.RepairService
	databaseUninstall      = database.UninstallService
	databaseForceUninstall = database.ForceUninstall
	databaseCreateDatabase = database.CreateDatabase
	databaseDropDatabase   = database.DropDatabase
)

func (d *dbDeps) StartService() error             { return databaseStartService() }
func (d *dbDeps) StopService() error              { return databaseStopService() }
func (d *dbDeps) RestartService() error           { return databaseRestartService() }
func (d *dbDeps) RepairService() (string, error)  { return databaseRepairService() }
func (d *dbDeps) Uninstall() (string, error)      { return databaseUninstall() }
func (d *dbDeps) ForceUninstall() (string, error) { return databaseForceUninstall() }
func (d *dbDeps) CreateDB(name, user, password, host string) (*database.CreateResult, error) {
	return databaseCreateDatabase(name, user, password, host)
}
func (d *dbDeps) DropDB(name, user, host string) error { return databaseDropDatabase(name, user, host) }

// initDBHandler creates the database sub-package handler.
func (s *Server) initDBHandler() {
	s.dbHandler = dbadmin.New(&dbDeps{s: s})
}

// ── Thin wrappers: delegate to the sub-package Handler ──
// Preserved so the 20+ test files that call s.handleDB* directly still compile.
// Once tests are migrated to the sub-package, these can be removed.

func (s *Server) handleDBStatus(w http.ResponseWriter, r *http.Request)  { s.dbHandler.Status(w, r) }
func (s *Server) handleDBList(w http.ResponseWriter, r *http.Request)    { s.dbHandler.List(w, r) }
func (s *Server) handleDBCreate(w http.ResponseWriter, r *http.Request)  { s.dbHandler.Create(w, r) }
func (s *Server) handleDBDrop(w http.ResponseWriter, r *http.Request)    { s.dbHandler.Drop(w, r) }
func (s *Server) handleDBInstall(w http.ResponseWriter, r *http.Request) { s.dbHandler.Install(w, r) }
func (s *Server) handleDBUninstall(w http.ResponseWriter, r *http.Request) {
	s.dbHandler.Uninstall(w, r)
}
func (s *Server) handleDBForceUninstall(w http.ResponseWriter, r *http.Request) {
	s.dbHandler.ForceUninstall(w, r)
}
func (s *Server) handleDBRepair(w http.ResponseWriter, r *http.Request)   { s.dbHandler.Repair(w, r) }
func (s *Server) handleDBDiagnose(w http.ResponseWriter, r *http.Request) { s.dbHandler.Diagnose(w, r) }
func (s *Server) handleDBUsers(w http.ResponseWriter, r *http.Request)    { s.dbHandler.Users(w, r) }
func (s *Server) handleDBChangePassword(w http.ResponseWriter, r *http.Request) {
	s.dbHandler.ChangePassword(w, r)
}
func (s *Server) handleDBRemoteAccess(w http.ResponseWriter, r *http.Request) {
	s.dbHandler.RemoteAccess(w, r)
}
func (s *Server) handleDBExport(w http.ResponseWriter, r *http.Request)  { s.dbHandler.Export(w, r) }
func (s *Server) handleDBImport(w http.ResponseWriter, r *http.Request)  { s.dbHandler.Import(w, r) }
func (s *Server) handleDBStart(w http.ResponseWriter, r *http.Request)   { s.dbHandler.Start(w, r) }
func (s *Server) handleDBStop(w http.ResponseWriter, r *http.Request)    { s.dbHandler.Stop(w, r) }
func (s *Server) handleDBRestart(w http.ResponseWriter, r *http.Request) { s.dbHandler.Restart(w, r) }

func (s *Server) handleDockerDBList(w http.ResponseWriter, r *http.Request) {
	s.dbHandler.DockerList(w, r)
}
func (s *Server) handleDockerDBCreate(w http.ResponseWriter, r *http.Request) {
	s.dbHandler.DockerCreate(w, r)
}
func (s *Server) handleDockerDBStart(w http.ResponseWriter, r *http.Request) {
	s.dbHandler.DockerStart(w, r)
}
func (s *Server) handleDockerDBStop(w http.ResponseWriter, r *http.Request) {
	s.dbHandler.DockerStop(w, r)
}
func (s *Server) handleDockerDBRemove(w http.ResponseWriter, r *http.Request) {
	s.dbHandler.DockerRemove(w, r)
}
func (s *Server) handleDockerDBListDatabases(w http.ResponseWriter, r *http.Request) {
	s.dbHandler.DockerListDatabases(w, r)
}
func (s *Server) handleDockerDBCreateDatabase(w http.ResponseWriter, r *http.Request) {
	s.dbHandler.DockerCreateDatabase(w, r)
}
func (s *Server) handleDockerDBDropDatabase(w http.ResponseWriter, r *http.Request) {
	s.dbHandler.DockerDropDatabase(w, r)
}
func (s *Server) handleDockerDBExport(w http.ResponseWriter, r *http.Request) {
	s.dbHandler.DockerExport(w, r)
}
func (s *Server) handleDockerDBImport(w http.ResponseWriter, r *http.Request) {
	s.dbHandler.DockerImport(w, r)
}

func (s *Server) handleDBExploreTables(w http.ResponseWriter, r *http.Request) {
	s.dbHandler.ExploreTables(w, r)
}
func (s *Server) handleDBExploreColumns(w http.ResponseWriter, r *http.Request) {
	s.dbHandler.ExploreColumns(w, r)
}
func (s *Server) handleDBExploreQuery(w http.ResponseWriter, r *http.Request) {
	s.dbHandler.ExploreQuery(w, r)
}

// Compile-time check: dbDeps implements database.Deps.
var _ dbadmin.Deps = (*dbDeps)(nil)

// Silence unused import (install is needed by TaskSubmit adapter).
var _ *install.Queue

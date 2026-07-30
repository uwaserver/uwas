// Package database provides admin API handlers for MySQL/MariaDB
// database management and Docker database containers.
//
// This is the proof-of-concept sub-package extraction (refactor.md A17).
// The pattern: Handler holds a Deps interface (implemented by the admin
// Server) plus testable function hooks. The admin package constructs the
// Handler, wires the Deps closures to Server methods, and registers
// routes. Thin Server-side wrappers preserve test compatibility for the
// 20+ existing test files that call s.handleDB* directly.
package database

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	dbpkg "github.com/uwaserver/uwas/internal/database"
	"github.com/uwaserver/uwas/internal/respond"
)

// Deps is the interface the sub-package needs from the admin Server.
// Each method maps to a Server helper that was previously called inline.
type Deps interface {
	// Auth
	RequireAdmin(w http.ResponseWriter, r *http.Request) bool
	RequirePin(w http.ResponseWriter, r *http.Request) bool
	// Logging
	LogInfo(msg string, args ...any)
	LogDebug(msg string, args ...any)
	LogError(msg string, args ...any)
	// Audit
	RecordAudit(r *http.Request, action, detail string, success bool)
	// Pagination
	ParsePagination(r *http.Request) (limit, offset int)
	// Task queue for installs
	TaskActive() *TaskInfo
	TaskSubmit(category, name, action string, fn func(appendOutput func(string)) error) *TaskInfo
	// Service hooks (test seams — read at call time, not construction time)
	StartService() error
	StopService() error
	RestartService() error
	RepairService() (string, error)
	Uninstall() (string, error)
	ForceUninstall() (string, error)
	CreateDB(name, user, password, host string) (*dbpkg.CreateResult, error)
	DropDB(name, user, host string) error
}

// TaskInfo is a minimal description of a background task.
type TaskInfo struct {
	ID   string
	Name string
	Type string
}

// Handler holds database admin API handlers.
type Handler struct {
	deps Deps
}

// New creates a database Handler with the given deps.
func New(deps Deps) *Handler {
	return &Handler{deps: deps}
}

// ── Helpers ──

func jsonResponse(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	respond.Error(w, code, msg)
}

// ── Database Management ──

func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	jsonResponse(w, dbpkg.GetStatus())
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	limit, offset := h.deps.ParsePagination(r)
	st := dbpkg.GetStatus()
	if !st.Installed || !st.Running {
		jsonResponse(w, PaginatedResponse{
			Items: []dbpkg.DBInfo{}, Total: 0, Limit: limit, Offset: offset,
		})
		return
	}
	dbs, err := dbpkg.ListDatabases()
	if err != nil {
		h.deps.LogDebug("database list failed", "error", err)
		jsonResponse(w, PaginatedResponse{
			Items: []dbpkg.DBInfo{}, Total: 0, Limit: limit, Offset: offset,
		})
		return
	}
	if dbs == nil {
		dbs = []dbpkg.DBInfo{}
	}
	dbs, total := paginateSlice(dbs, limit, offset)
	jsonResponse(w, PaginatedResponse{
		Items: dbs, Total: total, Limit: limit, Offset: offset,
	})
}

// PaginatedResponse is the typed list response (mirrors admin.PaginatedResponse).
type PaginatedResponse struct {
	Items  []dbpkg.DBInfo `json:"items"`
	Total  int            `json:"total"`
	Limit  int            `json:"limit"`
	Offset int            `json:"offset"`
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
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
	result, err := h.deps.CreateDB(req.Name, req.User, req.Password, req.Host)
	if err != nil {
		h.deps.LogError("database create failed", "name", req.Name, "error", err)
		jsonError(w, "database creation failed", http.StatusInternalServerError)
		return
	}
	h.deps.LogInfo("database created", "name", result.Name, "user", result.User)
	jsonResponse(w, result)
}

func (h *Handler) Drop(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) || !h.deps.RequirePin(w, r) {
		return
	}
	name := r.PathValue("name")
	if err := h.deps.DropDB(name, name, "localhost"); err != nil {
		h.deps.LogError("database drop failed", "name", name, "error", err)
		jsonError(w, "database drop failed", http.StatusInternalServerError)
		return
	}
	h.deps.LogInfo("database dropped", "name", name)
	jsonResponse(w, map[string]string{"status": "dropped", "name": name})
}

func (h *Handler) Install(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	st := dbpkg.GetStatus()
	if st.Installed {
		jsonResponse(w, map[string]string{"status": "already_installed", "version": st.Version})
		return
	}
	if active := h.deps.TaskActive(); active != nil {
		jsonError(w, fmt.Sprintf("another installation in progress: %s (%s)", active.Name, active.ID), http.StatusConflict)
		return
	}
	task := h.deps.TaskSubmit("database", "MariaDB", "install", func(appendOutput func(string)) error {
		output, err := dbpkg.InstallMySQL()
		appendOutput(output)
		if err != nil {
			h.deps.LogError("database install failed", "error", err)
			return err
		}
		h.deps.LogInfo("database install complete")
		return nil
	})
	jsonResponse(w, map[string]string{"status": "installing", "task_id": task.ID})
}

func (h *Handler) Users(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	users, err := dbpkg.ListUsers()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if users == nil {
		users = []dbpkg.DBUser{}
	}
	jsonResponse(w, users)
}

func (h *Handler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		User     string `json:"user"`
		Host     string `json:"host"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.User == "" || req.Password == "" {
		jsonError(w, "user and password required", http.StatusBadRequest)
		return
	}
	if err := dbpkg.ChangePassword(req.User, req.Host, req.Password); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.deps.RecordAudit(r, "database.password_change", "user: "+req.User, true)
	jsonResponse(w, map[string]string{"status": "changed"})
}

func (h *Handler) RemoteAccess(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) || !h.deps.RequirePin(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		User     string `json:"user"`
		Host     string `json:"host"`
		Password string `json:"password"`
		Database string `json:"database"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.User == "" {
		jsonError(w, "user is required", http.StatusBadRequest)
		return
	}
	result, err := dbpkg.ConfigureRemoteAccess(req.User, req.Host, req.Password, req.Database)
	if err != nil {
		h.deps.RecordAudit(r, "database.remote_access", "error: "+err.Error(), false)
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.deps.RecordAudit(r, "database.remote_access", "user: "+result.User+" host: "+result.Host, true)
	jsonResponse(w, result)
}

func (h *Handler) Export(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	name := r.PathValue("name")
	data, err := dbpkg.ExportDatabase(name)
	if err != nil {
		h.deps.LogError("database export failed", "name", name, "error", err)
		jsonError(w, "database export failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/sql")
	safeName := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '_'
	}, name)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.sql"`, safeName))
	w.Write(data)
}

func (h *Handler) Import(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	name := r.PathValue("name")
	r.Body = http.MaxBytesReader(w, r.Body, 256<<20)
	data, err := io.ReadAll(io.LimitReader(r.Body, 256<<20))
	if err != nil {
		jsonError(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := dbpkg.ImportDatabase(name, data); err != nil {
		h.deps.LogError("database import failed", "name", name, "error", err)
		jsonError(w, "database import failed", http.StatusInternalServerError)
		return
	}
	h.deps.RecordAudit(r, "database.import", "db: "+name, true)
	jsonResponse(w, map[string]string{"status": "imported", "database": name})
}

func (h *Handler) Uninstall(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) || !h.deps.RequirePin(w, r) {
		return
	}
	out, err := h.deps.Uninstall()
	if err != nil {
		h.deps.RecordAudit(r, "database.uninstall", "error: "+err.Error(), false)
		h.deps.LogError("database uninstall failed", "error", err)
		jsonError(w, "database uninstall failed", http.StatusInternalServerError)
		return
	}
	h.deps.RecordAudit(r, "database.uninstall", "success", true)
	h.deps.LogInfo("MySQL/MariaDB uninstalled")
	jsonResponse(w, map[string]string{"status": "uninstalled", "output": out})
}

func (h *Handler) Repair(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	out, err := h.deps.RepairService()
	if err != nil {
		h.deps.RecordAudit(r, "database.repair", "error: "+err.Error(), false)
		jsonError(w, err.Error()+"\n"+out, http.StatusInternalServerError)
		return
	}
	h.deps.RecordAudit(r, "database.repair", "success", true)
	h.deps.LogInfo("MySQL/MariaDB repaired")
	jsonResponse(w, map[string]string{"status": "repaired", "output": out})
}

func (h *Handler) ForceUninstall(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) || !h.deps.RequirePin(w, r) {
		return
	}
	out, err := h.deps.ForceUninstall()
	if err != nil {
		h.deps.RecordAudit(r, "database.force_uninstall", "error: "+err.Error(), false)
		jsonError(w, err.Error()+"\n"+out, http.StatusInternalServerError)
		return
	}
	h.deps.RecordAudit(r, "database.force_uninstall", "success", true)
	h.deps.LogInfo("MySQL/MariaDB force uninstalled")
	jsonResponse(w, map[string]string{"status": "force_uninstalled", "output": out})
}

func (h *Handler) Diagnose(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	jsonResponse(w, dbpkg.DiagnoseService())
}

// ── Service Control ──

func (h *Handler) Start(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	if err := h.deps.StartService(); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.deps.LogInfo("MySQL/MariaDB started")
	jsonResponse(w, map[string]string{"status": "started"})
}

func (h *Handler) Stop(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	if err := h.deps.StopService(); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.deps.LogInfo("MySQL/MariaDB stopped")
	jsonResponse(w, map[string]string{"status": "stopped"})
}

func (h *Handler) Restart(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	if err := h.deps.RestartService(); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.deps.LogInfo("MySQL/MariaDB restarted")
	jsonResponse(w, map[string]string{"status": "restarted"})
}

// ── Docker Database Containers ──

func (h *Handler) DockerList(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	if !dbpkg.DockerAvailable() {
		jsonResponse(w, map[string]any{"docker": false, "containers": []any{}})
		return
	}
	containers, err := dbpkg.ListDockerDBs()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if containers == nil {
		containers = []dbpkg.DockerDBContainer{}
	}
	jsonResponse(w, map[string]any{
		"docker":     true,
		"version":    dbpkg.DockerVersion(),
		"containers": containers,
	})
}

func (h *Handler) DockerCreate(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	if !dbpkg.DockerAvailable() {
		jsonError(w, "Docker is not installed or not running", http.StatusServiceUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Engine   string `json:"engine"`
		Name     string `json:"name"`
		Port     int    `json:"port"`
		RootPass string `json:"root_pass"`
		DataDir  string `json:"data_dir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Name == "" || req.Engine == "" || req.Port == 0 || req.RootPass == "" {
		jsonError(w, "name, engine, port, and root_pass are required", http.StatusBadRequest)
		return
	}
	engine := dbpkg.DockerDBEngine(req.Engine)
	container, err := dbpkg.CreateDockerDB(engine, req.Name, req.Port, req.RootPass, req.DataDir)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.deps.RecordAudit(r, "docker_db.create", fmt.Sprintf("engine: %s, name: %s, port: %d", req.Engine, req.Name, req.Port), true)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(container)
}

func (h *Handler) DockerStart(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	name := r.PathValue("name")
	if err := dbpkg.StartDockerDB(name); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "started"})
}

func (h *Handler) DockerStop(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	name := r.PathValue("name")
	if err := dbpkg.StopDockerDB(name); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "stopped"})
}

func (h *Handler) DockerRemove(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) || !h.deps.RequirePin(w, r) {
		return
	}
	name := r.PathValue("name")
	if err := dbpkg.RemoveDockerDB(name); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.deps.RecordAudit(r, "docker_db.remove", "name: "+name, true)
	jsonResponse(w, map[string]string{"status": "removed"})
}

func (h *Handler) DockerListDatabases(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	name := r.PathValue("name")
	dbs, err := dbpkg.DockerDBListDatabases(name)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if dbs == nil {
		dbs = []dbpkg.DBInfo{}
	}
	jsonResponse(w, dbs)
}

func (h *Handler) DockerCreateDatabase(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	name := r.PathValue("name")
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		DBName   string `json:"name"`
		User     string `json:"user"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.DBName == "" {
		jsonError(w, "database name required", http.StatusBadRequest)
		return
	}
	result, err := dbpkg.DockerDBCreateDatabase(name, req.DBName, req.User, req.Password)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.deps.RecordAudit(r, "docker_db.create_database", name+"/"+req.DBName, true)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(result)
}

func (h *Handler) DockerDropDatabase(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) || !h.deps.RequirePin(w, r) {
		return
	}
	name := r.PathValue("name")
	db := r.PathValue("db")
	if err := dbpkg.DockerDBDropDatabase(name, db); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.deps.RecordAudit(r, "docker_db.drop_database", name+"/"+db, true)
	jsonResponse(w, map[string]string{"status": "dropped"})
}

func (h *Handler) DockerExport(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	name := r.PathValue("name")
	db := r.PathValue("db")
	dump, err := dbpkg.DockerDBExport(name, db)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/sql")
	safeName := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '_'
	}, name+"_"+db)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.sql"`, safeName))
	w.Write([]byte(dump))
}

func (h *Handler) DockerImport(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	name := r.PathValue("name")
	db := r.PathValue("db")
	r.Body = http.MaxBytesReader(w, r.Body, 100<<20)
	data, err := io.ReadAll(io.LimitReader(r.Body, 100<<20))
	if err != nil {
		jsonError(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := dbpkg.DockerDBImport(name, db, string(data)); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.deps.RecordAudit(r, "docker_db.import", name+"/"+db, true)
	jsonResponse(w, map[string]string{"status": "imported"})
}

// ── Database Explorer ──

func (h *Handler) ExploreTables(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	db := r.PathValue("db")
	if db == "" {
		jsonError(w, "database name required", http.StatusBadRequest)
		return
	}
	if !dbpkg.ValidDBIdentifier(db) {
		jsonError(w, "invalid database name", http.StatusBadRequest)
		return
	}
	exists, err := dbpkg.DatabaseExists(db)
	if err != nil {
		jsonError(w, "database lookup failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if !exists {
		jsonError(w, "database "+db+" does not exist", http.StatusNotFound)
		return
	}
	sql := fmt.Sprintf("SELECT TABLE_NAME, TABLE_ROWS, DATA_LENGTH, INDEX_LENGTH, ENGINE, TABLE_COLLATION FROM information_schema.TABLES WHERE TABLE_SCHEMA = '%s' ORDER BY TABLE_NAME", dbpkg.EscapeSQL(db))
	out, err := dbpkg.RunSQL(sql)
	if err != nil {
		jsonError(w, "query failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	var tables []map[string]string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) >= 6 {
			tables = append(tables, map[string]string{
				"name": fields[0], "rows": fields[1], "data_size": fields[2],
				"index_size": fields[3], "engine": fields[4], "collation": fields[5],
			})
		}
	}
	jsonResponse(w, tables)
}

func (h *Handler) ExploreColumns(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	db := r.PathValue("db")
	table := r.PathValue("table")
	if !dbpkg.ValidDBIdentifier(db) || !dbpkg.ValidDBIdentifier(table) {
		jsonError(w, "invalid name", http.StatusBadRequest)
		return
	}
	sql := fmt.Sprintf("SELECT COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE, COLUMN_KEY, COLUMN_DEFAULT, EXTRA FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = '%s' AND TABLE_NAME = '%s' ORDER BY ORDINAL_POSITION", dbpkg.EscapeSQL(db), dbpkg.EscapeSQL(table))
	out, err := dbpkg.RunSQL(sql)
	if err != nil {
		jsonError(w, "query failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	var columns []map[string]string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) >= 6 {
			columns = append(columns, map[string]string{
				"name": fields[0], "type": fields[1], "nullable": fields[2],
				"key": fields[3], "default": fields[4], "extra": fields[5],
			})
		}
	}
	jsonResponse(w, columns)
}

func (h *Handler) ExploreQuery(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	db := r.PathValue("db")
	if !dbpkg.ValidDBIdentifier(db) {
		jsonError(w, "invalid database name", http.StatusBadRequest)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		SQL   string `json:"sql"`
		Limit int    `json:"limit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.SQL == "" {
		jsonError(w, "sql required", http.StatusBadRequest)
		return
	}
	trimmed := strings.TrimSpace(req.SQL)
	for strings.HasPrefix(trimmed, "/*") {
		if end := strings.Index(trimmed, "*/"); end >= 0 {
			trimmed = strings.TrimSpace(trimmed[end+2:])
		} else {
			break
		}
	}
	for strings.HasPrefix(trimmed, "--") || strings.HasPrefix(trimmed, "#") {
		if nl := strings.IndexByte(trimmed, '\n'); nl >= 0 {
			trimmed = strings.TrimSpace(trimmed[nl+1:])
		} else {
			trimmed = ""
		}
	}
	upper := strings.ToUpper(trimmed)
	if strings.Contains(req.SQL, ";") {
		jsonError(w, "multi-statement queries not allowed", http.StatusForbidden)
		return
	}
	if !strings.HasPrefix(upper, "SELECT") && !strings.HasPrefix(upper, "SHOW") &&
		!strings.HasPrefix(upper, "DESCRIBE") && !strings.HasPrefix(upper, "DESC ") &&
		!strings.HasPrefix(upper, "EXPLAIN") {
		jsonError(w, "only SELECT, SHOW, DESCRIBE, EXPLAIN are allowed in explorer", http.StatusForbidden)
		return
	}
	if strings.HasPrefix(upper, "SELECT") && !strings.Contains(upper, "LIMIT") {
		limit := req.Limit
		if limit <= 0 || limit > 500 {
			limit = 100
		}
		req.SQL = req.SQL + fmt.Sprintf(" LIMIT %d", limit)
	}
	if strings.HasPrefix(upper, "SELECT") {
		if strings.Contains(upper, "INTO OUTFILE") || strings.Contains(upper, "INTO DUMPFILE") {
			jsonError(w, "INTO OUTFILE/DUMPFILE is not allowed", http.StatusForbidden)
			return
		}
		if strings.Contains(upper, "FOR UPDATE") || strings.Contains(upper, "LOCK IN SHARE MODE") {
			jsonError(w, "row locking clauses are not allowed", http.StatusForbidden)
			return
		}
		if strings.Contains(upper, "LOAD_FILE") {
			jsonError(w, "LOAD_FILE() is not allowed", http.StatusForbidden)
			return
		}
	}
	fullSQL := fmt.Sprintf("USE %s;\n%s", dbpkg.BacktickID(db), req.SQL)
	out, err := dbpkg.RunSQL(fullSQL)
	if err != nil {
		h.deps.LogError("db explorer query failed", "db", db, "error", err)
		jsonError(w, "query execution failed", http.StatusBadRequest)
		return
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) == 0 {
		jsonResponse(w, map[string]any{"columns": []string{}, "rows": [][]string{}, "affected": out})
		return
	}
	headers := strings.Split(lines[0], "\t")
	var rows [][]string
	maxRows := 500
	for _, line := range lines[1:] {
		if line != "" {
			rows = append(rows, strings.Split(line, "\t"))
			if len(rows) >= maxRows {
				break
			}
		}
	}
	jsonResponse(w, map[string]any{
		"columns": headers,
		"rows":    rows,
		"count":   len(rows),
	})
}

// paginateSlice returns a paginated slice and the total count.
func paginateSlice[T any](items []T, limit, offset int) ([]T, int) {
	total := len(items)
	if offset >= total {
		return []T{}, total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return items[offset:end], total
}

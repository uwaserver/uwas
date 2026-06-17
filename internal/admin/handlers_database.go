package admin

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/uwaserver/uwas/internal/database"
)

var (
	databaseStartService   = database.StartService
	databaseStopService    = database.StopService
	databaseRestartService = database.RestartService
)

// ============ Database ============

func (s *Server) handleDBStatus(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	jsonResponse(w, database.GetStatus())
}

func (s *Server) handleDBList(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	limit, offset := parsePagination(r)
	// Check if MySQL is available before querying
	st := database.GetStatus()
	if !st.Installed || !st.Running {
		jsonResponse(w, map[string]any{"items": []database.DBInfo{}, "total": 0, "limit": limit, "offset": offset})
		return
	}
	dbs, err := database.ListDatabases()
	if err != nil {
		// Don't error — just return empty list with a log
		s.logger.Debug("database list failed", "error", err)
		jsonResponse(w, map[string]any{"items": []database.DBInfo{}, "total": 0, "limit": limit, "offset": offset})
		return
	}
	if dbs == nil {
		dbs = []database.DBInfo{}
	}
	dbs, total := paginateSlice(dbs, limit, offset)
	jsonResponse(w, map[string]any{"items": dbs, "total": total, "limit": limit, "offset": offset})
}

func (s *Server) handleDBCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
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
	result, err := database.CreateDatabase(req.Name, req.User, req.Password, req.Host)
	if err != nil {
		s.logger.Error("database create failed", "name", req.Name, "error", err)
		jsonError(w, "database creation failed", http.StatusInternalServerError)
		return
	}
	s.logger.Info("database created", "name", result.Name, "user", result.User)
	jsonResponse(w, result)
}

func (s *Server) handleDBDrop(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) || !s.requirePin(w, r) {
		return
	}
	name := r.PathValue("name")
	if err := database.DropDatabase(name, name, "localhost"); err != nil {
		s.logger.Error("database drop failed", "name", name, "error", err)
		jsonError(w, "database drop failed", http.StatusInternalServerError)
		return
	}
	s.logger.Info("database dropped", "name", name)
	jsonResponse(w, map[string]string{"status": "dropped", "name": name})
}

func (s *Server) handleDBInstall(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	st := database.GetStatus()
	if st.Installed {
		jsonResponse(w, map[string]string{"status": "already_installed", "version": st.Version})
		return
	}

	// Check if any install task is already running
	if active := s.taskMgr.Active(); active != nil {
		jsonError(w, fmt.Sprintf("another installation in progress: %s (%s)", active.Name, active.ID), http.StatusConflict)
		return
	}

	task := s.taskMgr.Submit("database", "MariaDB", "install", func(appendOutput func(string)) error {
		output, err := database.InstallMySQL()
		appendOutput(output)
		if err != nil {
			s.logger.Error("database install failed", "error", err)
			return err
		}
		s.logger.Info("database install complete")
		return nil
	})

	jsonResponse(w, map[string]string{"status": "installing", "task_id": task.ID})
}

func (s *Server) handleDBUsers(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	users, err := database.ListUsers()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if users == nil {
		users = []database.DBUser{}
	}
	jsonResponse(w, users)
}

func (s *Server) handleDBChangePassword(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
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
	if err := database.ChangePassword(req.User, req.Host, req.Password); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.recordAuditR(r, "database.password_change", "user: "+req.User, true)
	jsonResponse(w, map[string]string{"status": "changed"})
}

func (s *Server) handleDBRemoteAccess(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) || !s.requirePin(w, r) {
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
	result, err := database.ConfigureRemoteAccess(req.User, req.Host, req.Password, req.Database)
	if err != nil {
		s.recordAuditR(r, "database.remote_access", "error: "+err.Error(), false)
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.recordAuditR(r, "database.remote_access", "user: "+result.User+" host: "+result.Host, true)
	jsonResponse(w, result)
}

func (s *Server) handleDBExport(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	name := r.PathValue("name")
	data, err := database.ExportDatabase(name)
	if err != nil {
		s.logger.Error("database export failed", "name", name, "error", err)
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

func (s *Server) handleDBImport(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	name := r.PathValue("name")
	r.Body = http.MaxBytesReader(w, r.Body, 256<<20) // 256MB max
	data, err := io.ReadAll(io.LimitReader(r.Body, 256<<20))
	if err != nil {
		jsonError(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := database.ImportDatabase(name, data); err != nil {
		s.logger.Error("database import failed", "name", name, "error", err)
		jsonError(w, "database import failed", http.StatusInternalServerError)
		return
	}
	s.recordAuditR(r, "database.import", "db: "+name, true)
	jsonResponse(w, map[string]string{"status": "imported", "database": name})
}

func (s *Server) handleDBUninstall(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) || !s.requirePin(w, r) {
		return
	}
	out, err := database.UninstallService()
	if err != nil {
		s.recordAuditR(r, "database.uninstall", "error: "+err.Error(), false)
		s.logger.Error("database uninstall failed", "error", err)
		jsonError(w, "database uninstall failed", http.StatusInternalServerError)
		return
	}
	s.recordAuditR(r, "database.uninstall", "success", true)
	s.logger.Info("MySQL/MariaDB uninstalled")
	jsonResponse(w, map[string]string{"status": "uninstalled", "output": out})
}

func (s *Server) handleDBRepair(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	out, err := database.RepairService()
	if err != nil {
		s.recordAuditR(r, "database.repair", "error: "+err.Error(), false)
		jsonError(w, err.Error()+"\n"+out, http.StatusInternalServerError)
		return
	}
	s.recordAuditR(r, "database.repair", "success", true)
	s.logger.Info("MySQL/MariaDB repaired")
	jsonResponse(w, map[string]string{"status": "repaired", "output": out})
}

func (s *Server) handleDBForceUninstall(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) || !s.requirePin(w, r) {
		return
	}
	out, err := database.ForceUninstall()
	if err != nil {
		s.recordAuditR(r, "database.force_uninstall", "error: "+err.Error(), false)
		jsonError(w, err.Error()+"\n"+out, http.StatusInternalServerError)
		return
	}
	s.recordAuditR(r, "database.force_uninstall", "success", true)
	s.logger.Info("MySQL/MariaDB force uninstalled")
	jsonResponse(w, map[string]string{"status": "force_uninstalled", "output": out})
}

func (s *Server) handleDBDiagnose(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	jsonResponse(w, database.DiagnoseService())
}

// ============ Docker Database Containers ============

func (s *Server) handleDockerDBList(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if !database.DockerAvailable() {
		jsonResponse(w, map[string]any{"docker": false, "containers": []any{}})
		return
	}
	containers, err := database.ListDockerDBs()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if containers == nil {
		containers = []database.DockerDBContainer{}
	}
	jsonResponse(w, map[string]any{
		"docker":     true,
		"version":    database.DockerVersion(),
		"containers": containers,
	})
}

func (s *Server) handleDockerDBCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if !database.DockerAvailable() {
		jsonError(w, "Docker is not installed or not running", http.StatusServiceUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Engine   string `json:"engine"`    // mariadb, mysql, postgresql
		Name     string `json:"name"`      // container suffix
		Port     int    `json:"port"`      // host port
		RootPass string `json:"root_pass"` // root/admin password
		DataDir  string `json:"data_dir"`  // optional persistent volume
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Name == "" || req.Engine == "" || req.Port == 0 || req.RootPass == "" {
		jsonError(w, "name, engine, port, and root_pass are required", http.StatusBadRequest)
		return
	}

	engine := database.DockerDBEngine(req.Engine)
	container, err := database.CreateDockerDB(engine, req.Name, req.Port, req.RootPass, req.DataDir)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.recordAuditR(r, "docker_db.create", fmt.Sprintf("engine: %s, name: %s, port: %d", req.Engine, req.Name, req.Port), true)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(container)
}

func (s *Server) handleDockerDBStart(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	name := r.PathValue("name")
	if err := database.StartDockerDB(name); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "started"})
}

func (s *Server) handleDockerDBStop(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	name := r.PathValue("name")
	if err := database.StopDockerDB(name); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "stopped"})
}

func (s *Server) handleDockerDBRemove(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) || !s.requirePin(w, r) {
		return
	}
	name := r.PathValue("name")
	if err := database.RemoveDockerDB(name); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.recordAuditR(r, "docker_db.remove", "name: "+name, true)
	jsonResponse(w, map[string]string{"status": "removed"})
}

func (s *Server) handleDockerDBListDatabases(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	name := r.PathValue("name")
	dbs, err := database.DockerDBListDatabases(name)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if dbs == nil {
		dbs = []database.DBInfo{}
	}
	jsonResponse(w, dbs)
}

func (s *Server) handleDockerDBCreateDatabase(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
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
	result, err := database.DockerDBCreateDatabase(name, req.DBName, req.User, req.Password)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.recordAuditR(r, "docker_db.create_database", name+"/"+req.DBName, true)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(result)
}

func (s *Server) handleDockerDBDropDatabase(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) || !s.requirePin(w, r) {
		return
	}
	name := r.PathValue("name")
	db := r.PathValue("db")
	if err := database.DockerDBDropDatabase(name, db); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.recordAuditR(r, "docker_db.drop_database", name+"/"+db, true)
	jsonResponse(w, map[string]string{"status": "dropped"})
}

func (s *Server) handleDockerDBExport(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	name := r.PathValue("name")
	db := r.PathValue("db")
	dump, err := database.DockerDBExport(name, db)
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

func (s *Server) handleDockerDBImport(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	name := r.PathValue("name")
	db := r.PathValue("db")
	r.Body = http.MaxBytesReader(w, r.Body, 100<<20) // 100MB max
	data, err := io.ReadAll(io.LimitReader(r.Body, 100<<20))
	if err != nil {
		jsonError(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := database.DockerDBImport(name, db, string(data)); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.recordAuditR(r, "docker_db.import", name+"/"+db, true)
	jsonResponse(w, map[string]string{"status": "imported"})
}

// ============ Database Service Control ============

func (s *Server) handleDBStart(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if err := databaseStartService(); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.logger.Info("MySQL/MariaDB started")
	jsonResponse(w, map[string]string{"status": "started"})
}

func (s *Server) handleDBStop(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if err := databaseStopService(); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.logger.Info("MySQL/MariaDB stopped")
	jsonResponse(w, map[string]string{"status": "stopped"})
}

func (s *Server) handleDBRestart(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if err := databaseRestartService(); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.logger.Info("MySQL/MariaDB restarted")
	jsonResponse(w, map[string]string{"status": "restarted"})
}

// ── Database Explorer ──────────────────────────────────────────────

func (s *Server) handleDBExploreTables(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	db := r.PathValue("db")
	if db == "" {
		jsonError(w, "database name required", http.StatusBadRequest)
		return
	}
	if !database.ValidDBIdentifier(db) {
		jsonError(w, "invalid database name", http.StatusBadRequest)
		return
	}
	exists, err := database.DatabaseExists(db)
	if err != nil {
		jsonError(w, "database lookup failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if !exists {
		jsonError(w, "database "+db+" does not exist", http.StatusNotFound)
		return
	}
	sql := fmt.Sprintf("SELECT TABLE_NAME, TABLE_ROWS, DATA_LENGTH, INDEX_LENGTH, ENGINE, TABLE_COLLATION FROM information_schema.TABLES WHERE TABLE_SCHEMA = '%s' ORDER BY TABLE_NAME", database.EscapeSQL(db))
	out, err := database.RunSQL(sql)
	if err != nil {
		jsonError(w, "query failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// Parse tab-separated output into JSON
	var tables []map[string]string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) >= 6 {
			tables = append(tables, map[string]string{
				"name":       fields[0],
				"rows":       fields[1],
				"data_size":  fields[2],
				"index_size": fields[3],
				"engine":     fields[4],
				"collation":  fields[5],
			})
		}
	}
	jsonResponse(w, tables)
}

func (s *Server) handleDBExploreColumns(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	db := r.PathValue("db")
	table := r.PathValue("table")
	if !database.ValidDBIdentifier(db) || !database.ValidDBIdentifier(table) {
		jsonError(w, "invalid name", http.StatusBadRequest)
		return
	}
	sql := fmt.Sprintf("SELECT COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE, COLUMN_KEY, COLUMN_DEFAULT, EXTRA FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = '%s' AND TABLE_NAME = '%s' ORDER BY ORDINAL_POSITION", database.EscapeSQL(db), database.EscapeSQL(table))
	out, err := database.RunSQL(sql)
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
				"name":     fields[0],
				"type":     fields[1],
				"nullable": fields[2],
				"key":      fields[3],
				"default":  fields[4],
				"extra":    fields[5],
			})
		}
	}
	jsonResponse(w, columns)
}

func (s *Server) handleDBExploreQuery(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	db := r.PathValue("db")
	if !database.ValidDBIdentifier(db) {
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
	// Safety: only allow read-only statements (allowlist approach).
	// Strip leading comments and whitespace to prevent comment-based bypass.
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
	// Block multi-statement queries (semicolons).
	if strings.Contains(req.SQL, ";") {
		jsonError(w, "multi-statement queries not allowed", http.StatusForbidden)
		return
	}
	// Only allow SELECT, SHOW, DESCRIBE, EXPLAIN.
	if !strings.HasPrefix(upper, "SELECT") && !strings.HasPrefix(upper, "SHOW") &&
		!strings.HasPrefix(upper, "DESCRIBE") && !strings.HasPrefix(upper, "DESC ") &&
		!strings.HasPrefix(upper, "EXPLAIN") {
		jsonError(w, "only SELECT, SHOW, DESCRIBE, EXPLAIN are allowed in explorer", http.StatusForbidden)
		return
	}
	// Add LIMIT if SELECT and no LIMIT present
	if strings.HasPrefix(upper, "SELECT") && !strings.Contains(upper, "LIMIT") {
		limit := req.Limit
		if limit <= 0 || limit > 500 {
			limit = 100
		}
		req.SQL = req.SQL + fmt.Sprintf(" LIMIT %d", limit)
	}

	// Block dangerous SELECT variants that could read/write files or lock rows.
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

	// Use specific database
	fullSQL := fmt.Sprintf("USE %s;\n%s", database.BacktickID(db), req.SQL)
	out, err := database.RunSQL(fullSQL)
	if err != nil {
		s.logger.Error("db explorer query failed", "db", db, "error", err)
		jsonError(w, "query execution failed", http.StatusBadRequest)
		return
	}
	// Parse tab-separated into rows
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

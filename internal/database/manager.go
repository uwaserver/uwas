// Package database manages MySQL/MariaDB databases for hosted domains.
package database

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Testable hooks — replaced in tests for full coverage.
var (
	runtimeGOOS    = runtime.GOOS
	execCommandFn  = exec.Command
	execLookPathFn = exec.LookPath
	runMySQLFn     = runMySQL
	osStatFn       = os.Stat
	osReadFileFn   = os.ReadFile
	osWriteFileFn  = os.WriteFile
	osMkdirAllFn   = os.MkdirAll
	osRemoveAllFn  = os.RemoveAll
)

// DBInfo represents a database.
type DBInfo struct {
	Name     string `json:"name"`
	User     string `json:"user"`
	Password string `json:"password,omitempty"`
	Host     string `json:"host"`
	Size     string `json:"size,omitempty"`
	Tables   int    `json:"tables,omitempty"`
}

// Status checks if MySQL/MariaDB is installed and running.
type Status struct {
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	Version   string `json:"version"`
	Backend   string `json:"backend"` // "mysql", "mariadb", "none"
}

// GetStatus checks MySQL/MariaDB availability.
func GetStatus() Status {
	if runtimeGOOS == "windows" {
		return Status{Backend: "none"}
	}

	st := Status{}

	// Check mariadb first, then mysql
	for _, bin := range []string{"mariadb", "mysql"} {
		if path, err := execLookPathFn(bin); err == nil {
			st.Installed = true
			if bin == "mariadb" {
				st.Backend = "mariadb"
			} else {
				st.Backend = "mysql"
			}
			// Get version
			out, err := execCommandFn(path, "--version").Output()
			if err == nil {
				st.Version = strings.TrimSpace(string(out))
			}
			break
		}
	}

	if !st.Installed {
		st.Backend = "none"
		return st
	}

	// Check if running — try multiple methods
	for _, method := range [][]string{
		{"mysqladmin", "ping", "--silent"},
		{"mariadb-admin", "ping", "--silent"},
		{"mysql", "-u", "root", "-e", "SELECT 1"},
		{"mariadb", "-u", "root", "-e", "SELECT 1"},
	} {
		bin, err := execLookPathFn(method[0])
		if err != nil {
			continue
		}
		cmd := execCommandFn(bin, method[1:]...)
		out, err := cmd.CombinedOutput()
		if err == nil && (strings.Contains(string(out), "alive") || strings.Contains(string(out), "1")) {
			st.Running = true
			break
		}
	}

	return st
}

// StartService starts MySQL/MariaDB service.
func StartService() error {
	if runtimeGOOS == "windows" {
		return fmt.Errorf("not supported on Windows")
	}

	// Ensure socket directory exists (common issue after reboot).
	for _, dir := range []string{"/run/mysqld", "/var/run/mysqld"} {
		osMkdirAllFn(dir, 0755)
		execCommandFn("chown", "mysql:mysql", dir).Run()
	}

	// Try mariadb first, then mysql
	var lastErr string
	for _, svc := range []string{"mariadb", "mysql", "mysqld"} {
		out, err := execCommandFn("systemctl", "start", svc).CombinedOutput()
		if err == nil {
			execCommandFn("systemctl", "enable", svc).Run()
			return nil
		}
		lastErr = strings.TrimSpace(string(out))
	}
	// Collect journal for diagnosis
	diag := collectDBDiagnostics()
	return fmt.Errorf("could not start MySQL/MariaDB: %s\n%s", lastErr, diag)
}

// StopService stops MySQL/MariaDB service.
func StopService() error {
	if runtimeGOOS == "windows" {
		return fmt.Errorf("not supported on Windows")
	}
	var lastErr string
	for _, svc := range []string{"mariadb", "mysql", "mysqld"} {
		out, err := execCommandFn("systemctl", "stop", svc).CombinedOutput()
		if err == nil {
			return nil
		}
		lastErr = strings.TrimSpace(string(out))
	}
	// Force kill as fallback
	execCommandFn("pkill", "-9", "mysqld").Run()
	execCommandFn("pkill", "-9", "mariadbd").Run()
	return fmt.Errorf("could not stop MySQL/MariaDB (force killed): %s", lastErr)
}

// RestartService restarts MySQL/MariaDB service.
func RestartService() error {
	if runtimeGOOS == "windows" {
		return fmt.Errorf("not supported on Windows")
	}
	var lastErr string
	for _, svc := range []string{"mariadb", "mysql", "mysqld"} {
		out, err := execCommandFn("systemctl", "restart", svc).CombinedOutput()
		if err == nil {
			return nil
		}
		lastErr = strings.TrimSpace(string(out))
	}
	diag := collectDBDiagnostics()
	return fmt.Errorf("could not restart MySQL/MariaDB: %s\n%s", lastErr, diag)
}

// RepairService attempts to fix a broken MariaDB/MySQL installation:
// 1. Fix dpkg/apt broken state
// 2. Recreate data directory with proper ownership
// 3. Run mariadb-install-db / mysql_install_db
// 4. Start the service
func RepairService() (string, error) {
	if runtimeGOOS == "windows" {
		return "", fmt.Errorf("not supported on Windows")
	}

	var log strings.Builder

	// Step 1: Fix broken dpkg/apt state
	if _, err := execLookPathFn("dpkg"); err == nil {
		out, _ := execCommandFn("dpkg", "--configure", "-a").CombinedOutput()
		log.WriteString("dpkg --configure -a:\n" + string(out) + "\n")

		out, _ = execCommandFn("apt", "--fix-broken", "install", "-y").CombinedOutput()
		log.WriteString("apt --fix-broken install:\n" + string(out) + "\n")
	}

	// Step 2: Stop any stuck processes
	execCommandFn("systemctl", "stop", "mariadb").Run()
	execCommandFn("systemctl", "stop", "mysql").Run()
	execCommandFn("pkill", "-9", "mysqld").Run()
	execCommandFn("pkill", "-9", "mariadbd").Run()
	log.WriteString("Killed any running DB processes\n")

	// Step 3: Recreate data directory
	for _, dir := range []string{"/var/lib/mysql", "/run/mysqld", "/var/run/mysqld", "/var/log/mysql"} {
		osMkdirAllFn(dir, 0755)
		execCommandFn("chown", "mysql:mysql", dir).Run()
	}
	log.WriteString("Created /var/lib/mysql + socket dirs with mysql:mysql ownership\n")

	// Step 4: Initialize database (mariadb-install-db or mysql_install_db)
	for _, bin := range []string{"mariadb-install-db", "mysql_install_db"} {
		path, err := execLookPathFn(bin)
		if err != nil {
			continue
		}
		out, err := execCommandFn(path, "--user=mysql", "--datadir=/var/lib/mysql").CombinedOutput()
		log.WriteString(bin + ":\n" + string(out) + "\n")
		if err == nil {
			log.WriteString("Database initialized successfully\n")
			break
		}
	}

	// Step 5: Start service
	for _, svc := range []string{"mariadb", "mysql"} {
		out, err := execCommandFn("systemctl", "start", svc).CombinedOutput()
		if err == nil {
			execCommandFn("systemctl", "enable", svc).Run()
			log.WriteString("Service " + svc + " started\n")

			// Step 6: Secure installation
			runMySQLFn("DELETE FROM mysql.user WHERE User='';")
			runMySQLFn("FLUSH PRIVILEGES;")
			log.WriteString("Basic security applied\n")
			return log.String(), nil
		}
		log.WriteString("start " + svc + ": " + string(out) + "\n")
	}

	return log.String(), fmt.Errorf("repair completed but service could not start — check output for details")
}

// ForceUninstall does a more aggressive uninstall when normal uninstall fails:
// kills processes, removes packages with dpkg --force, cleans all data.
func ForceUninstall() (string, error) {
	if runtimeGOOS == "windows" {
		return "", fmt.Errorf("not supported on Windows")
	}

	var log strings.Builder

	// Kill everything
	execCommandFn("systemctl", "stop", "mariadb").Run()
	execCommandFn("systemctl", "stop", "mysql").Run()
	execCommandFn("pkill", "-9", "mysqld").Run()
	execCommandFn("pkill", "-9", "mariadbd").Run()
	log.WriteString("Killed all DB processes\n")

	// Force remove with dpkg
	if _, err := execLookPathFn("dpkg"); err == nil {
		// Find all mariadb/mysql packages
		out, _ := execCommandFn("dpkg", "-l").Output()
		for _, line := range strings.Split(string(out), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 2 && (strings.Contains(fields[1], "mariadb") || strings.Contains(fields[1], "mysql")) {
				pkg := fields[1]
				o, _ := execCommandFn("dpkg", "--force-all", "--purge", pkg).CombinedOutput()
				log.WriteString("purge " + pkg + ": " + strings.TrimSpace(string(o)) + "\n")
			}
		}
	}

	// Clean up everything
	for _, path := range []string{
		"/var/lib/mysql",
		"/var/log/mysql",
		"/run/mysqld",
		"/var/run/mysqld",
		"/etc/mysql",
		"/etc/my.cnf",
		"/etc/my.cnf.d",
	} {
		if _, err := osStatFn(path); err == nil {
			osRemoveAllFn(path)
			log.WriteString("Removed " + path + "\n")
		}
	}

	// Remove user
	execCommandFn("userdel", "-f", "mysql").Run()
	execCommandFn("groupdel", "mysql").Run()
	log.WriteString("Removed mysql user/group\n")

	// Clean apt cache
	execCommandFn("apt", "autoremove", "-y").Run()
	execCommandFn("apt", "clean").Run()
	execCommandFn("systemctl", "daemon-reload").Run()
	log.WriteString("Cleaned apt cache + daemon-reload\n")

	return log.String(), nil
}

// UninstallService completely removes MySQL/MariaDB packages and data.
func UninstallService() (string, error) {
	if runtimeGOOS == "windows" {
		return "", fmt.Errorf("not supported on Windows")
	}

	var log strings.Builder

	// Stop first
	StopService()
	log.WriteString("Service stopped\n")

	// Purge packages
	if _, err := execLookPathFn("apt"); err == nil {
		out, _ := execCommandFn("apt", "purge", "-y",
			"mariadb-server", "mariadb-client", "mariadb-common",
			"mysql-server", "mysql-client", "mysql-common",
		).CombinedOutput()
		log.WriteString(string(out))
		execCommandFn("apt", "autoremove", "-y").CombinedOutput()
	} else if _, err := execLookPathFn("dnf"); err == nil {
		out, _ := execCommandFn("dnf", "remove", "-y",
			"mariadb-server", "mariadb", "mysql-server", "mysql",
		).CombinedOutput()
		log.WriteString(string(out))
	}

	// Clean up leftover data and sockets
	for _, path := range []string{
		"/var/lib/mysql",
		"/var/log/mysql",
		"/run/mysqld",
		"/var/run/mysqld",
		"/etc/mysql",
	} {
		if _, err := osStatFn(path); err == nil {
			osRemoveAllFn(path)
			log.WriteString("Removed " + path + "\n")
		}
	}

	// Remove system user
	execCommandFn("userdel", "mysql").Run()
	execCommandFn("groupdel", "mysql").Run()
	log.WriteString("Removed mysql user/group\n")

	execCommandFn("systemctl", "daemon-reload").Run()
	log.WriteString("systemctl daemon-reload done\n")

	return log.String(), nil
}

// DiagnoseService returns diagnostic info about the database service.
func DiagnoseService() map[string]any {
	diag := map[string]any{}

	// Service status
	for _, svc := range []string{"mariadb", "mysql"} {
		out, err := execCommandFn("systemctl", "is-active", svc).Output()
		status := strings.TrimSpace(string(out))
		if err == nil || status != "" {
			diag["service_name"] = svc
			diag["service_status"] = status
			break
		}
	}

	// Journal errors (last 20 lines)
	for _, svc := range []string{"mariadb", "mysql", "mysqld"} {
		out, err := execCommandFn("journalctl", "-u", svc, "-n", "20", "--no-pager", "-q").Output()
		if err == nil && len(out) > 0 {
			diag["journal"] = strings.TrimSpace(string(out))
			break
		}
	}

	// Socket check
	for _, sock := range []string{"/run/mysqld/mysqld.sock", "/var/run/mysqld/mysqld.sock", "/tmp/mysql.sock"} {
		if _, err := osStatFn(sock); err == nil {
			diag["socket"] = sock
			break
		}
	}

	// PID file check
	for _, pid := range []string{"/run/mysqld/mysqld.pid", "/var/run/mysqld/mysqld.pid"} {
		if data, err := osReadFileFn(pid); err == nil {
			diag["pid_file"] = pid
			diag["pid"] = strings.TrimSpace(string(data))
			break
		}
	}

	// Disk space
	out, err := execCommandFn("df", "-h", "/var/lib/mysql").Output()
	if err == nil {
		diag["disk"] = strings.TrimSpace(string(out))
	}

	// Data directory permissions
	if info, err := osStatFn("/var/lib/mysql"); err == nil {
		diag["data_dir_mode"] = info.Mode().String()
	} else {
		diag["data_dir"] = "missing"
	}

	return diag
}

func collectDBDiagnostics() string {
	var sb strings.Builder
	for _, svc := range []string{"mariadb", "mysql", "mysqld"} {
		out, err := execCommandFn("journalctl", "-u", svc, "-n", "10", "--no-pager", "-q").Output()
		if err == nil && len(out) > 10 {
			sb.WriteString("--- journalctl -u " + svc + " ---\n")
			sb.WriteString(strings.TrimSpace(string(out)))
			sb.WriteString("\n")
			break
		}
	}
	return sb.String()
}

// ListDatabases returns all UWAS-managed databases.
func ListDatabases() ([]DBInfo, error) {
	sql := `SELECT
		SCHEMA_NAME,
		ROUND(COALESCE(SUM(DATA_LENGTH + INDEX_LENGTH), 0) / 1024 / 1024, 2) AS size_mb,
		COUNT(TABLE_NAME) AS table_count
	FROM information_schema.SCHEMATA
	LEFT JOIN information_schema.TABLES ON TABLE_SCHEMA = SCHEMA_NAME
	WHERE SCHEMA_NAME NOT IN ('information_schema', 'mysql', 'performance_schema', 'sys')
	GROUP BY SCHEMA_NAME
	ORDER BY SCHEMA_NAME`

	out, err := runMySQLFn(sql)
	if err != nil {
		return nil, fmt.Errorf("list databases: %w", err)
	}

	var dbs []DBInfo
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" || strings.HasPrefix(line, "SCHEMA_NAME") {
			continue
		}
		fields := strings.Fields(line)
		db := DBInfo{Name: fields[0], Host: "localhost"}
		if len(fields) >= 2 {
			db.Size = fields[1] + " MB"
		}
		if len(fields) >= 3 {
			if _, err := fmt.Sscanf(fields[2], "%d", &db.Tables); err != nil {
				db.Tables = 0
			}
		}
		dbs = append(dbs, db)
	}
	return dbs, nil
}

// CreateResult contains the created database credentials.
type CreateResult struct {
	Name     string `json:"name"`
	User     string `json:"user"`
	Password string `json:"password"`
	Host     string `json:"host"`
}

// RemoteAccessResult contains the effective settings applied for remote DB access.
type RemoteAccessResult struct {
	User       string `json:"user"`
	Host       string `json:"host"`
	Database   string `json:"database,omitempty"`
	Password   string `json:"password,omitempty"`
	ConfigPath string `json:"config_path"`
	Restarted  bool   `json:"restarted"`
}

// CreateDatabase creates a new MySQL database and user. Returns credentials.
func CreateDatabase(name, user, password, host string) (*CreateResult, error) {
	if name == "" {
		return nil, fmt.Errorf("database name required")
	}
	if !validDBIdentifier(name) {
		return nil, fmt.Errorf("invalid database name: only letters, digits, underscore, hyphen allowed (max 64 chars)")
	}
	if user == "" {
		user = name
	}
	if !validDBIdentifier(user) {
		return nil, fmt.Errorf("invalid username: only letters, digits, underscore, hyphen allowed (max 64 chars)")
	}
	if password == "" {
		password = generateDBPassword()
	}
	if host == "" {
		host = "localhost"
	}

	sql := fmt.Sprintf(`
		CREATE DATABASE IF NOT EXISTS %s CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
		CREATE USER IF NOT EXISTS '%s'@'%s' IDENTIFIED BY '%s';
		GRANT ALL PRIVILEGES ON %s.* TO '%s'@'%s';
		FLUSH PRIVILEGES;
	`, backtick(name), escapeSQL(user), escapeSQL(host), escapeSQL(password), backtick(name), escapeSQL(user), escapeSQL(host))

	_, err := runMySQLFn(sql)
	if err != nil {
		return nil, fmt.Errorf("create database %q (user %q@%q): %w", name, user, host, err)
	}
	return &CreateResult{Name: name, User: user, Password: password, Host: host}, nil
}

// DropDatabase removes a database and its user.
func DropDatabase(name, user, host string) error {
	if !validDBIdentifier(name) {
		return fmt.Errorf("invalid database name")
	}
	if user == "" {
		user = name
	}
	if host == "" {
		host = "localhost"
	}

	sql := fmt.Sprintf(`
		DROP DATABASE IF EXISTS %s;
		DROP USER IF EXISTS '%s'@'%s';
		FLUSH PRIVILEGES;
	`, backtick(name), escapeSQL(user), escapeSQL(host))

	if _, err := runMySQLFn(sql); err != nil {
		return fmt.Errorf("drop database %q (user %q@%q): %w", name, user, host, err)
	}
	return nil
}

// ChangePassword changes the password for a database user.
func ChangePassword(user, host, newPassword string) error {
	if host == "" {
		host = "localhost"
	}
	sql := fmt.Sprintf("ALTER USER '%s'@'%s' IDENTIFIED BY '%s'; FLUSH PRIVILEGES;", escapeSQL(user), escapeSQL(host), escapeSQL(newPassword))
	if _, err := runMySQLFn(sql); err != nil {
		return fmt.Errorf("change password for user %q@%q: %w", user, host, err)
	}
	return nil
}

// ConfigureRemoteAccess makes the native MySQL/MariaDB service listen on all
// interfaces and creates a remote-capable user. If databaseName is set, the
// user is granted full privileges on that database only.
func ConfigureRemoteAccess(user, host, password, databaseName string) (*RemoteAccessResult, error) {
	if runtimeGOOS == "windows" {
		return nil, fmt.Errorf("not supported on Windows")
	}
	user = strings.TrimSpace(user)
	host = strings.TrimSpace(host)
	databaseName = strings.TrimSpace(databaseName)
	if user == "" {
		return nil, fmt.Errorf("user is required")
	}
	if !validDBIdentifier(user) {
		return nil, fmt.Errorf("invalid username: only letters, digits, underscore, hyphen allowed (max 64 chars)")
	}
	if host == "" {
		host = "%"
	}
	if strings.ContainsAny(host, "\x00\n\r'") {
		return nil, fmt.Errorf("invalid host")
	}
	if databaseName != "" && !validDBIdentifier(databaseName) {
		return nil, fmt.Errorf("invalid database name")
	}
	if password == "" {
		password = generateDBPassword()
	}

	configPath, err := setBindAddressAllInterfaces()
	if err != nil {
		return nil, err
	}

	sql := fmt.Sprintf("CREATE USER IF NOT EXISTS '%s'@'%s' IDENTIFIED BY '%s';\n", escapeSQL(user), escapeSQL(host), escapeSQL(password))
	if databaseName != "" {
		sql += fmt.Sprintf("GRANT ALL PRIVILEGES ON %s.* TO '%s'@'%s';\n", backtick(databaseName), escapeSQL(user), escapeSQL(host))
	}
	sql += "FLUSH PRIVILEGES;"
	if _, err := runMySQLFn(sql); err != nil {
		return nil, fmt.Errorf("create remote user %q@%q: %w", user, host, err)
	}
	if err := RestartService(); err != nil {
		return nil, fmt.Errorf("remote user created but database restart failed: %w", err)
	}
	return &RemoteAccessResult{
		User:       user,
		Host:       host,
		Database:   databaseName,
		Password:   password,
		ConfigPath: configPath,
		Restarted:  true,
	}, nil
}

// ListUsers returns all non-system database users.
func ListUsers() ([]DBUser, error) {
	sql := `SELECT User, Host FROM mysql.user WHERE User NOT IN ('root', 'mysql', 'mariadb.sys', 'debian-sys-maint', '') ORDER BY User`
	out, err := runMySQLFn(sql)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	var users []DBUser
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			users = append(users, DBUser{User: parts[0], Host: parts[1]})
		}
	}
	return users, nil
}

func setBindAddressAllInterfaces() (string, error) {
	candidates := []string{
		"/etc/mysql/mariadb.conf.d/50-server.cnf",
		"/etc/mysql/mysql.conf.d/mysqld.cnf",
		"/etc/my.cnf.d/server.cnf",
		"/etc/mysql/my.cnf",
		"/etc/my.cnf",
	}
	path := candidates[0]
	for _, candidate := range candidates {
		if _, err := osStatFn(candidate); err == nil {
			path = candidate
			break
		}
	}

	data, err := osReadFileFn(path)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("read mysql config %s: %w", path, err)
	}
	updated := rewriteBindAddress(string(data))
	if strings.TrimSpace(updated) == "" {
		updated = "[mysqld]\nbind-address = 0.0.0.0\n"
	}
	if err := osMkdirAllFn(filepath.Dir(path), 0755); err != nil {
		return "", fmt.Errorf("create mysql config dir: %w", err)
	}
	if err := osWriteFileFn(path, []byte(updated), 0644); err != nil {
		return "", fmt.Errorf("write mysql config %s: %w", path, err)
	}
	return path, nil
}

func rewriteBindAddress(in string) string {
	lines := strings.Split(in, "\n")
	var out []string
	inMysqld := false
	seenMysqld := false
	wroteBind := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if inMysqld && !wroteBind {
				out = append(out, "bind-address = 0.0.0.0")
				wroteBind = true
			}
			section := strings.ToLower(strings.Trim(trimmed, "[]"))
			inMysqld = section == "mysqld" || section == "server"
			if inMysqld {
				seenMysqld = true
			}
			out = append(out, line)
			continue
		}
		lower := strings.ToLower(trimmed)
		if inMysqld && (strings.HasPrefix(lower, "bind-address") || strings.HasPrefix(lower, "bind_address")) {
			if !wroteBind {
				out = append(out, "bind-address = 0.0.0.0")
				wroteBind = true
			}
			continue
		}
		if inMysqld && strings.HasPrefix(lower, "skip-networking") {
			out = append(out, "# "+line)
			continue
		}
		out = append(out, line)
	}
	if seenMysqld {
		if !wroteBind {
			out = append(out, "bind-address = 0.0.0.0")
		}
		return strings.TrimRight(strings.Join(out, "\n"), "\n") + "\n"
	}
	if strings.TrimSpace(in) != "" {
		out = append(out, "", "[mysqld]", "bind-address = 0.0.0.0")
	} else {
		out = []string{"[mysqld]", "bind-address = 0.0.0.0"}
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n") + "\n"
}

// DBUser represents a MySQL/MariaDB user.
type DBUser struct {
	User string `json:"user"`
	Host string `json:"host"`
}

// ExportDatabase exports a database to SQL using mysqldump.
func ExportDatabase(name string) ([]byte, error) {
	if !validDBIdentifier(name) {
		return nil, fmt.Errorf("invalid database name: %s", name)
	}
	// Try mariadb-dump first, then mysqldump
	for _, bin := range []string{"mariadb-dump", "mysqldump"} {
		path, err := execLookPathFn(bin)
		if err != nil {
			continue
		}
		out, err := execCommandFn(path, "-u", "root", "--single-transaction", "--routines", "--triggers", name).Output()
		if err == nil {
			return out, nil
		}
		// Try without -u root (let socket auth auto-detect user)
		out, err = execCommandFn(path, "--single-transaction", "--routines", "--triggers", name).Output()
		if err == nil {
			return out, nil
		}
	}
	return nil, fmt.Errorf("mysqldump/mariadb-dump not found or failed")
}

// ImportDatabase imports SQL data into a database.
func ImportDatabase(name string, sqlData []byte) error {
	if !validDBIdentifier(name) {
		return fmt.Errorf("invalid database name: %s", name)
	}
	for _, client := range []string{"mariadb", "mysql"} {
		bin, err := execLookPathFn(client)
		if err != nil {
			continue
		}
		cmd := execCommandFn(bin, "-u", "root", name)
		cmd.Stdin = strings.NewReader(string(sqlData))
		_, err = cmd.CombinedOutput()
		if err == nil {
			return nil
		}
		// Try without -u root (let socket auth auto-detect user)
		cmd = execCommandFn(bin, name)
		cmd.Stdin = strings.NewReader(string(sqlData))
		_, err = cmd.CombinedOutput()
		if err == nil {
			return nil
		}
	}
	return fmt.Errorf("mysql/mariadb client not found or import failed")
}

// InstallMySQL attempts to install MySQL/MariaDB.
func InstallMySQL() (string, error) {
	if runtimeGOOS == "windows" {
		return "", fmt.Errorf("not supported on Windows")
	}

	// Try apt (Debian/Ubuntu)
	if _, err := execLookPathFn("apt"); err == nil {
		cmd := execCommandFn("apt", "install", "-y", "mariadb-server", "mariadb-client")
		out, err := cmd.CombinedOutput()
		if err == nil {
			// Start and enable
			execCommandFn("systemctl", "start", "mariadb").Run()
			execCommandFn("systemctl", "enable", "mariadb").Run()
			// Secure installation basics
			runMySQLFn("DELETE FROM mysql.user WHERE User='';")
			runMySQLFn("DELETE FROM mysql.user WHERE User='root' AND Host NOT IN ('localhost', '127.0.0.1', '::1');")
			runMySQLFn("FLUSH PRIVILEGES;")
		}
		return string(out), err
	}

	// Try dnf (RHEL/Fedora)
	if _, err := execLookPathFn("dnf"); err == nil {
		cmd := execCommandFn("dnf", "install", "-y", "mariadb-server", "mariadb")
		out, err := cmd.CombinedOutput()
		if err == nil {
			execCommandFn("systemctl", "start", "mariadb").Run()
			execCommandFn("systemctl", "enable", "mariadb").Run()
		}
		return string(out), err
	}

	return "", fmt.Errorf("no supported package manager found (apt/dnf)")
}

func runMySQL(sql string) (string, error) {
	return runMySQLOnHost(sql, "", 0, "")
}

func runMySQLOnHost(sql, host string, port int, password string) (string, error) {
	// Ensure socket directory exists for native installs
	if host == "" {
		for _, dir := range []string{"/run/mysqld", "/var/run/mysqld"} {
			osMkdirAllFn(dir, 0755)
			execCommandFn("chown", "mysql:mysql", dir).Run()
		}
	}

	for _, client := range []string{"mariadb", "mysql"} {
		bin, err := execLookPathFn(client)
		if err != nil {
			continue
		}

		// Docker/remote: connect via TCP with password
		if host != "" && password != "" {
			args := []string{"-u", "root", "-p" + password, "-h", "127.0.0.1",
				"--batch", "--skip-column-names", "-e", sql}
			if port > 0 {
				args = append(args, "-P", fmt.Sprintf("%d", port))
			}
			cmd := execCommandFn(bin, args...)
			out, err := cmd.CombinedOutput()
			if err == nil {
				return string(out), nil
			}
			return string(out), fmt.Errorf("%s TCP error: %w — %s", client, err, string(out))
		}

		// Native: direct as root (unix_socket auth)
		cmd := execCommandFn(bin, "-u", "root", "--batch", "--skip-column-names", "-e", sql)
		out, err := cmd.CombinedOutput()
		if err == nil {
			return string(out), nil
		}

		// Native: without -u root (let socket auth auto-detect user)
		cmd = execCommandFn(bin, "--batch", "--skip-column-names", "-e", sql)
		out, err = cmd.CombinedOutput()
		if err == nil {
			return string(out), nil
		}

		// Native: via socket explicitly
		for _, sock := range []string{"/run/mysqld/mysqld.sock", "/var/run/mysqld/mysqld.sock", "/tmp/mysql.sock"} {
			if _, statErr := osStatFn(sock); statErr != nil {
				continue
			}
			cmd = execCommandFn(bin, "-u", "root", "--socket="+sock, "--batch", "--skip-column-names", "-e", sql)
			out, err = cmd.CombinedOutput()
			if err == nil {
				return string(out), nil
			}
		}

		return string(out), fmt.Errorf("%s error: %w — output: %s", client, err, string(out))
	}

	return "", fmt.Errorf("neither mariadb nor mysql client found")
}

func generateDBPassword() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}

func backtick(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

// ValidDBIdentifier checks that a database/user name contains only safe characters.
func ValidDBIdentifier(s string) bool {
	return validDBIdentifier(s)
}

// EscapeSQL escapes a string for safe use in SQL statements.
func EscapeSQL(s string) string { return escapeSQL(s) }

// BacktickID wraps a name in backticks for use as a SQL identifier.
func BacktickID(s string) string { return backtick(s) }

// RunSQL executes a SQL statement and returns the output.
func RunSQL(sql string) (string, error) { return runMySQLFn(sql) }

// DatabaseExists reports whether the given schema name exists. Returns
// (false, nil) for a clean negative result, (false, err) only if the underlying
// MySQL command itself failed (mysql client missing, auth error, etc.).
func DatabaseExists(name string) (bool, error) {
	if !validDBIdentifier(name) {
		return false, nil
	}
	sql := fmt.Sprintf("SELECT SCHEMA_NAME FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = '%s'", EscapeSQL(name))
	out, err := runMySQLFn(sql)
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.EqualFold(line, "SCHEMA_NAME") {
			continue
		}
		// MySQL CLI prints the value on its own line.
		if strings.EqualFold(line, name) {
			return true, nil
		}
	}
	return false, nil
}

func validDBIdentifier(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	if strings.HasPrefix(s, "-") {
		return false
	}
	for _, c := range s {
		// Excluded '-' because it is a SQL operator that could be used
		// for comment injection (e.g., "db-; DROP--") in string-formatted queries.
		// Database names should use underscore '_' as separator instead.
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_') {
			return false
		}
	}
	return true
}

func escapeSQL(s string) string {
	// IMPORTANT: escape backslashes FIRST, then quotes.
	// Reversing this order breaks the escape (\ -> \\ -> \\' leaves quote unescaped).
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "'", "\\'")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "\x00", "")
	return s
}

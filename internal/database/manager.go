// Package database manages MySQL/MariaDB databases for hosted domains.
package database

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
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
	if runtime.GOOS == "windows" {
		return Status{Backend: "none"}
	}

	st := Status{}

	// Check mariadb first, then mysql
	for _, bin := range []string{"mariadb", "mysql"} {
		if path, err := exec.LookPath(bin); err == nil {
			st.Installed = true
			if bin == "mariadb" {
				st.Backend = "mariadb"
			} else {
				st.Backend = "mysql"
			}
			// Get version
			out, err := exec.Command(path, "--version").Output()
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
		bin, err := exec.LookPath(method[0])
		if err != nil {
			continue
		}
		cmd := exec.Command(bin, method[1:]...)
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
	if runtime.GOOS == "windows" {
		return fmt.Errorf("not supported on Windows")
	}

	// Ensure socket directory exists (common issue after reboot).
	for _, dir := range []string{"/run/mysqld", "/var/run/mysqld"} {
		os.MkdirAll(dir, 0755)
		exec.Command("chown", "mysql:mysql", dir).Run()
	}

	// Try mariadb first, then mysql
	var lastErr string
	for _, svc := range []string{"mariadb", "mysql", "mysqld"} {
		out, err := exec.Command("systemctl", "start", svc).CombinedOutput()
		if err == nil {
			exec.Command("systemctl", "enable", svc).Run()
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
	if runtime.GOOS == "windows" {
		return fmt.Errorf("not supported on Windows")
	}
	var lastErr string
	for _, svc := range []string{"mariadb", "mysql", "mysqld"} {
		out, err := exec.Command("systemctl", "stop", svc).CombinedOutput()
		if err == nil {
			return nil
		}
		lastErr = strings.TrimSpace(string(out))
	}
	// Force kill as fallback
	exec.Command("pkill", "-9", "mysqld").Run()
	exec.Command("pkill", "-9", "mariadbd").Run()
	return fmt.Errorf("could not stop MySQL/MariaDB (force killed): %s", lastErr)
}

// RestartService restarts MySQL/MariaDB service.
func RestartService() error {
	if runtime.GOOS == "windows" {
		return fmt.Errorf("not supported on Windows")
	}
	var lastErr string
	for _, svc := range []string{"mariadb", "mysql", "mysqld"} {
		out, err := exec.Command("systemctl", "restart", svc).CombinedOutput()
		if err == nil {
			return nil
		}
		lastErr = strings.TrimSpace(string(out))
	}
	diag := collectDBDiagnostics()
	return fmt.Errorf("could not restart MySQL/MariaDB: %s\n%s", lastErr, diag)
}

// UninstallService completely removes MySQL/MariaDB packages and data.
func UninstallService() (string, error) {
	if runtime.GOOS == "windows" {
		return "", fmt.Errorf("not supported on Windows")
	}

	var log strings.Builder

	// Stop first
	StopService()
	log.WriteString("Service stopped\n")

	// Purge packages
	if _, err := exec.LookPath("apt"); err == nil {
		out, _ := exec.Command("apt", "purge", "-y",
			"mariadb-server", "mariadb-client", "mariadb-common",
			"mysql-server", "mysql-client", "mysql-common",
		).CombinedOutput()
		log.WriteString(string(out))
		exec.Command("apt", "autoremove", "-y").CombinedOutput()
	} else if _, err := exec.LookPath("dnf"); err == nil {
		out, _ := exec.Command("dnf", "remove", "-y",
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
		if _, err := os.Stat(path); err == nil {
			os.RemoveAll(path)
			log.WriteString("Removed " + path + "\n")
		}
	}

	// Remove system user
	exec.Command("userdel", "mysql").Run()
	exec.Command("groupdel", "mysql").Run()
	log.WriteString("Removed mysql user/group\n")

	exec.Command("systemctl", "daemon-reload").Run()
	log.WriteString("systemctl daemon-reload done\n")

	return log.String(), nil
}

// DiagnoseService returns diagnostic info about the database service.
func DiagnoseService() map[string]any {
	diag := map[string]any{}

	// Service status
	for _, svc := range []string{"mariadb", "mysql"} {
		out, err := exec.Command("systemctl", "is-active", svc).Output()
		status := strings.TrimSpace(string(out))
		if err == nil || status != "" {
			diag["service_name"] = svc
			diag["service_status"] = status
			break
		}
	}

	// Journal errors (last 20 lines)
	for _, svc := range []string{"mariadb", "mysql", "mysqld"} {
		out, err := exec.Command("journalctl", "-u", svc, "-n", "20", "--no-pager", "-q").Output()
		if err == nil && len(out) > 0 {
			diag["journal"] = strings.TrimSpace(string(out))
			break
		}
	}

	// Socket check
	for _, sock := range []string{"/run/mysqld/mysqld.sock", "/var/run/mysqld/mysqld.sock", "/tmp/mysql.sock"} {
		if _, err := os.Stat(sock); err == nil {
			diag["socket"] = sock
			break
		}
	}

	// PID file check
	for _, pid := range []string{"/run/mysqld/mysqld.pid", "/var/run/mysqld/mysqld.pid"} {
		if data, err := os.ReadFile(pid); err == nil {
			diag["pid_file"] = pid
			diag["pid"] = strings.TrimSpace(string(data))
			break
		}
	}

	// Disk space
	out, err := exec.Command("df", "-h", "/var/lib/mysql").Output()
	if err == nil {
		diag["disk"] = strings.TrimSpace(string(out))
	}

	// Data directory permissions
	if info, err := os.Stat("/var/lib/mysql"); err == nil {
		diag["data_dir_mode"] = info.Mode().String()
	} else {
		diag["data_dir"] = "missing"
	}

	return diag
}

func collectDBDiagnostics() string {
	var sb strings.Builder
	for _, svc := range []string{"mariadb", "mysql", "mysqld"} {
		out, err := exec.Command("journalctl", "-u", svc, "-n", "10", "--no-pager", "-q").Output()
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

	out, err := runMySQL(sql)
	if err != nil {
		return nil, err
	}

	var dbs []DBInfo
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" || strings.HasPrefix(line, "SCHEMA_NAME") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 1 {
			continue
		}
		db := DBInfo{Name: fields[0], Host: "localhost"}
		if len(fields) >= 2 {
			db.Size = fields[1] + " MB"
		}
		if len(fields) >= 3 {
			fmt.Sscanf(fields[2], "%d", &db.Tables)
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

// CreateDatabase creates a new MySQL database and user. Returns credentials.
func CreateDatabase(name, user, password, host string) (*CreateResult, error) {
	if name == "" {
		return nil, fmt.Errorf("database name required")
	}
	if user == "" {
		user = name
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
	`, backtick(name), user, host, escapeSQL(password), backtick(name), user, host)

	_, err := runMySQL(sql)
	if err != nil {
		return nil, err
	}
	return &CreateResult{Name: name, User: user, Password: password, Host: host}, nil
}

// DropDatabase removes a database and its user.
func DropDatabase(name, user, host string) error {
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
	`, backtick(name), user, host)

	_, err := runMySQL(sql)
	return err
}

// ChangePassword changes the password for a database user.
func ChangePassword(user, host, newPassword string) error {
	if host == "" {
		host = "localhost"
	}
	sql := fmt.Sprintf("ALTER USER '%s'@'%s' IDENTIFIED BY '%s'; FLUSH PRIVILEGES;", user, host, newPassword)
	_, err := runMySQL(sql)
	return err
}

// ListUsers returns all non-system database users.
func ListUsers() ([]DBUser, error) {
	sql := `SELECT User, Host FROM mysql.user WHERE User NOT IN ('root', 'mysql', 'mariadb.sys', 'debian-sys-maint', '') ORDER BY User`
	out, err := runMySQL(sql)
	if err != nil {
		return nil, err
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

// DBUser represents a MySQL/MariaDB user.
type DBUser struct {
	User string `json:"user"`
	Host string `json:"host"`
}

// ExportDatabase exports a database to SQL using mysqldump.
func ExportDatabase(name string) ([]byte, error) {
	// Try mariadb-dump first, then mysqldump
	for _, bin := range []string{"mariadb-dump", "mysqldump"} {
		path, err := exec.LookPath(bin)
		if err != nil {
			continue
		}
		out, err := exec.Command(path, "-u", "root", "--single-transaction", "--routines", "--triggers", name).Output()
		if err == nil {
			return out, nil
		}
		// Try with sudo
		out, err = exec.Command("sudo", path, "--single-transaction", "--routines", "--triggers", name).Output()
		if err == nil {
			return out, nil
		}
	}
	return nil, fmt.Errorf("mysqldump/mariadb-dump not found or failed")
}

// ImportDatabase imports SQL data into a database.
func ImportDatabase(name string, sqlData []byte) error {
	for _, client := range []string{"mariadb", "mysql"} {
		bin, err := exec.LookPath(client)
		if err != nil {
			continue
		}
		cmd := exec.Command(bin, "-u", "root", name)
		cmd.Stdin = strings.NewReader(string(sqlData))
		out, err := cmd.CombinedOutput()
		if err == nil {
			return nil
		}
		// Try with sudo
		cmd = exec.Command("sudo", bin, name)
		cmd.Stdin = strings.NewReader(string(sqlData))
		out, err = cmd.CombinedOutput()
		if err == nil {
			return nil
		}
		_ = out
	}
	return fmt.Errorf("mysql/mariadb client not found or import failed")
}

// InstallMySQL attempts to install MySQL/MariaDB.
func InstallMySQL() (string, error) {
	if runtime.GOOS == "windows" {
		return "", fmt.Errorf("not supported on Windows")
	}

	// Try apt (Debian/Ubuntu)
	if _, err := exec.LookPath("apt"); err == nil {
		cmd := exec.Command("apt", "install", "-y", "mariadb-server", "mariadb-client")
		out, err := cmd.CombinedOutput()
		if err == nil {
			// Start and enable
			exec.Command("systemctl", "start", "mariadb").Run()
			exec.Command("systemctl", "enable", "mariadb").Run()
			// Secure installation basics
			runMySQL("DELETE FROM mysql.user WHERE User='';")
			runMySQL("DELETE FROM mysql.user WHERE User='root' AND Host NOT IN ('localhost', '127.0.0.1', '::1');")
			runMySQL("FLUSH PRIVILEGES;")
		}
		return string(out), err
	}

	// Try dnf (RHEL/Fedora)
	if _, err := exec.LookPath("dnf"); err == nil {
		cmd := exec.Command("dnf", "install", "-y", "mariadb-server", "mariadb")
		out, err := cmd.CombinedOutput()
		if err == nil {
			exec.Command("systemctl", "start", "mariadb").Run()
			exec.Command("systemctl", "enable", "mariadb").Run()
		}
		return string(out), err
	}

	return "", fmt.Errorf("no supported package manager found (apt/dnf)")
}

func runMySQL(sql string) (string, error) {
	// Ensure socket directory exists (common issue after reboot)
	for _, dir := range []string{"/run/mysqld", "/var/run/mysqld"} {
		os.MkdirAll(dir, 0755)
		exec.Command("chown", "mysql:mysql", dir).Run()
	}

	// Try mariadb client first (preferred on modern Ubuntu), then mysql
	for _, client := range []string{"mariadb", "mysql"} {
		bin, err := exec.LookPath(client)
		if err != nil {
			continue
		}

		// Method 1: Direct as root (unix_socket auth)
		cmd := exec.Command(bin, "-u", "root", "--batch", "--skip-column-names", "-e", sql)
		out, err := cmd.CombinedOutput()
		if err == nil {
			return string(out), nil
		}

		// Method 2: With sudo (if not running as root)
		cmd = exec.Command("sudo", bin, "--batch", "--skip-column-names", "-e", sql)
		out, err = cmd.CombinedOutput()
		if err == nil {
			return string(out), nil
		}

		// Method 3: Via socket explicitly
		for _, sock := range []string{"/run/mysqld/mysqld.sock", "/var/run/mysqld/mysqld.sock", "/tmp/mysql.sock"} {
			if _, statErr := os.Stat(sock); statErr != nil {
				continue
			}
			cmd = exec.Command(bin, "-u", "root", "--socket="+sock, "--batch", "--skip-column-names", "-e", sql)
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
	rand.Read(b)
	return hex.EncodeToString(b)
}

func backtick(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

func escapeSQL(s string) string {
	s = strings.ReplaceAll(s, "'", "\\'")
	s = strings.ReplaceAll(s, "\\", "\\\\")
	return s
}

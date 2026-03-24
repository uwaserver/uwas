// Package database manages MySQL/MariaDB databases for hosted domains.
package database

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
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

	// Check if running
	out, err := exec.Command("mysqladmin", "ping", "--silent").CombinedOutput()
	if err == nil && strings.Contains(string(out), "alive") {
		st.Running = true
	}

	return st
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
	// Try without password first (common for root on fresh installs)
	cmd := exec.Command("mysql", "-u", "root", "-e", sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Try with sudo
		cmd = exec.Command("sudo", "mysql", "-e", sql)
		out, err = cmd.CombinedOutput()
	}
	return string(out), err
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

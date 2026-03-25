// Package migrate provides site migration from remote servers via SSH.
package migrate

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// MigrateRequest contains migration parameters.
type MigrateRequest struct {
	SourceHost string `json:"source_host"` // SSH host (user@ip or user@hostname)
	SourcePort string `json:"source_port"` // SSH port (default 22)
	SourcePath string `json:"source_path"` // remote web root (e.g. /var/www/example.com)
	SSHKey     string `json:"ssh_key"`     // path to SSH private key
	SSHPass    string `json:"ssh_pass"`    // SSH password (if no key)
	Domain     string `json:"domain"`      // target domain on this server
	LocalRoot  string `json:"local_root"`  // local web root
	// Database migration
	DBHost     string `json:"db_host"`     // remote DB host (default: localhost on remote)
	DBName     string `json:"db_name"`     // remote database name
	DBUser     string `json:"db_user"`     // remote DB user
	DBPass     string `json:"db_pass"`     // remote DB password
}

// MigrateResult contains migration status.
type MigrateResult struct {
	Status     string    `json:"status"` // running, done, error
	Domain     string    `json:"domain"`
	FilesSync  string    `json:"files_sync"`
	DBImport   string    `json:"db_import"`
	Output     string    `json:"output"`
	Error      string    `json:"error,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
	Duration   string    `json:"duration,omitempty"`
}

// Migrate performs a full site migration from a remote server.
func Migrate(req MigrateRequest) *MigrateResult {
	result := &MigrateResult{
		Status:    "running",
		Domain:    req.Domain,
		StartedAt: time.Now(),
	}
	var log strings.Builder

	if req.SourceHost == "" {
		result.Status = "error"
		result.Error = "source_host is required"
		return result
	}
	if req.LocalRoot == "" {
		result.Status = "error"
		result.Error = "local_root is required"
		return result
	}
	if req.SourcePort == "" {
		req.SourcePort = "22"
	}

	// Ensure local directory exists
	os.MkdirAll(req.LocalRoot, 0755)

	// Step 1: Sync files via rsync over SSH
	log.WriteString("=== Syncing files ===\n")
	filesResult := syncFiles(req, &log)
	result.FilesSync = filesResult

	// Step 2: Dump and import database (if configured)
	if req.DBName != "" {
		log.WriteString("\n=== Migrating database ===\n")
		dbResult := migrateDB(req, &log)
		result.DBImport = dbResult
	}

	// Step 3: Fix permissions
	log.WriteString("\n=== Fixing permissions ===\n")
	exec.Command("chown", "-R", "www-data:www-data", req.LocalRoot).Run()
	log.WriteString("Ownership set to www-data:www-data\n")

	// Step 4: Update wp-config.php if WordPress
	wpConfig := filepath.Join(req.LocalRoot, "wp-config.php")
	if _, err := os.Stat(wpConfig); err == nil && req.DBName != "" {
		log.WriteString("\n=== Updating wp-config.php ===\n")
		updateWPConfigDB(wpConfig, req.DBName, req.DBUser, req.DBPass, &log)
	}

	result.Status = "done"
	result.Output = log.String()
	result.FinishedAt = time.Now()
	result.Duration = result.FinishedAt.Sub(result.StartedAt).Round(time.Second).String()
	return result
}

// syncFiles uses rsync to copy files from remote server.
func syncFiles(req MigrateRequest, log *strings.Builder) string {
	sshOpts := fmt.Sprintf("ssh -p %s -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null", req.SourcePort)
	if req.SSHKey != "" {
		sshOpts += " -i " + req.SSHKey
	}

	src := req.SourceHost + ":" + req.SourcePath + "/"
	dst := req.LocalRoot + "/"

	args := []string{
		"-avz", "--progress", "--delete",
		"-e", sshOpts,
		src, dst,
	}

	// If using password, use sshpass
	var cmd *exec.Cmd
	if req.SSHPass != "" && req.SSHKey == "" {
		if _, err := exec.LookPath("sshpass"); err != nil {
			log.WriteString("WARNING: sshpass not installed, password auth may fail\n")
			log.WriteString("Install with: apt install sshpass\n")
		}
		cmd = exec.Command("sshpass", append([]string{"-p", req.SSHPass, "rsync"}, args...)...)
	} else {
		cmd = exec.Command("rsync", args...)
	}

	out, err := cmd.CombinedOutput()
	log.Write(out)
	if err != nil {
		log.WriteString(fmt.Sprintf("\nrsync error: %s\n", err))
		return "error: " + err.Error()
	}
	return "ok"
}

// migrateDB dumps remote database and imports locally.
func migrateDB(req MigrateRequest, log *strings.Builder) string {
	dbHost := req.DBHost
	if dbHost == "" {
		dbHost = "localhost"
	}

	// Build SSH + mysqldump command
	sshArgs := []string{
		"-p", req.SourcePort,
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
	}
	if req.SSHKey != "" {
		sshArgs = append(sshArgs, "-i", req.SSHKey)
	}

	dumpCmd := fmt.Sprintf("mysqldump -h %s -u %s -p'%s' --single-transaction --quick %s",
		dbHost, req.DBUser, req.DBPass, req.DBName)

	sshArgs = append(sshArgs, req.SourceHost, dumpCmd)

	// Dump via SSH
	var cmd *exec.Cmd
	if req.SSHPass != "" && req.SSHKey == "" {
		cmd = exec.Command("sshpass", append([]string{"-p", req.SSHPass, "ssh"}, sshArgs...)...)
	} else {
		cmd = exec.Command("ssh", sshArgs...)
	}

	dumpFile := filepath.Join(os.TempDir(), fmt.Sprintf("uwas-migrate-%s-%d.sql", req.DBName, time.Now().Unix()))
	defer os.Remove(dumpFile)

	dump, err := cmd.Output()
	if err != nil {
		stderr := ""
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr = string(exitErr.Stderr)
		}
		log.WriteString(fmt.Sprintf("mysqldump failed: %s — %s\n", err, stderr))
		return "error: dump failed"
	}

	if err := os.WriteFile(dumpFile, dump, 0600); err != nil {
		log.WriteString(fmt.Sprintf("write dump: %s\n", err))
		return "error: write failed"
	}
	log.WriteString(fmt.Sprintf("Database dump: %d bytes\n", len(dump)))

	// Create local database
	log.WriteString("Creating local database...\n")
	for _, client := range []string{"mariadb", "mysql"} {
		bin, err := exec.LookPath(client)
		if err != nil {
			continue
		}
		// Create DB
		exec.Command(bin, "-u", "root", "-e",
			fmt.Sprintf("CREATE DATABASE IF NOT EXISTS `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci", req.DBName)).Run()
		// Create user
		exec.Command(bin, "-u", "root", "-e",
			fmt.Sprintf("CREATE USER IF NOT EXISTS '%s'@'localhost' IDENTIFIED BY '%s'", req.DBUser, req.DBPass)).Run()
		exec.Command(bin, "-u", "root", "-e",
			fmt.Sprintf("GRANT ALL PRIVILEGES ON `%s`.* TO '%s'@'localhost'; FLUSH PRIVILEGES", req.DBName, req.DBUser)).Run()

		// Import
		importCmd := exec.Command(bin, "-u", "root", req.DBName)
		importCmd.Stdin, _ = os.Open(dumpFile)
		if out, err := importCmd.CombinedOutput(); err != nil {
			log.WriteString(fmt.Sprintf("import error: %s — %s\n", err, string(out)))
			return "error: import failed"
		}
		log.WriteString("Database imported successfully\n")
		return "ok"
	}

	log.WriteString("Neither mariadb nor mysql client found\n")
	return "error: no mysql client"
}

// updateWPConfigDB updates database credentials in wp-config.php.
func updateWPConfigDB(path, dbName, dbUser, dbPass string, log *strings.Builder) {
	data, err := os.ReadFile(path)
	if err != nil {
		log.WriteString(fmt.Sprintf("read wp-config: %s\n", err))
		return
	}
	content := string(data)

	replacements := map[string]string{
		"DB_NAME": dbName,
		"DB_USER": dbUser,
		"DB_PASSWORD": dbPass,
		"DB_HOST": "localhost",
	}

	for key, val := range replacements {
		// Match: define('DB_NAME', 'anything');
		old := fmt.Sprintf("define('%s'", key)
		idx := strings.Index(content, old)
		if idx < 0 {
			continue
		}
		// Find the end of the define statement
		end := strings.Index(content[idx:], ");")
		if end < 0 {
			continue
		}
		newLine := fmt.Sprintf("define('%s', '%s')", key, strings.ReplaceAll(val, "'", "\\'"))
		content = content[:idx] + newLine + content[idx+end+2:]
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		log.WriteString(fmt.Sprintf("write wp-config: %s\n", err))
		return
	}
	log.WriteString("wp-config.php updated with local DB credentials\n")
}

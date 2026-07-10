// Package migrate provides site migration from remote servers via SSH.
package migrate

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// migrateHostRe matches an optional "user@" prefix followed by a hostname or IP.
// It deliberately excludes whitespace and leading dashes so a value can never be
// parsed by ssh/rsync as a flag.
var migrateHostRe = regexp.MustCompile(`^([a-zA-Z0-9._-]+@)?[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// validateSSHInput rejects migration parameters that could inject additional
// ssh/rsync options. The classic vector is a value like source_port="22
// -oProxyCommand=touch${IFS}/tmp/pwn" which, once interpolated into the rsync
// "-e ssh ..." string, is split on whitespace and reaches ssh as a flag,
// yielding local command execution.
func validateSSHInput(req MigrateRequest) error {
	if p, err := strconv.Atoi(req.SourcePort); err != nil || p < 1 || p > 65535 {
		return fmt.Errorf("invalid source_port")
	}
	if !migrateHostRe.MatchString(req.SourceHost) {
		return fmt.Errorf("invalid source_host")
	}
	if req.SSHKey != "" {
		if !filepath.IsAbs(req.SSHKey) ||
			strings.ContainsAny(req.SSHKey, " \t\r\n") ||
			strings.HasPrefix(filepath.Base(req.SSHKey), "-") {
			return fmt.Errorf("invalid ssh_key")
		}
	}
	if strings.HasPrefix(req.SourcePath, "-") ||
		strings.ContainsAny(req.SourcePath, "\r\n\x00") {
		return fmt.Errorf("invalid source_path")
	}
	return nil
}

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
	DBHost string `json:"db_host"` // remote DB host (default: localhost on remote)
	DBName string `json:"db_name"` // remote database name
	DBUser string `json:"db_user"` // remote DB user
	DBPass string `json:"db_pass"` // remote DB password
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

// Hooks for testing — override in tests to avoid real exec calls.
var (
	runSyncFiles = syncFilesReal
	runMigrateDB = migrateDBReal
	runChown     = func(root string) { exec.Command("chown", "-R", "www-data:www-data", root).Run() }
	// execCommandFn allows tests to intercept exec.Command calls.
	execCommandFn = exec.Command
	// execLookPathFn allows tests to intercept exec.LookPath calls.
	execLookPathFn = exec.LookPath
	// tempDirFn allows tests to override the temp directory.
	tempDirFn = os.TempDir
)

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
	if err := validateSSHInput(req); err != nil {
		result.Status = "error"
		result.Error = err.Error()
		return result
	}

	// Ensure local directory exists
	os.MkdirAll(req.LocalRoot, 0755)

	// Step 1: Sync files via rsync over SSH
	log.WriteString("=== Syncing files ===\n")
	filesResult := runSyncFiles(req, &log)
	result.FilesSync = filesResult

	// Step 2: Dump and import database (if configured)
	if req.DBName != "" {
		log.WriteString("\n=== Migrating database ===\n")
		dbResult := runMigrateDB(req, &log)
		result.DBImport = dbResult
	}

	// Step 3: Fix permissions
	log.WriteString("\n=== Fixing permissions ===\n")
	runChown(req.LocalRoot)
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

// buildSSHOpts builds the SSH command options string for rsync.
func buildSSHOpts(port, sshKey string) string {
	opts := fmt.Sprintf("ssh -p %s -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=/dev/null", port)
	if sshKey != "" {
		opts += " -i " + sshKey
	}
	return opts
}

// buildRsyncArgs builds the rsync argument list.
func buildRsyncArgs(sshOpts, src, dst string) []string {
	return []string{
		"-avz", "--progress", "--delete",
		"-e", sshOpts,
		src, dst,
	}
}

// buildSSHArgs builds the SSH argument list for remote commands.
func buildSSHArgs(port, sshKey, sourceHost, remoteCmd string) []string {
	args := []string{
		"-p", port,
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "UserKnownHostsFile=/dev/null",
	}
	if sshKey != "" {
		args = append(args, "-i", sshKey)
	}
	args = append(args, sourceHost, remoteCmd)
	return args
}

// buildMysqldumpCmd builds the remote mysqldump command string. The password is
// passed via MYSQL_PWD in the remote environment (the login shell evaluates the
// leading VAR=value assignment) instead of -p<pass>, so it does not appear in
// the long-lived mysqldump process's /proc/<pid>/cmdline on the source host.
func buildMysqldumpCmd(dbHost, dbUser, dbPass, dbName string) string {
	return fmt.Sprintf("MYSQL_PWD=%s mysqldump -h %s -u %s --single-transaction --quick %s",
		shellQuote(dbPass), shellQuote(dbHost), shellQuote(dbUser), shellQuote(dbName))
}

// syncFilesReal uses rsync to copy files from remote server.
func syncFilesReal(req MigrateRequest, log *strings.Builder) string {
	sshOpts := buildSSHOpts(req.SourcePort, req.SSHKey)

	src := req.SourceHost + ":" + req.SourcePath + "/"
	dst := req.LocalRoot + "/"
	args := buildRsyncArgs(sshOpts, src, dst)

	// If using password, use sshpass
	var cmd *exec.Cmd
	if req.SSHPass != "" && req.SSHKey == "" {
		if _, err := execLookPathFn("sshpass"); err != nil {
			log.WriteString("WARNING: sshpass not installed, password auth may fail\n")
			log.WriteString("Install with: apt install sshpass\n")
		}
		cmd = execCommandFn("sshpass", append([]string{"-e", "rsync"}, args...)...)
		cmd.Env = append(os.Environ(), "SSHPASS="+req.SSHPass)
	} else {
		cmd = execCommandFn("rsync", args...)
	}

	out, err := cmd.CombinedOutput()
	log.Write(out)
	if err != nil {
		log.WriteString(fmt.Sprintf("\nrsync error: %s\n", err))
		return "error: " + err.Error()
	}
	return "ok"
}

// migrateDBReal dumps remote database and imports locally.
func migrateDBReal(req MigrateRequest, log *strings.Builder) string {
	if !validMigrateDBIdentifier(req.DBName) {
		log.WriteString("invalid database name\n")
		return "error: invalid database name"
	}
	// An empty DBUser would make CREATE USER/GRANT below target ''@'localhost' —
	// MySQL's anonymous account — granting ALL PRIVILEGES on the migrated DB to
	// a passwordless account any local process can use. Require an explicit user.
	if req.DBUser == "" {
		log.WriteString("database user is required\n")
		return "error: database user is required"
	}
	if !validMigrateDBIdentifier(req.DBUser) {
		log.WriteString("invalid database user\n")
		return "error: invalid database user"
	}

	dbHost := req.DBHost
	if dbHost == "" {
		dbHost = "localhost"
	}

	dumpCmd := buildMysqldumpCmd(dbHost, req.DBUser, req.DBPass, req.DBName)
	sshArgs := buildSSHArgs(req.SourcePort, req.SSHKey, req.SourceHost, dumpCmd)

	// Dump via SSH
	var cmd *exec.Cmd
	if req.SSHPass != "" && req.SSHKey == "" {
		cmd = execCommandFn("sshpass", append([]string{"-e", "ssh"}, sshArgs...)...)
		cmd.Env = append(os.Environ(), "SSHPASS="+req.SSHPass)
	} else {
		cmd = execCommandFn("ssh", sshArgs...)
	}

	// Unique temp file via CreateTemp — a name built from only (DBName, unix
	// second) collides when two migrations of the same DB run in the same
	// second (concurrent processes, or rapid retries), and one's deferred
	// os.Remove then deletes the other's dump out from under it.
	dumpF, err := os.CreateTemp(tempDirFn(), fmt.Sprintf("uwas-migrate-%s-*.sql", req.DBName))
	if err != nil {
		log.WriteString(fmt.Sprintf("write dump file: %s\n", err))
		return "error: write failed"
	}
	dumpFile := dumpF.Name()
	_ = dumpF.Close()
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
		bin, err := execLookPathFn(client)
		if err != nil {
			continue
		}
		// Create DB
		execCommandFn(bin, "-u", "root", "-e",
			fmt.Sprintf("CREATE DATABASE IF NOT EXISTS %s CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci", sqlIdent(req.DBName))).Run()
		safeUser := sqlString(req.DBUser)
		safePass := sqlString(req.DBPass)
		safeName := sqlIdent(req.DBName)
		execCommandFn(bin, "-u", "root", "-e",
			fmt.Sprintf("CREATE USER IF NOT EXISTS '%s'@'localhost' IDENTIFIED BY '%s'", safeUser, safePass)).Run()
		execCommandFn(bin, "-u", "root", "-e",
			fmt.Sprintf("GRANT ALL PRIVILEGES ON %s.* TO '%s'@'localhost'; FLUSH PRIVILEGES", safeName, safeUser)).Run()

		// Import
		importCmd := execCommandFn(bin, "-u", "root", req.DBName)
		dump, err := os.Open(dumpFile)
		if err != nil {
			log.WriteString(fmt.Sprintf("import error: cannot open dump %s: %s\n", dumpFile, err))
			return "error: import failed"
		}
		defer dump.Close()
		importCmd.Stdin = dump
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
		"DB_NAME":     dbName,
		"DB_USER":     dbUser,
		"DB_PASSWORD": dbPass,
		"DB_HOST":     "localhost",
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

	// 0600: wp-config.php holds DB credentials; the installer writes it 0600 and
	// a migration must not relax that to world-readable.
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		log.WriteString(fmt.Sprintf("write wp-config: %s\n", err))
		return
	}
	log.WriteString("wp-config.php updated with local DB credentials\n")
}

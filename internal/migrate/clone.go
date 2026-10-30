package migrate

import (
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/uwaserver/uwas/internal/database"
)

// CloneRequest contains domain clone parameters.
type CloneRequest struct {
	SourceDomain string `json:"source_domain"`
	TargetDomain string `json:"target_domain"` // e.g. staging.example.com
	SourceRoot   string `json:"source_root"`
	TargetRoot   string `json:"target_root"`
	SourceDB     string `json:"source_db"` // source database name
	TargetDB     string `json:"target_db"` // target database name (auto if empty)
	DBUser       string `json:"db_user"`
	DBPass       string `json:"db_pass"`
}

// CloneResult contains clone operation status.
type CloneResult struct {
	Status       string `json:"status"`
	SourceDomain string `json:"source_domain"`
	TargetDomain string `json:"target_domain"`
	TargetRoot   string `json:"target_root"`
	TargetDB     string `json:"target_db,omitempty"`
	Output       string `json:"output"`
	Error        string `json:"error,omitempty"`
	Duration     string `json:"duration,omitempty"`
}

// Hooks for testing — override in tests to avoid real exec calls.
var (
	runCloneFiles = cloneFilesReal
	runCloneDB    = cloneDBReal
	runCloneChown = func(root string) { exec.Command("chown", "-R", "www-data:www-data", root).Run() }
	// cloneDBExists reports whether a schema of that name already exists; a
	// failing lookup (no client, no access) counts as "no" so the plain
	// derived name stays in use.
	cloneDBExists = func(name string) bool {
		ok, err := database.DatabaseExists(name)
		return err == nil && ok
	}
)

// deriveCloneDBName builds the target schema name from the target host. The
// mapping is lossy ("a.b.com" and "a-b.com" both give "a_b_com_db"), and the
// dump import replaces the tables of whatever schema it lands in, so a name
// that is already taken is not reused: the host hash keeps the two hosts apart
// (F11300). Existing, unambiguous names are unchanged.
func deriveCloneDBName(domain string) string {
	name := strings.ReplaceAll(domain, ".", "_") + "_db"
	name = strings.ReplaceAll(name, "-", "_")
	if len(name) > 60 {
		name = name[:60]
	}
	if cloneDBExists(name) {
		return database.DisambiguatedName(name, domain, 60)
	}
	return name
}

// Clone duplicates a domain's files and database for staging/testing.
func Clone(req CloneRequest) *CloneResult {
	start := time.Now()
	result := &CloneResult{
		Status:       "running",
		SourceDomain: req.SourceDomain,
		TargetDomain: req.TargetDomain,
	}
	var log strings.Builder

	if req.SourceRoot == "" || req.TargetRoot == "" {
		result.Status = "error"
		result.Error = "source_root and target_root required"
		return result
	}

	// Root-path contract: the admin handler (resolveClonePaths) resolves both
	// roots against the web root — including caller-supplied relative roots —
	// and enforces containment under it before calling Clone. Clone therefore
	// accepts the absolute roots its caller actually produces; the old blanket
	// rejection of absolute paths made every panel-initiated clone fail with
	// "must be relative paths" before a byte was copied. Containment stays the
	// handler's job because only it knows the web root.

	// Refuse to clone onto an existing NON-EMPTY target: rsync --delete would
	// destroy that site's files and the deterministic staging DB name would
	// clobber its tables, all reported as "done". The panel already refuses
	// existing targets; this is the API-side enforcement of the same contract.
	// An existing empty directory (leftover from a failed clone) is fine to
	// fill.
	if entries, err := os.ReadDir(req.TargetRoot); err == nil && len(entries) > 0 {
		result.Status = "error"
		result.Error = "target root already exists and is not empty — refusing to overwrite an existing site; remove it or choose another target domain"
		result.Output = log.String()
		return result
	}

	// Auto-generate target DB name
	if req.SourceDB != "" && req.TargetDB == "" {
		req.TargetDB = deriveCloneDBName(req.TargetDomain)
	}
	if req.SourceDB != "" {
		if !validMigrateDBIdentifier(req.SourceDB) {
			result.Status = "error"
			result.Error = "invalid source database name"
			return result
		}
		if !validMigrateDBIdentifier(req.TargetDB) {
			result.Status = "error"
			result.Error = "invalid target database name"
			return result
		}
		if req.DBUser != "" && !validMigrateDBIdentifier(req.DBUser) {
			result.Status = "error"
			result.Error = "invalid database user"
			return result
		}
		// sqlString panics on a NUL byte; reject it here, before any file or
		// database side effect, instead of aborting mid-clone.
		if strings.ContainsRune(req.DBPass, 0) {
			result.Status = "error"
			result.Error = "invalid database password"
			return result
		}
	}

	// Step 1: Copy files
	log.WriteString("=== Copying files ===\n")
	if err := os.MkdirAll(req.TargetRoot, 0755); err != nil {
		result.Status = "error"
		result.Error = "create target directory: " + err.Error()
		result.Output = log.String()
		return result
	}
	if err := runCloneFiles(req.SourceRoot, req.TargetRoot, &log); err != nil {
		result.Status = "error"
		result.Error = "file copy failed: " + err.Error()
		result.Output = log.String()
		return result
	}
	log.WriteString("Files copied\n")
	result.TargetRoot = req.TargetRoot

	// Compute the effective DB credentials ONCE so the user we create in the
	// database matches the one written into wp-config. Previously Step 2 created
	// a user only when both DBUser and DBPass were supplied, while Step 3 always
	// fell back to TargetDB + a random password — leaving wp-config pointing at a
	// MySQL user that was never created ("Error establishing a database
	// connection") even though the clone reported success.
	dbUser := req.DBUser
	dbPass := req.DBPass
	if req.TargetDB != "" {
		if dbUser == "" {
			dbUser = req.TargetDB
		}
		if dbPass == "" {
			dbPass = generatePassword()
		}
	}

	// Step 2: Clone database
	var dbCloneErr error
	if req.SourceDB != "" {
		log.WriteString("\n=== Cloning database ===\n")
		if dbCloneErr = runCloneDB(req.SourceDB, req.TargetDB, dbUser, dbPass, &log); dbCloneErr != nil {
			log.WriteString(fmt.Sprintf("DB clone error: %s\n", dbCloneErr))
			// Non-fatal (files are already cloned), but surface it so the
			// caller doesn't see an unqualified success.
			result.Error = fmt.Sprintf("database clone failed: %s", dbCloneErr)
		} else {
			result.TargetDB = req.TargetDB
			log.WriteString(fmt.Sprintf("Database cloned: %s → %s\n", req.SourceDB, req.TargetDB))
		}
	}

	// Step 3: Update wp-config.php in target
	wpConfig := filepath.Join(req.TargetRoot, "wp-config.php")
	if _, err := os.Stat(wpConfig); err == nil {
		log.WriteString("\n=== Updating target wp-config.php ===\n")
		// Skip the DB rewrite when the clone failed — otherwise wp-config
		// would point at a database/user that may not exist.
		if req.TargetDB != "" && dbCloneErr == nil {
			updateWPConfigDB(wpConfig, req.TargetDB, dbUser, dbPass, &log)
		}
		// Update siteurl/home to target domain
		updateWPConfigURLs(wpConfig, req.TargetDomain, &log)
	}

	// Step 4: Fix permissions
	runCloneChown(req.TargetRoot)
	log.WriteString("Permissions fixed\n")

	result.Status = "done"
	result.Output = log.String()
	result.Duration = time.Since(start).Round(time.Second).String()
	return result
}

// cloneFilesReal copies files using rsync.
func cloneFilesReal(src, dst string, log *strings.Builder) error {
	cmd := execCommandFn("rsync", "-a", "--delete", src+"/", dst+"/")
	out, err := cmd.CombinedOutput()
	log.Write(out)
	return err
}

// cloneDBReal creates a copy of a database.
func cloneDBReal(srcDB, dstDB, user, pass string, log *strings.Builder) error {
	if !validMigrateDBIdentifier(srcDB) || !validMigrateDBIdentifier(dstDB) {
		return fmt.Errorf("invalid database identifier")
	}
	if user != "" && !validMigrateDBIdentifier(user) {
		return fmt.Errorf("invalid database user")
	}

	for _, client := range []string{"mariadb", "mysql"} {
		bin, err := execLookPathFn(client)
		if err != nil {
			continue
		}

		// Create target database
		execCommandFn(bin, "-u", "root", "-e",
			fmt.Sprintf("CREATE DATABASE IF NOT EXISTS %s CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci", sqlIdent(dstDB))).Run()

		// Create user if provided
		if user != "" && pass != "" {
			// Feed CREATE USER over stdin, not -e: the SQL embeds the plaintext
			// password and -e would expose it on argv (/proc/<pid>/cmdline).
			userCmd := execCommandFn(bin, "-u", "root")
			userCmd.Stdin = strings.NewReader(
				fmt.Sprintf("CREATE USER IF NOT EXISTS '%s'@'localhost' IDENTIFIED BY '%s'", sqlString(user), sqlString(pass)))
			if err := userCmd.Run(); err != nil {
				return fmt.Errorf("create user %q in %s: %w", user, dstDB, err)
			}
			if err := execCommandFn(bin, "-u", "root", "-e",
				fmt.Sprintf("GRANT ALL PRIVILEGES ON %s.* TO '%s'@'localhost'; FLUSH PRIVILEGES", grantIdent(dstDB), sqlString(user))).Run(); err != nil {
				return fmt.Errorf("grant privileges to %q on %s: %w", user, dstDB, err)
			}
		}

		// Dump source and pipe to target
		dumpBin, _ := execLookPathFn("mysqldump")
		if dumpBin == "" {
			dumpBin, _ = execLookPathFn("mariadb-dump")
		}
		if dumpBin == "" {
			return fmt.Errorf("mysqldump not found")
		}

		dump, err := execCommandFn(dumpBin, "-u", "root", "--single-transaction", srcDB).Output()
		if err != nil {
			return fmt.Errorf("dump %s: %w", srcDB, err)
		}
		log.WriteString(fmt.Sprintf("Dump: %d bytes\n", len(dump)))

		importCmd := execCommandFn(bin, "-u", "root", dstDB)
		importCmd.Stdin = strings.NewReader(string(dump))
		if out, err := importCmd.CombinedOutput(); err != nil {
			return fmt.Errorf("import to %s: %w — %s", dstDB, err, string(out))
		}
		return nil
	}
	return fmt.Errorf("mysql client not found")
}

var wpRequireABSPATH = regexp.MustCompile(`require(?:_once)?\s*\(?\s*ABSPATH`)

// updateWPConfigURLs adds/updates WP_HOME and WP_SITEURL in wp-config.php.
func updateWPConfigURLs(path, domain string, log *strings.Builder) {
	data, err := readWPConfigNoFollow(path)
	if err != nil {
		if isSymlink(path) {
			log.WriteString("wp-config.php is a symlink — WP_HOME/WP_SITEURL not set\n")
		}
		return
	}
	content := string(data)

	// Remove existing WP_HOME/WP_SITEURL defines (static ones). PHP keeps the
	// first definition of a constant, so a stale define in any quoting or
	// spacing the exact-text match missed would win over the one added below.
	for _, key := range []string{"WP_HOME", "WP_SITEURL"} {
		re := regexp.MustCompile(`define\(\s*['"]` + key + `['"][^\n]*\n?`)
		content = re.ReplaceAllString(content, "")
	}

	// Add new static defines before require_once
	// Escape backslashes and single-quotes so the domain can safely be injected
	// into a PHP single-quoted string: "O'Brien.com" → "O\'Brien.com".
	escaped := strings.ReplaceAll(domain, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `'`, `\'`)
	newURL := fmt.Sprintf("https://%s", escaped)
	insert := fmt.Sprintf("define('WP_HOME', '%s');\ndefine('WP_SITEURL', '%s');\n\n", newURL, newURL)
	// Insert before the wp-settings require in any of its spellings; without
	// one, right after the opening tag. Dropping the defines while logging them
	// as set left the clone on the source site's URL from the database.
	at := -1
	if loc := wpRequireABSPATH.FindStringIndex(content); loc != nil {
		at = loc[0]
	} else if i := strings.Index(content, "<?php"); i >= 0 {
		if nl := strings.IndexByte(content[i:], '\n'); nl >= 0 {
			at = i + nl + 1
		}
	}
	if at < 0 {
		log.WriteString("WP_HOME/WP_SITEURL not set: no insertion point in wp-config.php\n")
		return
	}
	content = content[:at] + insert + content[at:]

	// 0600: wp-config.php holds DB credentials; keep it owner-only like the
	// installer writes it, and surface a write failure instead of dropping it.
	// Mode and contents go through one no-follow descriptor: os.Chmod and
	// os.WriteFile follow a symlink the source site planted (rsync -a copies it
	// as a link) and would rewrite whatever it points at (F45300).
	if err := writeWPConfigNoFollow(path, []byte(content), 0600); err != nil {
		log.WriteString(fmt.Sprintf("write wp-config: %s\n", err))
		return
	}
	log.WriteString(fmt.Sprintf("WP_HOME/WP_SITEURL set to %s\n", newURL))
}

func generatePassword() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback — should never happen.
		return fmt.Sprintf("uwas_%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("uwas_%x", b)
}

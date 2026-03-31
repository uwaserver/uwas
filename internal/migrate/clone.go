package migrate

import (
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// CloneRequest contains domain clone parameters.
type CloneRequest struct {
	SourceDomain string `json:"source_domain"`
	TargetDomain string `json:"target_domain"` // e.g. staging.example.com
	SourceRoot   string `json:"source_root"`
	TargetRoot   string `json:"target_root"`
	SourceDB     string `json:"source_db"`     // source database name
	TargetDB     string `json:"target_db"`     // target database name (auto if empty)
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
)

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

	// Auto-generate target DB name
	if req.SourceDB != "" && req.TargetDB == "" {
		req.TargetDB = strings.ReplaceAll(req.TargetDomain, ".", "_") + "_db"
		// Sanitize for MySQL
		req.TargetDB = strings.ReplaceAll(req.TargetDB, "-", "_")
		if len(req.TargetDB) > 60 {
			req.TargetDB = req.TargetDB[:60]
		}
	}

	// Step 1: Copy files
	log.WriteString("=== Copying files ===\n")
	os.MkdirAll(req.TargetRoot, 0755)
	if err := runCloneFiles(req.SourceRoot, req.TargetRoot, &log); err != nil {
		result.Status = "error"
		result.Error = "file copy failed: " + err.Error()
		result.Output = log.String()
		return result
	}
	log.WriteString("Files copied\n")
	result.TargetRoot = req.TargetRoot

	// Step 2: Clone database
	if req.SourceDB != "" {
		log.WriteString("\n=== Cloning database ===\n")
		if err := runCloneDB(req.SourceDB, req.TargetDB, req.DBUser, req.DBPass, &log); err != nil {
			log.WriteString(fmt.Sprintf("DB clone error: %s\n", err))
		} else {
			result.TargetDB = req.TargetDB
			log.WriteString(fmt.Sprintf("Database cloned: %s → %s\n", req.SourceDB, req.TargetDB))
		}
	}

	// Step 3: Update wp-config.php in target
	wpConfig := filepath.Join(req.TargetRoot, "wp-config.php")
	if _, err := os.Stat(wpConfig); err == nil {
		log.WriteString("\n=== Updating target wp-config.php ===\n")
		if req.TargetDB != "" {
			dbUser := req.DBUser
			dbPass := req.DBPass
			if dbUser == "" {
				dbUser = req.TargetDB
			}
			if dbPass == "" {
				dbPass = generatePassword()
			}
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
	for _, client := range []string{"mariadb", "mysql"} {
		bin, err := execLookPathFn(client)
		if err != nil {
			continue
		}

		// Create target database
		execCommandFn(bin, "-u", "root", "-e",
			fmt.Sprintf("CREATE DATABASE IF NOT EXISTS `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci", dstDB)).Run()

		// Create user if provided
		if user != "" && pass != "" {
			execCommandFn(bin, "-u", "root", "-e",
				fmt.Sprintf("CREATE USER IF NOT EXISTS '%s'@'localhost' IDENTIFIED BY '%s'", user, pass)).Run()
			execCommandFn(bin, "-u", "root", "-e",
				fmt.Sprintf("GRANT ALL PRIVILEGES ON `%s`.* TO '%s'@'localhost'; FLUSH PRIVILEGES", dstDB, user)).Run()
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

// updateWPConfigURLs adds/updates WP_HOME and WP_SITEURL in wp-config.php.
func updateWPConfigURLs(path, domain string, log *strings.Builder) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	content := string(data)

	// Remove existing WP_HOME/WP_SITEURL defines (static ones)
	for _, key := range []string{"WP_HOME", "WP_SITEURL"} {
		if idx := strings.Index(content, "define('"+key+"'"); idx >= 0 {
			end := strings.Index(content[idx:], "\n")
			if end >= 0 {
				content = content[:idx] + content[idx+end+1:]
			}
		}
	}

	// Add new static defines before require_once
	newURL := fmt.Sprintf("https://%s", domain)
	insert := fmt.Sprintf("define('WP_HOME', '%s');\ndefine('WP_SITEURL', '%s');\n\n", newURL, newURL)
	content = strings.Replace(content, "require_once ABSPATH", insert+"require_once ABSPATH", 1)

	os.WriteFile(path, []byte(content), 0644)
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

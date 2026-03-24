// Package wordpress provides one-click WordPress installation.
package wordpress

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// escSQL escapes a string for use inside SQL single-quoted literals.
func escSQL(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "'", "\\'")
	return s
}

// InstallRequest contains WordPress installation parameters.
type InstallRequest struct {
	Domain    string `json:"domain"`
	WebRoot   string `json:"web_root"`
	DBName    string `json:"db_name"`
	DBUser    string `json:"db_user"`
	DBPass    string `json:"db_pass"`
	DBHost    string `json:"db_host"`
	SiteTitle string `json:"site_title"`
}

// InstallResult contains the result of a WordPress installation.
type InstallResult struct {
	Status   string `json:"status"`
	Domain   string `json:"domain"`
	WebRoot  string `json:"web_root"`
	DBName   string `json:"db_name"`
	DBUser   string `json:"db_user"`
	DBPass   string `json:"db_pass"`
	AdminURL string `json:"admin_url"`
	Output   string `json:"output,omitempty"`
	Error    string `json:"error,omitempty"`
}

const wpDownloadURL = "https://wordpress.org/latest.tar.gz"

// Install performs a complete WordPress installation:
// 1. Creates MySQL database and user (if MySQL is available)
// 2. Downloads and extracts WordPress
// 3. Generates wp-config.php with proper credentials
func Install(req InstallRequest) InstallResult {
	result := InstallResult{
		Domain:  req.Domain,
		WebRoot: req.WebRoot,
		DBName:  req.DBName,
		DBUser:  req.DBUser,
		DBPass:  req.DBPass,
	}

	// Defaults
	if req.DBHost == "" {
		req.DBHost = "localhost"
	}
	if req.DBName == "" {
		req.DBName = sanitizeDBName(req.Domain)
	}
	if req.DBUser == "" {
		req.DBUser = req.DBName
	}
	if req.DBPass == "" {
		req.DBPass = generateSecret(16)
	}
	if req.SiteTitle == "" {
		req.SiteTitle = req.Domain
	}

	result.DBName = req.DBName
	result.DBUser = req.DBUser
	result.DBPass = req.DBPass

	var log strings.Builder

	// Step 0: Ensure PHP MySQL extension is installed
	log.WriteString("=== Checking PHP extensions ===\n")
	ensurePHPExtensions(&log)

	// Step 1: Create MySQL database and user
	log.WriteString("=== Creating database ===\n")
	if err := createMySQLDB(req.DBName, req.DBUser, req.DBPass, req.DBHost, &log); err != nil {
		log.WriteString(fmt.Sprintf("MySQL setup failed: %s\n", err))
		log.WriteString("You can create the database manually and WordPress will use it.\n")
	}

	// Step 1.5: Remove placeholder index.html (UWAS auto-generated)
	placeholderIndex := filepath.Join(req.WebRoot, "index.html")
	if data, err := os.ReadFile(placeholderIndex); err == nil {
		if strings.Contains(string(data), "Site is ready") || strings.Contains(string(data), "UWAS") {
			os.Remove(placeholderIndex)
			log.WriteString("Removed placeholder index.html\n")
		}
	}

	// Step 2: Download WordPress
	log.WriteString("\n=== Downloading WordPress ===\n")
	if err := downloadAndExtract(req.WebRoot, &log); err != nil {
		result.Status = "error"
		result.Error = fmt.Sprintf("download failed: %s", err)
		result.Output = log.String()
		return result
	}

	// Step 3: Generate wp-config.php
	log.WriteString("\n=== Generating wp-config.php ===\n")
	if err := generateWPConfig(req.WebRoot, req.DBName, req.DBUser, req.DBPass, req.DBHost); err != nil {
		result.Status = "error"
		result.Error = fmt.Sprintf("wp-config generation failed: %s", err)
		result.Output = log.String()
		return result
	}
	log.WriteString("wp-config.php created\n")

	// Step 4: Set permissions
	log.WriteString("\n=== Setting permissions ===\n")
	setWordPressPermissions(req.WebRoot, &log)

	// Step 5: Create .htaccess for WordPress pretty permalinks
	log.WriteString("\n=== Creating .htaccess ===\n")
	htaccess := `# BEGIN WordPress
<IfModule mod_rewrite.c>
RewriteEngine On
RewriteBase /
RewriteRule ^index\.php$ - [L]
RewriteCond %{REQUEST_FILENAME} !-f
RewriteCond %{REQUEST_FILENAME} !-d
RewriteRule . /index.php [L]
</IfModule>
# END WordPress
`
	htaccessPath := filepath.Join(req.WebRoot, ".htaccess")
	if err := os.WriteFile(htaccessPath, []byte(htaccess), 0644); err != nil {
		log.WriteString(fmt.Sprintf("Warning: failed to create .htaccess: %s\n", err))
	} else {
		log.WriteString(".htaccess created (pretty permalinks ready)\n")
	}

	// Step 6: Create mu-plugin to tell WordPress that mod_rewrite is available.
	// WordPress checks apache_get_modules() which only works under Apache SAPI.
	// Under PHP-CGI/FPM, got_mod_rewrite() returns false → WordPress uses
	// ugly /index.php/hello-world/ PATHINFO permalinks. This mu-plugin fixes it.
	muDir := filepath.Join(req.WebRoot, "wp-content", "mu-plugins")
	os.MkdirAll(muDir, 0755)
	muPlugin := `<?php
// UWAS: Tell WordPress that URL rewriting is available.
// UWAS handles rewrite rules via .htaccess parsing + built-in try_files.
add_filter('got_url_rewrite', '__return_true');
add_filter('got_rewrite', '__return_true');
`
	muPath := filepath.Join(muDir, "uwas-rewrite.php")
	if err := os.WriteFile(muPath, []byte(muPlugin), 0644); err != nil {
		log.WriteString(fmt.Sprintf("Warning: failed to create mu-plugin: %s\n", err))
	} else {
		log.WriteString("mu-plugin created (mod_rewrite compatibility)\n")
	}

	result.Status = "done"
	result.AdminURL = fmt.Sprintf("https://%s/wp-admin/install.php", req.Domain)
	result.Output = log.String()
	return result
}

func createMySQLDB(dbName, dbUser, dbPass, dbHost string, log *strings.Builder) error {
	// Try mysql client
	if _, err := exec.LookPath("mysql"); err != nil {
		return fmt.Errorf("mysql client not found")
	}

	cmds := []string{
		fmt.Sprintf("CREATE DATABASE IF NOT EXISTS `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;", escSQL(dbName)),
		fmt.Sprintf("CREATE USER IF NOT EXISTS '%s'@'%s' IDENTIFIED BY '%s';", escSQL(dbUser), escSQL(dbHost), escSQL(dbPass)),
		fmt.Sprintf("GRANT ALL PRIVILEGES ON `%s`.* TO '%s'@'%s';", escSQL(dbName), escSQL(dbUser), escSQL(dbHost)),
		"FLUSH PRIVILEGES;",
	}

	sql := strings.Join(cmds, "\n")
	cmd := exec.Command("mysql", "-u", "root", "-e", sql)
	out, err := cmd.CombinedOutput()
	log.Write(out)
	if err != nil {
		// Try with sudo
		cmd = exec.Command("sudo", "mysql", "-e", sql)
		out, err = cmd.CombinedOutput()
		log.Write(out)
	}
	return err
}

func downloadAndExtract(webRoot string, log *strings.Builder) error {
	os.MkdirAll(webRoot, 0755)

	// Download
	tarPath := filepath.Join(os.TempDir(), "wordpress-latest.tar.gz")
	log.WriteString(fmt.Sprintf("Downloading %s\n", wpDownloadURL))

	resp, err := http.Get(wpDownloadURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	f, err := os.Create(tarPath)
	if err != nil {
		return err
	}
	written, _ := io.Copy(f, resp.Body)
	f.Close()
	log.WriteString(fmt.Sprintf("Downloaded %.1f MB\n", float64(written)/1024/1024))

	// Extract — tar xzf to parent, then move wordpress/* to webRoot
	parentDir := filepath.Dir(webRoot)
	cmd := exec.Command("tar", "xzf", tarPath, "-C", parentDir)
	out, err := cmd.CombinedOutput()
	log.Write(out)
	if err != nil {
		return fmt.Errorf("extract failed: %w", err)
	}

	// Move wordpress/* to webRoot
	extractedDir := filepath.Join(parentDir, "wordpress")
	if _, err := os.Stat(extractedDir); err == nil {
		entries, _ := os.ReadDir(extractedDir)
		for _, entry := range entries {
			src := filepath.Join(extractedDir, entry.Name())
			dst := filepath.Join(webRoot, entry.Name())
			os.Rename(src, dst)
		}
		os.RemoveAll(extractedDir)
	}
	log.WriteString(fmt.Sprintf("Extracted to %s\n", webRoot))

	// Cleanup
	os.Remove(tarPath)
	return nil
}

func generateWPConfig(webRoot, dbName, dbUser, dbPass, dbHost string) error {
	salts := make([]string, 8)
	saltKeys := []string{
		"AUTH_KEY", "SECURE_AUTH_KEY", "LOGGED_IN_KEY", "NONCE_KEY",
		"AUTH_SALT", "SECURE_AUTH_SALT", "LOGGED_IN_SALT", "NONCE_SALT",
	}
	for i := range salts {
		salts[i] = fmt.Sprintf("define('%s', '%s');", saltKeys[i], generateSecret(32))
	}

	config := fmt.Sprintf(`<?php
define('DB_NAME', '%s');
define('DB_USER', '%s');
define('DB_PASSWORD', '%s');
define('DB_HOST', '%s');
define('DB_CHARSET', 'utf8mb4');
define('DB_COLLATE', '');

%s

$table_prefix = 'wp_';
define('WP_DEBUG', false);
define('FORCE_SSL_ADMIN', true);
define('DISALLOW_FILE_EDIT', true);

if ( ! defined('ABSPATH') ) {
    define('ABSPATH', __DIR__ . '/');
}
require_once ABSPATH . 'wp-settings.php';
`, dbName, dbUser, dbPass, dbHost, strings.Join(salts, "\n"))

	return os.WriteFile(filepath.Join(webRoot, "wp-config.php"), []byte(config), 0644)
}

func setWordPressPermissions(webRoot string, log *strings.Builder) {
	if runtime.GOOS == "windows" {
		return
	}
	exec.Command("chown", "-R", "www-data:www-data", webRoot).Run()
	exec.Command("find", webRoot, "-type", "d", "-exec", "chmod", "755", "{}", ";").Run()
	exec.Command("find", webRoot, "-type", "f", "-exec", "chmod", "644", "{}", ";").Run()
	// wp-content needs to be writable
	wpContent := filepath.Join(webRoot, "wp-content")
	exec.Command("chmod", "-R", "775", wpContent).Run()
	log.WriteString("Permissions set (www-data:www-data, 755/644, wp-content 775)\n")
}

func sanitizeDBName(domain string) string {
	name := strings.ReplaceAll(domain, ".", "_")
	name = strings.ReplaceAll(name, "-", "_")
	if len(name) > 32 {
		name = name[:32]
	}
	return "wp_" + name
}

func generateSecret(length int) string {
	b := make([]byte, length)
	rand.Read(b)
	return hex.EncodeToString(b)[:length]
}

// ensurePHPExtensions installs MySQL and other WordPress-required PHP extensions.
func ensurePHPExtensions(log *strings.Builder) {
	if runtime.GOOS == "windows" {
		return
	}

	// Detect PHP version
	out, err := exec.Command("php", "-r", "echo PHP_MAJOR_VERSION.'.'.PHP_MINOR_VERSION;").Output()
	if err != nil {
		log.WriteString("Could not detect PHP version\n")
		return
	}
	ver := strings.TrimSpace(string(out))

	// Check if mysqli is already loaded
	check, _ := exec.Command("php", "-m").Output()
	if strings.Contains(strings.ToLower(string(check)), "mysqli") {
		log.WriteString("mysqli extension: OK\n")
		return
	}

	log.WriteString("mysqli extension missing — installing...\n")

	// Install required extensions
	pkgs := []string{
		fmt.Sprintf("php%s-mysql", ver),
		fmt.Sprintf("php%s-curl", ver),
		fmt.Sprintf("php%s-gd", ver),
		fmt.Sprintf("php%s-mbstring", ver),
		fmt.Sprintf("php%s-xml", ver),
		fmt.Sprintf("php%s-zip", ver),
		fmt.Sprintf("php%s-intl", ver),
	}

	// Try apt
	if _, err := exec.LookPath("apt"); err == nil {
		cmd := exec.Command("apt", append([]string{"install", "-y"}, pkgs...)...)
		cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
		out, err := cmd.CombinedOutput()
		log.Write(out)
		if err != nil {
			log.WriteString(fmt.Sprintf("apt install failed: %s\n", err))
		} else {
			log.WriteString("PHP extensions installed\n")
		}
		return
	}

	// Try dnf
	if _, err := exec.LookPath("dnf"); err == nil {
		cmd := exec.Command("dnf", append([]string{"install", "-y"}, pkgs...)...)
		out, err := cmd.CombinedOutput()
		log.Write(out)
		if err != nil {
			log.WriteString(fmt.Sprintf("dnf install failed: %s\n", err))
		}
	}
}

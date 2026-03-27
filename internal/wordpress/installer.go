// Package wordpress provides one-click WordPress installation and management.
package wordpress

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// Testable hooks — replaced in tests to avoid real exec/HTTP/filesystem calls.
var (
	runtimeGOOS    = runtime.GOOS
	execCommandFn  = exec.Command
	execLookPathFn = exec.LookPath
	httpGetFn      = http.Get
	osStatFn       = os.Stat
	osReadFileFn   = os.ReadFile
	osWriteFileFn  = os.WriteFile
	osMkdirAllFn   = os.MkdirAll
	osRemoveAllFn  = os.RemoveAll
	osRenameFn     = os.Rename
	osReadDirFn    = os.ReadDir
	filepathWalkFn = filepath.Walk
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
	if data, err := osReadFileFn(placeholderIndex); err == nil {
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
	if err := osWriteFileFn(htaccessPath, []byte(htaccess), 0644); err != nil {
		log.WriteString(fmt.Sprintf("Warning: failed to create .htaccess: %s\n", err))
	} else {
		log.WriteString(".htaccess created (pretty permalinks ready)\n")
	}

	// Step 6: Create mu-plugin to tell WordPress that mod_rewrite is available.
	// WordPress checks apache_get_modules() which only works under Apache SAPI.
	// Under PHP-CGI/FPM, got_mod_rewrite() returns false → WordPress uses
	// ugly /index.php/hello-world/ PATHINFO permalinks. This mu-plugin fixes it.
	muDir := filepath.Join(req.WebRoot, "wp-content", "mu-plugins")
	osMkdirAllFn(muDir, 0755)
	muPlugin := `<?php
// UWAS: Tell WordPress that URL rewriting is available.
// UWAS handles rewrite rules via .htaccess parsing + built-in try_files.
add_filter('got_url_rewrite', '__return_true');
add_filter('got_rewrite', '__return_true');
`
	muPath := filepath.Join(muDir, "uwas-rewrite.php")
	if err := osWriteFileFn(muPath, []byte(muPlugin), 0644); err != nil {
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
	if _, err := execLookPathFn("mysql"); err != nil {
		return fmt.Errorf("mysql client not found")
	}

	cmds := []string{
		fmt.Sprintf("CREATE DATABASE IF NOT EXISTS `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;", escSQL(dbName)),
		fmt.Sprintf("CREATE USER IF NOT EXISTS '%s'@'%s' IDENTIFIED BY '%s';", escSQL(dbUser), escSQL(dbHost), escSQL(dbPass)),
		fmt.Sprintf("GRANT ALL PRIVILEGES ON `%s`.* TO '%s'@'%s';", escSQL(dbName), escSQL(dbUser), escSQL(dbHost)),
		"FLUSH PRIVILEGES;",
	}

	sql := strings.Join(cmds, "\n")
	cmd := execCommandFn("mysql", "-u", "root", "-e", sql)
	out, err := cmd.CombinedOutput()
	log.Write(out)
	if err != nil {
		// Try with sudo
		cmd = execCommandFn("sudo", "mysql", "-e", sql)
		out, err = cmd.CombinedOutput()
		log.Write(out)
	}
	return err
}

func downloadAndExtract(webRoot string, log *strings.Builder) error {
	osMkdirAllFn(webRoot, 0755)

	// Download
	tarPath := filepath.Join(os.TempDir(), "wordpress-latest.tar.gz")
	log.WriteString(fmt.Sprintf("Downloading %s\n", wpDownloadURL))

	resp, err := httpGetFn(wpDownloadURL)
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
	cmd := execCommandFn("tar", "xzf", tarPath, "-C", parentDir)
	out, err := cmd.CombinedOutput()
	log.Write(out)
	if err != nil {
		return fmt.Errorf("extract failed: %w", err)
	}

	// Move wordpress/* to webRoot
	extractedDir := filepath.Join(parentDir, "wordpress")
	if _, err := osStatFn(extractedDir); err == nil {
		entries, _ := osReadDirFn(extractedDir)
		for _, entry := range entries {
			src := filepath.Join(extractedDir, entry.Name())
			dst := filepath.Join(webRoot, entry.Name())
			osRenameFn(src, dst)
		}
		osRemoveAllFn(extractedDir)
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
define('DISALLOW_FILE_EDIT', true);
define('FS_METHOD', 'direct');
define('WP_TEMP_DIR', __DIR__ . '/.tmp');

/** UWAS: SSL handling — prevent redirect loops.
 * UWAS terminates SSL and forwards as HTTP internally.
 * Tell WordPress the connection is HTTPS via the forwarded proto header. */
if (isset($_SERVER['HTTP_X_FORWARDED_PROTO']) && $_SERVER['HTTP_X_FORWARDED_PROTO'] === 'https') {
    $_SERVER['HTTPS'] = 'on';
}
if (isset($_SERVER['HTTPS']) && $_SERVER['HTTPS'] === 'on') {
    define('FORCE_SSL_ADMIN', true);
}

/** UWAS: Set home/siteurl dynamically to avoid DB mismatch redirect loops. */
if (!defined('WP_HOME')) {
    $scheme = (!empty($_SERVER['HTTPS']) && $_SERVER['HTTPS'] !== 'off') ? 'https' : 'http';
    define('WP_HOME', $scheme . '://' . $_SERVER['HTTP_HOST']);
    define('WP_SITEURL', $scheme . '://' . $_SERVER['HTTP_HOST']);
}

if ( ! defined('ABSPATH') ) {
    define('ABSPATH', __DIR__ . '/');
}
require_once ABSPATH . 'wp-settings.php';
`, dbName, dbUser, dbPass, dbHost, strings.Join(salts, "\n"))

	return osWriteFileFn(filepath.Join(webRoot, "wp-config.php"), []byte(config), 0600)
}

func setWordPressPermissions(webRoot string, log *strings.Builder) {
	if runtimeGOOS == "windows" {
		return
	}
	execCommandFn("chown", "-R", "www-data:www-data", webRoot).Run()
	execCommandFn("find", webRoot, "-type", "d", "-exec", "chmod", "755", "{}", ";").Run()
	execCommandFn("find", webRoot, "-type", "f", "-exec", "chmod", "644", "{}", ";").Run()
	// wp-content needs to be writable
	wpContent := filepath.Join(webRoot, "wp-content")
	execCommandFn("chmod", "-R", "775", wpContent).Run()

	// Create directories WordPress needs for plugin/theme installs and uploads
	for _, sub := range []string{"upgrade", "uploads", "upgrade/skins", ".tmp"} {
		dir := filepath.Join(webRoot, "wp-content", sub)
		if sub == ".tmp" {
			dir = filepath.Join(webRoot, ".tmp")
		}
		osMkdirAllFn(dir, 0775)
		execCommandFn("chown", "www-data:www-data", dir).Run()
	}
	log.WriteString("Permissions set (www-data:www-data, 755/644, wp-content 775, upgrade/uploads created)\n")
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
	if runtimeGOOS == "windows" {
		return
	}

	// Detect PHP version
	out, err := execCommandFn("php", "-r", "echo PHP_MAJOR_VERSION.'.'.PHP_MINOR_VERSION;").Output()
	if err != nil {
		log.WriteString("Could not detect PHP version\n")
		return
	}
	ver := strings.TrimSpace(string(out))

	// Check if mysqli is already loaded
	check, _ := execCommandFn("php", "-m").Output()
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
	if _, err := execLookPathFn("apt"); err == nil {
		cmd := execCommandFn("apt", append([]string{"install", "-y"}, pkgs...)...)
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
	if _, err := execLookPathFn("dnf"); err == nil {
		cmd := execCommandFn("dnf", append([]string{"install", "-y"}, pkgs...)...)
		out, err := cmd.CombinedOutput()
		log.Write(out)
		if err != nil {
			log.WriteString(fmt.Sprintf("dnf install failed: %s\n", err))
		}
	}
}

// --- WordPress Site Detection & Management ---

// SiteInfo describes a detected WordPress installation.
type SiteInfo struct {
	Domain      string            `json:"domain"`
	WebRoot     string            `json:"web_root"`
	Version     string            `json:"version"`
	DBName      string            `json:"db_name"`
	DBUser      string            `json:"db_user"`
	DBHost      string            `json:"db_host"`
	SiteURL     string            `json:"site_url"`
	AdminURL    string            `json:"admin_url"`
	Plugins     []PluginInfo      `json:"plugins"`
	Themes      []ThemeInfo       `json:"themes"`
	Health      SiteHealth        `json:"health"`
	Permissions PermissionsReport `json:"permissions"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// PluginInfo describes a WordPress plugin.
type PluginInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Status  string `json:"status"` // active, inactive, must-use
	Update  string `json:"update"` // available update version, "" if none
}

// ThemeInfo describes a WordPress theme.
type ThemeInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Status  string `json:"status"` // active, inactive
	Update  string `json:"update"`
}

// SiteHealth summarizes WordPress site health.
type SiteHealth struct {
	CoreUpdate    bool   `json:"core_update"`    // WP core update available
	PluginUpdates int    `json:"plugin_updates"` // number of plugin updates
	ThemeUpdates  int    `json:"theme_updates"`
	PHPVersion    string `json:"php_version"`
	Debug         bool   `json:"debug"`          // WP_DEBUG enabled
	SSL           bool   `json:"ssl"`            // FORCE_SSL_ADMIN
	FileEdit      bool   `json:"file_edit"`      // DISALLOW_FILE_EDIT
}

// PermissionsReport shows file/directory permission status.
type PermissionsReport struct {
	WPConfig    string `json:"wp_config"`    // e.g. "0644 www-data:www-data"
	WPContent   string `json:"wp_content"`
	Uploads     string `json:"uploads"`
	Htaccess    string `json:"htaccess"`
	Owner       string `json:"owner"`        // detected owner user
	Writable    bool   `json:"writable"`     // wp-content writable by PHP
}

// DetectSites scans domain web roots for WordPress installations.
func DetectSites(domains []DomainInfo) []SiteInfo {
	var sites []SiteInfo
	for _, d := range domains {
		if d.WebRoot == "" {
			continue
		}
		wpConfig := filepath.Join(d.WebRoot, "wp-config.php")
		if _, err := osStatFn(wpConfig); err != nil {
			continue
		}
		site := SiteInfo{
			Domain:    d.Host,
			WebRoot:   d.WebRoot,
			AdminURL:  fmt.Sprintf("https://%s/wp-admin/", d.Host),
			UpdatedAt: time.Now(),
		}

		// Parse wp-config.php for DB info
		site.DBName, site.DBUser, site.DBHost = parseWPConfig(wpConfig)

		// Detect WP version from wp-includes/version.php
		site.Version = detectWPVersion(d.WebRoot)

		// Detect site URL
		site.SiteURL = fmt.Sprintf("https://%s", d.Host)

		// Check health indicators from wp-config
		site.Health = checkWPHealth(wpConfig, d.WebRoot)

		// Check permissions
		site.Permissions = checkPermissions(d.WebRoot)

		// Try WP-CLI for detailed plugin/theme info
		if hasWPCLI() {
			site.Plugins = listPlugins(d.WebRoot)
			site.Themes = listThemes(d.WebRoot)
			site.Health.PluginUpdates = countUpdates(site.Plugins)
			site.Health.ThemeUpdates = countUpdates2(site.Themes)
		} else {
			// Fallback: scan wp-content/plugins directory
			site.Plugins = scanPluginDirs(d.WebRoot)
		}

		sites = append(sites, site)
	}
	return sites
}

// DomainInfo is a minimal domain descriptor for WordPress detection.
type DomainInfo struct {
	Host    string
	WebRoot string
}

// IsWordPress checks if the given web root contains a WordPress installation.
func IsWordPress(webRoot string) bool {
	_, err := osStatFn(filepath.Join(webRoot, "wp-config.php"))
	return err == nil
}

// parseWPConfig extracts DB_NAME, DB_USER, DB_HOST from wp-config.php.
func parseWPConfig(path string) (dbName, dbUser, dbHost string) {
	data, err := osReadFileFn(path)
	if err != nil {
		return
	}
	content := string(data)
	re := regexp.MustCompile(`define\s*\(\s*'(DB_NAME|DB_USER|DB_HOST)'\s*,\s*'([^']*)'\s*\)`)
	for _, m := range re.FindAllStringSubmatch(content, -1) {
		switch m[1] {
		case "DB_NAME":
			dbName = m[2]
		case "DB_USER":
			dbUser = m[2]
		case "DB_HOST":
			dbHost = m[2]
		}
	}
	return
}

// detectWPVersion reads $wp_version from wp-includes/version.php.
func detectWPVersion(webRoot string) string {
	data, err := osReadFileFn(filepath.Join(webRoot, "wp-includes", "version.php"))
	if err != nil {
		return "unknown"
	}
	re := regexp.MustCompile(`\$wp_version\s*=\s*'([^']+)'`)
	m := re.FindStringSubmatch(string(data))
	if len(m) >= 2 {
		return m[1]
	}
	return "unknown"
}

// checkWPHealth reads wp-config.php for health indicators.
func checkWPHealth(wpConfigPath, webRoot string) SiteHealth {
	data, _ := osReadFileFn(wpConfigPath)
	content := string(data)
	h := SiteHealth{}
	h.Debug = strings.Contains(content, "'WP_DEBUG', true") || strings.Contains(content, "'WP_DEBUG',true")
	h.SSL = strings.Contains(content, "FORCE_SSL_ADMIN")
	h.FileEdit = strings.Contains(content, "DISALLOW_FILE_EDIT")
	return h
}

// checkPermissions reports file ownership and permission status.
func checkPermissions(webRoot string) PermissionsReport {
	r := PermissionsReport{}
	r.WPConfig = permString(filepath.Join(webRoot, "wp-config.php"))
	r.WPContent = permString(filepath.Join(webRoot, "wp-content"))
	r.Uploads = permString(filepath.Join(webRoot, "wp-content", "uploads"))
	r.Htaccess = permString(filepath.Join(webRoot, ".htaccess"))

	// Check owner (Linux only)
	if runtimeGOOS != "windows" {
		out, err := execCommandFn("stat", "-c", "%U:%G", webRoot).Output()
		if err == nil {
			r.Owner = strings.TrimSpace(string(out))
		}
	}

	// Check if wp-content is writable
	testFile := filepath.Join(webRoot, "wp-content", ".uwas-write-test")
	if err := osWriteFileFn(testFile, []byte("test"), 0644); err == nil {
		os.Remove(testFile)
		r.Writable = true
	}
	return r
}

func permString(path string) string {
	info, err := osStatFn(path)
	if err != nil {
		return "missing"
	}
	mode := info.Mode().Perm()
	// Get owner on Linux
	if runtimeGOOS != "windows" {
		out, err := execCommandFn("stat", "-c", "%U:%G", path).Output()
		if err == nil {
			return fmt.Sprintf("%04o %s", mode, strings.TrimSpace(string(out)))
		}
	}
	return fmt.Sprintf("%04o", mode)
}

// --- WP-CLI Integration ---

// hasWPCLI checks if wp-cli is installed.
func hasWPCLI() bool {
	_, err := execLookPathFn("wp")
	return err == nil
}

// wpCLI runs a WP-CLI command in the given web root.
// It auto-detects the site URL from wp-config.php to avoid HTTP_HOST warnings.
func wpCLI(webRoot string, args ...string) (string, error) {
	allArgs := append([]string{"--path=" + webRoot, "--allow-root", "--no-color"}, args...)

	// Detect site URL to pass --url (avoids "Undefined array key HTTP_HOST" warning)
	if url := detectSiteURL(webRoot); url != "" {
		allArgs = append([]string{"--url=" + url}, allArgs...)
	}

	cmd := execCommandFn("wp", allArgs...)
	cmd.Dir = webRoot
	// Separate stdout from stderr — PHP deprecation warnings go to stderr
	// and corrupt JSON output if mixed via CombinedOutput.
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	out := stdout.String()
	// If stdout is empty but stderr has content, return stderr for error context
	if out == "" && stderr.Len() > 0 {
		out = stderr.String()
	}
	return out, err
}

// detectSiteURL tries to find the domain from the directory structure.
// e.g. /var/www/example.com/public_html → https://example.com
func detectSiteURL(webRoot string) string {
	// Try parent directory name as domain (UWAS convention: /var/www/{domain}/public_html)
	parent := filepath.Base(filepath.Dir(webRoot))
	if parent != "" && parent != "." && parent != "/" && strings.Contains(parent, ".") {
		return "https://" + parent
	}
	// Fallback: directory name itself
	base := filepath.Base(webRoot)
	if strings.Contains(base, ".") {
		return "https://" + base
	}
	return ""
}

// extractJSON finds the first JSON array or object in a string that may
// contain PHP warnings/deprecation notices before the actual JSON.
func extractJSON(s string) string {
	// Find first [ or {
	for i, c := range s {
		if c == '[' || c == '{' {
			return s[i:]
		}
	}
	return s
}

// listPlugins uses WP-CLI to get plugin info.
func listPlugins(webRoot string) []PluginInfo {
	out, err := wpCLI(webRoot, "plugin", "list", "--format=json")
	if err != nil {
		return scanPluginDirs(webRoot) // fallback
	}
	out = extractJSON(out) // strip any PHP warnings before JSON
	var raw []struct {
		Name    string `json:"name"`
		Status  string `json:"status"`
		Version string `json:"version"`
		Update  string `json:"update"`
	}
	if json.Unmarshal([]byte(out), &raw) != nil {
		return scanPluginDirs(webRoot)
	}
	plugins := make([]PluginInfo, len(raw))
	for i, p := range raw {
		plugins[i] = PluginInfo{Name: p.Name, Version: p.Version, Status: p.Status, Update: p.Update}
	}
	return plugins
}

// listThemes uses WP-CLI to get theme info.
func listThemes(webRoot string) []ThemeInfo {
	out, err := wpCLI(webRoot, "theme", "list", "--format=json")
	if err != nil {
		return nil
	}
	out = extractJSON(out)
	var raw []struct {
		Name    string `json:"name"`
		Status  string `json:"status"`
		Version string `json:"version"`
		Update  string `json:"update"`
	}
	if json.Unmarshal([]byte(out), &raw) != nil {
		return nil
	}
	themes := make([]ThemeInfo, len(raw))
	for i, t := range raw {
		themes[i] = ThemeInfo{Name: t.Name, Version: t.Version, Status: t.Status, Update: t.Update}
	}
	return themes
}

// scanPluginDirs is a fallback when WP-CLI is not available.
func scanPluginDirs(webRoot string) []PluginInfo {
	pluginDir := filepath.Join(webRoot, "wp-content", "plugins")
	entries, err := osReadDirFn(pluginDir)
	if err != nil {
		return nil
	}
	var plugins []PluginInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if name == "." || name == ".." {
			continue
		}
		p := PluginInfo{Name: name, Status: "unknown"}
		// Try to read version from plugin header
		mainFile := filepath.Join(pluginDir, name, name+".php")
		if data, err := osReadFileFn(mainFile); err == nil {
			p.Version = parsePluginVersion(string(data))
		}
		plugins = append(plugins, p)
	}
	return plugins
}

func parsePluginVersion(content string) string {
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "* Version:") || strings.HasPrefix(line, " * Version:") {
			return strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, " * Version:"), "* Version:"))
		}
	}
	return ""
}

func countUpdates(plugins []PluginInfo) int {
	n := 0
	for _, p := range plugins {
		if p.Update != "" && p.Update != "none" {
			n++
		}
	}
	return n
}

func countUpdates2(themes []ThemeInfo) int {
	n := 0
	for _, t := range themes {
		if t.Update != "" && t.Update != "none" {
			n++
		}
	}
	return n
}

// --- WP-CLI Actions ---

// UpdateCore updates WordPress core. Uses WP-CLI if available, otherwise
// downloads latest WordPress and overwrites core files (preserves wp-content).
func UpdateCore(webRoot string) (string, error) {
	if hasWPCLI() {
		return wpCLI(webRoot, "core", "update")
	}

	// Fallback: download latest WP and overwrite core files
	var log strings.Builder
	log.WriteString("WP-CLI not found — using direct download method\n")

	// Download latest WordPress
	tarURL := "https://wordpress.org/latest.tar.gz"
	tarPath := filepath.Join(os.TempDir(), "wordpress-update.tar.gz")
	defer os.Remove(tarPath)

	resp, err := httpGetFn(tarURL)
	if err != nil {
		return log.String(), fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	f, err := os.Create(tarPath)
	if err != nil {
		return log.String(), err
	}
	io.Copy(f, resp.Body)
	f.Close()
	log.WriteString("Downloaded latest WordPress\n")

	// Extract to temp dir
	tmpDir, _ := os.MkdirTemp("", "wp-update-*")
	defer osRemoveAllFn(tmpDir)

	cmd := execCommandFn("tar", "xzf", tarPath, "-C", tmpDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return log.String(), fmt.Errorf("extract failed: %s — %w", string(out), err)
	}

	wpDir := filepath.Join(tmpDir, "wordpress")

	// Copy core files (NOT wp-content, NOT wp-config.php, NOT .htaccess)
	skipDirs := map[string]bool{"wp-content": true}
	skipFiles := map[string]bool{"wp-config.php": true, ".htaccess": true, "wp-config-sample.php": true}

	err = filepathWalkFn(wpDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(wpDir, path)
		if rel == "." {
			return nil
		}
		// Skip wp-content directory
		topDir := strings.SplitN(rel, string(filepath.Separator), 2)[0]
		if skipDirs[topDir] {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		// Skip protected files in root
		if !strings.Contains(rel, string(filepath.Separator)) && skipFiles[rel] {
			return nil
		}

		dst := filepath.Join(webRoot, rel)
		if info.IsDir() {
			return osMkdirAllFn(dst, 0755)
		}
		data, readErr := osReadFileFn(path)
		if readErr != nil {
			return readErr
		}
		return osWriteFileFn(dst, data, info.Mode())
	})

	if err != nil {
		return log.String(), fmt.Errorf("copy failed: %w", err)
	}

	// Fix ownership
	execCommandFn("chown", "-R", "www-data:www-data", webRoot).Run()
	log.WriteString("WordPress core updated (wp-content preserved)\n")

	return log.String(), nil
}

// ReinstallWordPress re-downloads WordPress core files without touching
// wp-content, wp-config.php, or the database. Useful for fixing corrupted installs.
func ReinstallWordPress(webRoot string) (string, error) {
	return UpdateCore(webRoot) // Same logic — overwrite core, preserve content
}

// UpdatePlugin updates a specific plugin.
func UpdatePlugin(webRoot, plugin string) (string, error) {
	return wpCLI(webRoot, "plugin", "update", plugin)
}

// UpdateAllPlugins updates all plugins.
func UpdateAllPlugins(webRoot string) (string, error) {
	return wpCLI(webRoot, "plugin", "update", "--all")
}

// ActivatePlugin activates a plugin.
func ActivatePlugin(webRoot, plugin string) (string, error) {
	return wpCLI(webRoot, "plugin", "activate", plugin)
}

// DeactivatePlugin deactivates a plugin.
func DeactivatePlugin(webRoot, plugin string) (string, error) {
	return wpCLI(webRoot, "plugin", "deactivate", plugin)
}

// DeletePlugin deletes a plugin.
func DeletePlugin(webRoot, plugin string) (string, error) {
	return wpCLI(webRoot, "plugin", "delete", plugin)
}

// UpdateTheme updates a specific theme.
func UpdateTheme(webRoot, theme string) (string, error) {
	return wpCLI(webRoot, "theme", "update", theme)
}

// FixPermissions sets correct WordPress file permissions.
func FixPermissions(webRoot string) (string, error) {
	var log strings.Builder
	// Directories: 755, files: 644
	if out, err := execCommandFn("find", webRoot, "-type", "d", "-exec", "chmod", "755", "{}", ";").CombinedOutput(); err != nil {
		log.WriteString(fmt.Sprintf("chmod dirs: %s\n", string(out)))
	} else {
		log.WriteString("Directories set to 755\n")
	}
	if out, err := execCommandFn("find", webRoot, "-type", "f", "-exec", "chmod", "644", "{}", ";").CombinedOutput(); err != nil {
		log.WriteString(fmt.Sprintf("chmod files: %s\n", string(out)))
	} else {
		log.WriteString("Files set to 644\n")
	}
	// wp-content writable
	execCommandFn("chmod", "-R", "775", filepath.Join(webRoot, "wp-content")).Run()
	log.WriteString("wp-content set to 775\n")
	// wp-config.php locked
	execCommandFn("chmod", "600", filepath.Join(webRoot, "wp-config.php")).Run()
	log.WriteString("wp-config.php set to 600\n")
	// Owner
	execCommandFn("chown", "-R", "www-data:www-data", webRoot).Run()
	log.WriteString("Owner set to www-data:www-data\n")

	// Ensure FS_METHOD is set in wp-config.php (prevents FTP prompt for plugin installs)
	wpConfig := filepath.Join(webRoot, "wp-config.php")
	if data, err := osReadFileFn(wpConfig); err == nil {
		content := string(data)
		if !strings.Contains(content, "FS_METHOD") {
			// Insert before require_once
			content = strings.Replace(content,
				"require_once ABSPATH",
				"define('FS_METHOD', 'direct');\ndefine('WP_TEMP_DIR', __DIR__ . '/.tmp');\n\nrequire_once ABSPATH",
				1)
			osWriteFileFn(wpConfig, []byte(content), 0600)
			log.WriteString("Added FS_METHOD=direct to wp-config.php\n")
		}
		if !strings.Contains(content, "WP_TEMP_DIR") {
			content = strings.Replace(content,
				"require_once ABSPATH",
				"define('WP_TEMP_DIR', __DIR__ . '/.tmp');\n\nrequire_once ABSPATH",
				1)
			osWriteFileFn(wpConfig, []byte(content), 0600)
			log.WriteString("Added WP_TEMP_DIR to wp-config.php\n")
		}
	}

	// Create directories WordPress needs for plugin/theme installs, uploads, and temp
	for _, sub := range []string{
		filepath.Join("wp-content", "upgrade"),
		filepath.Join("wp-content", "uploads"),
		".tmp",
	} {
		dir := filepath.Join(webRoot, sub)
		osMkdirAllFn(dir, 0775)
		execCommandFn("chown", "www-data:www-data", dir).Run()
	}
	log.WriteString("upgrade, uploads, .tmp directories created\n")

	return log.String(), nil
}

// SetDebugMode enables or disables WP_DEBUG, WP_DEBUG_LOG and WP_DEBUG_DISPLAY
// in wp-config.php. When enabled, PHP errors are written to wp-content/debug.log
// so white-page issues can be diagnosed.
func SetDebugMode(webRoot string, enable bool) error {
	configPath := filepath.Join(webRoot, "wp-config.php")
	data, err := osReadFileFn(configPath)
	if err != nil {
		return fmt.Errorf("read wp-config.php: %w", err)
	}

	content := string(data)

	// Remove existing debug defines
	re := regexp.MustCompile(`(?m)^\s*define\s*\(\s*'WP_DEBUG(?:_LOG|_DISPLAY)?'\s*,\s*(?:true|false)\s*\)\s*;\s*\n?`)
	content = re.ReplaceAllString(content, "")

	if enable {
		debugBlock := "define('WP_DEBUG', true);\ndefine('WP_DEBUG_LOG', true);\ndefine('WP_DEBUG_DISPLAY', true);\n\n"
		if idx := strings.Index(content, "require_once ABSPATH"); idx >= 0 {
			content = content[:idx] + debugBlock + content[idx:]
		} else if idx := strings.Index(content, "/* That's all"); idx >= 0 {
			content = content[:idx] + debugBlock + content[idx:]
		} else {
			content += "\n" + debugBlock
		}
	} else {
		debugOff := "define('WP_DEBUG', false);\n\n"
		if idx := strings.Index(content, "require_once ABSPATH"); idx >= 0 {
			content = content[:idx] + debugOff + content[idx:]
		}
	}

	return osWriteFileFn(configPath, []byte(content), 0600)
}

// --- WordPress User Management ---

// WPUser represents a WordPress user.
type WPUser struct {
	ID       string `json:"id"`
	Login    string `json:"login"`
	Email    string `json:"email"`
	Role     string `json:"role"`
	Registered string `json:"registered,omitempty"`
}

// ListUsers returns all WordPress users via wp-cli.
func ListUsers(webRoot string) ([]WPUser, error) {
	out, err := wpCLI(webRoot, "user", "list", "--fields=ID,user_login,user_email,roles,user_registered", "--format=json")
	if err != nil {
		return nil, fmt.Errorf("wp user list: %w", err)
	}
	out = extractJSON(out)
	var raw []struct {
		ID             string `json:"ID"`
		UserLogin      string `json:"user_login"`
		UserEmail      string `json:"user_email"`
		Roles          string `json:"roles"`
		UserRegistered string `json:"user_registered"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("parse user list: %w", err)
	}
	users := make([]WPUser, len(raw))
	for i, u := range raw {
		users[i] = WPUser{ID: u.ID, Login: u.UserLogin, Email: u.UserEmail, Role: u.Roles, Registered: u.UserRegistered}
	}
	return users, nil
}

// ChangeUserPassword changes a WordPress user's password.
func ChangeUserPassword(webRoot, username, newPassword string) error {
	_, err := wpCLI(webRoot, "user", "update", username, "--user_pass="+newPassword)
	return err
}

// --- Security Hardening ---

// SecurityStatus reports the current security state of a WordPress installation.
type SecurityStatus struct {
	XMLRPCDisabled     bool   `json:"xmlrpc_disabled"`
	FileEditDisabled   bool   `json:"file_edit_disabled"`
	DebugEnabled       bool   `json:"debug_enabled"`
	SSLForced          bool   `json:"ssl_forced"`
	AutoUpdatesCore    string `json:"auto_updates_core"`    // "true", "false", "minor"
	AutoUpdatesPlugins bool   `json:"auto_updates_plugins"`
	AutoUpdatesThemes  bool   `json:"auto_updates_themes"`
	TablePrefix        string `json:"table_prefix"`
	PHPVersion         string `json:"php_version"`
	WPVersion          string `json:"wp_version"`
	DirectoryListing   bool   `json:"directory_listing_blocked"`
	WPCronDisabled     bool   `json:"wp_cron_disabled"`
}

// GetSecurityStatus checks the security configuration of a WordPress site.
func GetSecurityStatus(webRoot string) SecurityStatus {
	st := SecurityStatus{
		WPVersion: detectWPVersion(webRoot),
	}

	configPath := filepath.Join(webRoot, "wp-config.php")
	data, err := osReadFileFn(configPath)
	if err != nil {
		return st
	}
	content := string(data)

	st.FileEditDisabled = strings.Contains(content, "DISALLOW_FILE_EDIT") && containsDefineTrue(content, "DISALLOW_FILE_EDIT")
	st.DebugEnabled = containsDefineTrue(content, "WP_DEBUG")
	st.SSLForced = containsDefineTrue(content, "FORCE_SSL_ADMIN")
	st.WPCronDisabled = containsDefineTrue(content, "DISABLE_WP_CRON")

	// Auto-updates
	if containsDefineTrue(content, "WP_AUTO_UPDATE_CORE") {
		st.AutoUpdatesCore = "true"
	} else if strings.Contains(content, "'WP_AUTO_UPDATE_CORE'") && strings.Contains(content, "'minor'") {
		st.AutoUpdatesCore = "minor"
	} else {
		st.AutoUpdatesCore = "default"
	}
	st.AutoUpdatesPlugins = strings.Contains(content, "auto_update_plugin") && strings.Contains(content, "__return_true")
	st.AutoUpdatesThemes = strings.Contains(content, "auto_update_theme") && strings.Contains(content, "__return_true")

	// Table prefix
	if re := regexp.MustCompile(`\$table_prefix\s*=\s*'([^']+)'`); re.MatchString(content) {
		st.TablePrefix = re.FindStringSubmatch(content)[1]
	}

	// PHP version via wp-cli (extract just the version number, skip warnings)
	if phpOut, err := wpCLI(webRoot, "eval", "echo PHP_VERSION;"); err == nil {
		phpOut = strings.TrimSpace(phpOut)
		// Output may contain "Deprecated: ..." lines before the version
		lines := strings.Split(phpOut, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if len(line) > 0 && line[0] >= '0' && line[0] <= '9' {
				st.PHPVersion = line
				break
			}
		}
	}

	// XML-RPC — check if blocked via mu-plugin or wp-config
	st.XMLRPCDisabled = checkXMLRPCDisabled(webRoot, content)

	// Directory listing — check .htaccess for Options -Indexes
	if htData, err := osReadFileFn(filepath.Join(webRoot, ".htaccess")); err == nil {
		st.DirectoryListing = strings.Contains(string(htData), "-Indexes")
	}

	return st
}

func containsDefineTrue(content, constant string) bool {
	re := regexp.MustCompile(`define\s*\(\s*'` + constant + `'\s*,\s*true\s*\)`)
	return re.MatchString(content)
}

func checkXMLRPCDisabled(webRoot, wpConfig string) bool {
	// Check mu-plugin
	muPath := filepath.Join(webRoot, "wp-content", "mu-plugins", "uwas-security.php")
	if data, err := osReadFileFn(muPath); err == nil {
		if strings.Contains(string(data), "xmlrpc_enabled") {
			return true
		}
	}
	// Check wp-config for xmlrpc filter
	return strings.Contains(wpConfig, "xmlrpc_enabled")
}

// HardenOptions specifies which security features to enable/disable.
type HardenOptions struct {
	DisableXMLRPC      *bool `json:"disable_xmlrpc,omitempty"`
	DisableFileEdit    *bool `json:"disable_file_edit,omitempty"`
	ForceSSLAdmin      *bool `json:"force_ssl_admin,omitempty"`
	DisableWPCron      *bool `json:"disable_wp_cron,omitempty"`
	BlockDirListing    *bool `json:"block_dir_listing,omitempty"`
}

// Harden applies security hardening options to a WordPress installation.
func Harden(webRoot string, opts HardenOptions) (string, error) {
	var log strings.Builder

	// wp-config.php modifications
	configPath := filepath.Join(webRoot, "wp-config.php")
	data, err := osReadFileFn(configPath)
	if err != nil {
		return "", fmt.Errorf("read wp-config.php: %w", err)
	}
	content := string(data)
	changed := false

	if opts.DisableFileEdit != nil {
		content = setWPConfigDefine(content, "DISALLOW_FILE_EDIT", *opts.DisableFileEdit)
		if *opts.DisableFileEdit {
			log.WriteString("File editing disabled (DISALLOW_FILE_EDIT)\n")
		} else {
			log.WriteString("File editing enabled\n")
		}
		changed = true
	}

	if opts.ForceSSLAdmin != nil {
		content = setWPConfigDefine(content, "FORCE_SSL_ADMIN", *opts.ForceSSLAdmin)
		if *opts.ForceSSLAdmin {
			log.WriteString("SSL forced for admin (FORCE_SSL_ADMIN)\n")
		} else {
			log.WriteString("SSL admin enforcement removed\n")
		}
		changed = true
	}

	if opts.DisableWPCron != nil {
		content = setWPConfigDefine(content, "DISABLE_WP_CRON", *opts.DisableWPCron)
		if *opts.DisableWPCron {
			log.WriteString("WP-Cron disabled (use system cron instead)\n")
		} else {
			log.WriteString("WP-Cron enabled\n")
		}
		changed = true
	}

	if changed {
		if err := osWriteFileFn(configPath, []byte(content), 0600); err != nil {
			return log.String(), fmt.Errorf("write wp-config.php: %w", err)
		}
	}

	// XML-RPC via mu-plugin (cleaner than wp-config, survives updates)
	if opts.DisableXMLRPC != nil {
		muDir := filepath.Join(webRoot, "wp-content", "mu-plugins")
		osMkdirAllFn(muDir, 0755)
		muPath := filepath.Join(muDir, "uwas-security.php")

		if *opts.DisableXMLRPC {
			muContent := `<?php
// UWAS Security: Disable XML-RPC (prevents brute-force and DDoS attacks)
add_filter('xmlrpc_enabled', '__return_false');
add_filter('xmlrpc_methods', '__return_empty_array');
// Remove XML-RPC discovery link from head
remove_action('wp_head', 'rsd_link');
`
			if err := osWriteFileFn(muPath, []byte(muContent), 0644); err != nil {
				return log.String(), fmt.Errorf("write mu-plugin: %w", err)
			}
			log.WriteString("XML-RPC disabled via mu-plugin\n")
		} else {
			os.Remove(muPath)
			log.WriteString("XML-RPC enabled (mu-plugin removed)\n")
		}
	}

	// Directory listing in .htaccess
	if opts.BlockDirListing != nil {
		htPath := filepath.Join(webRoot, ".htaccess")
		htData, _ := osReadFileFn(htPath)
		htContent := string(htData)

		if *opts.BlockDirListing {
			if !strings.Contains(htContent, "-Indexes") {
				htContent = "Options -Indexes\n\n" + htContent
				osWriteFileFn(htPath, []byte(htContent), 0644)
				log.WriteString("Directory listing blocked (Options -Indexes)\n")
			}
		} else {
			htContent = strings.Replace(htContent, "Options -Indexes\n\n", "", 1)
			htContent = strings.Replace(htContent, "Options -Indexes\n", "", 1)
			osWriteFileFn(htPath, []byte(htContent), 0644)
			log.WriteString("Directory listing allowed\n")
		}
	}

	return log.String(), nil
}

func setWPConfigDefine(content, constant string, value bool) string {
	// Remove existing define for this constant
	re := regexp.MustCompile(`(?m)^\s*define\s*\(\s*'` + constant + `'\s*,\s*(?:true|false)\s*\)\s*;\s*\n?`)
	content = re.ReplaceAllString(content, "")

	// Insert new define before require_once ABSPATH
	val := "false"
	if value {
		val = "true"
	}
	define := fmt.Sprintf("define('%s', %s);\n", constant, val)

	if idx := strings.Index(content, "require_once ABSPATH"); idx >= 0 {
		content = content[:idx] + define + content[idx:]
	}
	return content
}

// --- Database Optimization ---

// DBOptimizeResult holds results of database cleanup.
type DBOptimizeResult struct {
	RevisionsDeleted int    `json:"revisions_deleted"`
	SpamDeleted      int    `json:"spam_deleted"`
	TrashDeleted     int    `json:"trash_deleted"`
	TransientsCleaned int   `json:"transients_cleaned"`
	TablesOptimized  int    `json:"tables_optimized"`
	Output           string `json:"output"`
}

// OptimizeDatabase cleans up and optimizes the WordPress database.
func OptimizeDatabase(webRoot string) (*DBOptimizeResult, error) {
	result := &DBOptimizeResult{}
	var log strings.Builder

	// Delete post revisions
	if out, err := wpCLI(webRoot, "post", "delete", "--force",
		"$(wp post list --post_type=revision --format=ids --path="+webRoot+")"); err == nil {
		log.WriteString("Revisions: " + out + "\n")
	}
	// Use wp-cli db query for more reliable cleanup
	if out, err := wpCLI(webRoot, "db", "query",
		"SELECT COUNT(*) FROM $(wp db prefix --path="+webRoot+")posts WHERE post_type='revision'"); err == nil {
		log.WriteString("Revision check: " + out + "\n")
	}

	// Delete spam comments
	if out, err := wpCLI(webRoot, "comment", "delete",
		"$(wp comment list --status=spam --format=ids --path="+webRoot+")", "--force"); err == nil {
		log.WriteString("Spam comments cleaned: " + out + "\n")
	}

	// Delete trashed comments
	if out, err := wpCLI(webRoot, "comment", "delete",
		"$(wp comment list --status=trash --format=ids --path="+webRoot+")", "--force"); err == nil {
		log.WriteString("Trash comments cleaned: " + out + "\n")
	}

	// Delete trashed posts
	if out, err := wpCLI(webRoot, "post", "delete",
		"$(wp post list --post_status=trash --format=ids --path="+webRoot+")", "--force"); err == nil {
		log.WriteString("Trash posts cleaned: " + out + "\n")
	}

	// Clean expired transients
	if out, err := wpCLI(webRoot, "transient", "delete", "--expired"); err == nil {
		log.WriteString("Transients: " + out + "\n")
	}

	// Optimize database tables
	if out, err := wpCLI(webRoot, "db", "optimize"); err == nil {
		log.WriteString("Tables optimized: " + out + "\n")
		result.TablesOptimized = 1
	}

	result.Output = log.String()
	return result, nil
}

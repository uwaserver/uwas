// Package wordpress provides one-click WordPress installation and management.
package wordpress

import (
	"bufio"
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
define('FS_METHOD', 'direct');
define('WP_TEMP_DIR', __DIR__ . '/.tmp');

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
		if _, err := os.Stat(wpConfig); err != nil {
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
	_, err := os.Stat(filepath.Join(webRoot, "wp-config.php"))
	return err == nil
}

// parseWPConfig extracts DB_NAME, DB_USER, DB_HOST from wp-config.php.
func parseWPConfig(path string) (dbName, dbUser, dbHost string) {
	data, err := os.ReadFile(path)
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
	data, err := os.ReadFile(filepath.Join(webRoot, "wp-includes", "version.php"))
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
	data, _ := os.ReadFile(wpConfigPath)
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
	if runtime.GOOS != "windows" {
		out, err := exec.Command("stat", "-c", "%U:%G", webRoot).Output()
		if err == nil {
			r.Owner = strings.TrimSpace(string(out))
		}
	}

	// Check if wp-content is writable
	testFile := filepath.Join(webRoot, "wp-content", ".uwas-write-test")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err == nil {
		os.Remove(testFile)
		r.Writable = true
	}
	return r
}

func permString(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return "missing"
	}
	mode := info.Mode().Perm()
	// Get owner on Linux
	if runtime.GOOS != "windows" {
		out, err := exec.Command("stat", "-c", "%U:%G", path).Output()
		if err == nil {
			return fmt.Sprintf("%04o %s", mode, strings.TrimSpace(string(out)))
		}
	}
	return fmt.Sprintf("%04o", mode)
}

// --- WP-CLI Integration ---

// hasWPCLI checks if wp-cli is installed.
func hasWPCLI() bool {
	_, err := exec.LookPath("wp")
	return err == nil
}

// wpCLI runs a WP-CLI command in the given web root.
func wpCLI(webRoot string, args ...string) (string, error) {
	allArgs := append([]string{"--path=" + webRoot, "--allow-root", "--no-color"}, args...)
	cmd := exec.Command("wp", allArgs...)
	cmd.Dir = webRoot
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// listPlugins uses WP-CLI to get plugin info.
func listPlugins(webRoot string) []PluginInfo {
	out, err := wpCLI(webRoot, "plugin", "list", "--format=json")
	if err != nil {
		return scanPluginDirs(webRoot) // fallback
	}
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
	entries, err := os.ReadDir(pluginDir)
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
		if data, err := os.ReadFile(mainFile); err == nil {
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

// UpdateCore updates WordPress core via WP-CLI.
func UpdateCore(webRoot string) (string, error) {
	return wpCLI(webRoot, "core", "update")
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
	if out, err := exec.Command("find", webRoot, "-type", "d", "-exec", "chmod", "755", "{}", ";").CombinedOutput(); err != nil {
		log.WriteString(fmt.Sprintf("chmod dirs: %s\n", string(out)))
	} else {
		log.WriteString("Directories set to 755\n")
	}
	if out, err := exec.Command("find", webRoot, "-type", "f", "-exec", "chmod", "644", "{}", ";").CombinedOutput(); err != nil {
		log.WriteString(fmt.Sprintf("chmod files: %s\n", string(out)))
	} else {
		log.WriteString("Files set to 644\n")
	}
	// wp-content writable
	exec.Command("chmod", "-R", "775", filepath.Join(webRoot, "wp-content")).Run()
	log.WriteString("wp-content set to 775\n")
	// wp-config.php locked
	exec.Command("chmod", "600", filepath.Join(webRoot, "wp-config.php")).Run()
	log.WriteString("wp-config.php set to 600\n")
	// Owner
	exec.Command("chown", "-R", "www-data:www-data", webRoot).Run()
	log.WriteString("Owner set to www-data:www-data\n")

	// Ensure FS_METHOD is set in wp-config.php (prevents FTP prompt for plugin installs)
	wpConfig := filepath.Join(webRoot, "wp-config.php")
	if data, err := os.ReadFile(wpConfig); err == nil {
		content := string(data)
		if !strings.Contains(content, "FS_METHOD") {
			// Insert before require_once
			content = strings.Replace(content,
				"require_once ABSPATH",
				"define('FS_METHOD', 'direct');\ndefine('WP_TEMP_DIR', __DIR__ . '/.tmp');\n\nrequire_once ABSPATH",
				1)
			os.WriteFile(wpConfig, []byte(content), 0600)
			log.WriteString("Added FS_METHOD=direct to wp-config.php\n")
		}
		if !strings.Contains(content, "WP_TEMP_DIR") {
			content = strings.Replace(content,
				"require_once ABSPATH",
				"define('WP_TEMP_DIR', __DIR__ . '/.tmp');\n\nrequire_once ABSPATH",
				1)
			os.WriteFile(wpConfig, []byte(content), 0600)
			log.WriteString("Added WP_TEMP_DIR to wp-config.php\n")
		}
	}

	// Create .tmp directory for WordPress temp operations
	tmpDir := filepath.Join(webRoot, ".tmp")
	os.MkdirAll(tmpDir, 0775)
	exec.Command("chown", "www-data:www-data", tmpDir).Run()
	log.WriteString(".tmp directory created for WordPress temp operations\n")

	return log.String(), nil
}

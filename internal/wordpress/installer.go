// Package wordpress provides one-click WordPress installation and management.
package wordpress

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha1"
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

	"github.com/uwaserver/uwas/internal/database"
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
		var genErr error
		req.DBPass, genErr = generateSecret(16)
		if genErr != nil {
			result.Error = genErr.Error()
			return result
		}
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
	if !database.ValidDBIdentifier(dbName) {
		return fmt.Errorf("invalid database name")
	}
	if !database.ValidDBIdentifier(dbUser) {
		return fmt.Errorf("invalid database user")
	}

	// Try mysql client
	if _, err := execLookPathFn("mysql"); err != nil {
		return fmt.Errorf("mysql client not found")
	}

	dbIdent := database.BacktickID(dbName)
	cmds := []string{
		fmt.Sprintf("CREATE DATABASE IF NOT EXISTS %s CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;", dbIdent),
		fmt.Sprintf("CREATE USER IF NOT EXISTS '%s'@'%s' IDENTIFIED BY '%s';", escSQL(dbUser), escSQL(dbHost), escSQL(dbPass)),
		fmt.Sprintf("GRANT ALL PRIVILEGES ON %s.* TO '%s'@'%s';", dbIdent, escSQL(dbUser), escSQL(dbHost)),
		"FLUSH PRIVILEGES;",
	}

	sql := strings.Join(cmds, "\n")
	cmd := execCommandFn("mysql", "-u", "root", "-e", sql)
	out, err := cmd.CombinedOutput()
	log.Write(out)
	if err != nil {
		// Try without -u root (let socket auth auto-detect user)
		cmd = execCommandFn("mysql", "-e", sql)
		out, err = cmd.CombinedOutput()
		log.Write(out)
	}
	return err
}

// fetchWPChecksum returns the lowercased SHA1 hex from a wordpress.org
// checksum file (e.g. https://wordpress.org/latest.tar.gz.sha1), or "" if the
// response is missing, non-200, or not a 40-char hex string. Returning ""
// instead of an error keeps checksum verification best-effort: callers skip
// the check rather than block the install when the upstream file is broken.
func fetchWPChecksum(url string) string {
	resp, err := httpGetFn(url)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
	fields := strings.Fields(string(body))
	if len(fields) == 0 {
		return ""
	}
	sum := strings.ToLower(strings.TrimSpace(fields[0]))
	if len(sum) != 40 {
		return ""
	}
	if _, err := hex.DecodeString(sum); err != nil {
		return ""
	}
	return sum
}

// hashFileSHA1 returns the lowercase SHA1 hex of the file at path, or "" on error.
func hashFileSHA1(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha1.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

func downloadAndExtract(webRoot string, log *strings.Builder) error {
	osMkdirAllFn(webRoot, 0755)

	log.WriteString(fmt.Sprintf("Downloading %s\n", wpDownloadURL))

	resp, err := httpGetFn(wpDownloadURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	f, err := os.CreateTemp("", "wordpress-latest-*.tar.gz")
	if err != nil {
		return err
	}
	tarPath := f.Name()
	defer os.Remove(tarPath)
	const maxWPDownload = 100 << 20 // 100MB safety cap
	written, _ := io.Copy(f, io.LimitReader(resp.Body, maxWPDownload))
	f.Close()
	log.WriteString(fmt.Sprintf("Downloaded %.1f MB\n", float64(written)/1024/1024))

	// Verify SHA1 checksum (wordpress.org publishes .sha1 and .md5; not .sha256)
	if expected := fetchWPChecksum(wpDownloadURL + ".sha1"); expected != "" {
		if actual := hashFileSHA1(tarPath); actual != "" {
			if expected != actual {
				return fmt.Errorf("WordPress checksum mismatch: expected %s, got %s", expected, actual)
			}
			log.WriteString("  Checksum verified OK\n")
		}
	}
	// If checksum file unavailable, continue (best-effort)

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

	return nil
}

func generateWPConfig(webRoot, dbName, dbUser, dbPass, dbHost string) error {
	// Escape the DB credentials before interpolating them into PHP single-
	// quoted strings. A value containing ' or \ (e.g. a user-supplied password)
	// would otherwise corrupt wp-config.php and not match the credentials
	// createMySQLDB stored (which are escaped). escSQL's \-and-' escaping is
	// exactly PHP single-quote escaping.
	dbName = escSQL(dbName)
	dbUser = escSQL(dbUser)
	dbPass = escSQL(dbPass)
	dbHost = escSQL(dbHost)

	salts := make([]string, 8)
	saltKeys := []string{
		"AUTH_KEY", "SECURE_AUTH_KEY", "LOGGED_IN_KEY", "NONCE_KEY",
		"AUTH_SALT", "SECURE_AUTH_SALT", "LOGGED_IN_SALT", "NONCE_SALT",
	}
	for i := range salts {
		secret, err := generateSecret(32)
		if err != nil {
			return err
		}
		salts[i] = fmt.Sprintf("define('%s', '%s');", saltKeys[i], secret)
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

func generateSecret(length int) (string, error) {
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate secret: %w", err)
	}
	return hex.EncodeToString(b)[:length], nil
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
		cmd.Env = append(os.Environ(),
			"DEBIAN_FRONTEND=noninteractive",
			"NEEDRESTART_MODE=a",
			"APT_LISTCHANGES_FRONTEND=none",
			"DEBIAN_PRIORITY=critical",
		)
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
	Debug         bool   `json:"debug"`     // WP_DEBUG enabled
	SSL           bool   `json:"ssl"`       // FORCE_SSL_ADMIN
	FileEdit      bool   `json:"file_edit"` // DISALLOW_FILE_EDIT
}

// PermissionsReport shows file/directory permission status.
type PermissionsReport struct {
	WPConfig  string `json:"wp_config"` // e.g. "0644 www-data:www-data"
	WPContent string `json:"wp_content"`
	Uploads   string `json:"uploads"`
	Htaccess  string `json:"htaccess"`
	Owner     string `json:"owner"`    // detected owner user
	Writable  bool   `json:"writable"` // wp-content writable by PHP
}

// DetectSites scans domain web roots for WordPress installations.
// DetectSites quickly detects WordPress installations using only filesystem checks.
// Does NOT call wp-cli — returns instantly. Use EnrichSite for plugin/theme details.
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
			SiteURL:   fmt.Sprintf("https://%s", d.Host),
			UpdatedAt: time.Now(),
		}

		// Fast: parse wp-config.php for DB info (file read only)
		site.DBName, site.DBUser, site.DBHost = parseWPConfig(wpConfig)

		// Fast: detect WP version from version.php (file read only)
		site.Version = detectWPVersion(d.WebRoot)

		// Fast: check health from wp-config (file read only)
		site.Health = checkWPHealth(wpConfig, d.WebRoot)

		// Fast: check permissions (stat only)
		site.Permissions = checkPermissions(d.WebRoot)

		// Fast: scan plugin dirs without wp-cli
		site.Plugins = scanPluginDirs(d.WebRoot)

		sites = append(sites, site)
	}
	return sites
}

// EnrichSite uses wp-cli to add detailed plugin/theme info for a single site.
// This is slow (~2-3s per site) so should be called on-demand, not during list.
func EnrichSite(site *SiteInfo) {
	if !hasWPCLI() {
		return
	}
	site.Plugins = listPlugins(site.WebRoot)
	site.Themes = listThemes(site.WebRoot)
	site.Health.PluginUpdates = countUpdates(site.Plugins)
	site.Health.ThemeUpdates = countUpdates2(site.Themes)
	site.UpdatedAt = time.Now()
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

var wpCLIBinaryCandidates = []string{
	"wp",
	"/usr/local/bin/wp",
	"/usr/bin/wp",
	"/bin/wp",
	"/snap/bin/wp",
	"wp-cli",
	"/usr/local/bin/wp-cli",
	"/usr/bin/wp-cli",
	"/bin/wp-cli",
}

func resolveWPCLIBinary() (string, error) {
	for _, candidate := range wpCLIBinaryCandidates {
		if bin, err := execLookPathFn(candidate); err == nil && bin != "" {
			return bin, nil
		}
	}
	return "", fmt.Errorf("wp-cli not found (tried PATH and common install paths)")
}

func bestEffortWPCLIBinary() string {
	if bin, err := resolveWPCLIBinary(); err == nil && bin != "" {
		return bin
	}
	// Preserve legacy behavior: let exec report the concrete failure.
	return "wp"
}

// hasWPCLI checks if wp-cli is installed.
func hasWPCLI() bool {
	_, err := resolveWPCLIBinary()
	return err == nil
}

// wpCLI runs a WP-CLI command in the given web root.
// It auto-detects the site URL from wp-config.php to avoid HTTP_HOST warnings.
func wpCLI(webRoot string, args ...string) (string, error) {
	wpBin := bestEffortWPCLIBinary()
	allArgs := append([]string{"--path=" + webRoot, "--allow-root", "--no-color"}, args...)

	// Detect site URL to pass --url (avoids "Undefined array key HTTP_HOST" warning)
	if url := detectSiteURL(webRoot); url != "" {
		allArgs = append([]string{"--url=" + url}, allArgs...)
	}

	cmd := execCommandFn(wpBin, allArgs...)
	cmd.Dir = webRoot
	// Separate stdout from stderr — PHP deprecation warnings go to stderr
	// and corrupt JSON output if mixed via CombinedOutput.
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	out := stdout.String()
	if err != nil {
		// Include stderr in error for better diagnostics
		errMsg := stderr.String()
		if errMsg != "" {
			// Filter out PHP deprecation noise, keep actual errors
			var realErrors []string
			for _, line := range strings.Split(errMsg, "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				// Skip PHP deprecation/notice lines
				if strings.HasPrefix(line, "Deprecated:") || strings.HasPrefix(line, "Notice:") ||
					strings.HasPrefix(line, "Warning:") && strings.Contains(line, "PHP") {
					continue
				}
				realErrors = append(realErrors, line)
			}
			if len(realErrors) > 0 {
				err = fmt.Errorf("%w: %s", err, strings.Join(realErrors, "; "))
			}
		}
	}
	// If stdout is empty but stderr has content, return stderr for context
	if out == "" && stderr.Len() > 0 {
		out = stderr.String()
	}
	return out, err
}

// detectSiteURL finds the WordPress site URL for --url flag.
// Priority: 1) wp-config.php WP_HOME/WP_SITEURL 2) directory convention 3) empty
func detectSiteURL(webRoot string) string {
	// 1. Try reading site URL from wp-config.php
	configPath := filepath.Join(webRoot, "wp-config.php")
	if data, err := osReadFileFn(configPath); err == nil {
		content := string(data)
		// Check for WP_HOME or WP_SITEURL define
		for _, constant := range []string{"WP_HOME", "WP_SITEURL"} {
			re := regexp.MustCompile(`define\s*\(\s*'` + constant + `'\s*,\s*'([^']+)'`)
			if m := re.FindStringSubmatch(content); len(m) > 1 {
				return m[1]
			}
		}
	}

	// 2. Try parent directory name as domain (UWAS convention: /var/www/{domain}/public_html)
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

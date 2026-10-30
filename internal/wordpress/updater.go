// Updater, plugin/theme actions, and user management for WordPress
// sites. Split out of installer.go per refactor.md A13 so this file
// owns "things you do to an existing install" (update core, manage
// plugins/themes, change user passwords) while installer.go retains
// the "create a new install" path.
package wordpress

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// wpPluginSlugRe matches valid plugin slugs (directory name, optionally
// directory/file.php form). Anchored and must not start with "-" so a value
// can never be parsed by wp-cli as a flag.
var wpPluginSlugRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*(/[a-zA-Z0-9][a-zA-Z0-9._-]*)?$`)

// validPluginSlug guards user-supplied plugin identifiers against wp-cli
// argument injection (e.g. "foo --activate" or a leading-dash flag). wp-cli
// runs without a shell, so the only vector is flag injection via a value.
func validPluginSlug(plugin string) error {
	if plugin == "" {
		return fmt.Errorf("plugin is required")
	}
	if !wpPluginSlugRe.MatchString(plugin) {
		return fmt.Errorf("invalid plugin name")
	}
	return nil
}

// validWPUsername guards user-supplied wp-cli positional values (usernames)
// against flag injection. WordPress logins may contain spaces and a range of
// characters, so we only reject the dangerous shapes: empty, leading dash, and
// embedded control/quote characters.
func validWPUsername(username string) error {
	if username == "" {
		return fmt.Errorf("username is required")
	}
	if strings.HasPrefix(username, "-") {
		return fmt.Errorf("invalid username")
	}
	for _, r := range username {
		if r < 0x20 || r == '"' || r == '\'' || r == '`' {
			return fmt.Errorf("invalid username")
		}
	}
	return nil
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

	resp, err := httpGetFn(tarURL)
	if err != nil {
		return log.String(), fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()
	// Without this, a 404/500 error page is written into the tarball and only
	// surfaces later as a misleading "extract failed".
	if resp.StatusCode != http.StatusOK {
		return log.String(), fmt.Errorf("download WordPress: HTTP %d", resp.StatusCode)
	}

	f, err := os.CreateTemp("", "wordpress-update-*.tar.gz")
	if err != nil {
		return log.String(), err
	}
	tarPath := f.Name()
	defer os.Remove(tarPath)
	// Same 100MB cap as the installer (F5000); one extra byte detects overflow.
	const maxWPDownload = 100 << 20
	written, copyErr := io.Copy(f, io.LimitReader(resp.Body, maxWPDownload+1))
	f.Close()
	if copyErr != nil {
		return log.String(), fmt.Errorf("download WordPress: %w", copyErr)
	}
	if written > maxWPDownload {
		return log.String(), fmt.Errorf("download WordPress: archive exceeds %d bytes", maxWPDownload)
	}
	log.WriteString("Downloaded latest WordPress\n")

	// Verify SHA1 checksum (wordpress.org publishes .sha1 and .md5; not .sha256)
	if expected := fetchWPChecksum(tarURL + ".sha1"); expected != "" {
		if actual := hashFileSHA1(tarPath); actual != "" {
			if expected != actual {
				return log.String(), fmt.Errorf("WordPress checksum mismatch: expected %s, got %s", expected, actual)
			}
			log.WriteString("  Checksum verified OK\n")
		}
	}
	// If checksum file unavailable, continue (best-effort)

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

	// Every write goes through an os.Root on the docroot: the symlink check
	// below only sees the directory as it is now, and the tenant's own code can
	// replace a directory with a link before root writes into it (F58302). A
	// root-relative path that leaves the docroot is refused by the kernel-side
	// resolution instead.
	dstRoot, err := os.OpenRoot(webRoot)
	if err != nil {
		return log.String(), fmt.Errorf("open web root: %w", err)
	}
	defer dstRoot.Close()

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
		// Guard: verify the resolved path stays inside webRoot. Go's filepath.Join
		// does not cancel ".." components that span multiple path segments, so a
		// malicious tarball entry like "wordpress/../../etc/passwd" would otherwise
		// write outside webRoot.
		if safe, _ := filepath.Rel(webRoot, dst); safe == ".." || filepath.IsAbs(safe) {
			return fmt.Errorf("path traversal attempt detected: %s", dst)
		}
		// MkdirAll accepts an existing symlink-to-dir, and everything below it
		// would then be written through it (writeFileNoFollow only guards the
		// final component). The walk is top-down, so checking each directory
		// here covers every parent of every file.
		if fi, lerr := os.Lstat(dst); lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to write through symlink: %s", dst)
		}
		if info.IsDir() {
			return dstRoot.MkdirAll(rel, 0755)
		}
		data, readErr := osReadFileFn(path)
		if readErr != nil {
			return readErr
		}
		return writeFileInRoot(dstRoot, rel, data, info.Mode().Perm())
	})

	if err != nil {
		return log.String(), fmt.Errorf("copy failed: %w", err)
	}

	// Fix ownership. The result used to be dropped, so a host where chown fails
	// (UWAS unprivileged, or no www-data account) reported a clean update while
	// the new core files kept the extracting user's ownership.
	if out, err := execCommandFn("chown", "-R", "www-data:www-data", webRoot).CombinedOutput(); err != nil {
		log.WriteString(fmt.Sprintf("warning: chown -R www-data:www-data failed: %v %s\n", err, strings.TrimSpace(string(out))))
		log.WriteString("WordPress core updated (wp-content preserved), but file ownership was not changed — fix it manually or run Fix Permissions as a privileged user\n")
		return log.String(), nil
	}
	log.WriteString("WordPress core updated (wp-content preserved)\n")

	return log.String(), nil
}

// writeFileInRoot writes data to rel inside root, refusing a final-component
// symlink and anything that is not a regular file; the open never blocks on a
// FIFO planted at that name.
func writeFileInRoot(root *os.Root, rel string, data []byte, perm os.FileMode) error {
	f, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|noFollowFlag|nonBlockFlag, perm)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s: not a regular file", rel)
	}
	if err := f.Truncate(0); err != nil {
		return err
	}
	_, err = f.Write(data)
	return err
}

// ReinstallWordPress re-downloads WordPress core files without touching
// wp-content, wp-config.php, or the database. Useful for fixing corrupted installs.
func ReinstallWordPress(webRoot string) (string, error) {
	return UpdateCore(webRoot) // Same logic — overwrite core, preserve content
}

// UpdatePlugin updates a specific plugin.
func UpdatePlugin(webRoot, plugin string) (string, error) {
	if err := validPluginSlug(plugin); err != nil {
		return "", err
	}
	return wpCLI(webRoot, "plugin", "update", plugin)
}

// UpdateAllPlugins updates all plugins.
func UpdateAllPlugins(webRoot string) (string, error) {
	return wpCLI(webRoot, "plugin", "update", "--all")
}

// ActivatePlugin activates a plugin.
func ActivatePlugin(webRoot, plugin string) (string, error) {
	if err := validPluginSlug(plugin); err != nil {
		return "", err
	}
	return wpCLI(webRoot, "plugin", "activate", plugin)
}

// DeactivatePlugin deactivates a plugin.
func DeactivatePlugin(webRoot, plugin string) (string, error) {
	if err := validPluginSlug(plugin); err != nil {
		return "", err
	}
	return wpCLI(webRoot, "plugin", "deactivate", plugin)
}

// DeletePlugin deletes a plugin.
func DeletePlugin(webRoot, plugin string) (string, error) {
	if err := validPluginSlug(plugin); err != nil {
		return "", err
	}
	return wpCLI(webRoot, "plugin", "delete", plugin)
}

// pathHasSymlink reports whether any component of rel below root is a
// symlink, or cannot be inspected. chmod/chown/MkdirAll follow symlinks, so
// FixPermissions (running as root) must not touch a path the domain user has
// pointed elsewhere. A component that does not exist yet is safe.
func pathHasSymlink(root, rel string) bool {
	p := root
	for _, part := range strings.Split(filepath.Clean(rel), string(filepath.Separator)) {
		p = filepath.Join(p, part)
		fi, err := os.Lstat(p)
		if os.IsNotExist(err) {
			return false
		}
		if err != nil || fi.Mode()&os.ModeSymlink != 0 {
			return true
		}
	}
	return false
}

// FixPermissions sets correct WordPress file permissions.
func FixPermissions(webRoot string) (string, error) {
	var log strings.Builder
	// Every step used to log its "... set" line whether or not it worked, so a
	// host where chown/chmod fails (UWAS unprivileged, or no www-data account)
	// told the operator the permissions were fixed. Say what actually happened,
	// as setWordPressPermissions does for the installer.
	var failed int
	report := func(label, okMsg string, err error, out string) {
		if err != nil {
			failed++
			log.WriteString(fmt.Sprintf("warning: %s failed: %v %s\n", label, err, strings.TrimSpace(out)))
		} else if okMsg != "" {
			log.WriteString(okMsg + "\n")
		}
	}
	step := func(label, okMsg string, c *exec.Cmd) {
		out, err := c.CombinedOutput()
		report(label, okMsg, err, string(out))
	}
	// Directories: 755, files: 644. Done in Go through an os.Root rather than
	// `find -exec chmod`, which follows a symlink the tenant swaps in (F46001).
	report("chmod dirs 755 and files 644", "Directories set to 755\nFiles set to 644",
		normalizeTreeModes(webRoot, 0o755, 0o644, nil), "")
	// wp-content writable
	if pathHasSymlink(webRoot, "wp-content") {
		log.WriteString("wp-content is a symlink — skipped chmod\n")
	} else {
		step("chmod -R 775 wp-content", "wp-content set to 775", execCommandFn("chmod", "-R", "775", filepath.Join(webRoot, "wp-content")))
	}
	// wp-config.php locked
	if pathHasSymlink(webRoot, "wp-config.php") {
		log.WriteString("wp-config.php is a symlink — skipped chmod\n")
	} else {
		step("chmod 600 wp-config.php", "wp-config.php set to 600", execCommandFn("chmod", "600", filepath.Join(webRoot, "wp-config.php")))
	}
	// Owner
	step("chown -R www-data:www-data", "Owner set to www-data:www-data", execCommandFn("chown", "-R", "www-data:www-data", webRoot))

	// Ensure FS_METHOD is set in wp-config.php (prevents FTP prompt for plugin installs)
	wpConfig := filepath.Join(webRoot, "wp-config.php")
	if data, err := osReadFileFn(wpConfig); err == nil {
		content := string(data)
		// Both presence tests run against the ORIGINAL config. The first
		// replacement inserts FS_METHOD *and* WP_TEMP_DIR, so testing WP_TEMP_DIR
		// against the already-rewritten string saw it as present and skipped —
		// while the rewrite itself had added a second define beside the existing
		// one. A site that shipped WP_TEMP_DIR but not FS_METHOD then emitted
		// "Constant WP_TEMP_DIR already defined" on every request.
		hasFSMethod := strings.Contains(content, "FS_METHOD")
		hasWPTempDir := strings.Contains(content, "WP_TEMP_DIR")
		var added string
		switch {
		case !hasFSMethod && !hasWPTempDir:
			content = strings.Replace(content,
				"require_once ABSPATH",
				"define('FS_METHOD', 'direct');\ndefine('WP_TEMP_DIR', __DIR__ . '/.tmp');\n\nrequire_once ABSPATH",
				1)
			added = "Added FS_METHOD=direct to WP_TEMP_DIR to wp-config.php"
		case !hasFSMethod:
			content = strings.Replace(content,
				"require_once ABSPATH",
				"define('FS_METHOD', 'direct');\n\nrequire_once ABSPATH",
				1)
			added = "Added FS_METHOD=direct to wp-config.php"
		case !hasWPTempDir:
			content = strings.Replace(content,
				"require_once ABSPATH",
				"define('WP_TEMP_DIR', __DIR__ . '/.tmp');\n\nrequire_once ABSPATH",
				1)
			added = "Added WP_TEMP_DIR to wp-config.php"
		}
		if added != "" {
			if werr := osWriteFileFn(wpConfig, []byte(content), 0600); werr != nil {
				failed++
				log.WriteString(fmt.Sprintf("warning: update wp-config.php failed: %v\n", werr))
			} else {
				log.WriteString(added + "\n")
			}
		}
	}

	// Create directories WordPress needs for plugin/theme installs, uploads, and temp
	for _, sub := range []string{
		filepath.Join("wp-content", "upgrade"),
		filepath.Join("wp-content", "uploads"),
		".tmp",
	} {
		if pathHasSymlink(webRoot, sub) {
			log.WriteString(sub + " is under a symlink — skipped\n")
			continue
		}
		dir := filepath.Join(webRoot, sub)
		if mkErr := osMkdirAllFn(dir, 0775); mkErr != nil {
			failed++
			log.WriteString(fmt.Sprintf("warning: create %s failed: %v\n", sub, mkErr))
			continue
		}
		if c, done, cerr := chownDirToWebAccount(webRoot, sub); cerr != nil {
			failed++
			log.WriteString(fmt.Sprintf("warning: chown www-data:www-data %s refused: %v\n", sub, cerr))
		} else {
			step("chown www-data:www-data "+sub, "", c)
			done()
		}
	}
	if failed == 0 {
		log.WriteString("upgrade, uploads, .tmp directories created\n")
	} else {
		log.WriteString(fmt.Sprintf("WARNING: %d permission step(s) failed — WordPress may be unable to install plugins, upload media, or update until ownership is fixed\n", failed))
	}

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
	// Name quotes and the boolean's case vary in real configs (FALSE,
	// "WP_DEBUG"); a define this misses survives and, being first, wins in PHP.
	re := regexp.MustCompile(`(?mi)^\s*define\s*\(\s*['"]WP_DEBUG(?:_LOG|_DISPLAY)?['"]\s*,\s*(?:true|false)\s*\)\s*;\s*\n?`)
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
	ID         string `json:"id"`
	Login      string `json:"login"`
	Email      string `json:"email"`
	Role       string `json:"role"`
	Registered string `json:"registered,omitempty"`
}

func parseWPCLIStringField(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", nil
	}

	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}

	var n json.Number
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&n); err == nil {
		return n.String(), nil
	}

	return "", fmt.Errorf("unsupported value: %s", string(raw))
}

func parseWPCLIRolesField(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", nil
	}

	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}

	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		return strings.Join(arr, ","), nil
	}

	return parseWPCLIStringField(raw)
}

// ListUsers returns all WordPress users via wp-cli.
func ListUsers(webRoot string) ([]WPUser, error) {
	out, err := wpCLI(webRoot, "user", "list", "--fields=ID,user_login,user_email,roles,user_registered", "--format=json")
	if err != nil {
		return nil, fmt.Errorf("wp user list: %w", err)
	}
	out = extractJSON(out)
	var raw []struct {
		ID             json.RawMessage `json:"ID"`
		UserLogin      string          `json:"user_login"`
		UserEmail      string          `json:"user_email"`
		Roles          json.RawMessage `json:"roles"`
		UserRegistered string          `json:"user_registered"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("parse user list: %w", err)
	}
	users := make([]WPUser, len(raw))
	for i, u := range raw {
		id, err := parseWPCLIStringField(u.ID)
		if err != nil {
			return nil, fmt.Errorf("parse user list: invalid ID: %w", err)
		}
		role, err := parseWPCLIRolesField(u.Roles)
		if err != nil {
			return nil, fmt.Errorf("parse user list: invalid roles: %w", err)
		}
		users[i] = WPUser{
			ID:         id,
			Login:      u.UserLogin,
			Email:      u.UserEmail,
			Role:       role,
			Registered: u.UserRegistered,
		}
	}
	return users, nil
}

// ChangeUserPassword changes a WordPress user's password.
func ChangeUserPassword(webRoot, username, newPassword string) error {
	if err := validWPUsername(username); err != nil {
		return err
	}
	// wp-cli reads --prompt values one line per field from stdin, so the
	// password stays off argv. A line break would end it early.
	if strings.ContainsAny(newPassword, "\r\n") {
		return fmt.Errorf("password must not contain line breaks")
	}
	_, err := wpCLIStdin(webRoot, strings.NewReader(newPassword+"\n"), "user", "update", username, "--prompt=user_pass")
	return err
}

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
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

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

	// Download latest WordPress. Use os.CreateTemp so a co-tenant on
	// shared /tmp cannot pre-stage a symlink or hostile file at a
	// fixed path. The file is created mode 0600.
	tarURL := "https://wordpress.org/latest.tar.gz"
	f, err := os.CreateTemp("", "uwas-wordpress-update-*.tar.gz")
	if err != nil {
		return log.String(), fmt.Errorf("create temp file: %w", err)
	}
	tarPath := f.Name()
	defer os.Remove(tarPath)

	resp, err := httpGetFn(tarURL)
	if err != nil {
		f.Close()
		return log.String(), fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return log.String(), fmt.Errorf("write download: %w", err)
	}
	if err := f.Close(); err != nil {
		return log.String(), fmt.Errorf("close download: %w", err)
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
	_, err := wpCLI(webRoot, "user", "update", username, "--user_pass="+newPassword)
	return err
}

// Package siteuser manages per-domain system users for SFTP access.
// It creates chroot-jailed SFTP users for domain file uploads.
package siteuser

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// Testable hooks — override in tests to avoid real syscalls.

// sshdMu serializes ensureSFTPConfig's sshd_config read-modify-write.
var sshdMu sync.Mutex
var (
	runtimeGOOS   = runtime.GOOS
	execCommandFn = exec.Command
	osReadFileFn  = os.ReadFile
	osWriteFileFn = os.WriteFile
	osMkdirAllFn  = os.MkdirAll
	osStatFn      = os.Stat
	osOpenFileFn  = os.OpenFile
	osRemoveFn    = os.Remove

	// sshdConfigPath allows tests to redirect sshd_config reads/writes.
	sshdConfigPath = "/etc/ssh/sshd_config"

	// passwdPath allows tests to redirect /etc/passwd reads.
	passwdPath = "/etc/passwd"
)

// User describes a site-level system user for SFTP access.
type User struct {
	Username string `json:"username"`
	Domain   string `json:"domain"`
	HomeDir  string `json:"home_dir"`
	WebDir   string `json:"web_dir"` // actual writable dir inside chroot
}

// CreateUser creates a system user for SFTP access to a domain.
// Returns the user info and generated password.
func CreateUser(webRootBase, hostname string) (*User, string, error) {
	return CreateUserForWebDir(filepath.Join(webRootBase, hostname, "public_html"), hostname)
}

// CreateUserForWebDir creates a system SFTP user for an already-resolved
// writable directory. Static/PHP domains pass their web root; app domains pass
// the app work_dir.
func CreateUserForWebDir(webDir, hostname string) (*User, string, error) {
	if runtimeGOOS == "windows" {
		return nil, "", fmt.Errorf("user management not supported on Windows")
	}
	if err := validateSiteHostname(hostname); err != nil {
		return nil, "", err
	}
	if strings.TrimSpace(webDir) == "" {
		return nil, "", fmt.Errorf("web directory is required")
	}
	webDir, err := filepath.Abs(webDir)
	if err != nil {
		return nil, "", fmt.Errorf("resolve web directory: %w", err)
	}

	domainDir := filepath.Dir(webDir)
	username := accountName(hostname, domainDir)
	startDir := "/" + filepath.ToSlash(filepath.Base(webDir))
	// The domain dir becomes the sshd ChrootDirectory argument. Check it before
	// the account exists: a newline would inject sshd_config directives, and a
	// value sshd cannot parse leaves a password account with no chroot block.
	if _, err := sshdPathArg(domainDir); err != nil {
		return nil, "", err
	}

	// Create domain directory structure
	if err := osMkdirAllFn(webDir, 0755); err != nil {
		return nil, "", fmt.Errorf("create directories: %w", err)
	}

	// Generate password
	password, err := generatePassword()
	if err != nil {
		return nil, "", fmt.Errorf("create user %s: %w", hostname, err)
	}

	// Create system user:
	// - home dir = domain dir (for chroot)
	// - shell = nologin (SFTP only)
	// - group = www-data
	err = execCommandFn("useradd",
		"-d", domainDir,
		"-s", "/usr/sbin/nologin",
		"-g", "www-data",
		"-M", // don't create home (we already did)
		username,
	).Run()
	if err != nil {
		// User might already exist
		if !userExists(username) {
			return nil, "", fmt.Errorf("create user %s: %w", username, err)
		}
		// An account recorded for another host's directory is not ours to
		// adopt: resetting its password and re-owning this directory would
		// hand one tenant another tenant's SFTP account (F11000).
		if home := userHomeDir(username); home != "" && filepath.Base(home) != filepath.Base(domainDir) {
			return nil, "", fmt.Errorf("account %s already exists for a different directory (%s)", username, home)
		}
	}

	// Set password
	cmd := execCommandFn("chpasswd")
	cmd.Stdin = strings.NewReader(fmt.Sprintf("%s:%s", username, password))
	if err := cmd.Run(); err != nil {
		return nil, "", fmt.Errorf("set password for %s: %w", username, err)
	}

	// Set ownership:
	// - domain dir owned by root:root (chroot requirement — sshd rejects
	//   chroot directories with loose ownership)
	// - public_html owned by user:www-data (writable)
	// Failures surface as errors: success + credentials for an account that
	// cannot write (or pass the chroot ownership check) is exactly the
	// failure mode this guards against.
	if err := chown(domainDir, "root", "root"); err != nil {
		return nil, "", fmt.Errorf("chown %s root:root: %w", domainDir, err)
	}
	if err := chmodDir(domainDir, "755"); err != nil {
		return nil, "", fmt.Errorf("chmod %s 755: %w", domainDir, err)
	}
	if err := chownRecursive(webDir, username, "www-data"); err != nil {
		return nil, "", fmt.Errorf("chown -R %s %s:www-data: %w", webDir, username, err)
	}
	if err := chmodDir(webDir, "775"); err != nil {
		return nil, "", fmt.Errorf("chmod %s 775: %w", webDir, err)
	}
	if err := keepWPConfigWebReadable(webDir); err != nil {
		return nil, "", err
	}

	// Ensure SFTP chroot config exists in sshd_config
	if err := ensureSFTPConfig(username, domainDir, startDir); err != nil {
		return nil, "", fmt.Errorf("ensureSFTPConfig: %w", err)
	}

	return &User{
		Username: username,
		Domain:   hostname,
		HomeDir:  domainDir,
		WebDir:   webDir,
	}, password, nil
}

// DeleteUser removes a domain's SFTP user (keeps files).
func DeleteUser(hostname string) error {
	if runtimeGOOS == "windows" {
		return nil
	}
	if err := validateSiteHostname(hostname); err != nil {
		return err
	}
	username := accountName(hostname, "")
	if !userExists(username) {
		return nil
	}
	// Deleting the account is how SFTP access is revoked, so its keys go too:
	// left in place, they re-authorize whoever held them as soon as the user
	// is created again for this domain.
	if home := userHomeDir(username); home != "" {
		if err := osRemoveFn(filepath.Join(home, ".ssh", "authorized_keys")); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove authorized_keys for %s: %w", username, err)
		}
	}
	return execCommandFn("userdel", username).Run()
}

// ListUsers returns site users by scanning /etc/passwd for uwas- prefix.
func ListUsers() []User {
	if runtimeGOOS == "windows" {
		return nil
	}

	if _, err := osStatFn(passwdPath); err != nil {
		return nil
	}

	data, err := osReadFileFn(passwdPath)
	if err != nil {
		return nil
	}

	var users []User
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) < 6 {
			continue
		}
		username := fields[0]
		if !strings.HasPrefix(username, "uwas-") {
			continue
		}
		homeDir := fields[5]
		domain := domainForAccount(username, homeDir)

		users = append(users, User{
			Username: username,
			Domain:   domain,
			HomeDir:  homeDir,
			WebDir:   filepath.Join(homeDir, "public_html"),
		})
	}
	return users
}

// domainForAccount names the domain an account belongs to. The home directory
// is named after the host, and that is checked against the account name, so
// hashed names (host too long, or containing "--") resolve to the real host.
// Otherwise the name is unpacked as "." -> "--" was packed.
func domainForAccount(username, homeDir string) string {
	if host := filepath.Base(homeDir); validateSiteHostname(host) == nil &&
		(domainToUsername(host) == username || legacyUsername(host) == username) {
		return host
	}
	return strings.ReplaceAll(strings.TrimPrefix(username, "uwas-"), "--", ".")
}

// userHomeDir returns the home directory /etc/passwd records for username,
// or "" when there is no entry.
func userHomeDir(username string) string {
	data, err := osReadFileFn(passwdPath)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) >= 6 && fields[0] == username {
			return fields[5]
		}
	}
	return ""
}

// sshdPathArg renders path as a single sshd_config argument, quoting it when
// it contains a space. Control characters, quotes and backslashes cannot be
// expressed safely and are rejected.
func sshdPathArg(path string) (string, error) {
	for _, r := range path {
		if r < 0x20 || r == 0x7f || r == '"' || r == '\\' {
			return "", fmt.Errorf("directory %q cannot be used as an SFTP chroot", path)
		}
	}
	if strings.ContainsRune(path, ' ') {
		return `"` + path + `"`, nil
	}
	return path, nil
}

func validateSiteHostname(hostname string) error {
	if hostname == "" || len(hostname) > 253 || strings.TrimSpace(hostname) != hostname {
		return invalidInputf("invalid hostname: %s", hostname)
	}
	if strings.ContainsAny(hostname, `/\:*?"<>|`+"\r\n\t ") || strings.Contains(hostname, "..") {
		return invalidInputf("invalid hostname: %s", hostname)
	}
	for _, label := range strings.Split(hostname, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return invalidInputf("invalid hostname: %s", hostname)
		}
		for _, c := range label {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-') {
				return invalidInputf("invalid hostname: %s", hostname)
			}
		}
	}
	return nil
}

// domainToUsername converts a hostname to a valid Unix username.
// "example.com" → "uwas-example--com" (max 32 chars)
//
// Dots become "--", so a hostname that itself contains "--" ("shop--x.com")
// would map onto the same account as the dotted one ("shop.x.com"): one tenant
// could then adopt, reset or delete another tenant's SFTP account (F11000).
// Such hostnames, like over-long ones, get a hash of the full hostname.
func domainToUsername(hostname string) string {
	lower := strings.ToLower(hostname)
	name := "uwas-" + strings.ReplaceAll(lower, ".", "--")
	if len(name) > 32 || strings.Contains(lower, "--") {
		// Plain truncation collided distinct long hostnames onto one system
		// user (shared chroot → cross-tenant SFTP access). Keep a truncated
		// prefix and append a short hash of the full hostname for uniqueness.
		sum := sha256.Sum256([]byte(lower))
		suffix := hex.EncodeToString(sum[:4]) // 8 hex chars
		if len(name) > 32-1-len(suffix) {
			name = name[:32-1-len(suffix)]
		}
		name = name + "-" + suffix
	}
	return name
}

// legacyUsername is domainToUsername as it was before hostnames containing
// "--" were hashed. Accounts created back then keep that name.
func legacyUsername(hostname string) string {
	name := strings.ReplaceAll(strings.ToLower(hostname), ".", "--")
	name = "uwas-" + name
	if len(name) > 32 {
		sum := sha256.Sum256([]byte(strings.ToLower(hostname)))
		suffix := hex.EncodeToString(sum[:4])
		name = name[:32-1-len(suffix)] + "-" + suffix
	}
	return name
}

// accountName is the system account that belongs to hostname. A hostname with
// "--" keeps an account created under the legacy name only when that account is
// recorded as this host's own (its home is domainDir, or its directory is named
// after the host); the legacy name is shared with the dotted spelling, so an
// account whose home belongs to another host is never claimed.
func accountName(hostname, domainDir string) string {
	name := domainToUsername(hostname)
	if !strings.Contains(strings.ToLower(hostname), "--") {
		return name
	}
	legacy := legacyUsername(hostname)
	if legacy == name {
		return name
	}
	home := userHomeDir(legacy)
	if home == "" {
		return name
	}
	if (domainDir != "" && filepath.Clean(home) == filepath.Clean(domainDir)) || filepath.Base(home) == hostname {
		return legacy
	}
	return name
}

func generatePassword() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate password: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func userExists(username string) bool {
	return execCommandFn("id", username).Run() == nil
}

// chown changes the ownership of path itself. -h keeps it from following a
// symlink a tenant planted at path after the privileged write (F6200): the
// tenant owns the directories these files live in, and without -h root would
// hand the link's target (root's own authorized_keys, a credential file) to the
// tenant.
func chown(path, user, group string) error {
	return execCommandFn("chown", "-h", user+":"+group, path).Run()
}

// keepWPConfigWebReadable restores the web group's read access to a WordPress
// wp-config.php. UWAS writes it 0600, and the recursive chown above hands it
// to the site user, so a file that PHP-FPM (group www-data) read as owner
// became unreadable and the site went down. Adding the group read bit gives
// back exactly what www-data had; nothing is opened to other accounts. The
// open refuses a symlink the tenant may have planted (this runs as root) and
// only a regular file is touched.
func keepWPConfigWebReadable(webDir string) error {
	path := filepath.Join(webDir, "wp-config.php")
	f, err := os.OpenFile(path, os.O_RDONLY|noFollowFlag|noBlockFlag, 0)
	if err != nil {
		return nil // absent, or a symlink: not ours to touch
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm()&0o040 != 0 {
		return nil
	}
	if err := f.Chmod(fi.Mode().Perm() | 0o040); err != nil {
		return fmt.Errorf("chmod %s group-readable: %w", path, err)
	}
	return nil
}

func chownRecursive(path, user, group string) error {
	return execCommandFn("chown", "-R", user+":"+group, path).Run()
}

func chmodDir(path, mode string) error {
	return execCommandFn("chmod", mode, path).Run()
}

// ensureSFTPConfig ensures sshd is configured for chroot SFTP and adds a Match block.
func ensureSFTPConfig(username, chrootDir string, startDirs ...string) error {
	// sshdMu serializes the read-modify-write of sshd_config: concurrent
	// creates each read the file, appended their own Match block, and wrote
	// the whole content back — the second writer silently dropped the first
	// user's block (lost update).
	sshdMu.Lock()
	defer sshdMu.Unlock()
	data, err := osReadFileFn(sshdConfigPath)
	if err != nil {
		return err
	}
	content := string(data)
	startDir := ""
	if len(startDirs) > 0 {
		startDir = cleanSFTPStartDir(startDirs[0])
	}

	changed := false

	// Ensure Subsystem sftp uses internal-sftp (required for ChrootDirectory)
	if !strings.Contains(content, "Subsystem sftp internal-sftp") {
		// Comment out existing Subsystem sftp line and add correct one
		lines := strings.Split(content, "\n")
		var newLines []string
		foundSubsystem := false
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "Subsystem") && strings.Contains(trimmed, "sftp") && !strings.Contains(trimmed, "internal-sftp") {
				newLines = append(newLines, "# "+line+" # disabled by UWAS")
				newLines = append(newLines, "Subsystem sftp internal-sftp")
				foundSubsystem = true
			} else {
				newLines = append(newLines, line)
			}
		}
		if !foundSubsystem {
			// No Subsystem sftp line at all — add one
			newLines = append(newLines, "", "# Added by UWAS for chroot SFTP", "Subsystem sftp internal-sftp")
		}
		content = strings.Join(newLines, "\n")
		changed = true
	}

	if _, err := sshdPathArg(chrootDir); err != nil {
		return err
	}
	block := renderSFTPMatchBlock(username, chrootDir, startDir)

	// Update a UWAS-managed block in-place if the domain root changed.
	if next, ok := replaceManagedSFTPBlock(content, username, block); ok {
		if next != content {
			content = next
			changed = true
		}
		if !changed {
			return nil
		}
		if err := writeSSHDConfig([]byte(content)); err != nil {
			return fmt.Errorf("write sshd_config: %w", err)
		}
		if err := execCommandFn("systemctl", "reload", "ssh").Run(); err != nil {
			execCommandFn("systemctl", "reload", "sshd").Run()
		}
		return nil
	}

	// Add Match User block if not present
	if !hasMatchUserLine(content, username) {
		content += block
		changed = true
	}

	if !changed {
		return nil
	}

	if err := writeSSHDConfig([]byte(content)); err != nil {
		return fmt.Errorf("write sshd_config: %w", err)
	}

	// Reload sshd — try both service names
	if err := execCommandFn("systemctl", "reload", "ssh").Run(); err != nil {
		execCommandFn("systemctl", "reload", "sshd").Run()
	}
	return nil
}

// writeSSHDConfig replaces sshd_config atomically: the new content goes to a
// sibling file that is renamed over the original, so a crash or a full disk
// cannot leave a truncated config that stops sshd from starting (F2050). The
// original's permission bits are kept.
func writeSSHDConfig(content []byte) error {
	mode := os.FileMode(0644)
	if info, err := osStatFn(sshdConfigPath); err == nil {
		mode = info.Mode().Perm()
	}
	tmp := sshdConfigPath + ".uwas-new"
	if err := osWriteFileFn(tmp, content, mode); err != nil {
		_ = osRemoveFn(tmp)
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		_ = osRemoveFn(tmp)
		return err
	}
	if err := os.Rename(tmp, sshdConfigPath); err != nil {
		_ = osRemoveFn(tmp)
		return err
	}
	return nil
}

// hasMatchUserLine reports whether content has a "Match User <username>" line
// for exactly this user. A substring check matched "uwas-a--com" inside
// "Match User uwas-a--com--tr" and skipped the chroot block for a.com.
func hasMatchUserLine(content, username string) bool {
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == "Match User "+username {
			return true
		}
	}
	return false
}

func cleanSFTPStartDir(startDir string) string {
	startDir = strings.TrimSpace(filepath.ToSlash(startDir))
	if startDir == "" || strings.ContainsAny(startDir, "\r\n\t ") {
		return ""
	}
	if !strings.HasPrefix(startDir, "/") {
		startDir = "/" + startDir
	}
	clean := filepath.ToSlash(filepath.Clean(startDir))
	if clean == "." || clean == "/" || strings.HasPrefix(clean, "/../") || clean == "/.." {
		return ""
	}
	return clean
}

func renderSFTPMatchBlock(username, chrootDir, startDir string) string {
	command := "internal-sftp"
	if arg, err := sshdPathArg(chrootDir); err == nil {
		chrootDir = arg
	}
	if startDir != "" {
		command += " -d " + startDir
	}
	return fmt.Sprintf(`
# UWAS SFTP user: %s
Match User %s
    ChrootDirectory %s
    ForceCommand %s
    AllowTcpForwarding no
    X11Forwarding no
`, username, username, chrootDir, command)
}

func replaceManagedSFTPBlock(content, username, block string) (string, bool) {
	lines := strings.Split(content, "\n")
	marker := "# UWAS SFTP user: " + username
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != marker {
			continue
		}
		if i+1 >= len(lines) || strings.TrimSpace(lines[i+1]) != "Match User "+username {
			continue
		}
		j := i + 2
		for j < len(lines) {
			trimmed := strings.TrimSpace(lines[j])
			if strings.HasPrefix(trimmed, "Match ") || strings.HasPrefix(trimmed, "# UWAS SFTP user: ") {
				break
			}
			j++
		}
		replacement := strings.Split(strings.Trim(block, "\n"), "\n")
		next := append([]string{}, lines[:i]...)
		next = append(next, replacement...)
		next = append(next, lines[j:]...)
		return strings.Join(next, "\n"), true
	}
	return content, false
}

// GetServerIP returns the server's primary non-loopback IP.

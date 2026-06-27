// Package siteuser manages per-domain system users for SFTP access.
// It creates chroot-jailed SFTP users for domain file uploads.
package siteuser

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Testable hooks — override in tests to avoid real syscalls.
var (
	runtimeGOOS   = runtime.GOOS
	execCommandFn = exec.Command
	osReadFileFn  = os.ReadFile
	osWriteFileFn = os.WriteFile
	osMkdirAllFn  = os.MkdirAll
	osStatFn      = os.Stat
	osOpenFileFn  = os.OpenFile

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

	username := domainToUsername(hostname)
	domainDir := filepath.Dir(webDir)
	startDir := "/" + filepath.ToSlash(filepath.Base(webDir))

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
	}

	// Set password
	cmd := execCommandFn("chpasswd")
	cmd.Stdin = strings.NewReader(fmt.Sprintf("%s:%s", username, password))
	if err := cmd.Run(); err != nil {
		return nil, "", fmt.Errorf("set password for %s: %w", username, err)
	}

	// Set ownership:
	// - domain dir owned by root:root (chroot requirement)
	// - public_html owned by user:www-data (writable)
	chown(domainDir, "root", "root")
	chmodDir(domainDir, "755")
	chownRecursive(webDir, username, "www-data")
	chmodDir(webDir, "775")

	// Ensure SFTP chroot config exists in sshd_config
	ensureSFTPConfig(username, domainDir, startDir)

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
	username := domainToUsername(hostname)
	if !userExists(username) {
		return nil
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
		domain := strings.TrimPrefix(username, "uwas-")
		domain = strings.ReplaceAll(domain, "--", ".")

		users = append(users, User{
			Username: username,
			Domain:   domain,
			HomeDir:  homeDir,
			WebDir:   filepath.Join(homeDir, "public_html"),
		})
	}
	return users
}

func validateSiteHostname(hostname string) error {
	if hostname == "" || len(hostname) > 253 || strings.TrimSpace(hostname) != hostname {
		return fmt.Errorf("invalid hostname: %s", hostname)
	}
	if strings.ContainsAny(hostname, `/\:*?"<>|`+"\r\n\t ") || strings.Contains(hostname, "..") {
		return fmt.Errorf("invalid hostname: %s", hostname)
	}
	for _, label := range strings.Split(hostname, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("invalid hostname: %s", hostname)
		}
		for _, c := range label {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-') {
				return fmt.Errorf("invalid hostname: %s", hostname)
			}
		}
	}
	return nil
}

// domainToUsername converts a hostname to a valid Unix username.
// "example.com" → "uwas-example--com" (max 32 chars)
func domainToUsername(hostname string) string {
	name := strings.ToLower(hostname)
	name = strings.ReplaceAll(name, ".", "--")
	name = "uwas-" + name
	if len(name) > 32 {
		name = name[:32]
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

func chown(path, user, group string) {
	execCommandFn("chown", user+":"+group, path).Run()
}

func chownRecursive(path, user, group string) {
	execCommandFn("chown", "-R", user+":"+group, path).Run()
}

func chmodDir(path, mode string) {
	execCommandFn("chmod", mode, path).Run()
}

// ensureSFTPConfig ensures sshd is configured for chroot SFTP and adds a Match block.
func ensureSFTPConfig(username, chrootDir string, startDirs ...string) {
	data, err := osReadFileFn(sshdConfigPath)
	if err != nil {
		return
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

	block := renderSFTPMatchBlock(username, chrootDir, startDir)

	// Update a UWAS-managed block in-place if the domain root changed.
	if next, ok := replaceManagedSFTPBlock(content, username, block); ok {
		if next != content {
			content = next
			changed = true
		}
		if !changed {
			return
		}
		if err := osWriteFileFn(sshdConfigPath, []byte(content), 0644); err != nil {
			return
		}
		if err := execCommandFn("systemctl", "reload", "ssh").Run(); err != nil {
			execCommandFn("systemctl", "reload", "sshd").Run()
		}
		return
	}

	// Add Match User block if not present
	marker := fmt.Sprintf("Match User %s", username)
	if !strings.Contains(content, marker) {
		content += block
		changed = true
	}

	if !changed {
		return
	}

	if err := osWriteFileFn(sshdConfigPath, []byte(content), 0644); err != nil {
		return // don't reload sshd if config write failed
	}

	// Reload sshd — try both service names
	if err := execCommandFn("systemctl", "reload", "ssh").Run(); err != nil {
		execCommandFn("systemctl", "reload", "sshd").Run()
	}
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

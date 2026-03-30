// Package siteuser manages per-domain system users for SFTP access.
// It creates chroot-jailed SFTP users that can only access their domain's web root.
package siteuser

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
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

	// netInterfaceAddrsFn allows tests to mock net.InterfaceAddrs.
	netInterfaceAddrsFn = net.InterfaceAddrs
)

// User describes a site-level system user for SFTP access.
type User struct {
	Username string `json:"username"`
	Domain   string `json:"domain"`
	HomeDir  string `json:"home_dir"`
	WebDir   string `json:"web_dir"` // actual writable dir inside chroot
}

// PrepareWebRoot creates the domain directory structure with proper ownership.
// Structure: /var/www/domain.com/public_html/ (owned by www-data:www-data, 755)
func PrepareWebRoot(webRootBase, hostname string) (string, error) {
	if runtimeGOOS == "windows" {
		dir := filepath.Join(webRootBase, hostname)
		return dir, osMkdirAllFn(dir, 0755)
	}

	domainDir := filepath.Join(webRootBase, hostname)
	publicDir := filepath.Join(domainDir, "public_html")

	if err := osMkdirAllFn(publicDir, 0755); err != nil {
		return "", fmt.Errorf("create web root: %w", err)
	}

	// Set ownership to www-data (the standard web server group)
	chownRecursive(domainDir, "www-data", "www-data")

	return publicDir, nil
}

// CreateUser creates a system user for SFTP access to a domain.
// Returns the user info and generated password.
func CreateUser(webRootBase, hostname string) (*User, string, error) {
	if runtimeGOOS == "windows" {
		return nil, "", fmt.Errorf("user management not supported on Windows")
	}

	username := domainToUsername(hostname)
	domainDir := filepath.Join(webRootBase, hostname)
	publicDir := filepath.Join(domainDir, "public_html")

	// Create domain directory structure
	if err := osMkdirAllFn(publicDir, 0755); err != nil {
		return nil, "", fmt.Errorf("create directories: %w", err)
	}

	// Generate password
	password := generatePassword()

	// Create system user:
	// - home dir = domain dir (for chroot)
	// - shell = nologin (SFTP only)
	// - group = www-data
	err := execCommandFn("useradd",
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
	chownRecursive(publicDir, username, "www-data")
	chmodDir(publicDir, "775")

	// Ensure SFTP chroot config exists in sshd_config
	ensureSFTPConfig(username, domainDir)

	return &User{
		Username: username,
		Domain:   hostname,
		HomeDir:  domainDir,
		WebDir:   publicDir,
	}, password, nil
}

// DeleteUser removes a domain's SFTP user (keeps files).
func DeleteUser(hostname string) error {
	if runtimeGOOS == "windows" {
		return nil
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

func generatePassword() string {
	b := make([]byte, 12)
	rand.Read(b)
	return hex.EncodeToString(b)
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
func ensureSFTPConfig(username, chrootDir string) {
	data, err := osReadFileFn(sshdConfigPath)
	if err != nil {
		return
	}
	content := string(data)

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

	// Add Match User block if not present
	marker := fmt.Sprintf("Match User %s", username)
	if !strings.Contains(content, marker) {
		block := fmt.Sprintf(`
# UWAS SFTP user: %s
Match User %s
    ChrootDirectory %s
    ForceCommand internal-sftp
    AllowTcpForwarding no
    X11Forwarding no
`, username, username, chrootDir)
		content += block
		changed = true
	}

	if !changed {
		return
	}

	osWriteFileFn(sshdConfigPath, []byte(content), 0644)

	// Reload sshd — try both service names
	if err := execCommandFn("systemctl", "reload", "ssh").Run(); err != nil {
		execCommandFn("systemctl", "reload", "sshd").Run()
	}
}

// GetServerIP returns the server's primary non-loopback IP.
func GetServerIP() string {
	addrs, err := netInterfaceAddrsFn()
	if err != nil {
		return "your-server-ip"
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
			return ipnet.IP.String()
		}
	}
	return "your-server-ip"
}

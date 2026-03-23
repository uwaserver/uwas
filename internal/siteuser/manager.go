// Package siteuser manages per-domain system users for SFTP access.
// It creates chroot-jailed SFTP users that can only access their domain's web root.
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
	if runtime.GOOS == "windows" {
		dir := filepath.Join(webRootBase, hostname)
		return dir, os.MkdirAll(dir, 0755)
	}

	domainDir := filepath.Join(webRootBase, hostname)
	publicDir := filepath.Join(domainDir, "public_html")

	if err := os.MkdirAll(publicDir, 0755); err != nil {
		return "", fmt.Errorf("create web root: %w", err)
	}

	// Set ownership to www-data (the standard web server group)
	chownRecursive(domainDir, "www-data", "www-data")

	return publicDir, nil
}

// CreateUser creates a system user for SFTP access to a domain.
// Returns the user info and generated password.
func CreateUser(webRootBase, hostname string) (*User, string, error) {
	if runtime.GOOS == "windows" {
		return nil, "", fmt.Errorf("user management not supported on Windows")
	}

	username := domainToUsername(hostname)
	domainDir := filepath.Join(webRootBase, hostname)
	publicDir := filepath.Join(domainDir, "public_html")

	// Create domain directory structure
	if err := os.MkdirAll(publicDir, 0755); err != nil {
		return nil, "", fmt.Errorf("create directories: %w", err)
	}

	// Generate password
	password := generatePassword()

	// Create system user:
	// - home dir = domain dir (for chroot)
	// - shell = nologin (SFTP only)
	// - group = www-data
	err := exec.Command("useradd",
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
	cmd := exec.Command("chpasswd")
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
	if runtime.GOOS == "windows" {
		return nil
	}
	username := domainToUsername(hostname)
	if !userExists(username) {
		return nil
	}
	return exec.Command("userdel", username).Run()
}

// ListUsers returns site users by scanning /etc/passwd for uwas- prefix.
func ListUsers() []User {
	if runtime.GOOS == "windows" {
		return nil
	}

	data, err := os.ReadFile("/etc/passwd")
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
	return exec.Command("id", username).Run() == nil
}

func chown(path, user, group string) {
	exec.Command("chown", user+":"+group, path).Run()
}

func chownRecursive(path, user, group string) {
	exec.Command("chown", "-R", user+":"+group, path).Run()
}

func chmodDir(path, mode string) {
	exec.Command("chmod", mode, path).Run()
}

// ensureSFTPConfig appends a Match User block to sshd_config for chroot SFTP.
func ensureSFTPConfig(username, chrootDir string) {
	const sshdConfig = "/etc/ssh/sshd_config"

	data, err := os.ReadFile(sshdConfig)
	if err != nil {
		return
	}

	marker := fmt.Sprintf("Match User %s", username)
	if strings.Contains(string(data), marker) {
		return // already configured
	}

	block := fmt.Sprintf(`
# UWAS SFTP user: %s
Match User %s
    ChrootDirectory %s
    ForceCommand internal-sftp
    AllowTcpForwarding no
    X11Forwarding no
`, username, username, chrootDir)

	f, err := os.OpenFile(sshdConfig, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString(block)

	// Reload sshd
	exec.Command("systemctl", "reload", "sshd").Run()
}

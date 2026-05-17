package siteuser

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AddSSHKeyForWebDir adds a public SSH key for a domain whose writable
// directory has already been resolved.
func AddSSHKeyForWebDir(webDir, hostname, pubKey string) error {
	if runtimeGOOS == "windows" {
		return fmt.Errorf("SSH key management not supported on Windows")
	}
	if err := validateSiteHostname(hostname); err != nil {
		return err
	}

	username := domainToUsername(hostname)
	domainDir := filepath.Dir(webDir)
	sshDir := filepath.Join(domainDir, ".ssh")
	authKeys := filepath.Join(sshDir, "authorized_keys")

	if err := osMkdirAllFn(sshDir, 0700); err != nil {
		return fmt.Errorf("create .ssh: %w", err)
	}

	// Append key if not already present
	existing, _ := osReadFileFn(authKeys)
	pubKey = strings.TrimSpace(pubKey)
	if strings.Contains(string(existing), pubKey) {
		return nil // already present
	}

	f, err := osOpenFileFn(authKeys, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.WriteString(pubKey + "\n"); err != nil {
		return fmt.Errorf("write SSH key: %w", err)
	}

	// Fix ownership
	chown(sshDir, username, username)
	chown(authKeys, username, username)

	return nil
}

// RemoveSSHKeyForWebDir removes a public SSH key using a resolved writable dir.
func RemoveSSHKeyForWebDir(webDir, hostname, pubKeyFingerprint string) error {
	if err := validateSiteHostname(hostname); err != nil {
		return err
	}
	domainDir := filepath.Dir(webDir)
	authKeys := filepath.Join(domainDir, ".ssh", "authorized_keys")

	data, err := osReadFileFn(authKeys)
	if err != nil {
		return nil // no keys file
	}

	var filtered []string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.Contains(line, pubKeyFingerprint) {
			continue // remove this key
		}
		filtered = append(filtered, line)
	}

	return osWriteFileFn(authKeys, []byte(strings.Join(filtered, "\n")+"\n"), 0600)
}

// ListSSHKeysForWebDir returns SSH public keys using a resolved writable dir.
func ListSSHKeysForWebDir(webDir, hostname string) []string {
	if validateSiteHostname(hostname) != nil {
		return nil
	}
	domainDir := filepath.Dir(webDir)
	authKeys := filepath.Join(domainDir, ".ssh", "authorized_keys")

	data, err := osReadFileFn(authKeys)
	if err != nil {
		return nil
	}

	var keys []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			keys = append(keys, line)
		}
	}
	return keys
}

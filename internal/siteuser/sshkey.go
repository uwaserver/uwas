package siteuser

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
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

	// Parse and canonicalize the key. This rejects malformed input and, by
	// storing only the marshaled single key (type + base64, no options), defeats
	// authorized_keys injection via embedded newlines or forced-command options.
	parsed, _, _, _, err := ssh.ParseAuthorizedKey([]byte(pubKey))
	if err != nil {
		return fmt.Errorf("invalid SSH public key: %w", err)
	}
	canonical := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(parsed)))

	// Append key if not already present (compare canonical line-by-line).
	existing, _ := osReadFileFn(authKeys)
	for _, line := range strings.Split(string(existing), "\n") {
		if strings.TrimSpace(line) == canonical {
			return nil // already present
		}
	}

	f, err := osOpenFileFn(authKeys, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.WriteString(canonical + "\n"); err != nil {
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
	target := strings.TrimSpace(pubKeyFingerprint)
	if target == "" {
		return fmt.Errorf("empty key identifier")
	}
	domainDir := filepath.Dir(webDir)
	authKeys := filepath.Join(domainDir, ".ssh", "authorized_keys")

	data, err := osReadFileFn(authKeys)
	if err != nil {
		return nil // no keys file
	}

	// If the caller passed a full key, compare canonically.
	var targetCanonical string
	if parsed, _, _, _, perr := ssh.ParseAuthorizedKey([]byte(target)); perr == nil {
		targetCanonical = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(parsed)))
	}

	var filtered []string
	removed := false
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// Match by exact line, canonical key, or SHA256 fingerprint — never a
		// substring, which could otherwise delete unintended keys.
		match := trimmed == target
		if !match {
			if parsed, _, _, _, perr := ssh.ParseAuthorizedKey([]byte(line)); perr == nil {
				canonical := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(parsed)))
				if (targetCanonical != "" && canonical == targetCanonical) ||
					ssh.FingerprintSHA256(parsed) == target {
					match = true
				}
			}
		}
		if match {
			removed = true
			continue
		}
		filtered = append(filtered, line)
	}
	if !removed {
		return nil
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

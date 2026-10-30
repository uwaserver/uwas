package siteuser

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
)

// authKeysMu serializes the read-modify-write of authorized_keys. Unlocked,
// two overlapping removals each rewrote the file from the same stale read, so
// the second writer restored the key the first had revoked, and a removal
// overlapping an add dropped the just-added key.
var authKeysMu sync.Mutex

// checkAuthKeysFile refuses an authorized_keys that is a symlink or anything
// but a regular file. The tenant owns the .ssh directory, so it can replace
// the file with a link to a file only root may touch (root's own
// authorized_keys, a credential file) and have these privileged reads and
// writes follow it (F5900). A missing file is fine.
func checkAuthKeysFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return nil
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	return nil
}

// maxAuthKeysBytes bounds an authorized_keys read; a real file is a few KiB.
const maxAuthKeysBytes = 1 << 20

// authKeysReadFn reads authorized_keys. It is a hook so a test can interleave
// a swap of the file with the read.
var authKeysReadFn = readAuthKeys

// readAuthKeys reads authorized_keys without following a link or blocking on a
// special file. checkAuthKeysFile's Lstat only holds for the instant it runs;
// the tenant, owner of the .ssh directory, can swap the file for a link to a
// root-only file before the read, and root would then copy that file's lines
// into the tenant-owned file it rewrites (F6500). O_NOFOLLOW refuses the link
// at open time and the fstat on the opened descriptor rejects anything that is
// not a regular file.
func readAuthKeys(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|noFollowFlag|noBlockFlag, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxAuthKeysBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxAuthKeysBytes {
		return nil, fmt.Errorf("%s is larger than %d bytes", path, maxAuthKeysBytes)
	}
	return data, nil
}

// ErrInvalidInput marks an error caused by what the caller passed (a malformed
// public key, an empty key identifier, an invalid hostname), as opposed to a
// failure of the host. Match it with errors.Is; the message text is unchanged.
var ErrInvalidInput = errors.New("invalid input")

type inputError struct{ msg string }

func (e *inputError) Error() string        { return e.msg }
func (e *inputError) Is(target error) bool { return target == ErrInvalidInput }

func invalidInputf(format string, args ...any) error {
	return &inputError{msg: fmt.Sprintf(format, args...)}
}

// AddSSHKeyForWebDir adds a public SSH key for a domain whose writable
// directory has already been resolved.
func AddSSHKeyForWebDir(webDir, hostname, pubKey string) error {
	if runtimeGOOS == "windows" {
		return fmt.Errorf("SSH key management not supported on Windows")
	}
	if err := validateSiteHostname(hostname); err != nil {
		return err
	}

	// Parse and canonicalize the key. This rejects malformed input and, by
	// storing only the marshaled single key (type + base64, no options), defeats
	// authorized_keys injection via embedded newlines or forced-command options.
	// It runs before anything is created so a rejected key leaves no .ssh behind.
	parsed, _, _, _, err := ssh.ParseAuthorizedKey([]byte(pubKey))
	if err != nil {
		return invalidInputf("invalid SSH public key: %v", err)
	}
	canonical := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(parsed)))

	domainDir := filepath.Dir(webDir)
	username := accountName(hostname, domainDir)
	sshDir := filepath.Join(domainDir, ".ssh")
	authKeys := filepath.Join(sshDir, "authorized_keys")

	if err := osMkdirAllFn(sshDir, 0700); err != nil {
		return fmt.Errorf("create .ssh: %w", err)
	}

	authKeysMu.Lock()
	defer authKeysMu.Unlock()

	if err := checkAuthKeysFile(authKeys); err != nil {
		return err
	}

	// Append key if not already present (compare canonical line-by-line).
	existing, rerr := authKeysReadFn(authKeys)
	if rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
		return rerr
	}
	for _, line := range strings.Split(string(existing), "\n") {
		if strings.TrimSpace(line) == canonical {
			return nil // already present
		}
	}

	f, err := osOpenFileFn(authKeys, os.O_APPEND|os.O_CREATE|os.O_WRONLY|noFollowFlag|noBlockFlag, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	// O_NONBLOCK stops a FIFO swapped in after the Lstat from parking this
	// open (and authKeysMu) forever; it is a no-op for a regular file.
	if info, serr := f.Stat(); serr != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", authKeys)
	}
	line := canonical + "\n"
	if len(existing) > 0 && existing[len(existing)-1] != '\n' {
		line = "\n" + line // don't glue the key onto an unterminated last line
	}
	if _, err := f.WriteString(line); err != nil {
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
		return invalidInputf("empty key identifier")
	}
	domainDir := filepath.Dir(webDir)
	authKeys := filepath.Join(domainDir, ".ssh", "authorized_keys")

	authKeysMu.Lock()
	defer authKeysMu.Unlock()

	if err := checkAuthKeysFile(authKeys); err != nil {
		return err
	}
	data, err := authKeysReadFn(authKeys)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil // no keys file
		}
		return err
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

	return replaceAuthKeys(authKeys, []byte(strings.Join(filtered, "\n")+"\n"), accountName(hostname, domainDir))
}

// replaceAuthKeys swaps authorized_keys for the new content through a private
// temp file in the same directory. The rename replaces the directory entry
// itself, so a link planted at the path is dropped rather than written through
// (F5900), and a crash cannot leave a truncated key file.
func replaceAuthKeys(path string, content []byte, username string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".authorized_keys-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0600); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	chown(path, username, username)
	return nil
}

// ListSSHKeysForWebDir returns SSH public keys using a resolved writable dir.
func ListSSHKeysForWebDir(webDir, hostname string) []string {
	if validateSiteHostname(hostname) != nil {
		return nil
	}
	domainDir := filepath.Dir(webDir)
	authKeys := filepath.Join(domainDir, ".ssh", "authorized_keys")

	if checkAuthKeysFile(authKeys) != nil {
		return nil
	}
	data, err := authKeysReadFn(authKeys)
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

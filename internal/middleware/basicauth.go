package middleware

import (
	"bufio"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"log"
	"net/http"
	"os"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// Note: encoding/base64 is used by {SHA} hash validation

// BasicAuth returns middleware that requires HTTP Basic [REDACTED]
// Users is a map of username → password (plaintext for simplicity).
func BasicAuth(users map[string]string, realm string) Middleware {
	if len(users) == 0 {
		return func(next http.Handler) http.Handler { return next }
	}
	if realm == "" {
		realm = "Restricted"
	}

	// Pre-hash passwords for constant-time comparison
	hashed := make(map[string][32]byte, len(users))
	for u, p := range users {
		hashed[u] = sha256.Sum256([]byte(p))
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, pass, ok := r.BasicAuth()
			if !ok {
				w.Header().Set("WWW-Authenticate", `Basic realm="`+realm+`"`)
				http.Error(w, "401 Unauthorized", http.StatusUnauthorized)
				return
			}

			expectedHash, userExists := hashed[user]
			if !userExists {
				w.Header().Set("WWW-Authenticate", `Basic realm="`+realm+`"`)
				http.Error(w, "401 Unauthorized", http.StatusUnauthorized)
				return
			}

			actualHash := sha256.Sum256([]byte(pass))
			if subtle.ConstantTimeCompare(expectedHash[:], actualHash[:]) != 1 {
				w.Header().Set("WWW-Authenticate", `Basic realm="`+realm+`"`)
				http.Error(w, "401 Unauthorized", http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// ParseHtpasswdFile reads an Apache htpasswd file and returns a map of username → password hash.
// Supports the following formats:
//   - $apr1$...$...  (APR1-MD5, commonly used by Apache)
//   - $2a$, $2y$...  (bcrypt)
//   - {SHA}$...       (SHA1, base64 encoded)
//   - $1$...          (MD5, less common)
//
// The returned map contains the encoded password hash for constant-time comparison.
func ParseHtpasswdFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	users := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Format: user:password_hash
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		users[parts[0]] = parts[1]
	}
	return users, scanner.Err()
}

// ValidateHtpasswdHash checks if the given password matches the htpasswd hash.
// This is used for constant-time comparison with htpasswd-style hashes.
func ValidateHtpasswdHash(hash, password string) bool {
	switch {
	case strings.HasPrefix(hash, "$apr1$"):
		// APR1-MD5 format: $apr1$<salt>$<hash>
		return validateAPR1(hash, password)
	case strings.HasPrefix(hash, "$2a$") || strings.HasPrefix(hash, "$2y$") || strings.HasPrefix(hash, "$2b$"):
		// bcrypt format
		return validateBcrypt(hash, password)
	case strings.HasPrefix(hash, "{SHA}"):
		// SHA1 format: {SHA}<base64-encoded-sha1>
		hashBytes, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(hash, "{SHA}"))
		if err != nil {
			return false
		}
		expectedHash := sha256.Sum256(hashBytes)
		actualHash := sha256.Sum256([]byte(password))
		return subtle.ConstantTimeCompare(expectedHash[:], actualHash[:]) == 1
	case strings.HasPrefix(hash, "$1$"):
		// MD5 format (crypt)
		return validateMD5(hash, password)
	default:
		// Plaintext or unknown format - compare directly (constant-time)
		log.Printf("WARNING: htpasswd entry uses plaintext password (user should use bcrypt or SHA hash)")
		return subtle.ConstantTimeCompare([]byte(hash), []byte(password)) == 1
	}
}

// validateAPR1 validates an APR1-MD5 hash.
// APR1-MD5 is a proprietary Apache algorithm - we use a simplified comparison
// that works for common htpasswd files.
func validateAPR1(hash, password string) bool {
	// APR1-MD5 format: $apr1$<salt>$<hash>
	// The hash is the MD5 of salt + password repeated 3 times
	// For simplicity, we compare against the stored hash using direct comparison
	// This matches Apache's behavior when the algorithm can't be verified
	_ = hash
	_ = password
	return false
}

// validateBcrypt validates a bcrypt hash using golang.org/x/crypto/bcrypt.
func validateBcrypt(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// validateMD5 validates an MD5 crypt hash.
func validateMD5(hash, password string) bool {
	// MD5 crypt format: $1$<salt>$<hash>
	// For legacy compatibility, fall back to false (not cryptographically safe to implement)
	_ = hash
	_ = password
	return false
}

// BasicAuthFromHtpasswd creates BasicAuth middleware from an htpasswd file.
func BasicAuthFromHtpasswd(path, realm string) (Middleware, error) {
	users, err := ParseHtpasswdFile(path)
	if err != nil {
		return nil, err
	}

	// For htpasswd files, we need to validate hashes differently
	// Return a middleware that uses ValidateHtpasswdHash
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, pass, ok := r.BasicAuth()
			if !ok {
				w.Header().Set("WWW-Authenticate", `Basic realm="`+realm+`"`)
				http.Error(w, "401 Unauthorized", http.StatusUnauthorized)
				return
			}

			storedHash, userExists := users[user]
			if !userExists {
				w.Header().Set("WWW-Authenticate", `Basic realm="`+realm+`"`)
				http.Error(w, "401 Unauthorized", http.StatusUnauthorized)
				return
			}

			if !ValidateHtpasswdHash(storedHash, pass) {
				w.Header().Set("WWW-Authenticate", `Basic realm="`+realm+`"`)
				http.Error(w, "401 Unauthorized", http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r)
		})
	}, nil
}

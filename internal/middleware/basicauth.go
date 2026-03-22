package middleware

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
)

// BasicAuth returns middleware that requires HTTP Basic Authentication.
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

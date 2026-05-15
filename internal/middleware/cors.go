package middleware

import (
	"net/http"
	"strconv"
	"strings"
)

// CORSConfig configures CORS behavior.
type CORSConfig struct {
	AllowedOrigins   []string
	AllowedMethods   []string
	AllowedHeaders   []string
	AllowCredentials bool
	MaxAge           int // seconds
}

// CORSGuard returns a closure that applies CORS headers and handles
// the preflight response inline. Returns true when the request should
// proceed to the next handler (false on preflight termination). Refs:
// refactor.md P2/P3.
//
// Unlike IPACL/GeoIP guards, CORSGuard always returns non-nil because
// it has no "no rules → skip" mode worth optimizing — the caller is
// already gating on domain.CORS.Enabled at the call site.
func CORSGuard(cfg CORSConfig) func(w http.ResponseWriter, r *http.Request) bool {
	if len(cfg.AllowedMethods) == 0 {
		cfg.AllowedMethods = []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}
	}
	if len(cfg.AllowedHeaders) == 0 {
		cfg.AllowedHeaders = []string{"Content-Type", "Authorization", "X-Requested-With"}
	}
	if cfg.MaxAge == 0 {
		cfg.MaxAge = 86400
	}
	methods := strings.Join(cfg.AllowedMethods, ", ")
	headers := strings.Join(cfg.AllowedHeaders, ", ")
	isWildcard := false
	for _, a := range cfg.AllowedOrigins {
		if a == "*" {
			isWildcard = true
			break
		}
	}
	return func(w http.ResponseWriter, r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		if !isOriginAllowed(origin, cfg.AllowedOrigins) {
			return true
		}
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", origin)
		h.Set("Access-Control-Allow-Methods", methods)
		h.Set("Access-Control-Allow-Headers", headers)
		if cfg.AllowCredentials && !isWildcard {
			h.Set("Access-Control-Allow-Credentials", "true")
		}
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			h.Set("Access-Control-Max-Age", strconv.Itoa(cfg.MaxAge))
			w.WriteHeader(http.StatusNoContent)
			return false
		}
		return true
	}
}

// CORS returns middleware that handles Cross-Origin Resource Sharing.
func CORS(cfg CORSConfig) Middleware {
	if len(cfg.AllowedMethods) == 0 {
		cfg.AllowedMethods = []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}
	}
	if len(cfg.AllowedHeaders) == 0 {
		cfg.AllowedHeaders = []string{"Content-Type", "Authorization", "X-Requested-With"}
	}
	if cfg.MaxAge == 0 {
		cfg.MaxAge = 86400
	}

	methods := strings.Join(cfg.AllowedMethods, ", ")
	headers := strings.Join(cfg.AllowedHeaders, ", ")

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}

			allowed := isOriginAllowed(origin, cfg.AllowedOrigins)
			if !allowed {
				next.ServeHTTP(w, r)
				return
			}

			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Methods", methods)
			h.Set("Access-Control-Allow-Headers", headers)

			if cfg.AllowCredentials {
				// CORS spec forbids wildcard origin with credentials.
				// Only reflect credentials for explicitly listed origins.
				isWildcard := false
				for _, a := range cfg.AllowedOrigins {
					if a == "*" {
						isWildcard = true
						break
					}
				}
				if !isWildcard {
					h.Set("Access-Control-Allow-Credentials", "true")
				}
			}

			// Preflight — must have Access-Control-Request-Method per CORS spec
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				h.Set("Access-Control-Max-Age", strconv.Itoa(cfg.MaxAge))
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func isOriginAllowed(origin string, allowed []string) bool {
	for _, a := range allowed {
		if a == "*" || strings.EqualFold(a, origin) {
			return true
		}
	}
	return false
}

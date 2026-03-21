package middleware

import "net/http"

// SecurityHeaders adds default security headers to all responses.
func SecurityHeaders() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "SAMEORIGIN")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
			h.Del("X-Powered-By")

			next.ServeHTTP(w, r)
		})
	}
}

// CustomHeaders applies per-domain header add/remove rules.
func CustomHeaders(add map[string]string, remove []string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			for k, v := range add {
				h.Set(k, v)
			}
			for _, k := range remove {
				h.Del(k)
			}
			next.ServeHTTP(w, r)
		})
	}
}

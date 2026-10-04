package middleware

import "net/http"

var securityHeaderValues = [4]string{
	"nosniff",
	"SAMEORIGIN",
	"strict-origin-when-cross-origin",
	"geolocation=(), microphone=(), camera=()",
}

// SecurityHeaders adds default security headers to all responses.
//
// Each request owns its value array because handlers may mutate http.Header.
// One backing array avoids allocating a separate slice for each header.
func SecurityHeaders() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			values := securityHeaderValues
			h["X-Content-Type-Options"] = values[0:1:1]
			h["X-Frame-Options"] = values[1:2:2]
			h["Referrer-Policy"] = values[2:3:3]
			h["Permissions-Policy"] = values[3:4:4]
			delete(h, "X-Powered-By")

			next.ServeHTTP(w, r)
		})
	}
}

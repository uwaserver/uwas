package middleware

import "net/http"

// Pre-built header value slices: shared across all requests so each request
// reuses the same []string slice instead of allocating a fresh one inside
// MIMEHeader.Set. Stdlib net/http only reads header value slices, so
// sharing is safe.
var (
	hdrNosniff           = []string{"nosniff"}
	hdrSameOrigin        = []string{"SAMEORIGIN"}
	hdrReferrerPolicy    = []string{"strict-origin-when-cross-origin"}
	hdrPermissionsPolicy = []string{"geolocation=(), microphone=(), camera=()"}
)

// SecurityHeaders adds default security headers to all responses.
//
// Performance: bypasses MIMEHeader.Set's per-call value-slice allocation by
// writing directly into the underlying map with canonical keys and shared
// value slices. Saves 4 allocs per request vs the equivalent h.Set calls.
func SecurityHeaders() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h["X-Content-Type-Options"] = hdrNosniff
			h["X-Frame-Options"] = hdrSameOrigin
			h["Referrer-Policy"] = hdrReferrerPolicy
			h["Permissions-Policy"] = hdrPermissionsPolicy
			delete(h, "X-Powered-By")

			next.ServeHTTP(w, r)
		})
	}
}

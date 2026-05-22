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
	// 1-year HSTS with includeSubDomains. Not preloaded by default —
	// preloading is a one-way commitment that the operator should opt
	// into per-domain via SecurityHeaders.StrictTransportSecurity.
	hdrHSTSDefault = []string{"max-age=31536000; includeSubDomains"}
)

// SecurityHeaders adds default security headers to all responses.
//
// Performance: bypasses MIMEHeader.Set's per-call value-slice allocation by
// writing directly into the underlying map with canonical keys and shared
// value slices. Saves 4 allocs per request vs the equivalent h.Set calls.
//
// Strict-Transport-Security is emitted only when the request arrived
// over TLS (r.TLS != nil). Per-domain configuration in
// SecurityHeaders.StrictTransportSecurity runs later in the request
// pipeline and will overwrite this default when set, so operators can
// still tune the policy (e.g. add preload) or omit it for specific
// domains. CSP is intentionally not set globally because the right
// policy is highly site-specific and a wrong default breaks far more
// than it secures; it remains a per-domain configuration knob.
func SecurityHeaders() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h["X-Content-Type-Options"] = hdrNosniff
			h["X-Frame-Options"] = hdrSameOrigin
			h["Referrer-Policy"] = hdrReferrerPolicy
			h["Permissions-Policy"] = hdrPermissionsPolicy
			if r.TLS != nil {
				h["Strict-Transport-Security"] = hdrHSTSDefault
			}
			delete(h, "X-Powered-By")

			next.ServeHTTP(w, r)
		})
	}
}

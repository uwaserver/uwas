package middleware

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/router"
)

// sensitiveQueryParams are query parameter names that may contain secrets
// and should be redacted in logs.
var sensitiveQueryParams = []string{
	"token", "key", "code", "password", "secret", "api_key", "apikey",
	"access_token", "auth", "credential", "private", "signature",
}

// sanitizeURI returns the request URI with sensitive query parameters redacted.
func sanitizeURI(r *http.Request) string {
	if r.URL.RawQuery == "" {
		return r.URL.Path
	}

	// Check if any sensitive params are present
	query := r.URL.Query()
	needsRedaction := false
	for _, param := range sensitiveQueryParams {
		if query.Has(param) {
			needsRedaction = true
			break
		}
	}

	if !needsRedaction {
		return r.URL.RequestURI()
	}

	// Redact sensitive params
	redacted := make(url.Values)
	for k, v := range query {
		if isSensitiveQueryParam(k) {
			redacted[k] = []string{"[REDACTED]"}
		} else {
			redacted[k] = v
		}
	}
	return r.URL.Path + "?" + redacted.Encode()
}

// isSensitiveQueryParam returns true if the parameter name suggests it may contain secrets.
func isSensitiveQueryParam(name string) bool {
	name = strings.ToLower(name)
	for _, sensitive := range sensitiveQueryParams {
		if strings.Contains(name, sensitive) {
			return true
		}
	}
	return false
}

// AccessLog logs each completed request in structured format.
//
// enabled=false drops the line entirely, for a deployment that already writes
// per-domain access_log files and does not want the same data a second time
// in the main log.
func AccessLog(log *logger.Logger, enabled bool) Middleware {
	if !enabled {
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Wrap response writer to capture status + bytes
			rw := router.NewResponseWriter(w)

			next.ServeHTTP(rw, r)

			duration := time.Since(start)

			fields := []any{
				"method", r.Method,
				"host", r.Host,
				"uri", sanitizeURI(r), // redact sensitive query params
				"status", rw.StatusCode(),
				"bytes", rw.BytesWritten(),
				"duration_ms", duration.Milliseconds(),
				"ttfb_ms", rw.TTFB().Milliseconds(),
				"remote", clientIP(nil, r),
				"user_agent", r.Header.Get("User-Agent"),
				"request_id", w.Header().Get("X-Request-ID"),
			}
			if tp := r.Header.Get("Traceparent"); tp != "" {
				fields = append(fields, "traceparent", tp)
			}
			if ref := r.Referer(); ref != "" {
				// Also redact sensitive info from Referer
				ref = redactReferer(ref)
				fields = append(fields, "referer", ref)
			}
			// The level follows the status. Every request used to be Info,
			// so lowering global.log_level to quieten the stream also hid the
			// failures — the one part worth keeping. A 5xx is the server
			// breaking; everything else is telemetry.
			//
			// 4xx deliberately stays Info: scanner 404s are constant on any
			// public site and would put the noise straight back into warn.
			// Blocked requests are already reported at Warn by the security
			// and WAF guards.
			if rw.StatusCode() >= 500 {
				log.Error("request", fields...)
				return
			}
			log.Info("request", fields...)
		})
	}
}

// redactReferer redacts sensitive query params from the Referer header.
func redactReferer(ref string) string {
	if !strings.Contains(ref, "?") {
		return ref
	}

	parts := strings.SplitN(ref, "?", 2)
	if len(parts) != 2 {
		return ref
	}

	query, err := url.ParseQuery(parts[1])
	if err != nil {
		return parts[0] + "?[REDACTED]"
	}

	redacted := false
	for k := range query {
		if isSensitiveQueryParam(k) {
			query.Set(k, "[REDACTED]")
			redacted = true
		}
	}

	if !redacted {
		return ref
	}

	return parts[0] + "?" + query.Encode()
}

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

// sensitiveHeaders are header names that contain credentials.
var sensitiveHeaders = []string{
	"Authorization", "Cookie", "X-TOTP-Code", "X-API-Key",
	"X-Session-Token", "Proxy-Authorization",
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

// sanitizeHeader returns "[REDACTED]" if the header is sensitive, otherwise the value.
func sanitizeHeader(name, value string) string {
	name = strings.ToLower(name)
	for _, sensitive := range sensitiveHeaders {
		if strings.ToLower(sensitive) == name {
			return "[REDACTED]"
		}
	}
	return value
}

// AccessLog logs each completed request in structured format.
func AccessLog(log *logger.Logger) Middleware {
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


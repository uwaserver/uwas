// Package respond centralizes JSON HTTP response writing so callers
// don't reach for json.NewEncoder + manual header sets at every
// handler. Refs: refactor.md A10.
//
// The three exported helpers always:
//   - set Content-Type: application/json + standard hardening headers
//     (X-Content-Type-Options, X-Frame-Options, HSTS)
//   - call WriteHeader exactly once with the supplied code
//   - emit a single newline-terminated JSON document
//
// Error and ErrorCause additionally log at error level for 5xx codes,
// using the package logger registered via SetLogger and (if present)
// the X-Request-ID set on the response header by RequestID middleware,
// so operators can correlate the failure without reproducing it.
package respond

import (
	"encoding/json"
	"net/http"
	"sync/atomic"

	"github.com/uwaserver/uwas/internal/logger"
)

// pkgLogger is the logger used for 5xx error logging. Stored via atomic
// pointer so SetLogger is safe to call concurrently with Error/ErrorCause.
// nil-safe: tests that exercise these helpers in isolation don't need
// to wire a logger.
var pkgLogger atomic.Pointer[logger.Logger]

// SetLogger registers the logger used by Error / ErrorCause when they
// emit a 5xx response. Safe to call multiple times; subsequent calls
// replace the previous logger.
func SetLogger(l *logger.Logger) {
	pkgLogger.Store(l)
}

func setBaseHeaders(h http.Header) {
	h.Set("Content-Type", "application/json")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
}

// JSON writes data as application/json with the given status code.
// Always calls WriteHeader(code), so callers must not call WriteHeader
// themselves before invoking JSON.
func JSON(w http.ResponseWriter, code int, data any) {
	setBaseHeaders(w.Header())
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(data)
}

// Error writes a JSON error body {"error": msg} with the given status
// code. For 5xx responses the message is also logged at error level
// with the request ID (if RequestID middleware set X-Request-ID on the
// response header).
func Error(w http.ResponseWriter, code int, msg string) {
	writeError(w, code, msg, nil)
}

// ErrorCause is Error with an explicit underlying error. The cause is
// logged alongside msg for 5xx codes but is never serialized to the
// client — only the sanitized msg goes on the wire.
func ErrorCause(w http.ResponseWriter, code int, msg string, cause error) {
	writeError(w, code, msg, cause)
}

func writeError(w http.ResponseWriter, code int, msg string, cause error) {
	h := w.Header()
	setBaseHeaders(h)
	w.WriteHeader(code)
	reqID := h.Get("X-Request-ID")
	if code >= 500 {
		if l := pkgLogger.Load(); l != nil {
			if cause != nil {
				l.Error("server 5xx", "status", code, "message", msg, "error", cause, "request_id", reqID)
			} else {
				l.Error("server 5xx", "status", code, "message", msg, "request_id", reqID)
			}
		}
	}
	body := map[string]string{"error": msg}
	if reqID != "" {
		body["request_id"] = reqID
	}
	_ = json.NewEncoder(w).Encode(body)
}

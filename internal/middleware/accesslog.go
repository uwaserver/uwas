package middleware

import (
	"net/http"
	"time"

	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/router"
)

// AccessLog logs each completed request in structured format.
func AccessLog(log *logger.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Wrap response writer to capture status + bytes
			rw := router.NewResponseWriter(w)

			next.ServeHTTP(rw, r)

			duration := time.Since(start)

			log.Info("request",
				"method", r.Method,
				"host", r.Host,
				"path", r.URL.Path,
				"status", rw.StatusCode(),
				"bytes", rw.BytesWritten(),
				"duration_ms", duration.Milliseconds(),
				"ttfb_ms", rw.TTFB().Milliseconds(),
				"remote", clientIP(r),
				"user_agent", r.Header.Get("User-Agent"),
				"request_id", w.Header().Get("X-Request-ID"),
			)
		})
	}
}

package middleware

import (
	"fmt"
	"net/http"
	"runtime/debug"

	"github.com/uwaserver/uwas/internal/logger"
)

// Recovery catches panics and returns 500 Internal Server Error.
func Recovery(log *logger.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					stack := string(debug.Stack())
					log.Error("panic recovered",
						"error", fmt.Sprint(err),
						"method", r.Method,
						"path", r.URL.Path,
						"stack", stack,
					)
					http.Error(w, "500 Internal Server Error", http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

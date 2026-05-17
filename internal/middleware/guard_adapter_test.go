package middleware

import (
	"net/http"

	"github.com/uwaserver/uwas/internal/logger"
)

func CORS(cfg CORSConfig) Middleware {
	return middlewareFromGuard(CORSGuard(cfg))
}

func DomainWAF(log *logger.Logger, bypassPaths []string, stats *SecurityStats) Middleware {
	return middlewareFromGuard(DomainWAFGuard(log, bypassPaths, stats))
}

func middlewareFromGuard(guard func(http.ResponseWriter, *http.Request) bool) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if guard(w, r) {
				next.ServeHTTP(w, r)
			}
		})
	}
}

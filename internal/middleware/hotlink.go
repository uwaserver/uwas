package middleware

import (
	"net/http"
	"strings"

	"github.com/uwaserver/uwas/internal/logger"
)

// HotlinkProtection blocks direct linking to resources from unauthorized referers.
func HotlinkProtection(log *logger.Logger, allowedReferers []string, extensions []string) Middleware {
	if len(extensions) == 0 {
		extensions = []string{".jpg", ".jpeg", ".png", ".gif", ".webp", ".avif", ".svg", ".mp4", ".webm", ".mp3", ".zip", ".pdf"}
	}

	extMap := make(map[string]bool, len(extensions))
	for _, ext := range extensions {
		extMap[strings.ToLower(ext)] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Only check protected extensions
			path := strings.ToLower(r.URL.Path)
			isProtected := false
			for ext := range extMap {
				if strings.HasSuffix(path, ext) {
					isProtected = true
					break
				}
			}

			if !isProtected {
				next.ServeHTTP(w, r)
				return
			}

			referer := r.Referer()
			if referer == "" {
				// No referer = direct access, allow (browser address bar, RSS readers, etc.)
				next.ServeHTTP(w, r)
				return
			}

			// Check if referer is from allowed domains
			refLower := strings.ToLower(referer)
			allowed := false
			for _, ref := range allowedReferers {
				if strings.Contains(refLower, strings.ToLower(ref)) {
					allowed = true
					break
				}
			}

			// Always allow same-host referer
			host := strings.ToLower(r.Host)
			if strings.Contains(refLower, host) {
				allowed = true
			}

			if !allowed {
				log.Warn("hotlink blocked", "path", r.URL.Path, "referer", referer, "remote", r.RemoteAddr)
				http.Error(w, "403 Forbidden — hotlinking not allowed", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

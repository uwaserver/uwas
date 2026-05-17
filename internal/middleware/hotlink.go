package middleware

import (
	"net/http"
	"strings"

	"github.com/uwaserver/uwas/internal/logger"
)

// HotlinkGuard blocks direct linking to resources from unauthorized referers.
// It returns true when the request should proceed.
func HotlinkGuard(log *logger.Logger, allowedReferers []string, extensions []string) func(http.ResponseWriter, *http.Request) bool {
	if len(extensions) == 0 {
		extensions = []string{".jpg", ".jpeg", ".png", ".gif", ".webp", ".avif", ".svg", ".mp4", ".webm", ".mp3", ".zip", ".pdf"}
	}

	extMap := make(map[string]bool, len(extensions))
	for _, ext := range extensions {
		extMap[strings.ToLower(ext)] = true
	}

	return func(w http.ResponseWriter, r *http.Request) bool {
		path := strings.ToLower(r.URL.Path)
		isProtected := false
		for ext := range extMap {
			if strings.HasSuffix(path, ext) {
				isProtected = true
				break
			}
		}

		if !isProtected {
			return true
		}

		referer := r.Referer()
		if referer == "" {
			return true
		}

		refLower := strings.ToLower(referer)
		allowed := false
		for _, ref := range allowedReferers {
			if strings.Contains(refLower, strings.ToLower(ref)) {
				allowed = true
				break
			}
		}

		host := strings.ToLower(r.Host)
		if strings.Contains(refLower, host) {
			allowed = true
		}

		if !allowed {
			log.Warn("hotlink blocked", "path", r.URL.Path, "referer", referer, "remote", r.RemoteAddr)
			http.Error(w, "403 Forbidden - hotlinking not allowed", http.StatusForbidden)
			return false
		}

		return true
	}
}

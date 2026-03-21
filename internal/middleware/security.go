package middleware

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/uwaserver/uwas/internal/logger"
)

// Default blocked path patterns.
var defaultBlockedPaths = []string{
	".git", ".svn", ".hg",
	".env", ".env.local", ".env.production",
	"wp-config.php", ".htpasswd", ".htaccess",
	".DS_Store", "Thumbs.db",
	"web.config", "composer.json", "composer.lock",
	"package.json", "package-lock.json",
	".editorconfig", ".gitignore",
}

// Basic WAF patterns.
var wafPatterns = []*regexp.Regexp{
	// SQL injection
	regexp.MustCompile(`(?i)(union\s+select|select\s+.*\s+from|insert\s+into|delete\s+from|drop\s+table|alter\s+table)`),
	regexp.MustCompile(`(?i)(--|;)\s*(drop|alter|delete|insert|update|select)`),
	// XSS
	regexp.MustCompile(`(?i)<script[^>]*>`),
	regexp.MustCompile(`(?i)(javascript|vbscript|data)\s*:`),
	regexp.MustCompile(`(?i)on(error|load|click|mouseover|focus|blur)\s*=`),
	// Path traversal
	regexp.MustCompile(`\.\./`),
	regexp.MustCompile(`\.\.\\`),
}

// SecurityGuard blocks access to sensitive paths and detects basic attacks.
func SecurityGuard(log *logger.Logger, blockedPaths []string, wafEnabled bool) Middleware {
	allBlocked := append(defaultBlockedPaths, blockedPaths...)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path

			// Check blocked paths
			for _, blocked := range allBlocked {
				if strings.Contains(path, blocked) {
					log.Warn("blocked path access",
						"path", path,
						"blocked", blocked,
						"remote", r.RemoteAddr,
					)
					http.Error(w, "403 Forbidden", http.StatusForbidden)
					return
				}
			}

			// WAF checks on path + query
			if wafEnabled {
				fullURI := path
				if r.URL.RawQuery != "" {
					fullURI += "?" + r.URL.RawQuery
				}
				for _, pattern := range wafPatterns {
					if pattern.MatchString(fullURI) {
						log.Warn("WAF blocked request",
							"path", path,
							"pattern", pattern.String(),
							"remote", r.RemoteAddr,
						)
						http.Error(w, "403 Forbidden", http.StatusForbidden)
						return
					}
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

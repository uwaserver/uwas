package middleware

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/uwaserver/uwas/internal/logger"
)

const maxBodyScan = 64 * 1024 // scan first 64KB of request body

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

// WAF patterns — checked against URL, query, and request body.
var wafPatterns = []*regexp.Regexp{
	// SQL injection
	regexp.MustCompile(`(?i)(union\s+select|select\s+.*\s+from|insert\s+into|delete\s+from|drop\s+table|alter\s+table)`),
	regexp.MustCompile(`(?i)(--|;)\s*(drop|alter|delete|insert|update|select)`),
	regexp.MustCompile(`(?i)(sleep\s*\(|benchmark\s*\(|load_file\s*\(|into\s+outfile)`),
	regexp.MustCompile(`(?i)('|")\s*(or|and)\s*('|"|\d)`),
	// XSS
	regexp.MustCompile(`(?i)<script[^>]*>`),
	regexp.MustCompile(`(?i)(javascript|vbscript|data)\s*:`),
	regexp.MustCompile(`(?i)on(error|load|click|mouseover|focus|blur|submit|change|input)\s*=`),
	regexp.MustCompile(`(?i)<(iframe|object|embed|applet|form|base|link|meta|svg)\b`),
	// Path traversal
	regexp.MustCompile(`\.\./`),
	regexp.MustCompile(`\.\.\\`),
	// Shell injection
	regexp.MustCompile("(?i)(;|\\||`|\\$\\(|\\$\\{)\\s*(cat|ls|rm|wget|curl|nc|bash|sh|python|perl|ruby|php)"),
	regexp.MustCompile(`(?i)/etc/(passwd|shadow|hosts)`),
	regexp.MustCompile(`(?i)/proc/self/`),
	// PHP specific
	regexp.MustCompile(`(?i)(eval|assert|system|exec|passthru|shell_exec|popen)\s*\(`),
	regexp.MustCompile(`(?i)php://(input|filter|data)`),
}

// SecurityGuard blocks access to sensitive paths and detects attacks in URL, query, and body.
func SecurityGuard(log *logger.Logger, blockedPaths []string, wafEnabled bool, stats *SecurityStats) Middleware {
	allBlocked := append(defaultBlockedPaths, blockedPaths...)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path

			// Check blocked paths
			for _, blocked := range allBlocked {
				if strings.Contains(path, blocked) {
					if stats != nil {
						stats.Record(r.RemoteAddr, path, "waf", r.UserAgent())
					}
					log.Warn("blocked path access",
						"path", path,
						"blocked", blocked,
						"remote", r.RemoteAddr,
					)
					http.Error(w, "403 Forbidden", http.StatusForbidden)
					return
				}
			}

			// WAF checks
			if wafEnabled {
				// Check URL + query
				fullURI := path
				if r.URL.RawQuery != "" {
					fullURI += "?" + r.URL.RawQuery
				}
				decodedURI, _ := url.QueryUnescape(fullURI)

				if matchWAF(fullURI, decodedURI) {
					if stats != nil {
						stats.Record(r.RemoteAddr, path, "waf", r.UserAgent())
					}
					log.Warn("WAF blocked request (URL)", "path", path, "remote", r.RemoteAddr)
					http.Error(w, "403 Forbidden", http.StatusForbidden)
					return
				}

				// Check request body (POST/PUT/PATCH) — first 64KB
				if r.Body != nil && (r.Method == "POST" || r.Method == "PUT" || r.Method == "PATCH") {
					bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, maxBodyScan))
					if err == nil && len(bodyBytes) > 0 {
						// Restore body for downstream handlers
						r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
						body := string(bodyBytes)
						decodedBody, _ := url.QueryUnescape(body)

						if matchWAF(body, decodedBody) {
							if stats != nil {
								stats.Record(r.RemoteAddr, path, "waf", r.UserAgent())
							}
							log.Warn("WAF blocked request (body)", "path", path, "remote", r.RemoteAddr)
							http.Error(w, "403 Forbidden", http.StatusForbidden)
							return
						}
					}
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

func matchWAF(raw, decoded string) bool {
	for _, pattern := range wafPatterns {
		if pattern.MatchString(raw) || (decoded != raw && pattern.MatchString(decoded)) {
			return true
		}
	}
	return false
}

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

// wafURLPatterns are checked against URL + query string only.
var wafURLPatterns = []*regexp.Regexp{
	// SQL injection
	regexp.MustCompile(`(?i)(union\s+select|insert\s+into|delete\s+from|drop\s+table|alter\s+table)`),
	regexp.MustCompile(`(?i)(--|;)\s+(drop|alter|delete|insert|update)`),
	regexp.MustCompile(`(?i)(sleep\s*\(|benchmark\s*\(|load_file\s*\(|into\s+outfile)`),
	// XSS in URL
	regexp.MustCompile(`(?i)<script[^>]*>`),
	regexp.MustCompile(`(?i)(javascript|vbscript)\s*:`),
	regexp.MustCompile(`(?i)on(error|load|click|mouseover)\s*=`),
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

// wafBodyPatterns are checked against POST body only.
// Intentionally less strict than URL patterns:
//   - No <script> check; CMS editors and email templates submit HTML.
//   - No sleep()/benchmark(); code playgrounds and JS snippets are legitimate.
//   - Only patterns that are almost certainly attacks in form data.
var wafBodyPatterns = []*regexp.Regexp{
	// XSS protocol execution is never legitimate in form data.
	regexp.MustCompile(`(?i)(javascript|vbscript)\s*:\s*[a-z]`),
	// SQL injection multi-word patterns have very low false positive rate.
	regexp.MustCompile(`(?i)(union\s+select|drop\s+table|alter\s+table)`),
	// PHP stream wrappers are never legitimate in form submissions.
	regexp.MustCompile(`(?i)php://(input|filter|data)`),
}

// SecurityGuard blocks access to sensitive paths (global middleware).
func SecurityGuard(log *logger.Logger, blockedPaths []string, stats *SecurityStats) Middleware {
	allBlocked := make([]string, 0, len(defaultBlockedPaths)+len(blockedPaths))
	allBlocked = append(allBlocked, defaultBlockedPaths...)
	allBlocked = append(allBlocked, blockedPaths...)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path

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

			next.ServeHTTP(w, r)
		})
	}
}

// DomainWAFGuard returns a predicate closure for per-domain WAF checks.
// It returns true when the request should proceed.
func DomainWAFGuard(log *logger.Logger, bypassPaths []string, stats *SecurityStats) func(w http.ResponseWriter, r *http.Request) bool {
	return func(w http.ResponseWriter, r *http.Request) bool {
		path := r.URL.Path
		for _, prefix := range bypassPaths {
			if strings.HasPrefix(path, prefix) {
				return true
			}
		}

		fullURI := path
		if r.URL.RawQuery != "" {
			fullURI += "?" + r.URL.RawQuery
		}
		decodedURI, _ := url.QueryUnescape(fullURI)
		if matchWAFURL(fullURI, decodedURI) {
			if stats != nil {
				stats.Record(r.RemoteAddr, path, "waf", r.UserAgent())
			}
			log.Warn("WAF blocked request (URL)", "path", path, "remote", r.RemoteAddr)
			if r.Header.Get("Expect") != "" {
				http.Error(w, "417 Expectation Failed", http.StatusExpectationFailed)
			} else {
				http.Error(w, "403 Forbidden", http.StatusForbidden)
			}
			return false
		}

		if r.Body != nil && (r.Method == "POST" || r.Method == "PUT" || r.Method == "PATCH") {
			ct := r.Header.Get("Content-Type")
			if isAPContentType(ct) {
				return true
			}
			bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, maxBodyScan))
			if err == nil && len(bodyBytes) > 0 {
				r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(bodyBytes), r.Body))
				body := string(bodyBytes)
				decodedBody, _ := url.QueryUnescape(body)
				if matchWAFBody(body, decodedBody) {
					if stats != nil {
						stats.Record(r.RemoteAddr, path, "waf", r.UserAgent())
					}
					log.Warn("WAF blocked request (body)", "path", path, "remote", r.RemoteAddr)
					http.Error(w, "403 Forbidden", http.StatusForbidden)
					return false
				}
			}
		}

		return true
	}
}

// isAPContentType returns true for content types that should skip WAF body scanning.
func isAPContentType(ct string) bool {
	ct = strings.ToLower(ct)
	if idx := strings.Index(ct, ";"); idx != -1 {
		ct = ct[:idx]
	}
	ct = strings.TrimSpace(ct)
	switch ct {
	case "application/json",
		"multipart/form-data",
		"application/xml",
		"text/xml",
		"application/soap+xml",
		"application/x-protobuf",
		"application/octet-stream",
		"application/grpc",
		"application/grpc-web",
		"application/graphql+json":
		return true
	}
	return strings.HasSuffix(ct, "+json") || strings.HasSuffix(ct, "+xml")
}

func matchWAFURL(raw, decoded string) bool {
	for _, pattern := range wafURLPatterns {
		if pattern.MatchString(raw) || (decoded != raw && pattern.MatchString(decoded)) {
			return true
		}
	}
	return false
}

func matchWAFBody(raw, decoded string) bool {
	for _, pattern := range wafBodyPatterns {
		if pattern.MatchString(raw) || (decoded != raw && pattern.MatchString(decoded)) {
			return true
		}
	}
	return false
}

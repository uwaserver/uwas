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

// WAF rule families. security.waf.rules names these; an empty list means all
// of them, which is what the WAF has always done.
const (
	WAFSQLInjection   = "sql_injection"
	WAFXSS            = "xss"
	WAFPathTraversal  = "path_traversal"
	WAFShellInjection = "shell_injection"
	WAFPHP            = "php"
)

// wafFamilies lists every family in a stable order, for reporting.
var wafFamilies = []string{
	WAFSQLInjection, WAFXSS, WAFPathTraversal, WAFShellInjection, WAFPHP,
}

// WAFRuleNames returns every family name, for error messages.
func WAFRuleNames() []string {
	out := make([]string, len(wafFamilies))
	copy(out, wafFamilies)
	return out
}

// KnownWAFRule reports whether a configured rule names a family this WAF has.
func KnownWAFRule(name string) bool {
	for _, f := range wafFamilies {
		if strings.EqualFold(strings.TrimSpace(name), f) {
			return true
		}
	}
	return false
}

type wafRule struct {
	family string
	re     *regexp.Regexp
}

// wafURLPatterns are checked against URL + query string only.
var wafURLPatterns = []wafRule{
	// SQL injection
	{WAFSQLInjection, regexp.MustCompile(`(?i)(union\s+select|insert\s+into|delete\s+from|drop\s+table|alter\s+table)`)},
	{WAFSQLInjection, regexp.MustCompile(`(?i)(--|;)\s+(drop|alter|delete|insert|update)`)},
	{WAFSQLInjection, regexp.MustCompile(`(?i)(sleep\s*\(|benchmark\s*\(|load_file\s*\(|into\s+outfile)`)},
	// XSS in URL
	{WAFXSS, regexp.MustCompile(`(?i)<script[^>]*>`)},
	{WAFXSS, regexp.MustCompile(`(?i)(javascript|vbscript)\s*:`)},
	{WAFXSS, regexp.MustCompile(`(?i)on(error|load|click|mouseover)\s*=`)},
	// Path traversal
	{WAFPathTraversal, regexp.MustCompile(`\.\./`)},
	{WAFPathTraversal, regexp.MustCompile(`\.\.\\`)},
	// Shell injection
	{WAFShellInjection, regexp.MustCompile("(?i)(;|\\||`|\\$\\(|\\$\\{)\\s*(cat|ls|rm|wget|curl|nc|bash|sh|python|perl|ruby|php)")},
	{WAFShellInjection, regexp.MustCompile(`(?i)/etc/(passwd|shadow|hosts)`)},
	{WAFShellInjection, regexp.MustCompile(`(?i)/proc/self/`)},
	// PHP specific
	{WAFPHP, regexp.MustCompile(`(?i)(eval|assert|system|exec|passthru|shell_exec|popen)\s*\(`)},
	{WAFPHP, regexp.MustCompile(`(?i)php://(input|filter|data)`)},
}

// wafBodyPatterns are checked against POST body only.
// Intentionally less strict than URL patterns:
//   - No <script> check; CMS editors and email templates submit HTML.
//   - No sleep()/benchmark(); code playgrounds and JS snippets are legitimate.
//   - Only patterns that are almost certainly attacks in form data.
var wafBodyPatterns = []wafRule{
	// XSS protocol execution is never legitimate in form data.
	{WAFXSS, regexp.MustCompile(`(?i)(javascript|vbscript)\s*:\s*[a-z]`)},
	// SQL injection multi-word patterns have very low false positive rate.
	{WAFSQLInjection, regexp.MustCompile(`(?i)(union\s+select|drop\s+table|alter\s+table)`)},
	// PHP stream wrappers are never legitimate in form submissions.
	{WAFPHP, regexp.MustCompile(`(?i)php://(input|filter|data)`)},
}

// wafFamilySet turns a configured rule list into a lookup. A nil result means
// "every family", which is what an empty list has always meant in practice —
// the list was never read, so every deployment has been getting all of them.
func wafFamilySet(rules []string) map[string]bool {
	if len(rules) == 0 {
		return nil
	}
	set := make(map[string]bool, len(rules))
	for _, r := range rules {
		r = strings.ToLower(strings.TrimSpace(r))
		// Unrecognised names are dropped rather than kept: a set that names
		// only families this WAF does not have matches nothing, and every
		// request would pass. A typo in the rule list must not be a way to
		// turn the WAF off.
		if r != "" && KnownWAFRule(r) {
			set[r] = true
		}
	}
	if len(set) == 0 {
		// Nothing usable was configured. Fall back to every family — the
		// behaviour before the list was read — instead of enforcing none.
		return nil
	}
	return set
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
// rules names the families to enforce; empty means all of them.
//
// security.waf.rules was dead configuration: documented as
// `sql_injection | xss | path_traversal` and never read, so every WAF-enabled
// domain got every family whatever it listed.
func DomainWAFGuard(log *logger.Logger, bypassPaths []string, rules []string, stats *SecurityStats) func(w http.ResponseWriter, r *http.Request) bool {
	families := wafFamilySet(rules)
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
		if matchWAF(wafURLPatterns, families, fullURI, decodedURI) {
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
			// Read up to maxBodyScan for inspection. Reconstruct the body from
			// whatever was consumed REGARDLESS of a read error — gating the
			// MultiReader on err==nil would silently drop the already-consumed
			// prefix on a partial read, handing the downstream handler a
			// truncated (corrupted) POST body.
			bodyBytes, _ := io.ReadAll(io.LimitReader(r.Body, maxBodyScan))
			if len(bodyBytes) > 0 {
				r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(bodyBytes), r.Body))
				body := string(bodyBytes)
				decodedBody, _ := url.QueryUnescape(body)
				if matchWAF(wafBodyPatterns, families, body, decodedBody) {
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

// matchWAF reports whether any enabled rule matches. families nil means every
// family, which is what an empty security.waf.rules has always meant.
func matchWAF(rules []wafRule, families map[string]bool, raw, decoded string) bool {
	for _, r := range rules {
		if families != nil && !families[r.family] {
			continue
		}
		if r.re.MatchString(raw) || (decoded != raw && r.re.MatchString(decoded)) {
			return true
		}
	}
	return false
}

func matchWAFURL(raw, decoded string) bool {
	return matchWAF(wafURLPatterns, nil, raw, decoded)
}

func matchWAFBody(raw, decoded string) bool {
	return matchWAF(wafBodyPatterns, nil, raw, decoded)
}

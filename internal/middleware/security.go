package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/pathmatch"
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
	WAFFileProbe      = "file_probe"
)

// wafFamilies lists every family in a stable order, for reporting.
var wafFamilies = []string{
	WAFSQLInjection, WAFXSS, WAFPathTraversal, WAFShellInjection, WAFPHP, WAFFileProbe,
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

// sqlSep is what SQL accepts between two keywords: whitespace, an inline
// comment, or a MySQL versioned-comment opener ("/*!50000"). Requiring plain
// \s+ let UNION/**/SELECT, UNION ALL SELECT and UNION(SELECT through while
// "union select" was blocked.
const sqlSep = `(?:\s|/\*!\d*|/\*(?s:.*?)\*/)+`

// sqlUnion matches UNION [ALL|DISTINCT] SELECT with any sqlSep (or an opening
// parenthesis) between the words. The trailing \b keeps prose such as
// "European Union (selected members)" from matching.
const sqlUnion = `union(?:\s|\(|/\*!\d*|/\*(?s:.*?)\*/)+(?:(?:all|distinct)(?:\s|\(|/\*!\d*|/\*(?s:.*?)\*/)+)?select\b`

// wafURLPatterns are checked against URL + query string only.
var wafURLPatterns = []wafRule{
	// SQL injection
	{WAFSQLInjection, regexp.MustCompile(`(?i)(` + sqlUnion + `|insert` + sqlSep + `into|delete` + sqlSep + `from|drop` + sqlSep + `table|alter` + sqlSep + `table)`)},
	{WAFSQLInjection, regexp.MustCompile(`(?i)(--|;)\s+(drop|alter|delete|insert|update)`)},
	{WAFSQLInjection, regexp.MustCompile(`(?i)(sleep\s*\(|benchmark\s*\(|load_file\s*\(|into` + sqlSep + `outfile)`)},
	// Boolean tautologies: `' OR '1'='1`, `" or 1=1--`, ` and 2=2`. The rules
	// above are keyword-driven and miss this family completely, which is
	// awkward because it is the first thing every scanner sends and the
	// classic auth bypass.
	//
	// URL patterns only, following this file's split: a query string carrying
	// a quote, OR/AND and an equality is not something a real link contains,
	// while a form body legitimately carries prose that would trip it.
	{WAFSQLInjection, regexp.MustCompile(`(?i)['"]\s*(or|and)\s+['"]?\w{1,12}['"]?\s*=\s*['"]?\w{1,12}`)},
	{WAFSQLInjection, regexp.MustCompile(`(?i)\b(or|and)\s+\d{1,6}\s*=\s*\d{1,6}\b`)},
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
	// Secret/backup file probes: scanners guess names, they do not inject.
	{WAFFileProbe, regexp.MustCompile(`(?i)(^|/)(phpinfo\.php|secrets?\.json|credentials?\.json|config\.(ya?ml|ini)|dump\.sql|backup\.sql|database\.sql|id_rsa(\.pub)?)(\?|$)`)},
	{WAFFileProbe, regexp.MustCompile(`(?i)\.(bak|orig|old|save|swp|swo|php\.txt)(\?|$)`)},
	{WAFFileProbe, regexp.MustCompile(`(?i)(~|%7e)(\?|$)`)},
	{WAFFileProbe, regexp.MustCompile(`(?i)(%20| )copy\.[^/?]+`)},
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
	{WAFSQLInjection, regexp.MustCompile(`(?i)(` + sqlUnion + `|drop` + sqlSep + `table|alter` + sqlSep + `table)`)},
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

// pathSegmentContains reports whether segment appears inside path as a
// whole path component — as /segment, segment/, or /segment/ — and not as
// a substring of a longer name.  For example, ".git" matches "/.git/",
// "/.git/HEAD", and "/.gitignore" (a file named .gitignore, same segment)
// but does NOT match "/v2/my-git-config.txt" or "/api/.github/workflows".
func pathSegmentContains(path, segment string) bool {
	return strings.HasPrefix(path, segment+"/") ||
		strings.HasSuffix(path, "/"+segment) ||
		strings.Contains(path, "/"+segment+"/") ||
		path == segment
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
				if pathSegmentContains(path, blocked) {
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
		// The exemption is decided on the canonical path: "/wp-admin/../x"
		// starts with an exempt prefix but is served as "/x" (F2501).
		canonical := pathmatch.Clean(path)
		for _, prefix := range bypassPaths {
			if strings.HasPrefix(canonical, prefix) {
				return true
			}
		}

		fullURI := path
		if r.URL.RawQuery != "" {
			fullURI += "?" + r.URL.RawQuery
		}
		decodedURI := wafUnescape(fullURI)
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
				if isJSONContentType(ct) {
					if scanJSONBody(bodyBytes, families) {
						if stats != nil {
							stats.Record(r.RemoteAddr, path, "waf", r.UserAgent())
						}
						log.Warn("WAF blocked request (body)", "path", path, "remote", r.RemoteAddr)
						http.Error(w, "403 Forbidden", http.StatusForbidden)
						return false
					}
				} else if isMultipartContentType(ct) {
					if scanMultipartBody(bodyBytes, ct, families) {
						if stats != nil {
							stats.Record(r.RemoteAddr, path, "waf", r.UserAgent())
						}
						log.Warn("WAF blocked request (body)", "path", path, "remote", r.RemoteAddr)
						http.Error(w, "403 Forbidden", http.StatusForbidden)
						return false
					}
				} else {
					body := string(bodyBytes)
					decodedBody := wafUnescape(body)
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
	case "application/xml",
		"text/xml",
		"application/soap+xml",
		"application/x-protobuf",
		"application/octet-stream",
		"application/grpc",
		"application/grpc-web",
		"application/graphql+json":
		return true
	}
	return strings.HasSuffix(ct, "+xml")
}

// isJSONContentType reports whether ct is application/json or a +json suffix type.
func isJSONContentType(ct string) bool {
	ct = strings.ToLower(ct)
	if idx := strings.Index(ct, ";"); idx != -1 {
		ct = ct[:idx]
	}
	return strings.TrimSpace(ct) == "application/json" || strings.HasSuffix(ct, "+json")
}

// isMultipartContentType reports whether ct is multipart/form-data.
func isMultipartContentType(ct string) bool {
	ct = strings.ToLower(ct)
	if idx := strings.Index(ct, ";"); idx != -1 {
		ct = ct[:idx]
	}
	return strings.TrimSpace(ct) == "multipart/form-data"
}

// scanJSONBody recursively extracts every JSON string value from bodyBytes
// and checks each against the WAF patterns. Returns true if any value is blocked.
func scanJSONBody(bodyBytes []byte, families map[string]bool) bool {
	var value interface{}
	if err := json.Unmarshal(bodyBytes, &value); err != nil {
		// Failing to parse must not mean "not an attack". The guard only reads
		// the first maxBodyScan bytes (see DomainWAFGuard), so any JSON body
		// larger than that arrives here TRUNCATED mid-structure and can never
		// unmarshal — returning false made every oversized JSON body skip the
		// WAF entirely. Falling back to a raw scan of the bytes we do have
		// matches what the non-JSON sibling branch already does, so a body is
		// never silently exempt because of its size.
		//
		// The raw bytes still carry JSON string escapes ("union\u0020select",
		// "php:\/\/input") that every downstream JSON parser decodes, so the
		// fallback also checks a leniently unescaped view of them.
		raw := string(bodyBytes)
		if matchWAF(wafBodyPatterns, families, raw, "") {
			return true
		}
		unesc := jsonUnescapeLenient(raw)
		return matchWAF(wafBodyPatterns, families, unesc, wafUnescape(unesc))
	}
	return scanJSONValue(value, families)
}

// jsonUnescapeLenient decodes JSON string escapes (\uXXXX including surrogate
// pairs, \/, \", \\, \b, \f, \n, \r, \t) wherever they appear in s and
// keeps anything malformed as literal text. It works on a truncated document,
// which json.Unmarshal cannot.
func jsonUnescapeLenient(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' || i+1 >= len(s) {
			b.WriteByte(c)
			continue
		}
		switch e := s[i+1]; e {
		case '"', '\\', '/':
			b.WriteByte(e)
			i++
		case 'b':
			b.WriteByte('\b')
			i++
		case 'f':
			b.WriteByte('\f')
			i++
		case 'n':
			b.WriteByte('\n')
			i++
		case 'r':
			b.WriteByte('\r')
			i++
		case 't':
			b.WriteByte('\t')
			i++
		case 'u':
			r1, ok := jsonHex4(s, i+2)
			if !ok {
				b.WriteByte(c)
				continue
			}
			i += 5
			r := rune(r1)
			if utf16.IsSurrogate(r) {
				if r2, ok2 := jsonHex4(s, i+3); ok2 && s[i+1] == '\\' && s[i+2] == 'u' {
					if d := utf16.DecodeRune(r, rune(r2)); d != utf8.RuneError {
						r = d
						i += 6
					}
				}
			}
			b.WriteRune(r)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// jsonHex4 parses the four hex digits of a \u escape starting at s[at].
func jsonHex4(s string, at int) (uint16, bool) {
	if at < 0 || at+4 > len(s) {
		return 0, false
	}
	v, err := strconv.ParseUint(s[at:at+4], 16, 16)
	if err != nil {
		return 0, false
	}
	return uint16(v), true
}

// scanJSONValue dispatches a decoded JSON value for WAF checking.
func scanJSONValue(v interface{}, families map[string]bool) bool {
	switch val := v.(type) {
	case string:
		decoded := wafUnescape(val)
		if matchWAF(wafBodyPatterns, families, val, decoded) {
			return true
		}
	case map[string]interface{}:
		for _, sub := range val {
			if scanJSONValue(sub, families) {
				return true
			}
		}
	case []interface{}:
		for _, sub := range val {
			if scanJSONValue(sub, families) {
				return true
			}
		}
	}
	return false
}

// scanMultipartBody extracts every form-data field value from bodyBytes
// (using the boundary from ct) and checks each against the WAF patterns.
// Returns true if any field value is blocked.
func scanMultipartBody(bodyBytes []byte, ct string, families map[string]bool) bool {
	boundary := multipartBoundary(ct)
	if boundary == "" {
		// No usable boundary: scan the raw body rather than skip it.
		return scanRawBody(bodyBytes, families)
	}
	mr := multipart.NewReader(bytes.NewReader(bodyBytes), boundary)
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Malformed or cut off at the scan window: backends may still
			// parse what follows, so fall back to the raw bytes.
			return scanRawBody(bodyBytes, families)
		}
		// Only scan form-data fields, not file content with Content-Disposition.
		if part.FormName() == "" {
			continue
		}
		// A value cut off by the scan window still gets its read prefix
		// checked; discarding it on the read error exempted any payload
		// placed at the start of a field longer than maxBodyScan.
		value, _ := io.ReadAll(io.LimitReader(part, maxBodyScan))
		s := string(value)
		decoded := wafUnescape(s)
		if matchWAF(wafBodyPatterns, families, s, decoded) {
			return true
		}
	}
	return false
}

// multipartBoundary returns the boundary parameter of ct. When strict MIME
// parsing rejects the header (a stray parameter without "=", duplicates), it
// falls back to the first "boundary=" value, which is what lenient backends
// such as PHP use to split the body.
func multipartBoundary(ct string) string {
	if _, params, err := mime.ParseMediaType(ct); err == nil {
		return params["boundary"]
	}
	i := strings.Index(strings.ToLower(ct), "boundary=")
	if i < 0 {
		return ""
	}
	b := ct[i+len("boundary="):]
	if j := strings.IndexAny(b, "; \t,"); j >= 0 {
		b = b[:j]
	}
	return strings.Trim(b, `"`)
}

func scanRawBody(bodyBytes []byte, families map[string]bool) bool {
	body := string(bodyBytes)
	return matchWAF(wafBodyPatterns, families, body, wafUnescape(body))
}

// wafUnescape decodes %XX and '+' like url.QueryUnescape, but keeps a
// malformed escape as literal text instead of failing. QueryUnescape returns
// "" on the first bad escape, which left only the still-encoded input to
// match: one stray "%zz" anywhere in a query or body exempted every encoded
// payload next to it, while lenient decoders downstream (PHP's urldecode)
// still decode the rest.
func wafUnescape(s string) string {
	if v, err := url.QueryUnescape(s); err == nil {
		return v
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '+':
			b.WriteByte(' ')
		case c == '%' && i+2 < len(s) && isHexDigit(s[i+1]) && isHexDigit(s[i+2]):
			b.WriteByte(unhexDigit(s[i+1])<<4 | unhexDigit(s[i+2]))
			i += 2
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func isHexDigit(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

func unhexDigit(c byte) byte {
	switch {
	case '0' <= c && c <= '9':
		return c - '0'
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10
	}
	return c - 'A' + 10
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

package middleware

import (
	"bufio"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
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
	// Lock files and tool config that sit next to the ones above: the same
	// dependency inventory, listed under another package manager (F49001).
	"yarn.lock", "pnpm-lock.yaml", "npm-shrinkwrap.json",
	// A php.ini left in the docroot and PHP's default error log (cPanel and
	// Plesk write error_log into the script's directory): configuration and
	// absolute paths, never content (F49001).
	"php.ini", "error_log",
	// docs/SPECIFICATION.md §16.2 blocks node_modules/ by default (F49000).
	// vendor/ is handled by isComposerVendorPath: a front-end theme keeps its
	// JavaScript in /vendor/ at the root, so blocking the name would break it.
	"node_modules",
	".editorconfig", ".gitignore",
}

// isComposerVendorPath reports whether path points into a Composer vendor
// directory. §16.2 blocks vendor/, but "vendor" is also where Bootstrap-style
// static templates (/vendor/bootstrap/, /vendor/jquery/) and theme assets
// (/assets/vendor/) keep their JavaScript, so the name alone is not enough.
// What identifies the PHP one is what Composer puts there: autoload.php,
// composer/ (installed.json lists every dependency and version), bin/ and
// phpunit/ (eval-stdin.php, CVE-2017-9841, executes the request body). The
// first two are also the paths scanners probe (F49000).
func isComposerVendorPath(path string) bool {
	segs := strings.Split(path, "/")
	for i, seg := range segs {
		if !strings.EqualFold(seg, "vendor") || i+1 >= len(segs) {
			continue
		}
		next := strings.ToLower(segs[i+1])
		switch {
		case next == "composer", next == "phpunit", next == "bin":
			return true
		case strings.HasPrefix(next, "autoload") && strings.HasSuffix(next, ".php"):
			return true
		}
	}
	return false
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

// sqlSepAlt is what SQL accepts between two keywords: whitespace (a vertical
// tab included, which RE2's \s leaves out although MySQL and SQLite skip it),
// an inline comment, a MySQL versioned-comment opener ("/*!50000"), or a "#" or
// "--" comment that runs to the end of the line. Requiring plain \s+ let
// UNION/**/SELECT, UNION ALL SELECT and UNION(SELECT through while "union
// select" was blocked, and the line comments (F18701) did the same.
const sqlSepAlt = `\s|\x0b|/\*!\d*|/\*(?s:.*?)\*/|#[^\n]*\n|--[^\n]*\n`

const sqlSep = `(?:` + sqlSepAlt + `)+`

// sqlWS is a single whitespace character as SQL sees it.
const sqlWS = `[\s\x0b]`

// sqlUnion matches UNION [ALL|DISTINCT] SELECT with any sqlSep (or an opening
// parenthesis) between the words. The trailing \b keeps prose such as
// "European Union (selected members)" from matching.
const sqlUnion = `union(?:` + sqlSepAlt + `|\()+(?:(?:all|distinct)(?:` + sqlSepAlt + `|\()+)?select\b`

// wafURLPatterns are checked against URL + query string only.
var wafURLPatterns = []wafRule{
	// SQL injection
	{WAFSQLInjection, regexp.MustCompile(`(?i)(` + sqlUnion + `|insert` + sqlSep + `into|delete` + sqlSep + `from|drop` + sqlSep + `table|alter` + sqlSep + `table)`)},
	{WAFSQLInjection, regexp.MustCompile(`(?i)(--|;)` + sqlWS + `+(drop|alter|delete|insert|update)`)},
	{WAFSQLInjection, regexp.MustCompile(`(?i)(sleep` + sqlWS + `*\(|benchmark` + sqlWS + `*\(|load_file` + sqlWS + `*\(|into` + sqlSep + `outfile)`)},
	// Boolean tautologies: `' OR '1'='1`, `" or 1=1--`, ` and 2=2`. The rules
	// above are keyword-driven and miss this family completely, which is
	// awkward because it is the first thing every scanner sends and the
	// classic auth bypass.
	//
	// URL patterns only, following this file's split: a query string carrying
	// a quote, OR/AND and an equality is not something a real link contains,
	// while a form body legitimately carries prose that would trip it.
	{WAFSQLInjection, regexp.MustCompile(`(?i)['"]` + sqlWS + `*(or|and)` + sqlSep + `['"]?\w{1,12}['"]?` + sqlWS + `*=` + sqlWS + `*['"]?\w{1,12}`)},
	{WAFSQLInjection, regexp.MustCompile(`(?i)\b(or|and)` + sqlSep + `\d{1,6}` + sqlWS + `*=` + sqlWS + `*\d{1,6}\b`)},
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

// isWPConfigCopy reports whether the last component of path names a copy of
// wp-config.php (wp-config.php.bak, wp-config.php~, wp-config.old ...). The
// exact name is on the default list; the file handler serves a copy as plain
// text, database credentials included (F9610). Backups of other files are the
// WAF's file_probe family and stay out of this always-on guard.
func isWPConfigCopy(path string) bool {
	name := path
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		name = path[i+1:]
	}
	name = strings.ToLower(name)
	if strings.HasPrefix(name, "wp-config.php") {
		return name != "wp-config.php"
	}
	switch strings.TrimPrefix(name, "wp-config.") {
	case "bak", "old", "orig", "save", "swp", "swo", "txt", "backup", "copy":
		return strings.HasPrefix(name, "wp-config.")
	}
	return false
}

// SecurityGuard blocks access to sensitive paths (global middleware).
func SecurityGuard(log *logger.Logger, blockedPaths []string, stats *SecurityStats) Middleware {
	allBlocked := make([]string, 0, len(defaultBlockedPaths)+len(blockedPaths))
	allBlocked = append(allBlocked, defaultBlockedPaths...)
	allBlocked = append(allBlocked, blockedPaths...)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path

			if isWPConfigCopy(path) {
				if stats != nil {
					stats.Record(r.RemoteAddr, path, "waf", r.UserAgent())
				}
				log.Warn("blocked path access",
					"path", path,
					"blocked", "wp-config copy",
					"remote", r.RemoteAddr,
				)
				http.Error(w, "403 Forbidden", http.StatusForbidden)
				return
			}

			// The static handler opens files through the cleaned path, so a
			// name hidden behind dot segments reaches the same file; decide
			// on both forms (F49000).
			clean := pathmatch.Clean(path)
			if isComposerVendorPath(clean) {
				if stats != nil {
					stats.Record(r.RemoteAddr, path, "waf", r.UserAgent())
				}
				log.Warn("blocked path access",
					"path", path,
					"blocked", "composer vendor",
					"remote", r.RemoteAddr,
				)
				http.Error(w, "403 Forbidden", http.StatusForbidden)
				return
			}
			for _, blocked := range allBlocked {
				if pathSegmentContains(path, blocked) || (clean != path && pathSegmentContains(clean, blocked)) {
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

		// Every method can carry a body the application reads (DELETE with a
		// JSON body, GET bodies in Express and friends); gating on
		// POST/PUT/PATCH left those unscanned (F10001).
		if r.Body != nil && r.Body != http.NoBody {
			ct := r.Header.Get("Content-Type")
			asJSON := isJSONContentType(ct)
			if isAPContentType(ct) {
				// An XML, binary or streaming body is not read ahead: a gRPC
				// client waits for the response with its stream still open, so
				// waiting for 64 KiB would stall it. But the type is the
				// client's word, and an application that reads the raw body
				// and decodes JSON ignores it, so a body that opens as JSON is
				// scanned as JSON whatever it is labelled (F16000). Real XML
				// (it opens with '<') and binary bodies still pass.
				var jsonStart bool
				if enc := strings.Join(r.Header.Values("Content-Encoding"), ","); isEncodedBody(enc) {
					// A compressed body opens with the codec's magic bytes, so the
					// sniff has to look at the inflated text (F18702).
					head, _ := io.ReadAll(io.LimitReader(r.Body, maxBodyScan))
					r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(head), r.Body))
					jsonStart = opensAsJSON(decodeBodyForScan(enc, head))
				} else {
					r.Body, jsonStart = sniffJSONStart(r.Body)
				}
				if !jsonStart {
					return true
				}
				asJSON = true
			}
			// Read up to maxBodyScan for inspection. Reconstruct the body from
			// whatever was consumed REGARDLESS of a read error — gating the
			// MultiReader on err==nil would silently drop the already-consumed
			// prefix on a partial read, handing the downstream handler a
			// truncated (corrupted) POST body.
			bodyBytes, _ := io.ReadAll(io.LimitReader(r.Body, maxBodyScan))
			if len(bodyBytes) > 0 {
				r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(bodyBytes), r.Body))
				// The downstream body stays as sent; only the scan sees the
				// decoded view, because upstreams such as Express inflate a
				// Content-Encoding: gzip body before parsing it (F10000).
				bodyBytes = decodeBodyForScan(strings.Join(r.Header.Values("Content-Encoding"), ","), bodyBytes)
				if asJSON {
					if scanJSONBody(bodyBytes, families) || scanWideJSON(bodyBytes, families) {
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
					// A JSON document labelled text/plain, a form type or
					// nothing is still decoded by an application that reads the
					// raw body, and the text scan does not see JSON string
					// escapes ("\u0055NION"): judge it as the JSON it is (F16001).
					if matchWAF(wafBodyPatterns, families, body, decodedBody) ||
						(opensAsJSON(bodyBytes) && scanJSONBody(bodyBytes, families)) ||
						scanWideJSON(bodyBytes, families) {
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

// isEncodedBody reports whether a Content-Encoding value names a coding other
// than identity.
func isEncodedBody(encoding string) bool {
	for _, l := range strings.Split(strings.ToLower(encoding), ",") {
		if l = strings.TrimSpace(l); l != "" && l != "identity" {
			return true
		}
	}
	return false
}

// decodeBodyForScan undoes the request Content-Encoding (gzip, x-gzip, deflate,
// in the reverse of the listed order) so the WAF sees what a decoding backend
// will parse. The output of each layer is capped at maxBodyScan, which also
// bounds a decompression bomb, and a truncated stream keeps what it produced.
// An unsupported coding (br, zstd) or a stream that yields nothing leaves the
// bytes as they are: they cannot be inspected, and the raw scan still runs.
func decodeBodyForScan(encoding string, body []byte) []byte {
	if strings.TrimSpace(encoding) == "" {
		return body
	}
	layers := strings.Split(strings.ToLower(encoding), ",")
	for i := len(layers) - 1; i >= 0; i-- {
		var zr io.Reader
		switch strings.TrimSpace(layers[i]) {
		case "", "identity":
			continue
		case "gzip", "x-gzip":
			gr, err := gzip.NewReader(bytes.NewReader(body))
			if err != nil {
				return body
			}
			zr = gr
		case "deflate":
			// "deflate" is zlib-wrapped in the spec and raw in some clients.
			if zl, err := zlib.NewReader(bytes.NewReader(body)); err == nil {
				zr = zl
			} else {
				zr = flate.NewReader(bytes.NewReader(body))
			}
		default:
			return body
		}
		out, _ := io.ReadAll(io.LimitReader(zr, maxBodyScan))
		if len(out) == 0 {
			return body
		}
		body = out
	}
	return body
}

// isAPContentType returns true for content types that should skip WAF body
// scanning: XML payloads (SOAP, XML-RPC, webhooks that legitimately carry SQL
// text), protobuf, gRPC and raw binary. The type is the client's word, so a body
// that opens as JSON is scanned whatever it is labelled (F16000); see
// sniffJSONStart. Real XML and binary bodies still pass unscanned.
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

// opensAsJSON reports whether b starts, after whitespace, with a JSON object or
// array. Only the first jsonSniffLimit bytes are looked at.
func opensAsJSON(b []byte) bool {
	for i := 0; i < len(b) && i < jsonSniffLimit; i++ {
		switch b[i] {
		case ' ', '\t', '\r', '\n':
			continue
		case '{', '[':
			return true
		default:
			return false
		}
	}
	return false
}

// jsonSniffLimit bounds how many leading bytes sniffJSONStart looks at.
const jsonSniffLimit = 64

// sniffJSONStart reports whether a body opens as a JSON object or array (its
// first non-whitespace byte is '{' or '['), waiting for no more bytes than it
// needs to decide: a gRPC frame starts with a 0x00 or 0x01 flag and a protobuf
// message with a field tag, so a streaming client is never held up. The
// returned body replays every byte the sniff looked at.
func sniffJSONStart(body io.ReadCloser) (io.ReadCloser, bool) {
	br := bufio.NewReaderSize(body, 4096)
	replay := struct {
		io.Reader
		io.Closer
	}{br, body}
	for n := 1; n <= jsonSniffLimit; n++ {
		b, _ := br.Peek(n)
		if len(b) < n {
			return replay, false
		}
		switch b[n-1] {
		case ' ', '\t', '\r', '\n':
			continue
		case '{', '[':
			return replay, true
		default:
			return replay, false
		}
	}
	return replay, false
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
	if !json.Valid(bodyBytes) {
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
	// Every string token is inspected rather than the unmarshalled tree: a map
	// keeps only the last of a duplicated key, and parsers disagree on which
	// duplicate wins (F15702).
	return scanJSONStrings(bodyBytes, families)
}

// scanJSONStrings checks every string token of a JSON document, object keys
// and the values a duplicated key shadows included. A document that stops
// parsing partway keeps the strings read up to that point.
func scanJSONStrings(b []byte, families map[string]bool) bool {
	dec := json.NewDecoder(bytes.NewReader(b))
	for {
		tok, err := dec.Token()
		if err != nil {
			return false
		}
		if s, ok := tok.(string); ok && matchWAF(wafBodyPatterns, families, s, wafUnescape(s)) {
			return true
		}
	}
}

// scanWideJSON scans a JSON text that is encoded as UTF-16 or UTF-32. JSON
// parsers such as Python's json.loads and Jackson detect the encoding from the
// first bytes (RFC 4627 section 3), and Node's body-parser honours a utf-16
// charset, so they read a payload the byte-wise scan sees only as characters
// separated by NULs (F15701). Plain UTF-8 returns false at once.
func scanWideJSON(b []byte, families map[string]bool) bool {
	dec, ok := decodeWideJSON(b)
	if !ok {
		return false
	}
	return scanJSONBody(dec, families)
}

// decodeWideJSON converts a UTF-16 or UTF-32 JSON text to UTF-8, detecting the
// encoding from a byte order mark or from where the NULs fall among the first
// four bytes (a JSON text starts with an ASCII character).
func decodeWideJSON(b []byte) ([]byte, bool) {
	if len(b) < 2 {
		return nil, false
	}
	order := func(hi, lo byte) bool { return hi == 0 && lo != 0 }
	var width int
	var bigEndian bool
	switch {
	case len(b) >= 4 && b[0] == 0 && b[1] == 0 && b[2] == 0xFE && b[3] == 0xFF:
		width, bigEndian, b = 4, true, b[4:]
	case len(b) >= 4 && b[0] == 0xFF && b[1] == 0xFE && b[2] == 0 && b[3] == 0:
		width, bigEndian, b = 4, false, b[4:]
	case b[0] == 0xFE && b[1] == 0xFF:
		width, bigEndian, b = 2, true, b[2:]
	case b[0] == 0xFF && b[1] == 0xFE:
		width, bigEndian, b = 2, false, b[2:]
	case len(b) >= 4 && b[0] == 0 && b[1] == 0 && b[2] == 0 && b[3] != 0:
		width, bigEndian = 4, true
	case len(b) >= 4 && b[0] != 0 && b[1] == 0 && b[2] == 0 && b[3] == 0:
		width, bigEndian = 4, false
	case len(b) >= 4 && order(b[0], b[1]) && order(b[2], b[3]):
		width, bigEndian = 2, true
	case len(b) >= 4 && b[0] != 0 && b[1] == 0 && b[2] != 0 && b[3] == 0:
		width, bigEndian = 2, false
	case len(b) == 2 && order(b[0], b[1]):
		width, bigEndian = 2, true
	case len(b) == 2 && b[0] != 0 && b[1] == 0:
		width, bigEndian = 2, false
	default:
		return nil, false
	}
	out := make([]byte, 0, len(b)/width+1)
	if width == 4 {
		for i := 0; i+4 <= len(b); i += 4 {
			var r uint32
			if bigEndian {
				r = uint32(b[i])<<24 | uint32(b[i+1])<<16 | uint32(b[i+2])<<8 | uint32(b[i+3])
			} else {
				r = uint32(b[i+3])<<24 | uint32(b[i+2])<<16 | uint32(b[i+1])<<8 | uint32(b[i])
			}
			out = utf8.AppendRune(out, rune(r))
		}
		return out, true
	}
	units := make([]uint16, 0, len(b)/2)
	for i := 0; i+2 <= len(b); i += 2 {
		if bigEndian {
			units = append(units, uint16(b[i])<<8|uint16(b[i+1]))
		} else {
			units = append(units, uint16(b[i+1])<<8|uint16(b[i]))
		}
	}
	for _, r := range utf16.Decode(units) {
		out = utf8.AppendRune(out, r)
	}
	return out, true
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
		// A part without a usable form-data name is still a field to lenient
		// parsers (another disposition type, a missing name), so its value is
		// scanned like any other (F18703).
		name := part.FormName()
		// The field name is request data the application reads like a
		// urlencoded key, which the non-multipart branch already scans.
		if name != "" && matchWAF(wafBodyPatterns, families, name, wafUnescape(name)) {
			return true
		}
		// The upload's filename is request data too ($_FILES[..]['name']).
		if fn := part.FileName(); fn != "" && matchWAF(wafBodyPatterns, families, fn, wafUnescape(fn)) {
			return true
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

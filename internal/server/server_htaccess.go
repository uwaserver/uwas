package server

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/handler/static"
	"github.com/uwaserver/uwas/internal/pathmatch"
	"github.com/uwaserver/uwas/internal/phpmanager"
	"github.com/uwaserver/uwas/internal/rewrite"
	"github.com/uwaserver/uwas/internal/router"
	"github.com/uwaserver/uwas/pkg/htaccess"
)

func (s *Server) applyRewrites(ctx *router.RequestContext, domain *config.Domain) bool {
	engine := s.rewriteEngineFor(domain.Host)
	if engine == nil {
		return false
	}

	// Cheap pre-check: skip the Variables allocation + Process loop when
	// no rule pattern can match this URI. Big win for domains with
	// rewrites configured but most paths uninteresting (the WP-Admin /
	// rest of-site split). Refs: refactor.md P12.
	//
	// Rules decide on the canonical path: the static handler opens files
	// through filepath.Clean, so "//private/x" reaches the same file as
	// "/private/x" and must meet the same ^/private/ [F] rule (F2532).
	reqPath := pathmatch.Clean(ctx.Request.URL.Path)
	if !engine.MightMatch(reqPath) {
		return false
	}

	vars := rewrite.BuildVariables(ctx.Request, domain.Root, ctx.ResolvedPath, ctx.IsHTTPS)
	result := engine.Process(reqPath, ctx.Request.URL.RawQuery, vars)

	if result.Forbidden {
		s.renderDomainErrorFor(ctx.Request, ctx.Response, http.StatusForbidden, domain)
		return true
	}
	if result.Gone {
		s.renderDomainErrorFor(ctx.Request, ctx.Response, http.StatusGone, domain)
		return true
	}
	if result.Redirect {
		http.Redirect(ctx.Response, ctx.Request, result.URI, result.StatusCode)
		return true
	}
	if result.Modified {
		ctx.Request.URL.Path = result.URI
		if result.Query != "" {
			ctx.Request.URL.RawQuery = result.Query
		}
		ctx.RewrittenURI = result.URI
	}
	return false
}

// applyHtaccess reads and applies .htaccess rewrite rules from the document root.
// Parsed rules are cached per domain root and invalidated on config reload.
// Returns true when the request was fully handled (forbidden/gone/redirect) and
// the caller must stop dispatch — mirroring applyRewrites for the YAML path.
// internalRewrite=false still honors [F]/[G]/[R] results but leaves the
// request path unchanged.
func (s *Server) applyHtaccess(ctx *router.RequestContext, domain *config.Domain, internalRewrite bool) bool {
	if s.applyRootHtaccess(ctx, domain, internalRewrite) {
		return true
	}
	if s.applySubdirHtaccess(ctx, domain, internalRewrite) {
		return true
	}
	s.applyHtaccessLevels(ctx, domain)
	return false
}

// htaccessLevel is a sub-directory's .htaccess entry with the URL path of the
// directory it sits in ("/uploads/").
type htaccessLevel struct {
	prefix string
	entry  *htaccessCacheEntry
}

// forEachExistingDir calls fn for every directory below root on the way to rel
// (a relative path), outermost first, and stops at the first one that is not an
// existing directory. A directory that does not exist has no .htaccess and
// neither does anything below it, so nothing is lost, and the walk (and
// getHtaccessRuleSet's cache) is bounded by the depth of the real tree instead
// of by the length of the URL: a 40 KB path of nonexistent segments cost 4 s
// and pinned ~400 MiB of cache keys for good (F14400).
func forEachExistingDir(root, rel string, fn func(dir, part string)) {
	cur := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		cur = filepath.Join(cur, part)
		if info, err := os.Stat(cur); err != nil || !info.IsDir() {
			return
		}
		fn(cur, part)
	}
}

// htaccessSubdirLevels returns the .htaccess files below the document root on
// the way to the directory the request is for, outermost first. A level whose
// file does not parse is left out (htaccessAccess already refuses it).
func (s *Server) htaccessSubdirLevels(domain *config.Domain, urlPath string) []htaccessLevel {
	target := filepath.Join(domain.Root, filepath.Clean("/"+urlPath))
	dir := target
	if !strings.HasSuffix(urlPath, "/") {
		if info, err := os.Stat(target); err != nil || !info.IsDir() {
			dir = filepath.Dir(target)
		}
	}
	rel, err := filepath.Rel(domain.Root, dir)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil
	}
	var levels []htaccessLevel
	prefix := "/"
	forEachExistingDir(domain.Root, rel, func(cur, part string) {
		prefix += part + "/"
		if entry := s.getHtaccessRuleSet(cur); entry != nil && entry.raw != nil && !entry.parseFailed {
			levels = append(levels, htaccessLevel{prefix: prefix, entry: entry})
		}
	})
	return levels
}

// applySubdirHtaccess applies the RewriteRule and Redirect directives of the
// .htaccess files below the document root. Only the root file's were applied,
// so the usual upload protection in uploads/.htaccess ("RewriteRule \.php$ -
// [F]") was parsed and then never consulted, and the scripts it guards were
// served (F11201).
func (s *Server) applySubdirHtaccess(ctx *router.RequestContext, domain *config.Domain, internalRewrite bool) bool {
	if domain.Root == "" {
		return false
	}
	for _, lvl := range s.htaccessSubdirLevels(domain, ctx.Request.URL.Path) {
		if s.applyLevelRewrites(ctx, domain, lvl.entry, lvl.prefix, internalRewrite) {
			return true
		}
	}
	return false
}

// htaccessDirChain returns the rule sets of the .htaccess files from the
// document root down to the directory the request is for, outermost first. A
// level without a file, or whose file does not parse, is left out.
func (s *Server) htaccessDirChain(domain *config.Domain, urlPath string) []*htaccess.RuleSet {
	var chain []*htaccess.RuleSet
	if root := s.getHtaccessRuleSet(domain.Root); root != nil && root.raw != nil && !root.parseFailed {
		chain = append(chain, root.raw)
	}
	target := filepath.Join(domain.Root, filepath.Clean("/"+urlPath))
	dir := target
	if !strings.HasSuffix(urlPath, "/") {
		if info, err := os.Stat(target); err != nil || !info.IsDir() {
			dir = filepath.Dir(target)
		}
	}
	rel, err := filepath.Rel(domain.Root, dir)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return chain
	}
	forEachExistingDir(domain.Root, rel, func(cur, _ string) {
		if entry := s.getHtaccessRuleSet(cur); entry != nil && entry.raw != nil && !entry.parseFailed {
			chain = append(chain, entry.raw)
		}
	})
	return chain
}

// servedFileName is the name the <Files>/<FilesMatch> sections and AddType are
// matched against: the file the request resolves to, so a directory URL served
// from index.html meets "\.html$" and does not meet a section for the
// directory's own name (F9403). It was the URL's last segment. The lookup only
// runs when a rule of the chain can use the name.
func servedFileName(ctx *router.RequestContext, domain *config.Domain, chain []*htaccess.RuleSet) string {
	urlPath := ctx.Request.URL.Path
	for _, rs := range chain {
		if len(rs.FilesMatch) == 0 && len(rs.MimeTypes) == 0 {
			continue
		}
		probe := *ctx
		probe.ResolvedPath, probe.FileInfo = "", nil
		if static.ResolveRequest(&probe, domain) && probe.FileInfo != nil && !probe.FileInfo.IsDir() {
			return filepath.Base(probe.ResolvedPath)
		}
		break
	}
	if strings.HasSuffix(urlPath, "/") {
		return ""
	}
	return path.Base(urlPath)
}

// applyHtaccessLevels applies what follows the directory chain: DirectoryIndex,
// the Header directives of every level (outermost first, the deepest wins), and
// AddType / Expires. The document root's file alone used to be read for the
// settings and a sub-directory's only for Header (F9104), so an
// uploads/.htaccess with AddType, ExpiresDefault or DirectoryIndex had no effect
// (F9402), and none applied when only a sub-directory had a file.
func (s *Server) applyHtaccessLevels(ctx *router.RequestContext, domain *config.Domain) {
	if domain.Root == "" {
		return
	}
	urlPath := ctx.Request.URL.Path
	chain := s.htaccessDirChain(domain, urlPath)
	if len(chain) == 0 {
		return
	}
	eff := htaccess.DirSettings(chain)

	// DirectoryIndex feeds static.ResolveRequest's index order via ctx
	// (per-request; never mutated on the shared domain). Apache honors the
	// htaccess list as-given: no built-in fallbacks are appended.
	if len(eff.DirectoryIndex) > 0 {
		ctx.IndexFiles = eff.DirectoryIndex
	}
	servedFile := servedFileName(ctx, domain, chain)

	attrs := &htaccess.RequestAttrs{
		RemoteAddr: normalizedRemoteIP(ctx.Request),
		Method:     ctx.Request.Method,
		URI:        urlPath,
		Proto:      ctx.Request.Proto,
		Host:       ctx.Request.Host,
		Header:     ctx.Request.Header,
		HTTPS:      ctx.IsHTTPS,
	}
	for _, rs := range chain {
		rules := append(append([]htaccess.HeaderRule(nil), rs.Headers...), rs.HeadersForFile(servedFile)...)
		for _, h := range rules {
			// "env=VAR" / "env=!VAR" limits a rule to requests where the variable
			// is (not) set; it used to be dropped and the rule applied to all (F8830).
			if !h.AppliesTo(rs, attrs) {
				continue
			}
			switch h.Action {
			case "set":
				ctx.Response.Header().Set(h.Name, h.Value)
			case "unset":
				ctx.Response.Header().Del(h.Name)
			case "append", "add":
				ctx.Response.Header().Add(h.Name, h.Value)
			}
		}
		attrs.Inherit(rs)
	}

	// AddType: the declared content type replaces the extension default for a
	// successful response (F9101). It runs before ExpiresByType below, which
	// keys on the content type.
	mimeType := eff.MimeTypeFor(servedFile)

	// Apply ExpiresByType as Cache-Control headers. The file handler sets
	// Content-Type after this runs, so the rule is applied when the response
	// headers are committed (F6000).
	var expires map[string]string
	expiresDefault := eff.ExpiresDefault
	if eff.ExpiresActive && (len(eff.ExpiresByType) > 0 || expiresDefault != "") {
		expires = eff.ExpiresByType
	}
	if expires != nil || mimeType != "" {
		apply := func(h http.Header, code int) {
			if mimeType != "" && code < 400 {
				h.Set("Content-Type", mimeType)
			}
			// A Cache-Control the response already carries (an upstream's
			// "private, no-store", a location/cache rule) is explicit intent
			// and wins, as mod_expires leaves an existing header alone (F6300).
			if expires == nil || code >= 400 || h.Get("Cache-Control") != "" {
				return
			}
			dur, ok := expiresForType(expires, h.Get("Content-Type"))
			if !ok && expiresDefault != "" {
				dur, ok = expiresDefault, true // ExpiresDefault (F9102)
			}
			if ok {
				h.Set("Cache-Control", "max-age="+parseExpiresDuration(dur))
			}
		}
		if mimeType == "" && ctx.Response.Header().Get("Content-Type") != "" {
			apply(ctx.Response.Header(), http.StatusOK)
		} else {
			// Chain, do not assign: stampCacheControl installed a hook for a
			// matching location (server_dispatch.go:416) that withdraws the
			// location's lifetime from an error response, and it runs BEFORE
			// applyHtaccess in handleRequest. Assigning here discarded it, so a
			// 404 under a domain with both a location Cache-Control and an
			// imported .htaccess kept the page's max-age (F9010). Compose the
			// same way stampCacheControl composes through `prev`.
			prev := ctx.Response.BeforeHeader
			ctx.Response.BeforeHeader = func(h http.Header, code int) {
				apply(h, code)
				if prev != nil {
					prev(h, code)
				}
			}
		}
	}
}

func (s *Server) applyRootHtaccess(ctx *router.RequestContext, domain *config.Domain, internalRewrite bool) bool {
	ruleSet := s.getHtaccessRuleSet(domain.Root)
	if ruleSet == nil || ruleSet.raw == nil {
		return false
	}

	// 1. Rewrite rules and mod_alias redirects of this directory's file.
	if s.applyLevelRewrites(ctx, domain, ruleSet, "/", internalRewrite) {
		return true
	}

	// 4. Apply ErrorDocument — already precomputed in parseHtaccessFull cache entry.
	// renderDomainError reads from the domain.ErrorPages field directly (errors.go),
	// so no runtime mutation of domain.ErrorPages is needed. The htaccess cache
	// entry holds the precomputed map for potential future use.

	// 5. Apply php_value / php_flag — store per-request override instead of mutating domain.
	// PHP-FPM reads PHP_VALUE and PHP_ADMIN_VALUE from FastCGI env to override ini settings.
	if len(ruleSet.raw.PHPValues) > 0 || len(ruleSet.raw.PHPFlags) > 0 {
		// A tenant writes .htaccess, so apply the same directive checks as
		// the per-domain php.ini override path: a blocked directive (or a
		// value carrying a line break PHP's ini parser would split on) must
		// not reach PHP through PHP_VALUE instead.
		var phpValues []string
		for k, v := range ruleSet.raw.PHPValues {
			if phpmanager.INIOverrideAllowed(k, v) {
				phpValues = append(phpValues, k+" = "+v)
			}
		}
		for k, v := range ruleSet.raw.PHPFlags {
			if phpmanager.INIOverrideAllowed(k, v) {
				phpValues = append(phpValues, k+" = "+v)
			}
		}
		if len(phpValues) > 0 {
			ctx.PHPEnvOverride = map[string]string{
				"PHP_VALUE": strings.Join(phpValues, "\n"),
			}
		}
	}
	return false
}

// applyLevelRewrites applies one directory's RewriteRule and Redirect
// directives; prefix is the URL path of that directory ("/" for the document
// root). It reports whether the request was fully handled.
func (s *Server) applyLevelRewrites(ctx *router.RequestContext, domain *config.Domain, entry *htaccessCacheEntry, prefix string, internalRewrite bool) bool {
	// 1. Apply rewrite rules. Engine was built once at parse time
	// (parseHtaccessFull) and cached on the entry; we don't reconstruct it
	// per request. Skip Variables allocation + Process loop when no rule
	// pattern can match this URI (refactor.md P12).
	// Apache matches per-directory RewriteRule patterns against the path with
	// the directory prefix ("/" for the docroot .htaccess) removed, so
	// "^backup/" must see "backup/db.sql", not "/backup/db.sql".
	// The path is canonicalised first: "//backup/x" is served as "/backup/x",
	// so it must meet the same "^backup/" [F] rule (F2530).
	clean := pathmatch.Clean(ctx.Request.URL.Path)
	perDirPath := strings.TrimPrefix(clean, prefix)
	if clean+"/" == prefix {
		perDirPath = ""
	} else if perDirPath == clean && prefix != "/" {
		return false // the request is no longer below this directory
	}
	if entry.engine != nil && entry.engine.MightMatch(perDirPath) {
		requestFilename := filepath.Join(domain.Root, filepath.Clean("/"+ctx.Request.URL.Path))
		vars := rewrite.BuildVariables(ctx.Request, domain.Root, requestFilename, ctx.IsHTTPS)
		result := entry.engine.Process(perDirPath, ctx.Request.URL.RawQuery, vars)
		// Apache adds the directory prefix back to a relative substitution.
		if result.Modified && !strings.HasPrefix(result.URI, "/") && !strings.Contains(result.URI, "://") {
			result.URI = prefix + result.URI
		}

		// Honor access-control results, exactly like applyRewrites. Without
		// this, .htaccess [F]/[G]/[R] rules (commonly guarding backups,
		// includes, uploads on migrated Apache sites) were computed and then
		// silently discarded — a fail-open security gap.
		if result.Forbidden {
			s.renderDomainErrorFor(ctx.Request, ctx.Response, http.StatusForbidden, domain)
			return true
		}
		if result.Gone {
			s.renderDomainErrorFor(ctx.Request, ctx.Response, http.StatusGone, domain)
			return true
		}
		if result.Redirect {
			http.Redirect(ctx.Response, ctx.Request, result.URI, result.StatusCode)
			return true
		}
		if result.Modified && internalRewrite {
			ctx.Request.URL.Path = result.URI
			if result.Query != "" {
				ctx.Request.URL.RawQuery = result.Query
			}
			ctx.RewrittenURI = result.URI
		}
	}

	// 1b. Apply mod_alias Redirect/RedirectMatch. Apache runs mod_alias
	// after mod_rewrite: these fire only when no rewrite above handled the
	// request. Before this block, Redirect rules were parsed into the
	// RuleSet and silently dropped — a migrated Apache site's redirects
	// were no-ops and requests fell through to 404.
	for _, redir := range entry.raw.Redirects {
		location, status, ok := htaccess.MatchRedirect(redir, ctx.Request.URL.Path)
		if !ok {
			continue
		}
		if location == "" || status == http.StatusGone {
			// Empty target (the "gone" form) responds 410 without Location.
			s.renderDomainErrorFor(ctx.Request, ctx.Response, http.StatusGone, domain)
			return true
		}
		if status < 300 || status > 399 {
			status = http.StatusFound
		}
		http.Redirect(ctx.Response, ctx.Request, location, status)
		return true
	}
	return false
}

// expiresForType finds the ExpiresByType entry for a Content-Type. Apache
// matches the MIME type case-insensitively and ignores the charset parameter.
func expiresForType(m map[string]string, ct string) (string, bool) {
	if idx := strings.Index(ct, ";"); idx != -1 {
		ct = ct[:idx]
	}
	ct = strings.TrimSpace(ct)
	if ct == "" {
		return "", false
	}
	if v, ok := m[ct]; ok {
		return v, true
	}
	for k, v := range m {
		if strings.EqualFold(k, ct) {
			return v, true
		}
	}
	return "", false
}

// parseExpiresDuration converts Apache Expires format to seconds.
// e.g. "access plus 1 month" → "2592000", "access plus 1 year" → "31536000"
func parseExpiresDuration(expr string) string {
	expr = strings.ToLower(strings.TrimSpace(expr))
	// Apache's compact forms: "A<seconds>" (after access) and "M<seconds>"
	// (after modification) (F9103).
	if len(expr) > 1 && (expr[0] == 'a' || expr[0] == 'm') {
		if n, err := strconv.Atoi(expr[1:]); err == nil && n >= 0 {
			return strconv.Itoa(n)
		}
	}
	expr = strings.Replace(expr, "access plus ", "", 1)
	expr = strings.Replace(expr, "modification plus ", "", 1)

	seconds := 0
	parsed := false
	parts := strings.Fields(expr)
	for i := 0; i+1 < len(parts); i += 2 {
		n := 0
		if _, err := fmt.Sscanf(parts[i], "%d", &n); err != nil {
			continue
		}
		unit := parts[i+1]
		switch {
		case strings.HasPrefix(unit, "second"):
			seconds += n
		case strings.HasPrefix(unit, "minute"):
			seconds += n * 60
		case strings.HasPrefix(unit, "hour"):
			seconds += n * 3600
		case strings.HasPrefix(unit, "day"):
			seconds += n * 86400
		case strings.HasPrefix(unit, "week"):
			seconds += n * 604800
		case strings.HasPrefix(unit, "month"):
			seconds += n * 2592000
		case strings.HasPrefix(unit, "year"):
			seconds += n * 31536000
		default:
			continue
		}
		parsed = true
	}
	if !parsed {
		seconds = 3600 // 1 hour default
	}
	return fmt.Sprintf("%d", seconds)
}

// htaccessCacheEntry holds both raw and compiled htaccess rules.
type htaccessCacheEntry struct {
	raw           *htaccess.RuleSet
	compiledRules []*rewrite.Rule
	engine        *rewrite.Engine // pre-built rewrite engine, nil when RewriteEnabled is false
	modTime       time.Time       // file modification time for auto-invalidation
	size          int64           // file size, so a replacement that kept its mtime (cp -p, rsync -t) is still noticed
	errorPages    map[int]string  // precomputed ErrorDocument map (immutable after parseHtaccessFull)
	parseFailed   bool            // .htaccess exists but could not be parsed; its denies are unknown
}

func (s *Server) getHtaccessRuleSet(root string) *htaccessCacheEntry {
	htPath := filepath.Join(root, ".htaccess")

	s.htaccessCacheMu.RLock()
	if entry, ok := s.htaccessCache[root]; ok {
		s.htaccessCacheMu.RUnlock()
		// Check if file changed since last parse
		if info, err := os.Stat(htPath); err == nil {
			if !info.ModTime().Equal(entry.modTime) || info.Size() != entry.size {
				// File changed — re-parse
				newEntry := s.parseHtaccessFull(root)
				s.htaccessCacheMu.Lock()
				s.htaccessCache[root] = newEntry
				s.htaccessCacheMu.Unlock()
				return newEntry
			}
		} else if entry.raw == nil && !entry.parseFailed {
			// File still doesn't exist and cache is nil — that's fine
			return entry
		} else {
			// File was deleted — invalidate
			s.htaccessCacheMu.Lock()
			delete(s.htaccessCache, root)
			s.htaccessCacheMu.Unlock()
			return nil
		}
		return entry
	}
	s.htaccessCacheMu.RUnlock()

	entry := s.parseHtaccessFull(root)

	s.htaccessCacheMu.Lock()
	if s.htaccessCache == nil {
		s.htaccessCache = make(map[string]*htaccessCacheEntry)
	}
	s.htaccessCache[root] = entry
	s.htaccessCacheMu.Unlock()

	return entry
}

// htaccessDeny is the access-control outcome of the .htaccess chain for one
// target: status 0 means allowed; reason is "auth" when an AuthUserFile
// guard (which UWAS cannot verify) caused the denial.
type htaccessDeny struct {
	status int
	reason string
	// noFollow: "Options -FollowSymLinks" governs the target.
	noFollow bool
}

// htaccessAccess applies the access-control directives of every .htaccess
// from root down to target's directory (target itself when isDir), the way
// Apache merges per-directory configuration: an unparseable file anywhere
// answers 500; a matching <Files>/<FilesMatch> deny or an AuthUserFile in any
// of them answers 403; and the deepest directory whose top-level
// Require/Allow/Deny decides wins, answering 403 when it denies. target must
// be lexically inside root (ResolveRequest guarantees this); anything else
// is checked against root's .htaccess only. clientIP is the client address
// RealIP resolved (trusted proxies honoured), against which IP-based
// Require/Allow/Deny forms are evaluated; nil matches no IP condition. method
// is the request method, which <Limit>/<LimitExcept> sections key on.
func (s *Server) htaccessAccess(root, target string, isDir bool, clientIP net.IP, method string) htaccessDeny {
	return s.htaccessAccessAttrs(root, target, isDir, clientIP, method, nil)
}

// htaccessAccessReq is htaccessAccess for a live request: the client address,
// method and the attributes SetEnvIf/BrowserMatch match against all come from
// r, so "Deny from env=bad_bot" and "Require env X" are decided instead of
// failing closed for every client (F6700).
func (s *Server) htaccessAccessReq(root, target string, isDir bool, r *http.Request) htaccessDeny {
	return s.htaccessAccessAttrs(root, target, isDir, net.ParseIP(normalizedRemoteIP(r)), r.Method, &htaccess.RequestAttrs{
		RemoteAddr: normalizedRemoteIP(r),
		Method:     r.Method,
		URI:        r.URL.Path,
		Proto:      r.Proto,
		Host:       r.Host,
		Header:     r.Header,
		HTTPS:      r.TLS != nil,
	})
}

func (s *Server) htaccessAccessAttrs(root, target string, isDir bool, clientIP net.IP, method string, attrs *htaccess.RequestAttrs) htaccessDeny {
	dir := target
	if !isDir {
		dir = filepath.Dir(target)
	}
	dirs := []string{root}
	if rel, err := filepath.Rel(root, dir); err == nil && rel != "." && rel != ".." &&
		!strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		forEachExistingDir(root, rel, func(cur, _ string) { dirs = append(dirs, cur) })
	}

	var res htaccessDeny
	access := htaccess.AccessUnset
	// "Options -FollowSymLinks" governs the directory it is written in and
	// everything below it; the deepest level that says anything wins, so
	// "+FollowSymLinks" in a subdirectory lifts a parent's refusal (F16900).
	follow, followDir := true, ""
	for _, d := range dirs {
		entry := s.getHtaccessRuleSet(d)
		if entry == nil {
			continue
		}
		if entry.parseFailed {
			return htaccessDeny{status: http.StatusInternalServerError}
		}
		if entry.raw == nil {
			continue
		}
		if entry.raw.FollowSymlinks != nil {
			follow, followDir = *entry.raw.FollowSymlinks, d
		}
		if !isDir && res.status == 0 && htaccess.FilesMatchDeniesForRequest(entry.raw, filepath.Base(target), clientIP, method, attrs) {
			res = htaccessDeny{status: http.StatusForbidden}
		}
		if htaccess.AuthUserFileRequiresAuth(entry.raw) && res.reason == "" {
			res = htaccessDeny{status: http.StatusForbidden, reason: "auth"}
		}
		if a := entry.raw.AccessForRequest(clientIP, method, attrs); a != htaccess.AccessUnset {
			access = a
		}
		// SetEnvIf/BrowserMatch here govern the access rules of the
		// directories below (F7000).
		attrs.Inherit(entry.raw)
	}
	if res.status == 0 && access == htaccess.AccessDenied {
		res.status = http.StatusForbidden
	}
	if res.status == 0 && !follow && symlinkBelow(followDir, target) {
		res.status = http.StatusForbidden
	}
	res.noFollow = !follow
	return res
}

// maxHtaccessSize bounds the .htaccess a tenant can make the server parse.
const maxHtaccessSize = 8 << 20

func (s *Server) parseHtaccessFull(root string) *htaccessCacheEntry {
	htPath := filepath.Join(root, ".htaccess")
	// O_NONBLOCK: a FIFO planted as .htaccess must not hang every request.
	f, err := os.OpenFile(htPath, os.O_RDONLY|nonBlockFlag, 0)
	if err != nil {
		return &htaccessCacheEntry{} // cache "no file" to avoid repeated stat
	}
	defer f.Close()

	info, _ := f.Stat()
	if info != nil && (!info.Mode().IsRegular() || info.Size() > maxHtaccessSize) {
		// A device or FIFO cannot be a real .htaccess, and a file this large
		// is not read (truncating it would drop denies); its denies are
		// unknown, so fail closed like an unparsable file.
		s.logger.Warn("htaccess is not a regular file or too large; refusing to serve until fixed", "path", htPath)
		return &htaccessCacheEntry{parseFailed: true, modTime: info.ModTime(), size: info.Size()}
	}

	directives, err := htaccess.Parse(f)
	if err != nil {
		// Fail closed: the file's <Files> denies and AuthUserFile guards are
		// unknown, so the caller refuses to serve rather than drop them.
		// Keep modTime so an unchanged broken file is not re-parsed (and
		// re-logged) on every request.
		s.logger.Warn("htaccess parse error; refusing to serve until fixed", "path", htPath, "error", err)
		failed := &htaccessCacheEntry{parseFailed: true}
		if info != nil {
			failed.modTime = info.ModTime()
			failed.size = info.Size()
		}
		return failed
	}

	ruleSet := htaccess.Convert(directives)
	entry := &htaccessCacheEntry{raw: ruleSet}
	if info != nil {
		entry.modTime = info.ModTime()
		entry.size = info.Size()
	}

	// Compile rewrite rules
	if ruleSet.RewriteEnabled {
		base := ruleSet.RewriteBase // may be "/" or "/subdir/" or ""
		for _, rw := range ruleSet.Rewrites {
			target := rw.Target
			// RewriteBase is prepended to relative targets (those not starting with /).
			// Apache behavior: RewriteBase only affects targets that don't start with /.
			// An absolute http(s) URL is already complete (F30600).
			lt := strings.ToLower(target)
			absolute := strings.HasPrefix(lt, "http://") || strings.HasPrefix(lt, "https://")
			if base != "" && target != "" && target != "-" && !strings.HasPrefix(target, "/") && !absolute {
				target = base + target
			}
			rule, err := rewrite.ParseRule(perDirPattern(rw.Pattern), target, rw.Flags)
			// Skipping an ordinary rewrite only loses the rewrite, but an
			// [F]/[G] rule is an access guard (e.g. a PCRE-only
			// "^(?!index\\.php$).*\\.php$" pattern Go's RE2 cannot compile):
			// dropping it would serve what it blocks, so fail closed like an
			// unparsable file (F6100).
			failClosed := func(msg string, args ...any) *htaccessCacheEntry {
				s.logger.Warn(msg, append([]any{"path", htPath, "pattern", rw.Pattern}, args...)...)
				failed := &htaccessCacheEntry{parseFailed: true}
				if info != nil {
					failed.modTime = info.ModTime()
					failed.size = info.Size()
				}
				return failed
			}
			if err != nil {
				if fl := rewrite.ParseFlags(rw.Flags); fl.Forbidden || fl.Gone {
					return failClosed("htaccess access rule does not compile; refusing to serve until fixed", "error", err)
				}
				continue
			}
			condFailed := false
			for _, cond := range rw.Conditions {
				c, err := rewrite.ParseCondition(cond.Variable, cond.Pattern, cond.Flags)
				if err != nil {
					// Dropping the condition would make the rule fire for
					// every request (F6401); drop the rule instead, or fail
					// closed for an access guard.
					if rule.Flags.Forbidden || rule.Flags.Gone {
						return failClosed("htaccess access rule condition does not compile; refusing to serve until fixed",
							"condition", cond.Pattern, "error", err)
					}
					condFailed = true
					break
				}
				rule.Conditions = append(rule.Conditions, *c)
			}
			if condFailed {
				continue
			}
			// A rule that rewrites ends the pass: the engine does not
			// re-resolve REQUEST_FILENAME for a second pass. A "-" rule only
			// sets state or flags ([E=..], [S=N], [C]) and Apache goes on to
			// the following rules, so it must not end the pass (F6400).
			if rule.Target != "-" {
				rule.Flags.Last = true
			}
			entry.compiledRules = append(entry.compiledRules, rule)
		}
		// Pre-build the engine once so applyHtaccess doesn't re-construct it
		// per request (was P9).
		if len(entry.compiledRules) > 0 {
			entry.engine = rewrite.NewEngine(entry.compiledRules)
		}
	}

	// Precompute ErrorDocument map (immutable, avoids concurrent map writes on domain)
	if len(ruleSet.ErrorDocuments) > 0 {
		pages := make(map[int]string, len(ruleSet.ErrorDocuments))
		for code, page := range ruleSet.ErrorDocuments {
			pages[code] = page
		}
		entry.errorPages = pages
	}

	return entry
}

// perDirPattern keeps uwas-style .htaccess patterns written with an explicit
// leading slash ("^/old$") working now that rules are matched against the
// per-directory path without it: "^/X" on "/p" is equivalent to "^X" on "p".
// "^/?", "^/*" etc. already match the stripped path and are left unchanged.
func perDirPattern(p string) string {
	if strings.HasPrefix(p, "^/") && !strings.ContainsAny(p[2:min(len(p), 3)], "?*+{") {
		return "^" + p[2:]
	}
	return p
}

// phpDisabledForFile reports whether the .htaccess levels from the document
// root down to the directory of file switch PHP execution off.
func (s *Server) phpDisabledForFile(domain *config.Domain, file string) bool {
	rel, err := filepath.Rel(domain.Root, filepath.Dir(file))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	var chain []*htaccess.RuleSet
	dirs := []string{domain.Root}
	if rel != "." {
		forEachExistingDir(domain.Root, rel, func(cur, _ string) { dirs = append(dirs, cur) })
	}
	for _, d := range dirs {
		if entry := s.getHtaccessRuleSet(d); entry != nil && entry.raw != nil && !entry.parseFailed {
			chain = append(chain, entry.raw)
		}
	}
	return htaccess.PHPDisabledFor(chain)
}

// symlinkBelow reports whether the path from dir down to target passes through
// a symbolic link (the last component included). Apache answers 403 for it
// where "Options -FollowSymLinks" applies; a link on the way to dir itself is
// the business of the levels above.
func symlinkBelow(dir, target string) bool {
	rel, err := filepath.Rel(dir, target)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	cur := dir
	for _, seg := range strings.Split(rel, string(filepath.Separator)) {
		cur = filepath.Join(cur, seg)
		if fi, err := os.Lstat(cur); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			return true
		}
	}
	return false
}

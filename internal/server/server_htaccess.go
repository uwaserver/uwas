package server

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/uwaserver/uwas/internal/config"
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
		s.renderDomainError(ctx.Response, http.StatusForbidden, domain)
		return true
	}
	if result.Gone {
		s.renderDomainError(ctx.Response, http.StatusGone, domain)
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
	ruleSet := s.getHtaccessRuleSet(domain.Root)
	if ruleSet == nil || ruleSet.raw == nil {
		return false
	}

	// 1. Apply rewrite rules. Engine was built once at parse time
	// (parseHtaccessFull) and cached on the entry; we don't reconstruct it
	// per request. Skip Variables allocation + Process loop when no rule
	// pattern can match this URI (refactor.md P12).
	// Apache matches per-directory RewriteRule patterns against the path with
	// the directory prefix ("/" for the docroot .htaccess) removed, so
	// "^backup/" must see "backup/db.sql", not "/backup/db.sql".
	// The path is canonicalised first: "//backup/x" is served as "/backup/x",
	// so it must meet the same "^backup/" [F] rule (F2530).
	perDirPath := strings.TrimPrefix(pathmatch.Clean(ctx.Request.URL.Path), "/")
	if ruleSet.engine != nil && ruleSet.engine.MightMatch(perDirPath) {
		requestFilename := filepath.Join(domain.Root, filepath.Clean("/"+ctx.Request.URL.Path))
		vars := rewrite.BuildVariables(ctx.Request, domain.Root, requestFilename, ctx.IsHTTPS)
		result := ruleSet.engine.Process(perDirPath, ctx.Request.URL.RawQuery, vars)
		// Apache adds the directory prefix back to a relative substitution.
		if result.Modified && !strings.HasPrefix(result.URI, "/") && !strings.Contains(result.URI, "://") {
			result.URI = "/" + result.URI
		}

		// Honor access-control results, exactly like applyRewrites. Without
		// this, .htaccess [F]/[G]/[R] rules (commonly guarding backups,
		// includes, uploads on migrated Apache sites) were computed and then
		// silently discarded — a fail-open security gap.
		if result.Forbidden {
			s.renderDomainError(ctx.Response, http.StatusForbidden, domain)
			return true
		}
		if result.Gone {
			s.renderDomainError(ctx.Response, http.StatusGone, domain)
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
	for _, redir := range ruleSet.raw.Redirects {
		location, status, ok := htaccess.MatchRedirect(redir, ctx.Request.URL.Path)
		if !ok {
			continue
		}
		if location == "" || status == http.StatusGone {
			// Empty target (the "gone" form) responds 410 without Location.
			s.renderDomainError(ctx.Response, http.StatusGone, domain)
			return true
		}
		if status < 300 || status > 399 {
			status = http.StatusFound
		}
		http.Redirect(ctx.Response, ctx.Request, location, status)
		return true
	}

	// 1c. DirectoryIndex — feeds static.ResolveRequest's index order via
	// ctx (per-request; never mutated on the shared domain). Apache honors
	// the htaccess list as-given: no built-in fallbacks are appended.
	if len(ruleSet.raw.DirectoryIndex) > 0 {
		ctx.IndexFiles = ruleSet.raw.DirectoryIndex
	}

	// 2. Apply Header directives
	for _, h := range ruleSet.raw.Headers {
		switch h.Action {
		case "set":
			ctx.Response.Header().Set(h.Name, h.Value)
		case "unset":
			ctx.Response.Header().Del(h.Name)
		case "append":
			ctx.Response.Header().Add(h.Name, h.Value)
		case "add":
			ctx.Response.Header().Add(h.Name, h.Value)
		}
	}

	// 3. Apply ExpiresByType as Cache-Control headers
	if ruleSet.raw.ExpiresActive {
		ct := ctx.Response.Header().Get("Content-Type")
		if ct != "" {
			// Strip charset: "text/html; charset=utf-8" → "text/html"
			if idx := strings.Index(ct, ";"); idx != -1 {
				ct = strings.TrimSpace(ct[:idx])
			}
			if dur, ok := ruleSet.raw.ExpiresByType[ct]; ok {
				ctx.Response.Header().Set("Cache-Control", "max-age="+parseExpiresDuration(dur))
			}
		}
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

// parseExpiresDuration converts Apache Expires format to seconds.
// e.g. "access plus 1 month" → "2592000", "access plus 1 year" → "31536000"
func parseExpiresDuration(expr string) string {
	expr = strings.ToLower(expr)
	expr = strings.Replace(expr, "access plus ", "", 1)
	expr = strings.Replace(expr, "modification plus ", "", 1)

	seconds := 0
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
		}
	}
	if seconds == 0 {
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
	dir := target
	if !isDir {
		dir = filepath.Dir(target)
	}
	dirs := []string{root}
	if rel, err := filepath.Rel(root, dir); err == nil && rel != "." && rel != ".." &&
		!strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		cur := root
		for _, part := range strings.Split(rel, string(filepath.Separator)) {
			cur = filepath.Join(cur, part)
			dirs = append(dirs, cur)
		}
	}

	var res htaccessDeny
	access := htaccess.AccessUnset
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
		if !isDir && res.status == 0 && htaccess.FilesMatchDeniesFor(entry.raw, filepath.Base(target), clientIP) {
			res = htaccessDeny{status: http.StatusForbidden}
		}
		if htaccess.AuthUserFileRequiresAuth(entry.raw) && res.reason == "" {
			res = htaccessDeny{status: http.StatusForbidden, reason: "auth"}
		}
		if a := entry.raw.AccessForMethod(clientIP, method); a != htaccess.AccessUnset {
			access = a
		}
	}
	if res.status == 0 && access == htaccess.AccessDenied {
		res.status = http.StatusForbidden
	}
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
			if base != "" && target != "" && target != "-" && !strings.HasPrefix(target, "/") {
				target = base + target
			}
			rule, err := rewrite.ParseRule(perDirPattern(rw.Pattern), target, rw.Flags)
			if err != nil {
				continue
			}
			for _, cond := range rw.Conditions {
				c, err := rewrite.ParseCondition(cond.Variable, cond.Pattern, cond.Flags)
				if err != nil {
					continue
				}
				rule.Conditions = append(rule.Conditions, *c)
			}
			rule.Flags.Last = true
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

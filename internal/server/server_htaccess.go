package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/uwaserver/uwas/internal/config"
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
	if !engine.MightMatch(ctx.Request.URL.Path) {
		return false
	}

	vars := rewrite.BuildVariables(ctx.Request, domain.Root, ctx.ResolvedPath, ctx.IsHTTPS)
	result := engine.Process(ctx.Request.URL.Path, ctx.Request.URL.RawQuery, vars)

	if result.Forbidden {
		renderDomainError(ctx.Response, http.StatusForbidden, domain)
		return true
	}
	if result.Gone {
		renderDomainError(ctx.Response, http.StatusGone, domain)
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
func (s *Server) applyHtaccess(ctx *router.RequestContext, domain *config.Domain) bool {
	ruleSet := s.getHtaccessRuleSet(domain.Root)
	if ruleSet == nil || ruleSet.raw == nil {
		return false
	}

	// 1. Apply rewrite rules. Engine was built once at parse time
	// (parseHtaccessFull) and cached on the entry; we don't reconstruct it
	// per request. Skip Variables allocation + Process loop when no rule
	// pattern can match this URI (refactor.md P12).
	if ruleSet.engine != nil && ruleSet.engine.MightMatch(ctx.Request.URL.Path) {
		requestFilename := filepath.Join(domain.Root, filepath.Clean("/"+ctx.Request.URL.Path))
		vars := rewrite.BuildVariables(ctx.Request, domain.Root, requestFilename, ctx.IsHTTPS)
		result := ruleSet.engine.Process(ctx.Request.URL.Path, ctx.Request.URL.RawQuery, vars)

		// Honor access-control results, exactly like applyRewrites. Without
		// this, .htaccess [F]/[G]/[R] rules (commonly guarding backups,
		// includes, uploads on migrated Apache sites) were computed and then
		// silently discarded — a fail-open security gap.
		if result.Forbidden {
			renderDomainError(ctx.Response, http.StatusForbidden, domain)
			return true
		}
		if result.Gone {
			renderDomainError(ctx.Response, http.StatusGone, domain)
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
	// renderDomainError reads from the cache entry directly (errors.go), so no
	// domain.ErrorPages mutation is needed.
	_ = ruleSet.raw.ErrorDocuments // referenced for coverage

	// 5. Apply php_value / php_flag — store per-request override instead of mutating domain.
	// PHP-FPM reads PHP_VALUE and PHP_ADMIN_VALUE from FastCGI env to override ini settings.
	if len(ruleSet.raw.PHPValues) > 0 || len(ruleSet.raw.PHPFlags) > 0 {
		var phpValues []string
		for k, v := range ruleSet.raw.PHPValues {
			phpValues = append(phpValues, k+" = "+v)
		}
		for k, v := range ruleSet.raw.PHPFlags {
			phpValues = append(phpValues, k+" = "+v)
		}
		ctx.PHPEnvOverride = map[string]string{
			"PHP_VALUE": strings.Join(phpValues, "\n"),
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
	errorPages    map[int]string  // precomputed ErrorDocument map (immutable after parseHtaccessFull)
}

func (s *Server) getHtaccessRuleSet(root string) *htaccessCacheEntry {
	htPath := filepath.Join(root, ".htaccess")

	s.htaccessCacheMu.RLock()
	if entry, ok := s.htaccessCacheV2[root]; ok {
		s.htaccessCacheMu.RUnlock()
		// Check if file changed since last parse
		if info, err := os.Stat(htPath); err == nil {
			if !info.ModTime().Equal(entry.modTime) {
				// File changed — re-parse
				newEntry := s.parseHtaccessFull(root)
				s.htaccessCacheMu.Lock()
				s.htaccessCacheV2[root] = newEntry
				s.htaccessCacheMu.Unlock()
				return newEntry
			}
		} else if entry.raw == nil {
			// File still doesn't exist and cache is nil — that's fine
			return entry
		} else {
			// File was deleted — invalidate
			s.htaccessCacheMu.Lock()
			delete(s.htaccessCacheV2, root)
			s.htaccessCacheMu.Unlock()
			return nil
		}
		return entry
	}
	s.htaccessCacheMu.RUnlock()

	entry := s.parseHtaccessFull(root)

	s.htaccessCacheMu.Lock()
	if s.htaccessCacheV2 == nil {
		s.htaccessCacheV2 = make(map[string]*htaccessCacheEntry)
	}
	s.htaccessCacheV2[root] = entry
	s.htaccessCacheMu.Unlock()

	return entry
}

func (s *Server) parseHtaccessFull(root string) *htaccessCacheEntry {
	htPath := filepath.Join(root, ".htaccess")
	f, err := os.Open(htPath)
	if err != nil {
		return &htaccessCacheEntry{} // cache "no file" to avoid repeated stat
	}
	defer f.Close()

	info, _ := f.Stat()

	directives, err := htaccess.Parse(f)
	if err != nil {
		s.logger.Warn("htaccess parse error", "path", htPath, "error", err)
		return &htaccessCacheEntry{}
	}

	ruleSet := htaccess.Convert(directives)
	entry := &htaccessCacheEntry{raw: ruleSet}
	if info != nil {
		entry.modTime = info.ModTime()
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
			rule, err := rewrite.ParseRule(rw.Pattern, target, rw.Flags)
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

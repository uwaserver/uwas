// Package pathmatch holds the two URL-path matchers UWAS applies to
// per-domain configuration: location patterns and cache-rule regexes.
//
// They live in their own package because both the request path and the admin
// API need them, and the admin package cannot import the server package —
// the server constructs the admin server, so the dependency only runs one
// way. Duplicating the matchers instead would let the panel's answer drift
// away from what the server actually does.
package pathmatch

import (
	"path"
	"regexp"
	"strings"
	"sync"
)

// Clean returns the canonical form of a request path: dot segments and
// repeated slashes resolved, a trailing slash kept. The static handler opens
// files through filepath.Clean, so "//private/x" and "/a/../private/x" reach
// the same file as "/private/x"; a rule that decides on the path as sent
// (a location's basic_auth, a WAF bypass prefix) must decide on this form or
// the rule can be skipped while the file is still served (F2500).
func Clean(p string) string {
	if p == "" || p[0] != '/' {
		return p
	}
	c := path.Clean(p)
	if c != p && c != "/" && strings.HasSuffix(p, "/") {
		c += "/"
	}
	return c
}

// regexCache keeps compiled patterns so a request never pays for a recompile.
// Patterns come from operator config, so the key space is bounded by it.
var regexCache sync.Map

func compile(pattern string) *regexp.Regexp {
	if v, ok := regexCache.Load(pattern); ok {
		return v.(*regexp.Regexp)
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		regexCache.Store(pattern, (*regexp.Regexp)(nil))
		return nil
	}
	regexCache.Store(pattern, re)
	return re
}

// Regex reports whether a path matches a cache-rule pattern, which is always
// a regular expression. An uncompilable pattern never matches; config
// validation warns about those separately.
func Regex(path, pattern string) bool {
	re := compile(pattern)
	return re != nil && re.MatchString(path)
}

// Location reports whether a path matches a location pattern. A leading "~"
// marks a regex, as in nginx; anything else is a prefix.
//
// An empty pattern is UNSET, not a wildcard. strings.HasPrefix(path, "") is
// true for every path, so without this guard a location block with no match
// became a domain-wide catch-all: it applied its headers and Cache-Control to
// every request and, because callers break on first match, suppressed every
// location block after it. config validation treats an empty match as absent
// (validate.go guards `if loc.Match != ""`), and the rewrite engine drops a
// rule whose pattern will not parse rather than applying it everywhere — an
// unset match does the same here.
func Location(path, pattern string) bool {
	if pattern == "" {
		return false
	}
	path = Clean(path)
	if regexStr, ok := strings.CutPrefix(pattern, "~"); ok {
		re := compile(strings.TrimSpace(regexStr))
		return re != nil && re.MatchString(path)
	}
	return strings.HasPrefix(path, pattern)
}

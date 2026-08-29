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
	"regexp"
	"strings"
	"sync"
)

// regexCache keeps compiled patterns so a request never pays for a recompile.
// Patterns come from operator config, so the key space is bounded by it.
var regexCache sync.Map

func compile(pattern string) *regexp.Regexp {
	if v, ok := regexCache.Load(pattern); ok {
		return v.(*regexp.Regexp)
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
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
func Location(path, pattern string) bool {
	if regexStr, ok := strings.CutPrefix(pattern, "~"); ok {
		re := compile(strings.TrimSpace(regexStr))
		return re != nil && re.MatchString(path)
	}
	return strings.HasPrefix(path, pattern)
}

package static

import (
	"strings"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/pathmatch"
)

// CacheControlDecision explains which piece of a domain's configuration
// decides the Cache-Control header for one URL path.
type CacheControlDecision struct {
	// Value is the header that will be sent. Empty means none is sent and the
	// browser revalidates on its own.
	Value string `json:"value"`

	// Source names the setting that won: "location", "headers", "cache_rule",
	// "browser_cache" or "none".
	Source string `json:"source"`

	// Detail is the specific pattern or field that matched, for display.
	Detail string `json:"detail,omitempty"`
}

// ResolveCacheControl reports what Cache-Control a request for urlPath will
// receive, and why.
//
// Four settings can decide it and the order is not the order they appear in
// the config file, which is exactly why this exists — an operator reading the
// YAML cannot tell that a cache rule silently overrides a location, or that a
// page under an immutable_paths prefix still revalidates. Mirrors the order in
// server_dispatch.go, and TestResolveCacheControlMatchesServer drives real
// requests through the server to catch the two drifting apart.
//
// cacheEnabled says whether the response cache is live for this request; when
// it is off the cache rules never run, so they cannot set a header either.
func ResolveCacheControl(d *config.Domain, urlPath string, cacheEnabled bool) CacheControlDecision {
	out := CacheControlDecision{Source: "none"}

	// 1. Locations. First match wins and the loop stops there, as in nginx.
	for _, loc := range d.Locations {
		if !pathmatch.Location(urlPath, loc.Match) {
			continue
		}
		if loc.CacheControl != "" {
			out = CacheControlDecision{
				Value:  loc.CacheControl,
				Source: "location",
				Detail: loc.Match,
			}
		}
		break
	}

	// 2. Domain headers. These are applied after the location block, so a
	// Cache-Control set here beats the location's.
	for _, set := range []map[string]string{d.Headers.Add, d.Headers.ResponseAdd} {
		for k, v := range set {
			if strings.EqualFold(k, "Cache-Control") && v != "" {
				out = CacheControlDecision{Value: v, Source: "headers", Detail: k}
			}
		}
	}

	// 3. Cache rules, evaluated in order with the last match winning — and
	// applied after the headers above, so they win over both. A bypass rule
	// stops the loop without setting anything.
	if cacheEnabled {
		for _, rule := range d.Cache.Rules {
			if !pathmatch.Regex(urlPath, rule.Match) {
				continue
			}
			if rule.Bypass {
				break
			}
			if rule.CacheControl != "" {
				out = CacheControlDecision{
					Value:  rule.CacheControl,
					Source: "cache_rule",
					Detail: rule.Match,
				}
			}
		}
	}

	// 4. browser_cache only fills a gap; it never overrides an explicit value.
	if out.Value == "" {
		if v := BrowserCacheFor(d.BrowserCache, urlPath, urlPath); v != "" {
			out = CacheControlDecision{
				Value:  v,
				Source: "browser_cache",
				Detail: browserCacheReason(d.BrowserCache, urlPath),
			}
		}
	}
	return out
}

// browserCacheReason names which browser_cache field produced the value, so
// the panel can say "html" rather than leaving the operator to work out why a
// path under immutable_paths still revalidates.
func browserCacheReason(cfg config.BrowserCache, urlPath string) string {
	switch strings.ToLower(extOf(urlPath)) {
	case "", ".html", ".htm":
		return "html"
	}
	for _, pattern := range cfg.ImmutablePaths {
		if matchURLPattern(pattern, urlPath) {
			return "immutable_paths: " + pattern
		}
	}
	return "assets"
}

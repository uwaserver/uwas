package static

import (
	"path"
	"strings"

	"github.com/uwaserver/uwas/internal/config"
)

// BrowserCacheFor returns the Cache-Control value for one static file, or ""
// when UWAS should stay silent and let the browser revalidate on its own.
//
// urlPath is the request path (what immutable_paths patterns match against);
// filePath is the file finally resolved, which may differ after index
// resolution or an image-optimization swap.
func BrowserCacheFor(cfg config.BrowserCache, urlPath, filePath string) string {
	if !cfg.BrowserCacheEnabled() {
		return ""
	}

	switch strings.ToLower(extOf(filePath)) {
	case "", ".html", ".htm":
		return cfg.HTML
	}

	for _, pattern := range cfg.ImmutablePaths {
		if matchURLPattern(pattern, urlPath) {
			return cfg.Immutable
		}
	}
	return cfg.Assets
}

// matchURLPattern matches an immutable_paths entry against a request path.
//
// A trailing * is a prefix match covering nested directories, because that is
// what "/assets/*" is universally taken to mean and path.Match would stop at
// the first slash. A leading * is the matching suffix form, for "*.woff2".
// Anything more elaborate falls through to path.Match.
func matchURLPattern(pattern, urlPath string) bool {
	if pattern == "" {
		return false
	}
	if prefix, ok := strings.CutSuffix(pattern, "*"); ok && !strings.Contains(prefix, "*") {
		return strings.HasPrefix(urlPath, prefix)
	}
	// "*.woff2" is a suffix match. path.Match would refuse it: its * never
	// crosses a slash, so it can only ever match a bare filename.
	if suffix, ok := strings.CutPrefix(pattern, "*"); ok && !strings.Contains(suffix, "*") {
		return strings.HasSuffix(urlPath, suffix)
	}
	if ok, err := path.Match(pattern, urlPath); err == nil && ok {
		return true
	}
	return false
}

// extOf is filepath.Ext with the directory separator ignored, so it behaves
// the same on a URL path as on a filesystem path.
func extOf(p string) string {
	if i := strings.LastIndexByte(p, "/"[0]); i >= 0 {
		p = p[i+1:]
	}
	if i := strings.LastIndexByte(p, "."[0]); i >= 0 {
		return p[i:]
	}
	return ""
}

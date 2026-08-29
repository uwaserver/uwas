package static

import (
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

func defaultBrowserCache() config.BrowserCache {
	return config.BrowserCache{
		HTML:      config.DefaultBrowserCacheHTML,
		Immutable: config.DefaultBrowserCacheImmutable,
	}
}

func TestBrowserCacheForDefaults(t *testing.T) {
	cfg := defaultBrowserCache()
	tests := []struct {
		name     string
		urlPath  string
		filePath string
		want     string
	}{
		{"html revalidates", "/", "/srv/index.html", "no-cache"},
		{"htm revalidates", "/about.htm", "/srv/about.htm", "no-cache"},
		{"extensionless revalidates", "/about", "/srv/about", "no-cache"},
		// Nothing is claimed about ordinary assets until an operator opts in:
		// pinning a file that gets edited in place is the failure to avoid.
		{"css untouched", "/style.css", "/srv/style.css", ""},
		{"js untouched", "/app.js", "/srv/app.js", ""},
		{"image untouched", "/logo.png", "/srv/logo.png", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BrowserCacheFor(cfg, tt.urlPath, tt.filePath); got != tt.want {
				t.Errorf("= %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBrowserCacheForImmutablePaths(t *testing.T) {
	cfg := defaultBrowserCache()
	cfg.ImmutablePaths = []string{"/assets/*", "/_next/static/*", "*.woff2"}

	tests := []struct {
		name     string
		urlPath  string
		filePath string
		want     string
	}{
		{"prefix match", "/assets/app.4f2a1c.js", "/srv/assets/app.4f2a1c.js", config.DefaultBrowserCacheImmutable},
		{"prefix matches nested dirs", "/assets/img/hero.a1b2.webp", "/srv/assets/img/hero.a1b2.webp", config.DefaultBrowserCacheImmutable},
		{"second prefix", "/_next/static/chunks/main.js", "/srv/_next/static/chunks/main.js", config.DefaultBrowserCacheImmutable},
		{"glob match", "/fonts/inter.woff2", "/srv/fonts/inter.woff2", config.DefaultBrowserCacheImmutable},
		{"outside the prefix", "/other/app.js", "/srv/other/app.js", ""},
		// HTML wins over an immutable prefix — an SPA shell under /assets/
		// still has to revalidate or the site can never update.
		{"html inside immutable dir", "/assets/index.html", "/srv/assets/index.html", "no-cache"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BrowserCacheFor(cfg, tt.urlPath, tt.filePath); got != tt.want {
				t.Errorf("= %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBrowserCacheForAssetsOverride(t *testing.T) {
	cfg := defaultBrowserCache()
	cfg.Assets = "public, max-age=3600"
	if got := BrowserCacheFor(cfg, "/style.css", "/srv/style.css"); got != "public, max-age=3600" {
		t.Errorf("= %q, want the configured assets value", got)
	}
	if got := BrowserCacheFor(cfg, "/", "/srv/index.html"); got != "no-cache" {
		t.Errorf("html = %q, want no-cache — assets must not swallow HTML", got)
	}
}

func TestBrowserCacheForDisabled(t *testing.T) {
	cfg := defaultBrowserCache()
	cfg.Enabled = config.BoolPtr(false)
	for _, p := range []string{"/srv/index.html", "/srv/style.css", "/srv/app.js"} {
		if got := BrowserCacheFor(cfg, p, p); got != "" {
			t.Errorf("%s = %q, want empty when disabled", p, got)
		}
	}
}

func TestMatchURLPattern(t *testing.T) {
	tests := []struct {
		pattern, urlPath string
		want             bool
	}{
		{"/assets/*", "/assets/a.js", true},
		{"/assets/*", "/assets/deep/a.js", true},
		// The trimmed prefix keeps its slash, so a sibling directory whose name
		// merely starts the same way is not swept in.
		{"/assets/*", "/assetsx/a.js", false},
		{"/assets/*", "/other/a.js", false},
		{"*.woff2", "/f/inter.woff2", true},
		{"*.woff2", "/f/inter.woff", false},
		{"/exact.js", "/exact.js", true},
		{"/exact.js", "/other.js", false},
		{"", "/a.js", false},
	}
	for _, tt := range tests {
		if got := matchURLPattern(tt.pattern, tt.urlPath); got != tt.want {
			t.Errorf("matchURLPattern(%q, %q) = %v, want %v", tt.pattern, tt.urlPath, got, tt.want)
		}
	}
}

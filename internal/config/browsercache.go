package config

// BrowserCache controls the Cache-Control header UWAS puts on static
// responses — how long the *browser* may reuse a file without asking again.
//
// This is a different lever from `cache:`, which decides what the server keeps
// in memory. The server cache makes a response cheap to produce; Cache-Control
// removes the request altogether, which is the larger win and the reason this
// defaults to on.
//
// The defaults are deliberately conservative. HTML gets no-cache, so pages
// revalidate (cheaply, via ETag → 304) instead of being served stale from a
// browser's heuristic cache — which is what happens today when no header is
// sent at all. Everything else is left alone unless the operator opts in:
// guessing which of a site's assets are content-hashed and pinning the wrong
// one for a year is not a mistake a web server should make on its own.
type BrowserCache struct {
	// Enabled is a pointer so "absent" and "explicitly false" stay
	// distinguishable; absent means enabled.
	Enabled *bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`

	// HTML applies to .html, .htm and extensionless URLs.
	HTML string `yaml:"html,omitempty" json:"html,omitempty"`

	// Assets applies to every other static file. Empty means UWAS sends no
	// Cache-Control at all, leaving the browser to revalidate — today's
	// behavior, and the safe default for files that change in place.
	Assets string `yaml:"assets,omitempty" json:"assets,omitempty"`

	// ImmutablePaths lists URL path patterns whose files are content-addressed
	// — build output like /assets/app.4f2a1c.js, where the name changes
	// whenever the bytes do. A trailing * matches everything below the prefix.
	// Point this at a build directory, never at a directory whose files are
	// edited in place: Immutable tells browsers not to revalidate for a year.
	ImmutablePaths []string `yaml:"immutable_paths,omitempty" json:"immutable_paths,omitempty"`

	// Immutable is the value used for ImmutablePaths matches.
	Immutable string `yaml:"immutable,omitempty" json:"immutable,omitempty"`
}

// BrowserCacheEnabled reports whether UWAS should set Cache-Control for this
// domain. Absent configuration means yes.
func (b BrowserCache) BrowserCacheEnabled() bool {
	return b.Enabled == nil || *b.Enabled
}

// Default Cache-Control values, applied by applyDefaults.
const (
	// DefaultBrowserCacheHTML keeps pages correct: the browser may reuse its
	// copy but must revalidate first, which the ETag turns into a 304.
	DefaultBrowserCacheHTML = "no-cache"

	// DefaultBrowserCacheImmutable is a year, the longest value RFC 8246
	// treats as meaningful, plus immutable to suppress revalidation on
	// reload. Only ever applied to paths the operator listed.
	DefaultBrowserCacheImmutable = "public, max-age=31536000, immutable"
)

// StaticFileCache bounds the in-memory cache of static file bytes that the
// static handler keeps, so a repeatedly requested file is served without
// touching the filesystem.
//
// Distinct from `cache:`, which stores whole rendered responses per domain and
// can be switched off per site. This one is server-wide and sits underneath
// every static handler path.
type StaticFileCache struct {
	// Enabled is a pointer so absent and explicitly-false stay
	// distinguishable; absent means enabled.
	Enabled *bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`

	// MaxFileSize is the largest file kept in memory. Bigger files stream from
	// disk: the cache exists for repeated small assets, not for one download.
	MaxFileSize ByteSize `yaml:"max_file_size,omitempty" json:"max_file_size,omitempty"`

	// MaxBytes caps total memory held across all cached files.
	MaxBytes ByteSize `yaml:"max_bytes,omitempty" json:"max_bytes,omitempty"`

	// Revalidate is how long a cached file is trusted before its size and
	// mtime are checked again. Inside the window a hit costs no syscall; past
	// it, one Stat. Zero means the default.
	Revalidate Duration `yaml:"revalidate,omitempty" json:"revalidate,omitempty"`
}

// StaticFileCacheEnabled reports whether the file cache should run. Absent
// configuration means yes.
func (s StaticFileCache) StaticFileCacheEnabled() bool {
	return s.Enabled == nil || *s.Enabled
}

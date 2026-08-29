package config

import "testing"

// A PUT that never mentions browser_cache must leave it alone; one that does
// must be able to change it, including clearing it back to nothing.
func TestMergeDomainBrowserCache(t *testing.T) {
	existing := Domain{
		Host: "a.test",
		BrowserCache: BrowserCache{
			HTML:           "no-cache",
			ImmutablePaths: []string{"/assets/*"},
		},
	}

	t.Run("absent key preserves", func(t *testing.T) {
		out := MergeDomain(existing, Domain{Host: "a.test"}, DomainPatchFields{}, false)
		if len(out.BrowserCache.ImmutablePaths) != 1 {
			t.Errorf("immutable_paths = %v, want preserved", out.BrowserCache.ImmutablePaths)
		}
	})

	t.Run("present key replaces", func(t *testing.T) {
		patch := Domain{BrowserCache: BrowserCache{HTML: "no-store", ImmutablePaths: []string{"/static/*"}}}
		out := MergeDomain(existing, patch, DomainPatchFields{HasBrowserCache: true}, false)
		if out.BrowserCache.HTML != "no-store" {
			t.Errorf("html = %q, want no-store", out.BrowserCache.HTML)
		}
		if len(out.BrowserCache.ImmutablePaths) != 1 || out.BrowserCache.ImmutablePaths[0] != "/static/*" {
			t.Errorf("immutable_paths = %v, want [/static/*]", out.BrowserCache.ImmutablePaths)
		}
	})

	t.Run("present key can disable", func(t *testing.T) {
		patch := Domain{BrowserCache: BrowserCache{Enabled: BoolPtr(false)}}
		out := MergeDomain(existing, patch, DomainPatchFields{HasBrowserCache: true}, false)
		if out.BrowserCache.BrowserCacheEnabled() {
			t.Error("browser cache still enabled after an explicit disable")
		}
	})

	t.Run("replace mode takes patch verbatim", func(t *testing.T) {
		out := MergeDomain(existing, Domain{Host: "a.test"}, DomainPatchFields{}, true)
		if len(out.BrowserCache.ImmutablePaths) != 0 {
			t.Errorf("replace mode kept old values: %+v", out.BrowserCache)
		}
	})
}

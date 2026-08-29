package server

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/uwaserver/uwas/internal/cache"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// siteTagServer builds a cache-enabled static domain with no operator tags —
// the configuration the regression below is about.
func siteTagServer(t *testing.T, host string, tags []string) *Server {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "page.html"), []byte("body"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
			Cache: config.CacheConfig{
				Enabled:     true,
				MemoryLimit: config.ByteSize(10 * 1024 * 1024),
			},
		},
		Domains: []config.Domain{{
			Host:  host,
			Root:  dir,
			Type:  "static",
			SSL:   config.SSLConfig{Mode: "off"},
			Cache: config.DomainCache{Enabled: true, TTL: 120, Tags: tags},
		}},
	}
	s := New(cfg, logger.New("error", "text"))
	t.Cleanup(func() { s.cancel() })
	return s
}

func cachedEntries(s *Server) int64 { return s.cache.Stats()["entries"] }

func primeCache(t *testing.T, s *Server, host string) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/page.html", nil)
	req.Host = host
	s.handleRequest(rec, req)
	if rec.Code != 200 {
		t.Fatalf("priming request: status = %d, want 200", rec.Code)
	}
	if cachedEntries(s) == 0 {
		t.Fatal("priming request did not populate the cache")
	}
}

// TestPurgeByHostWithoutOperatorTags is the regression for the silent
// per-domain purge. Entries used to carry only the operator's configured
// tags, so purging an untagged domain matched nothing, removed nothing and
// still reported success.
func TestPurgeByHostWithoutOperatorTags(t *testing.T) {
	const host = "untagged.test"
	s := siteTagServer(t, host, nil)
	primeCache(t, s, host)

	count := s.cache.PurgeByTag(cache.SiteTag(host))
	if count == 0 {
		t.Fatal("purge by host removed 0 entries for a domain with no operator tags")
	}
	if n := cachedEntries(s); n != 0 {
		t.Errorf("cache still holds %d entries after purging the only domain", n)
	}
}

// TestPurgeByHostNormalizesHost covers the second half of the old bug: the
// caller's host casing and port must not matter.
func TestPurgeByHostNormalizesHost(t *testing.T) {
	const host = "mixed.test"
	s := siteTagServer(t, host, nil)
	primeCache(t, s, host)

	if count := s.cache.PurgeByTag(cache.SiteTag("MIXED.test:8080")); count == 0 {
		t.Fatal("purge with uppercase host and explicit port removed 0 entries")
	}
}

// TestPurgeByHostKeepsOperatorTags checks the implicit tag is additive: a
// domain tagged by hand stays purgeable by that tag too.
func TestPurgeByHostKeepsOperatorTags(t *testing.T) {
	const host = "both.test"
	s := siteTagServer(t, host, []string{"release-2024"})
	primeCache(t, s, host)

	if count := s.cache.PurgeByTag("release-2024"); count == 0 {
		t.Fatal("operator tag no longer purges after adding the implicit site tag")
	}
}

// TestCacheTagsForDoesNotMutateConfig guards the shared config slice: appending
// the implicit tag in place would leak it into the domain's configuration and
// grow it on every request.
func TestCacheTagsForDoesNotMutateConfig(t *testing.T) {
	domain := &config.Domain{
		Host:  "shared.test",
		Cache: config.DomainCache{Tags: []string{"a"}},
	}
	first := cacheTagsFor(domain, "shared.test")
	second := cacheTagsFor(domain, "other.test")

	if len(domain.Cache.Tags) != 1 || domain.Cache.Tags[0] != "a" {
		t.Fatalf("config tags mutated: %v", domain.Cache.Tags)
	}
	if first[len(first)-1] == second[len(second)-1] {
		t.Fatalf("both calls produced the same site tag: %v / %v", first, second)
	}
}

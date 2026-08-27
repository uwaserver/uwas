package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// NOT: cache.rules[].match bir REGEX'tir, glob değil. "*.html" geçersiz bir
// regex olduğu için matchPath sessizce false döner — testler de bu yüzden
// önce eşleşmiyordu. Desen doğrulaması validate.go'ya eklendi.
//
// Two cache settings were dead configuration: a cache rule's `ttl` and
// `global.cache.default_ttl`. The store path hardcoded a 60 second fallback and
// read neither, so an operator could set them in the dashboard, see them echoed
// back by the API, and get no change in behaviour.
//
// These tests assert the resolved TTL that actually lands on the cache entry,
// so they fail if the settings go back to being ignored.

func TestCacheTTLForPrecedence(t *testing.T) {
	cases := []struct {
		ad       string
		rule     int
		domain   int
		global   int
		beklenen time.Duration
	}{
		{"kural her şeyi ezer", 30, 120, 3600, 30 * time.Second},
		{"kural yoksa domain", 0, 120, 3600, 120 * time.Second},
		{"domain yoksa global", 0, 0, 3600, 3600 * time.Second},
		{"hiçbiri yoksa bir dakika", 0, 0, 0, 60 * time.Second},
		{"negatif değerler yok sayılır", -5, -1, 900, 900 * time.Second},
	}

	for _, c := range cases {
		t.Run(c.ad, func(t *testing.T) {
			got := cacheTTLFor(c.rule, c.domain, c.global)
			if got != c.beklenen {
				t.Errorf("cacheTTLFor(%d, %d, %d) = %v, want %v",
					c.rule, c.domain, c.global, got, c.beklenen)
			}
		})
	}
}

// ttlFixture serves one static file through the full middleware chain and
// returns the server so the test can inspect what landed in the cache.
func ttlFixture(t *testing.T, globalDefaultTTL, domainTTL int, rules []config.CacheRule) (*Server, http.Handler) {
	t.Helper()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"),
		[]byte("<!doctype html><title>ttl</title>"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
			// A memory limit is required: with the default 0 the L1 store keeps
			// nothing, every request is a MISS and nothing is ever stored — the
			// assertions below would pass vacuously.
			Cache: config.CacheConfig{
				Enabled:     true,
				MemoryLimit: config.ByteSize(64 << 20),
				DefaultTTL:  globalDefaultTTL,
			},
		},
		Domains: []config.Domain{{
			Host:  "ttl.test",
			Type:  "static",
			Root:  root,
			SSL:   config.SSLConfig{Mode: "off"},
			Cache: config.DomainCache{Enabled: true, TTL: domainTTL, Rules: rules},
		}},
	}

	s := New(cfg, logger.New("error", "text"))
	t.Cleanup(func() { s.cancel() })
	return s, s.buildMiddlewareChain()
}

// istekYap sends one request through the chain. The bot guard answers an
// empty User-Agent with 403, so it must be set.
func istekYap(h http.Handler, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Host = "ttl.test"
	req.Header.Set("User-Agent", "uwas-test")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// saklananTTL issues one request and returns the TTL of the entry it stored.
func saklananTTL(t *testing.T, s *Server, h http.Handler, path string) time.Duration {
	t.Helper()

	rec := istekYap(h, path)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s durum %d döndü, 200 bekleniyordu", path, rec.Code)
	}

	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Host = "ttl.test"
	entry, _ := s.cache.Get(req)
	if entry == nil {
		t.Fatalf("%s önbelleğe yazılmadı — test boşa geçemez", path)
	}
	return entry.TTL
}

func TestCacheEntryUsesGlobalDefaultTTL(t *testing.T) {
	// Domain leaves ttl unset; global.cache.default_ttl must apply.
	s, h := ttlFixture(t, 1800, 0, nil)

	if got := saklananTTL(t, s, h, "/index.html"); got != 1800*time.Second {
		t.Errorf("saklanan TTL = %v, want 30m (global.cache.default_ttl)", got)
	}
}

func TestCacheEntryUsesDomainTTLOverGlobal(t *testing.T) {
	s, h := ttlFixture(t, 1800, 300, nil)

	if got := saklananTTL(t, s, h, "/index.html"); got != 300*time.Second {
		t.Errorf("saklanan TTL = %v, want 5m (domain cache.ttl)", got)
	}
}

func TestCacheEntryUsesMatchingRuleTTL(t *testing.T) {
	// The rule matches and must win over both the domain and the global value.
	s, h := ttlFixture(t, 1800, 300, []config.CacheRule{
		{Match: `\.html$`, TTL: 45},
	})

	if got := saklananTTL(t, s, h, "/index.html"); got != 45*time.Second {
		t.Errorf("saklanan TTL = %v, want 45s (eşleşen kural)", got)
	}
}

func TestCacheEntryIgnoresNonMatchingRuleTTL(t *testing.T) {
	// A rule that does not match must leave the domain value in place —
	// otherwise the rule TTL would leak onto every path.
	s, h := ttlFixture(t, 1800, 300, []config.CacheRule{
		{Match: `\.css$`, TTL: 45},
	})

	if got := saklananTTL(t, s, h, "/index.html"); got != 300*time.Second {
		t.Errorf("saklanan TTL = %v, want 5m (kural eşleşmiyor)", got)
	}
}

func TestCacheBypassRuleStillWins(t *testing.T) {
	// Adding a ttl to the rule loop must not disturb bypass: a bypassed path
	// is not cached at all.
	s, h := ttlFixture(t, 1800, 300, []config.CacheRule{
		{Match: `\.html$`, Bypass: true, TTL: 45},
	})

	if rec := istekYap(h, "/index.html"); rec.Code != http.StatusOK {
		t.Fatalf("durum %d döndü, 200 bekleniyordu", rec.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/index.html", nil)
	req.Host = "ttl.test"
	if entry, _ := s.cache.Get(req); entry != nil {
		t.Errorf("bypass edilen yol önbelleğe yazıldı (TTL %v)", entry.TTL)
	}
}

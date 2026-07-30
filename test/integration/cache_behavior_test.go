package integration

// Cache behavior integration tests.
//
// These tests verify the real cache engine as a behavior contract — not code
// paths for coverage. They replace the coverage-chasing unit tests in
// internal/cache/coverage*_test.go with scenario-driven assertions.
//
// What these tests prove:
//   1. A cached response is served on subsequent identical requests
//   2. Expired entries are not served as fresh (but stale-while-revalidate works)
//   3. Tag-based purging removes only matching entries
//   4. PurgeAll clears everything
//   5. POST requests bypass the cache
//   6. .php requests bypass the cache
//   7. Vary headers produce separate cache entries

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/cache"
	"github.com/uwaserver/uwas/internal/logger"
)

func newTestEngine(t *testing.T, diskPath string) *cache.Engine {
	t.Helper()
	log := logger.New("error", "text")
	ctx := context.Background()
	e := cache.NewEngine(ctx, 64*1024*1024, diskPath, 256*1024*1024, log)
	e.SetVaryHeaders(nil) // default: Accept-Encoding only
	return e
}

// TestCacheLifecycle_SetGetHit verifies the fundamental cache contract:
// store a response, retrieve it on the next identical request.
func TestCacheLifecycle_SetGetHit(t *testing.T) {
	e := newTestEngine(t, "")

	req := httptest.NewRequest(http.MethodGet, "https://example.com/page", nil)
	req.Header.Set("Accept-Encoding", "gzip")

	resp := &cache.CachedResponse{
		StatusCode: 200,
		Headers:    http.Header{"Content-Type": []string{"text/html"}},
		Body:       []byte("<h1>Cached Page</h1>"),
		Created:    time.Now(),
		TTL:        60 * time.Second,
	}
	e.Set(req, resp)

	got, status := e.Get(req)
	if got == nil || status != cache.StatusHit {
		t.Fatalf("expected HIT, got status=%q resp=%v", status, got)
	}
	if got.StatusCode != 200 {
		t.Errorf("status code = %d, want 200", got.StatusCode)
	}
	if string(got.Body) != "<h1>Cached Page</h1>" {
		t.Errorf("body mismatch: got %q", string(got.Body))
	}
}

// TestCacheLifecycle_DifferentPathsAreSeparate verifies that two different
// URLs produce independent cache entries.
func TestCacheLifecycle_DifferentPathsAreSeparate(t *testing.T) {
	e := newTestEngine(t, "")

	reqA := httptest.NewRequest(http.MethodGet, "https://example.com/a", nil)
	reqB := httptest.NewRequest(http.MethodGet, "https://example.com/b", nil)

	e.Set(reqA, &cache.CachedResponse{
		StatusCode: 200, Body: []byte("A"),
		Created: time.Now(), TTL: 60 * time.Second,
	})
	e.Set(reqB, &cache.CachedResponse{
		StatusCode: 200, Body: []byte("B"),
		Created: time.Now(), TTL: 60 * time.Second,
	})

	gotA, statusA := e.Get(reqA)
	if statusA != cache.StatusHit || string(gotA.Body) != "A" {
		t.Errorf("path A: status=%q body=%q", statusA, gotA.Body)
	}

	gotB, statusB := e.Get(reqB)
	if statusB != cache.StatusHit || string(gotB.Body) != "B" {
		t.Errorf("path B: status=%q body=%q", statusB, gotB.Body)
	}
}

// TestCacheLifecycle_ExpiredNotFresh verifies that an expired entry is not
// served as a fresh hit.
func TestCacheLifecycle_ExpiredNotFresh(t *testing.T) {
	e := newTestEngine(t, "")

	req := httptest.NewRequest(http.MethodGet, "https://example.com/expired", nil)

	e.Set(req, &cache.CachedResponse{
		StatusCode: 200,
		Body:       []byte("old"),
		Created:    time.Now().Add(-2 * time.Minute),
		TTL:        1 * time.Second, // already expired
	})

	got, status := e.Get(req)
	// An expired entry can be served as STALE (grace period) or MISS.
	// Either way, it must NOT be served as a fresh HIT.
	if status == cache.StatusHit {
		t.Fatalf("expired entry served as HIT — should be STALE or MISS")
	}
	if got != nil && status == cache.StatusStale {
		// Stale serving is acceptable (stale-while-revalidate).
		if string(got.Body) != "old" {
			t.Errorf("stale body mismatch")
		}
	}
}

// TestCacheLifecycle_PurgeByTag verifies that tag-based purging removes only
// entries matching the tag.
func TestCacheLifecycle_PurgeByTag(t *testing.T) {
	e := newTestEngine(t, "")

	// Entry with tag "blog"
	reqTagged := httptest.NewRequest(http.MethodGet, "https://example.com/blog/post1", nil)
	e.Set(reqTagged, &cache.CachedResponse{
		StatusCode: 200, Body: []byte("blog1"),
		Created: time.Now(), TTL: 60 * time.Second,
		Tags: []string{"blog"},
	})

	// Entry without tag
	reqUntagged := httptest.NewRequest(http.MethodGet, "https://example.com/static/page", nil)
	e.Set(reqUntagged, &cache.CachedResponse{
		StatusCode: 200, Body: []byte("static"),
		Created: time.Now(), TTL: 60 * time.Second,
	})

	// Purge only "blog" tagged entries
	purged := e.PurgeByTag("blog")
	if purged < 1 {
		t.Errorf("expected at least 1 purge, got %d", purged)
	}

	// Blog entry should be gone
	if got, status := e.Get(reqTagged); status == cache.StatusHit {
		t.Errorf("tagged entry still served after purge: %v", got)
	}

	// Static entry should remain
	gotStatic, statusStatic := e.Get(reqUntagged)
	if statusStatic != cache.StatusHit {
		t.Errorf("untagged entry purged — should still be cached: status=%q", statusStatic)
	}
	if string(gotStatic.Body) != "static" {
		t.Errorf("static body mismatch: %q", gotStatic.Body)
	}
}

// TestCacheLifecycle_PurgeAll verifies that PurgeAll removes all entries.
func TestCacheLifecycle_PurgeAll(t *testing.T) {
	e := newTestEngine(t, "")

	for _, path := range []string{"/a", "/b", "/c", "/d"} {
		req := httptest.NewRequest(http.MethodGet, "https://example.com"+path, nil)
		e.Set(req, &cache.CachedResponse{
			StatusCode: 200, Body: []byte(path),
			Created: time.Now(), TTL: 60 * time.Second,
		})
	}

	e.PurgeAll()

	for _, path := range []string{"/a", "/b", "/c", "/d"} {
		req := httptest.NewRequest(http.MethodGet, "https://example.com"+path, nil)
		if got, status := e.Get(req); status == cache.StatusHit {
			t.Errorf("entry %q still cached after PurgeAll: %v", path, got)
		}
	}
}

// TestCacheBypass_POSTRequests verifies that POST requests are classified as
// cache-bypassing — they should never be cached or served from cache.
func TestCacheBypass_POSTRequests(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "https://example.com/api/submit", nil)
	if !cache.ShouldBypass(req) {
		t.Error("POST request should bypass cache")
	}
}

// TestCacheBypass_PHPRequests verifies that .php requests bypass the cache.
func TestCacheBypass_PHPRequests(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://example.com/index.php?page=1", nil)
	if !cache.ShouldBypass(req) {
		t.Error(".php request should bypass cache")
	}
}

// TestCacheBypass_NoCacheHeader verifies that Cache-Control: no-cache bypasses.
func TestCacheBypass_NoCacheHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://example.com/page", nil)
	req.Header.Set("Cache-Control", "no-cache")
	if !cache.ShouldBypass(req) {
		t.Error("Cache-Control: no-cache should bypass")
	}
}

// TestCacheBypass_GETAllowed verifies that normal GET requests are cacheable.
func TestCacheBypass_GETAllowed(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://example.com/page.html", nil)
	if cache.ShouldBypass(req) {
		t.Error("normal GET should NOT bypass cache")
	}
}

// TestCacheVary_DifferentAcceptEncoding verifies that requests with different
// Accept-Encoding produce separate cache entries.
func TestCacheVary_DifferentAcceptEncoding(t *testing.T) {
	e := newTestEngine(t, "")

	// Store a gzip response
	reqGzip := httptest.NewRequest(http.MethodGet, "https://example.com/compressed", nil)
	reqGzip.Header.Set("Accept-Encoding", "gzip")
	e.Set(reqGzip, &cache.CachedResponse{
		StatusCode: 200, Body: []byte("gzip-version"),
		Created: time.Now(), TTL: 60 * time.Second,
	})

	// Store a br response for the same URL
	reqBr := httptest.NewRequest(http.MethodGet, "https://example.com/compressed", nil)
	reqBr.Header.Set("Accept-Encoding", "br")
	e.Set(reqBr, &cache.CachedResponse{
		StatusCode: 200, Body: []byte("br-version"),
		Created: time.Now(), TTL: 60 * time.Second,
	})

	// gzip request should get gzip-version
	gotGzip, statusGzip := e.Get(reqGzip)
	if statusGzip != cache.StatusHit {
		t.Fatalf("gzip variant: expected HIT, got %q", statusGzip)
	}
	if string(gotGzip.Body) != "gzip-version" {
		t.Errorf("gzip variant body = %q, want %q", gotGzip.Body, "gzip-version")
	}

	// br request should get br-version
	gotBr, statusBr := e.Get(reqBr)
	if statusBr != cache.StatusHit {
		t.Fatalf("br variant: expected HIT, got %q", statusBr)
	}
	if string(gotBr.Body) != "br-version" {
		t.Errorf("br variant body = %q, want %q", gotBr.Body, "br-version")
	}
}

// TestCacheIsCacheable_RejectsPrivate verifies that responses with
// Cache-Control: private are not cached.
func TestCacheIsCacheable_RejectsPrivate(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://example.com/private", nil)
	hdrs := http.Header{}
	hdrs.Set("Cache-Control", "private")
	if cache.IsCacheable(req, 200, hdrs) {
		t.Error("private response should not be cacheable")
	}
}

// TestCacheIsCacheable_RejectsSetCookie verifies that responses with Set-Cookie
// are not cached (to avoid serving personalized content to other users).
func TestCacheIsCacheable_RejectsSetCookie(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://example.com/loggedin", nil)
	hdrs := http.Header{}
	hdrs.Set("Set-Cookie", "session=abc123")
	if cache.IsCacheable(req, 200, hdrs) {
		t.Error("Set-Cookie response should not be cacheable")
	}
}

// TestCacheIsCacheable_RejectsPOST verifies that POST responses are not cached.
func TestCacheIsCacheable_RejectsPOST(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "https://example.com/submit", nil)
	if cache.IsCacheable(req, 200, http.Header{}) {
		t.Error("POST response should not be cacheable")
	}
}

// TestCacheIsCacheable_Accepts200 verifies that normal 200 GET responses
// without session indicators are cacheable.
func TestCacheIsCacheable_Accepts200(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://example.com/article", nil)
	hdrs := http.Header{"Content-Type": []string{"text/html"}}
	if !cache.IsCacheable(req, 200, hdrs) {
		t.Error("normal 200 GET should be cacheable")
	}
}

// TestCacheIsCacheable_Accepts301 verifies that permanent redirects are cached.
func TestCacheIsCacheable_Accepts301(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://example.com/old-path", nil)
	hdrs := http.Header{"Location": []string{"/new-path"}}
	if !cache.IsCacheable(req, 301, hdrs) {
		t.Error("301 redirect should be cacheable")
	}
}

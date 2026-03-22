package cache

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/logger"
)

// --- Key ---

func TestGenerateKeySame(t *testing.T) {
	r1 := httptest.NewRequest("GET", "/page?b=2&a=1", nil)
	r2 := httptest.NewRequest("GET", "/page?a=1&b=2", nil)

	k1 := GenerateKey(r1, nil)
	k2 := GenerateKey(r2, nil)

	if k1 != k2 {
		t.Errorf("same request different query order should produce same key: %q != %q", k1, k2)
	}
}

func TestGenerateKeyDifferent(t *testing.T) {
	r1 := httptest.NewRequest("GET", "/page1", nil)
	r2 := httptest.NewRequest("GET", "/page2", nil)

	k1 := GenerateKey(r1, nil)
	k2 := GenerateKey(r2, nil)

	if k1 == k2 {
		t.Error("different paths should produce different keys")
	}
}

func TestGenerateKeyVary(t *testing.T) {
	r1 := httptest.NewRequest("GET", "/page", nil)
	r1.Header.Set("Accept-Encoding", "gzip")

	r2 := httptest.NewRequest("GET", "/page", nil)
	r2.Header.Set("Accept-Encoding", "br")

	k1 := GenerateKey(r1, []string{"Accept-Encoding"})
	k2 := GenerateKey(r2, []string{"Accept-Encoding"})

	if k1 == k2 {
		t.Error("different Vary headers should produce different keys")
	}
}

func TestKeyPrefix(t *testing.T) {
	d1, d2 := KeyPrefix("GET|example.com|/page|")
	if len(d1) != 2 || len(d2) != 2 {
		t.Errorf("prefix lengths = %d/%d, want 2/2", len(d1), len(d2))
	}
	// KeyPrefix should be deterministic
	d1b, d2b := KeyPrefix("GET|example.com|/page|")
	if d1 != d1b || d2 != d2b {
		t.Errorf("KeyPrefix not deterministic: %q/%q vs %q/%q", d1, d2, d1b, d2b)
	}
}

// --- Entry ---

func TestCachedResponseFreshStaleExpired(t *testing.T) {
	r := &CachedResponse{
		Created:  time.Now(),
		TTL:      1 * time.Second,
		GraceTTL: 1 * time.Second,
	}

	if !r.IsFresh() {
		t.Error("should be fresh")
	}
	if r.IsStale() {
		t.Error("should not be stale")
	}

	// Wait for TTL to expire
	time.Sleep(1100 * time.Millisecond)

	if r.IsFresh() {
		t.Error("should not be fresh after TTL")
	}
	if !r.IsStale() {
		t.Error("should be stale within grace")
	}
}

func TestSerializeDeserialize(t *testing.T) {
	resp := &CachedResponse{
		StatusCode: 200,
		Headers:    http.Header{"Content-Type": {"text/html"}, "X-Custom": {"val"}},
		Body:       []byte("<h1>Hello</h1>"),
		Created:    time.Now().Truncate(time.Nanosecond),
		TTL:        5 * time.Minute,
		GraceTTL:   1 * time.Hour,
	}

	data := resp.Serialize()
	restored, err := Deserialize(data)
	if err != nil {
		t.Fatalf("Deserialize: %v", err)
	}

	if restored.StatusCode != 200 {
		t.Errorf("status = %d, want 200", restored.StatusCode)
	}
	if restored.Headers.Get("Content-Type") != "text/html" {
		t.Errorf("Content-Type = %q", restored.Headers.Get("Content-Type"))
	}
	if string(restored.Body) != "<h1>Hello</h1>" {
		t.Errorf("body = %q", string(restored.Body))
	}
	if restored.TTL != 5*time.Minute {
		t.Errorf("TTL = %v", restored.TTL)
	}
}

func TestDeserializeCorrupt(t *testing.T) {
	_, err := Deserialize([]byte{0, 1, 2})
	if err == nil {
		t.Error("should return error for corrupt data")
	}
}

func TestResponseSize(t *testing.T) {
	r := &CachedResponse{
		Headers: http.Header{"Content-Type": {"text/html"}},
		Body:    make([]byte, 1000),
	}
	size := r.Size()
	if size < 1000 {
		t.Errorf("size = %d, should be >= 1000", size)
	}
}

// --- Memory Cache ---

func TestMemoryCacheSetGet(t *testing.T) {
	mc := NewMemoryCache(1 << 20) // 1MB

	resp := &CachedResponse{
		StatusCode: 200,
		Body:       []byte("hello"),
		Created:    time.Now(),
		TTL:        1 * time.Minute,
	}

	mc.Set("test-key", resp)

	got, status := mc.Get("test-key")
	if status != StatusHit {
		t.Errorf("status = %q, want HIT", status)
	}
	if string(got.Body) != "hello" {
		t.Errorf("body = %q", string(got.Body))
	}
}

func TestMemoryCacheMiss(t *testing.T) {
	mc := NewMemoryCache(1 << 20)

	_, status := mc.Get("nonexistent")
	if status != StatusMiss {
		t.Errorf("status = %q, want MISS", status)
	}
}

func TestMemoryCacheLRUEviction(t *testing.T) {
	// Each entry: 100 body + 64 overhead = 164 bytes
	// Limit 10000 should hold ~60 entries, but we insert 200
	mc := NewMemoryCache(10000)

	for i := 0; i < 200; i++ {
		resp := &CachedResponse{
			Body:    make([]byte, 100),
			Created: time.Now(),
			TTL:     1 * time.Minute,
		}
		mc.Set(formatKey(uint64(i)), resp)
	}

	// Should have evicted to stay near limit
	if mc.usedBytes.Load() > 12000 {
		t.Errorf("used bytes = %d, should be <= ~10000", mc.usedBytes.Load())
	}
	if mc.Len() >= 200 {
		t.Errorf("entries = %d, should be < 200 (eviction happened)", mc.Len())
	}
}

func TestMemoryCacheDelete(t *testing.T) {
	mc := NewMemoryCache(1 << 20)

	resp := &CachedResponse{
		Body:    []byte("delete-me"),
		Created: time.Now(),
		TTL:     1 * time.Minute,
	}
	mc.Set("del-key", resp)
	mc.Delete("del-key")

	_, status := mc.Get("del-key")
	if status != StatusMiss {
		t.Errorf("status = %q after delete, want MISS", status)
	}
}

func TestMemoryCachePurgeByTag(t *testing.T) {
	mc := NewMemoryCache(1 << 20)

	mc.Set("a", &CachedResponse{Body: []byte("a"), Created: time.Now(), TTL: time.Minute, Tags: []string{"blog"}})
	mc.Set("b", &CachedResponse{Body: []byte("b"), Created: time.Now(), TTL: time.Minute, Tags: []string{"blog"}})
	mc.Set("c", &CachedResponse{Body: []byte("c"), Created: time.Now(), TTL: time.Minute, Tags: []string{"shop"}})

	count := mc.PurgeByTag("blog")
	if count != 2 {
		t.Errorf("purged = %d, want 2", count)
	}

	if mc.Len() != 1 {
		t.Errorf("remaining = %d, want 1", mc.Len())
	}
}

func TestMemoryCachePurgeAll(t *testing.T) {
	mc := NewMemoryCache(1 << 20)

	for i := 0; i < 10; i++ {
		mc.Set(formatKey(uint64(i)), &CachedResponse{Body: []byte("x"), Created: time.Now(), TTL: time.Minute})
	}

	mc.PurgeAll()

	if mc.Len() != 0 {
		t.Errorf("entries = %d after purge, want 0", mc.Len())
	}
	if mc.usedBytes.Load() != 0 {
		t.Errorf("usedBytes = %d after purge, want 0", mc.usedBytes.Load())
	}
}

// --- Disk Cache ---

func TestDiskCacheSetGet(t *testing.T) {
	dir := t.TempDir()
	dc := NewDiskCache(dir, 1<<20)

	resp := &CachedResponse{
		StatusCode: 200,
		Headers:    http.Header{"Content-Type": {"text/html"}},
		Body:       []byte("<h1>Cached</h1>"),
		Created:    time.Now(),
		TTL:        5 * time.Minute,
		GraceTTL:   1 * time.Hour,
	}

	if err := dc.Set("disk-key-1234567890abcdef", resp); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, err := dc.Get("disk-key-1234567890abcdef")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.StatusCode != 200 {
		t.Errorf("status = %d", got.StatusCode)
	}
	if string(got.Body) != "<h1>Cached</h1>" {
		t.Errorf("body = %q", string(got.Body))
	}
}

func TestDiskCacheDelete(t *testing.T) {
	dir := t.TempDir()
	dc := NewDiskCache(dir, 1<<20)

	resp := &CachedResponse{Body: []byte("x"), Created: time.Now(), TTL: time.Minute}
	dc.Set("del-key-1234567890abcdef", resp)
	dc.Delete("del-key-1234567890abcdef")

	_, err := dc.Get("del-key-1234567890abcdef")
	if err == nil {
		t.Error("should return error after delete")
	}
}

// --- Engine ---

func TestEngineGetSet(t *testing.T) {
	log := logger.New("error", "text")
	e := NewEngine(context.Background(), 1<<20, "", 0, log) // memory only, no disk (avoid async cleanup race)

	req := httptest.NewRequest("GET", "/page", nil)

	resp := &CachedResponse{
		StatusCode: 200,
		Headers:    http.Header{"Content-Type": {"text/html"}},
		Body:       []byte("cached page"),
		Created:    time.Now(),
		TTL:        5 * time.Minute,
	}

	e.Set(req, resp)

	got, status := e.Get(req)
	if status != StatusHit {
		t.Errorf("status = %q, want HIT", status)
	}
	if string(got.Body) != "cached page" {
		t.Errorf("body = %q", string(got.Body))
	}
}

func TestEngineStats(t *testing.T) {
	log := logger.New("error", "text")
	e := NewEngine(context.Background(), 1<<20, "", 0, log)

	req := httptest.NewRequest("GET", "/stats-test", nil)
	e.Get(req) // miss

	stats := e.Stats()
	if stats["misses"] != 1 {
		t.Errorf("misses = %d, want 1", stats["misses"])
	}
}

// --- Cacheable / Bypass ---

func TestIsCacheable(t *testing.T) {
	tests := []struct {
		method string
		status int
		setCookie bool
		want   bool
	}{
		{"GET", 200, false, true},
		{"GET", 301, false, true},
		{"GET", 404, false, true},
		{"POST", 200, false, false},
		{"GET", 500, false, false},
		{"GET", 200, true, false},
	}

	for _, tt := range tests {
		r := httptest.NewRequest(tt.method, "/", nil)
		h := http.Header{}
		if tt.setCookie {
			h.Set("Set-Cookie", "session=abc")
		}
		got := IsCacheable(r, tt.status, h)
		if got != tt.want {
			t.Errorf("IsCacheable(%s, %d, setCookie=%v) = %v, want %v",
				tt.method, tt.status, tt.setCookie, got, tt.want)
		}
	}
}

func TestShouldBypass(t *testing.T) {
	// POST should bypass
	r := httptest.NewRequest("POST", "/", nil)
	if !ShouldBypass(r) {
		t.Error("POST should bypass")
	}

	// GET with no-cache should bypass
	r2 := httptest.NewRequest("GET", "/", nil)
	r2.Header.Set("Cache-Control", "no-cache")
	if !ShouldBypass(r2) {
		t.Error("no-cache should bypass")
	}

	// Normal GET should not bypass
	r3 := httptest.NewRequest("GET", "/", nil)
	if ShouldBypass(r3) {
		t.Error("normal GET should not bypass")
	}
}

// --- Engine: PurgeByTag ---

func TestEnginePurgeByTag(t *testing.T) {
	log := logger.New("error", "text")
	e := NewEngine(context.Background(), 1<<20, "", 0, log)

	r1 := httptest.NewRequest("GET", "/a", nil)
	e.Set(r1, &CachedResponse{
		StatusCode: 200, Body: []byte("a"), Created: time.Now(), TTL: time.Minute, Tags: []string{"blog"},
	})
	r2 := httptest.NewRequest("GET", "/b", nil)
	e.Set(r2, &CachedResponse{
		StatusCode: 200, Body: []byte("b"), Created: time.Now(), TTL: time.Minute, Tags: []string{"shop"},
	})

	purged := e.PurgeByTag("blog")
	if purged != 1 {
		t.Errorf("purged = %d, want 1", purged)
	}

	// /b should still be there
	got, status := e.Get(r2)
	if status != StatusHit || got == nil {
		t.Errorf("expected /b to remain, status=%q", status)
	}
}

// --- Engine: PurgeAll ---

func TestEnginePurgeAll(t *testing.T) {
	log := logger.New("error", "text")
	dir := t.TempDir()
	e := NewEngine(context.Background(), 1<<20, dir, 1<<20, log)

	req := httptest.NewRequest("GET", "/page", nil)
	e.Set(req, &CachedResponse{
		StatusCode: 200, Body: []byte("hello"), Created: time.Now(), TTL: time.Minute,
	})

	// Allow async disk write to complete
	time.Sleep(50 * time.Millisecond)

	e.PurgeAll()

	_, status := e.Get(req)
	if status != StatusMiss {
		t.Errorf("status = %q after PurgeAll, want MISS", status)
	}
}

// --- Disk: PurgeAll ---

func TestDiskCachePurgeAll(t *testing.T) {
	dir := t.TempDir()
	dc := NewDiskCache(dir, 1<<20)

	resp := &CachedResponse{
		StatusCode: 200, Body: []byte("disk"), Created: time.Now(), TTL: time.Minute, GraceTTL: time.Minute,
	}
	if err := dc.Set("purge-key-1234567890abcdef", resp); err != nil {
		t.Fatalf("Set: %v", err)
	}

	if err := dc.PurgeAll(); err != nil {
		t.Fatalf("PurgeAll: %v", err)
	}

	_, err := dc.Get("purge-key-1234567890abcdef")
	if err == nil {
		t.Error("expected error after PurgeAll")
	}
}

// --- Entry: Age ---

func TestCachedResponseAge(t *testing.T) {
	r := &CachedResponse{
		Created: time.Now().Add(-2 * time.Second),
	}
	age := r.Age()
	if age < time.Second || age > 5*time.Second {
		t.Errorf("age = %v, expected around 2s", age)
	}
}

// --- Entry: IsExpired ---

func TestCachedResponseIsExpired(t *testing.T) {
	r := &CachedResponse{
		Created:  time.Now().Add(-5 * time.Second),
		TTL:      1 * time.Second,
		GraceTTL: 1 * time.Second,
	}
	if !r.IsExpired() {
		t.Error("should be expired when age >= TTL + GraceTTL")
	}
}

// --- Entry: CacheError.Error ---

func TestCacheErrorError(t *testing.T) {
	e := &CacheError{Message: "something went wrong"}
	if e.Error() != "something went wrong" {
		t.Errorf("Error() = %q", e.Error())
	}
}

// --- MemoryCache: cleanExpired via short cleanup interval ---

func TestCleanExpiredViaStartCleanup(t *testing.T) {
	mc := NewMemoryCache(1 << 20)

	// Insert an entry that is already expired
	mc.Set("expire-me", &CachedResponse{
		Body:     []byte("old"),
		Created:  time.Now().Add(-10 * time.Second),
		TTL:      1 * time.Millisecond,
		GraceTTL: 1 * time.Millisecond,
	})

	if mc.Len() != 1 {
		t.Fatalf("expected 1 entry before cleanup, got %d", mc.Len())
	}

	// Start cleanup with a very short interval
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mc.StartCleanup(ctx, 50*time.Millisecond)

	// Wait for cleanup to run
	time.Sleep(200 * time.Millisecond)

	if mc.Len() != 0 {
		t.Errorf("expected 0 entries after cleanup, got %d", mc.Len())
	}
}

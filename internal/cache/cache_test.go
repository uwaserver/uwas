package cache

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
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
		method    string
		status    int
		setCookie bool
		want      bool
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

// --- MemoryCache: Get with stale entry ---

func TestMemoryCacheGetStale(t *testing.T) {
	mc := NewMemoryCache(1 << 20)

	// Insert an entry that is past TTL but within GraceTTL
	mc.Set("stale-key", &CachedResponse{
		Body:     []byte("stale data"),
		Created:  time.Now().Add(-5 * time.Second),
		TTL:      1 * time.Second,  // expired 4 seconds ago
		GraceTTL: 10 * time.Second, // but within grace period
	})

	got, status := mc.Get("stale-key")
	if status != StatusStale {
		t.Errorf("status = %q, want STALE", status)
	}
	if got == nil {
		t.Fatal("expected non-nil response for stale entry")
	}
	if string(got.Body) != "stale data" {
		t.Errorf("body = %q, want 'stale data'", string(got.Body))
	}
}

func TestMemoryCacheGetExpiredBeyondGrace(t *testing.T) {
	mc := NewMemoryCache(1 << 20)

	// Insert an entry that is expired beyond both TTL and GraceTTL
	mc.Set("expired-key", &CachedResponse{
		Body:     []byte("expired"),
		Created:  time.Now().Add(-20 * time.Second),
		TTL:      1 * time.Second,
		GraceTTL: 1 * time.Second,
	})

	got, status := mc.Get("expired-key")
	if status != StatusMiss {
		t.Errorf("status = %q, want MISS for expired-beyond-grace entry", status)
	}
	if got != nil {
		t.Error("expected nil response for expired-beyond-grace entry")
	}
}

// --- MemoryCache: evictLRU ---

func TestMemoryCacheEvictLRUOrder(t *testing.T) {
	// Find keys that hash to the same shard so LRU eviction is predictable
	var sameShardKeys []string
	targetShard := shardIdx("seed")
	for i := 0; len(sameShardKeys) < 5; i++ {
		k := formatKey(uint64(i)) + "_evict"
		if shardIdx(k) == targetShard {
			sameShardKeys = append(sameShardKeys, k)
		}
	}

	// Each entry: 100 body + 64 overhead = 164 bytes
	// Limit of 500 bytes allows ~3 entries, so adding a 4th should evict oldest from that shard
	mc := NewMemoryCache(500)

	mc.Set(sameShardKeys[0], &CachedResponse{Body: make([]byte, 100), Created: time.Now(), TTL: time.Minute})
	mc.Set(sameShardKeys[1], &CachedResponse{Body: make([]byte, 100), Created: time.Now(), TTL: time.Minute})
	mc.Set(sameShardKeys[2], &CachedResponse{Body: make([]byte, 100), Created: time.Now(), TTL: time.Minute})
	mc.Set(sameShardKeys[3], &CachedResponse{Body: make([]byte, 100), Created: time.Now(), TTL: time.Minute})

	// Oldest key (sameShardKeys[0]) should have been evicted from the shard LRU
	_, status := mc.Get(sameShardKeys[0])
	if status != StatusMiss {
		t.Errorf("oldest entry should be evicted (LRU), got status %q", status)
	}

	// Newest key should still be present
	_, status = mc.Get(sameShardKeys[3])
	if status == StatusMiss {
		t.Error("newest entry should still be present")
	}
}

func TestMemoryCacheEvictLRUPromotesOnAccess(t *testing.T) {
	// Find keys that hash to the same shard
	var sameShardKeys []string
	targetShard := shardIdx("promote_seed")
	for i := 0; len(sameShardKeys) < 5; i++ {
		k := formatKey(uint64(i)) + "_promote"
		if shardIdx(k) == targetShard {
			sameShardKeys = append(sameShardKeys, k)
		}
	}

	// Each entry ~164 bytes, limit allows ~3 entries in the shard
	mc := NewMemoryCache(500)

	mc.Set(sameShardKeys[0], &CachedResponse{Body: make([]byte, 100), Created: time.Now(), TTL: time.Minute})
	mc.Set(sameShardKeys[1], &CachedResponse{Body: make([]byte, 100), Created: time.Now(), TTL: time.Minute})

	// Access sameShardKeys[0] to promote it to front of LRU
	mc.Get(sameShardKeys[0])

	// Add two more entries to fill and evict
	mc.Set(sameShardKeys[2], &CachedResponse{Body: make([]byte, 100), Created: time.Now(), TTL: time.Minute})
	mc.Set(sameShardKeys[3], &CachedResponse{Body: make([]byte, 100), Created: time.Now(), TTL: time.Minute})

	// sameShardKeys[1] was not accessed so it should be evicted first
	_, statusPromoted := mc.Get(sameShardKeys[0])
	_, statusUnpromoted := mc.Get(sameShardKeys[1])

	// The promoted key should survive eviction while the unpromoted one may be evicted
	if statusPromoted == StatusMiss && statusUnpromoted != StatusMiss {
		t.Error("promoted key should survive eviction better than unpromoted key")
	}
}

// --- MemoryCache: concurrent access ---

func TestMemoryCacheConcurrentAccess(t *testing.T) {
	mc := NewMemoryCache(1 << 20) // 1MB
	var wg sync.WaitGroup

	// Concurrent writers
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := formatKey(uint64(n))
			mc.Set(key, &CachedResponse{
				Body:    []byte("concurrent"),
				Created: time.Now(),
				TTL:     time.Minute,
			})
		}(i)
	}

	// Concurrent readers
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := formatKey(uint64(n))
			mc.Get(key)
		}(i)
	}

	// Concurrent deleters
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := formatKey(uint64(n))
			mc.Delete(key)
		}(i)
	}

	wg.Wait()

	// Just verify no panics/races occurred and stats are reasonable
	_, _, _, used := mc.Stats()
	if used < 0 {
		t.Errorf("usedBytes = %d, should be >= 0", used)
	}
}

func TestMemoryCacheConcurrentSetSameKey(t *testing.T) {
	mc := NewMemoryCache(1 << 20)
	var wg sync.WaitGroup

	// Many goroutines writing the same key
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			mc.Set("same-key", &CachedResponse{
				Body:    []byte("value"),
				Created: time.Now(),
				TTL:     time.Minute,
			})
		}(i)
	}
	wg.Wait()

	// Should have exactly one entry for "same-key"
	got, status := mc.Get("same-key")
	if status != StatusHit {
		t.Errorf("status = %q, want HIT", status)
	}
	if got == nil {
		t.Error("expected non-nil response")
	}
}

// --- DiskCache: Set over limit (should skip) ---

func TestDiskCacheSetOverLimit(t *testing.T) {
	dir := t.TempDir()
	// Create a disk cache with a very small limit (100 bytes)
	dc := NewDiskCache(dir, 100)

	resp := &CachedResponse{
		StatusCode: 200,
		Body:       make([]byte, 200), // larger than the limit
		Created:    time.Now(),
		TTL:        time.Minute,
		GraceTTL:   time.Minute,
	}

	err := dc.Set("big-key-1234567890abcdef", resp)
	if err != nil {
		t.Fatalf("Set should not return error when over limit, got: %v", err)
	}

	// The entry should not have been stored
	_, err = dc.Get("big-key-1234567890abcdef")
	if err == nil {
		t.Error("should not be able to Get an entry that was skipped due to disk limit")
	}
}

func TestDiskCacheSetUnderLimit(t *testing.T) {
	dir := t.TempDir()
	dc := NewDiskCache(dir, 1<<20) // 1MB limit

	resp := &CachedResponse{
		StatusCode: 200,
		Body:       []byte("small"),
		Created:    time.Now(),
		TTL:        time.Minute,
		GraceTTL:   time.Minute,
	}

	err := dc.Set("small-key-1234567890abcdef", resp)
	if err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, err := dc.Get("small-key-1234567890abcdef")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got.Body) != "small" {
		t.Errorf("body = %q, want 'small'", string(got.Body))
	}
}

// --- DiskCache: concurrent Get/Set ---

func TestDiskCacheConcurrentGetSet(t *testing.T) {
	dir := t.TempDir()
	dc := NewDiskCache(dir, 1<<20)

	var wg sync.WaitGroup

	// Concurrent writers
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := formatKey(uint64(n)) + "1234567890ab"
			resp := &CachedResponse{
				StatusCode: 200,
				Body:       []byte("concurrent disk write"),
				Created:    time.Now(),
				TTL:        time.Minute,
				GraceTTL:   time.Minute,
			}
			dc.Set(key, resp)
		}(i)
	}

	wg.Wait()

	// Concurrent readers after writes are done
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := formatKey(uint64(n)) + "1234567890ab"
			dc.Get(key)
		}(i)
	}

	wg.Wait()
	// No panics or races = success
}

// --- Engine: Get with disk promotion ---

func TestEngineGetDiskPromotion(t *testing.T) {
	log := logger.New("error", "text")
	dir := t.TempDir()
	e := NewEngine(context.Background(), 1<<20, dir, 1<<20, log)

	req := httptest.NewRequest("GET", "/promote-test", nil)
	key := GenerateKey(req, []string{"Accept-Encoding"})

	// Write directly to disk, bypassing memory
	diskResp := &CachedResponse{
		StatusCode: 200,
		Headers:    http.Header{"Content-Type": {"text/html"}},
		Body:       []byte("from disk"),
		Created:    time.Now(),
		TTL:        5 * time.Minute,
		GraceTTL:   1 * time.Hour,
	}
	if err := e.disk.Set(key, diskResp); err != nil {
		t.Fatalf("disk.Set: %v", err)
	}

	// Verify memory cache doesn't have it
	memResp, memStatus := e.memory.Get(key)
	if memResp != nil {
		t.Fatal("memory should not have entry before promotion")
	}
	_ = memStatus

	// Engine.Get should find it on disk and promote to memory
	got, status := e.Get(req)
	if status != StatusHit {
		t.Errorf("status = %q, want HIT after disk promotion", status)
	}
	if got == nil {
		t.Fatal("expected non-nil response")
	}
	if string(got.Body) != "from disk" {
		t.Errorf("body = %q, want 'from disk'", string(got.Body))
	}

	// Now verify it's been promoted to memory
	memResp, memStatus = e.memory.Get(key)
	if memResp == nil {
		t.Error("entry should have been promoted to memory")
	}
	if memStatus != StatusHit {
		t.Errorf("memory status = %q, want HIT after promotion", memStatus)
	}
}

func TestEngineGetDiskPromotionStale(t *testing.T) {
	log := logger.New("error", "text")
	dir := t.TempDir()
	e := NewEngine(context.Background(), 1<<20, dir, 1<<20, log)

	req := httptest.NewRequest("GET", "/stale-disk", nil)
	key := GenerateKey(req, []string{"Accept-Encoding"})

	// Write a stale entry directly to disk
	staleResp := &CachedResponse{
		StatusCode: 200,
		Body:       []byte("stale disk entry"),
		Created:    time.Now().Add(-10 * time.Second),
		TTL:        1 * time.Second, // expired
		GraceTTL:   1 * time.Minute, // but within grace
	}
	if err := e.disk.Set(key, staleResp); err != nil {
		t.Fatalf("disk.Set: %v", err)
	}

	got, status := e.Get(req)
	if status != StatusStale {
		t.Errorf("status = %q, want STALE for stale disk entry", status)
	}
	if got == nil {
		t.Fatal("expected non-nil stale response")
	}
	if string(got.Body) != "stale disk entry" {
		t.Errorf("body = %q", string(got.Body))
	}
}

func TestEngineGetDiskExpiredBeyondGrace(t *testing.T) {
	log := logger.New("error", "text")
	dir := t.TempDir()
	e := NewEngine(context.Background(), 1<<20, dir, 1<<20, log)

	req := httptest.NewRequest("GET", "/expired-disk", nil)
	key := GenerateKey(req, []string{"Accept-Encoding"})

	// Write a fully expired entry to disk
	expiredResp := &CachedResponse{
		StatusCode: 200,
		Body:       []byte("expired"),
		Created:    time.Now().Add(-1 * time.Hour),
		TTL:        1 * time.Second,
		GraceTTL:   1 * time.Second,
	}
	if err := e.disk.Set(key, expiredResp); err != nil {
		t.Fatalf("disk.Set: %v", err)
	}

	_, status := e.Get(req)
	if status != StatusMiss {
		t.Errorf("status = %q, want MISS for fully expired disk entry", status)
	}
}

// --- Engine: Set with async disk write ---

func TestEngineSetAsyncDiskWrite(t *testing.T) {
	log := logger.New("error", "text")
	dir := t.TempDir()
	e := NewEngine(context.Background(), 1<<20, dir, 1<<20, log)

	req := httptest.NewRequest("GET", "/async-write", nil)
	key := GenerateKey(req, []string{"Accept-Encoding"})

	resp := &CachedResponse{
		StatusCode: 200,
		Headers:    http.Header{"Content-Type": {"text/plain"}},
		Body:       []byte("async content"),
		Created:    time.Now(),
		TTL:        5 * time.Minute,
		GraceTTL:   time.Hour,
	}

	e.Set(req, resp)

	// Memory should have it immediately
	memResp, memStatus := e.memory.Get(key)
	if memResp == nil || memStatus != StatusHit {
		t.Fatal("memory should have entry immediately after Set")
	}

	// Wait for async disk write
	time.Sleep(100 * time.Millisecond)

	// Disk should also have it
	diskResp, err := e.disk.Get(key)
	if err != nil {
		t.Fatalf("disk.Get: %v", err)
	}
	if string(diskResp.Body) != "async content" {
		t.Errorf("disk body = %q, want 'async content'", string(diskResp.Body))
	}
}

func TestEngineSetMemoryOnlyNoDisk(t *testing.T) {
	log := logger.New("error", "text")
	e := NewEngine(context.Background(), 1<<20, "", 0, log) // no disk

	req := httptest.NewRequest("GET", "/memory-only", nil)

	resp := &CachedResponse{
		StatusCode: 200,
		Body:       []byte("mem only"),
		Created:    time.Now(),
		TTL:        5 * time.Minute,
	}

	e.Set(req, resp)

	got, status := e.Get(req)
	if status != StatusHit {
		t.Errorf("status = %q, want HIT", status)
	}
	if string(got.Body) != "mem only" {
		t.Errorf("body = %q", string(got.Body))
	}
}

// --- Engine: concurrent operations ---

func TestEngineConcurrentGetSet(t *testing.T) {
	log := logger.New("error", "text")
	e := NewEngine(context.Background(), 1<<20, "", 0, log)

	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func(n int) {
			defer wg.Done()
			req := httptest.NewRequest("GET", "/concurrent/"+formatKey(uint64(n)), nil)
			e.Set(req, &CachedResponse{
				StatusCode: 200,
				Body:       []byte("concurrent"),
				Created:    time.Now(),
				TTL:        time.Minute,
			})
		}(i)
		go func(n int) {
			defer wg.Done()
			req := httptest.NewRequest("GET", "/concurrent/"+formatKey(uint64(n)), nil)
			e.Get(req)
		}(i)
	}

	wg.Wait()

	stats := e.Stats()
	if stats["entries"] < 0 {
		t.Errorf("entries = %d, should be >= 0", stats["entries"])
	}
}

// --- MemoryCache: Stats accuracy ---

func TestMemoryCacheStatsAccuracy(t *testing.T) {
	mc := NewMemoryCache(1 << 20)

	mc.Set("hit-me", &CachedResponse{
		Body:    []byte("data"),
		Created: time.Now(),
		TTL:     time.Minute,
	})

	// 3 hits
	for i := 0; i < 3; i++ {
		mc.Get("hit-me")
	}
	// 2 misses
	mc.Get("nope1")
	mc.Get("nope2")

	hits, misses, _, used := mc.Stats()
	if hits != 3 {
		t.Errorf("hits = %d, want 3", hits)
	}
	if misses != 2 {
		t.Errorf("misses = %d, want 2", misses)
	}
	if used <= 0 {
		t.Errorf("usedBytes = %d, should be > 0", used)
	}
}

// --- MemoryCache: Set replacing existing key ---

func TestMemoryCacheSetReplacesExisting(t *testing.T) {
	mc := NewMemoryCache(1 << 20)

	mc.Set("replace-key", &CachedResponse{
		Body:    []byte("original"),
		Created: time.Now(),
		TTL:     time.Minute,
	})

	mc.Set("replace-key", &CachedResponse{
		Body:    []byte("replacement"),
		Created: time.Now(),
		TTL:     time.Minute,
	})

	got, status := mc.Get("replace-key")
	if status != StatusHit {
		t.Errorf("status = %q, want HIT", status)
	}
	if string(got.Body) != "replacement" {
		t.Errorf("body = %q, want 'replacement'", string(got.Body))
	}
	if mc.Len() != 1 {
		t.Errorf("len = %d, want 1 (should not duplicate)", mc.Len())
	}
}

// --- DiskCache: Get nonexistent key ---

func TestDiskCacheGetMiss(t *testing.T) {
	dir := t.TempDir()
	dc := NewDiskCache(dir, 1<<20)

	_, err := dc.Get("nonexistent-key-abcdef")
	if err == nil {
		t.Error("Get for nonexistent key should return error")
	}
}

// --- DiskCache: usedBytes tracking ---

func TestDiskCacheUsedBytesTracking(t *testing.T) {
	dir := t.TempDir()
	dc := NewDiskCache(dir, 1<<20)

	initial := dc.usedBytes.Load()

	resp := &CachedResponse{
		StatusCode: 200,
		Body:       []byte("tracking test data"),
		Created:    time.Now(),
		TTL:        time.Minute,
		GraceTTL:   time.Minute,
	}
	dc.Set("track-key-1234567890abcdef", resp)

	afterSet := dc.usedBytes.Load()
	if afterSet <= initial {
		t.Errorf("usedBytes after Set (%d) should be > initial (%d)", afterSet, initial)
	}

	dc.Delete("track-key-1234567890abcdef")

	afterDelete := dc.usedBytes.Load()
	if afterDelete >= afterSet {
		t.Errorf("usedBytes after Delete (%d) should be < after Set (%d)", afterDelete, afterSet)
	}
}

// --- DiskCache: accumulated usage across restarts ---

func TestDiskCacheInitialUsedBytes(t *testing.T) {
	dir := t.TempDir()
	dc1 := NewDiskCache(dir, 1<<20)

	resp := &CachedResponse{
		StatusCode: 200,
		Body:       []byte("persist me"),
		Created:    time.Now(),
		TTL:        time.Minute,
		GraceTTL:   time.Minute,
	}
	dc1.Set("persist-key-1234567890abcdef", resp)
	usedAfterWrite := dc1.usedBytes.Load()

	// Create a new DiskCache on the same dir (simulating restart)
	dc2 := NewDiskCache(dir, 1<<20)
	initialUsed := dc2.usedBytes.Load()

	if initialUsed != usedAfterWrite {
		t.Errorf("new DiskCache usedBytes = %d, want %d (should scan existing files)", initialUsed, usedAfterWrite)
	}
}

// --- IsCacheable: HEAD method should be cacheable ---

func TestIsCacheableHEAD(t *testing.T) {
	r := httptest.NewRequest("HEAD", "/", nil)
	h := http.Header{}
	if !IsCacheable(r, 200, h) {
		t.Error("HEAD 200 should be cacheable")
	}
}

// --- IsCacheable: Cache-Control: no-store ---

func TestIsCacheableNoStore(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	h := http.Header{}
	h.Set("Cache-Control", "no-store")
	if IsCacheable(r, 200, h) {
		t.Error("no-store should not be cacheable")
	}
}

// --- IsCacheable: Cache-Control: private ---

func TestIsCacheablePrivate(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	h := http.Header{}
	h.Set("Cache-Control", "private, max-age=60")
	if IsCacheable(r, 200, h) {
		t.Error("private should not be cacheable")
	}
}

// --- ShouldBypass: Pragma: no-cache ---

func TestShouldBypassPragmaNoCache(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Pragma", "no-cache")
	if !ShouldBypass(r) {
		t.Error("Pragma: no-cache should bypass")
	}
}

// --- ShouldBypass: HEAD should not bypass ---

func TestShouldBypassHEAD(t *testing.T) {
	r := httptest.NewRequest("HEAD", "/", nil)
	if ShouldBypass(r) {
		t.Error("HEAD should not bypass")
	}
}

// --- Deserialize: truncated header data (body length check) ---

func TestDeserializeTruncatedBodyLength(t *testing.T) {
	// Build valid data up to headers but truncate before body length
	resp := &CachedResponse{
		StatusCode: 200,
		Headers:    http.Header{},
		Body:       []byte("hello"),
		Created:    time.Now(),
		TTL:        time.Minute,
		GraceTTL:   time.Minute,
	}
	data := resp.Serialize()
	// Truncate just before body length (remove last 4 + body bytes)
	truncated := data[:len(data)-len("hello")-4+2] // cut into body length field
	_, err := Deserialize(truncated)
	if err == nil {
		t.Error("expected error for truncated body length")
	}
}

// --- Deserialize: body shorter than declared ---

func TestDeserializeTruncatedBody(t *testing.T) {
	resp := &CachedResponse{
		StatusCode: 200,
		Headers:    http.Header{},
		Body:       []byte("hello world here"),
		Created:    time.Now(),
		TTL:        time.Minute,
		GraceTTL:   time.Minute,
	}
	data := resp.Serialize()
	// Cut off the last few bytes from the body
	truncated := data[:len(data)-5]
	_, err := Deserialize(truncated)
	if err == nil {
		t.Error("expected error for truncated body data")
	}
}

// --- Deserialize: truncated header key/val data ---

func TestDeserializeTruncatedHeaderData(t *testing.T) {
	resp := &CachedResponse{
		StatusCode: 200,
		Headers:    http.Header{"Content-Type": {"text/html"}},
		Body:       []byte("x"),
		Created:    time.Now(),
		TTL:        time.Minute,
		GraceTTL:   time.Minute,
	}
	data := resp.Serialize()
	// Cut right after header count but before all header data
	// Fixed header: 4+8+8+8+4 = 32 bytes, then header data starts
	truncated := data[:34] // just 2 bytes into header data
	_, err := Deserialize(truncated)
	if err == nil {
		t.Error("expected error for truncated header data")
	}
}

// --- Engine.Set: without disk (nil disk) ---

func TestEngineSetNoDisk(t *testing.T) {
	log := logger.New("error", "text")
	e := NewEngine(context.Background(), 1<<20, "", 0, log) // no disk

	req := httptest.NewRequest("GET", "/no-disk", nil)
	resp := &CachedResponse{
		StatusCode: 200,
		Body:       []byte("no disk"),
		Created:    time.Now(),
		TTL:        5 * time.Minute,
	}

	e.Set(req, resp)

	got, status := e.Get(req)
	if status != StatusHit {
		t.Errorf("status = %q, want HIT", status)
	}
	if string(got.Body) != "no disk" {
		t.Errorf("body = %q", string(got.Body))
	}
}

// --- DiskCache.Set: maxBytes 0 means no limit ---

func TestDiskCacheSetNoLimit(t *testing.T) {
	dir := t.TempDir()
	dc := NewDiskCache(dir, 0) // 0 means no limit

	resp := &CachedResponse{
		StatusCode: 200,
		Body:       []byte("unlimited"),
		Created:    time.Now(),
		TTL:        time.Minute,
		GraceTTL:   time.Minute,
	}

	err := dc.Set("unlimited-key-1234567890", resp)
	if err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, err := dc.Get("unlimited-key-1234567890")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got.Body) != "unlimited" {
		t.Errorf("body = %q", string(got.Body))
	}
}

// --- MemoryCache: Set over global limit when shard is empty (skip storing) ---

func TestMemoryCacheSetOverLimitNoEviction(t *testing.T) {
	// Very small limit, one large entry
	mc := NewMemoryCache(10) // 10 bytes limit

	resp := &CachedResponse{
		Body:    make([]byte, 1000), // way over limit
		Created: time.Now(),
		TTL:     time.Minute,
	}

	mc.Set("too-big", resp)

	// Entry should not be stored (over limit and nothing to evict in this shard)
	_, status := mc.Get("too-big")
	if status != StatusMiss {
		t.Errorf("status = %q, want MISS for entry over limit", status)
	}
}

// --- Deserialize: truncated at header key/val length field ---

func TestDeserializeTruncatedAtHeaderKeyValLen(t *testing.T) {
	resp := &CachedResponse{
		StatusCode: 200,
		Headers:    http.Header{"X-Long-Header-Name": {"some-value-here"}},
		Body:       []byte("body"),
		Created:    time.Now(),
		TTL:        time.Minute,
		GraceTTL:   time.Minute,
	}
	data := resp.Serialize()

	// Find the position after fixed header (28 bytes) + header count (4 bytes) = 32 bytes
	// Then truncate in the middle of the header key/value length fields
	if len(data) > 34 {
		truncated := data[:33] // just 1 byte into first header key/val lengths
		_, err := Deserialize(truncated)
		if err == nil {
			t.Error("expected error for truncated header key/val lengths")
		}
	}
}

// --- Deserialize: header key+val data truncated after lengths are read ---

func TestDeserializeTruncatedHeaderKeyVal(t *testing.T) {
	// Build a minimal valid buffer and then truncate after reading header count + key/val lengths
	// Fixed header: 4(status) + 8(created) + 8(ttl) + 8(grace) = 28 bytes
	// + 4(headerCount) = 32 bytes
	// Then for each header: 2(keyLen) + 2(valLen) + key + val

	// Manual construction:
	buf := make([]byte, 0, 64)
	b4 := make([]byte, 4)
	b8 := make([]byte, 8)

	// Status code = 200
	b4[0], b4[1], b4[2], b4[3] = 0, 0, 0, 200
	buf = append(buf, b4...)

	// Created = 0
	buf = append(buf, b8...)
	// TTL = 0
	buf = append(buf, b8...)
	// GraceTTL = 0
	buf = append(buf, b8...)

	// HeaderCount = 1
	b4[0], b4[1], b4[2], b4[3] = 0, 0, 0, 1
	buf = append(buf, b4...)

	// Key length = 5, Value length = 10
	b2 := make([]byte, 2)
	b2[0], b2[1] = 0, 5
	buf = append(buf, b2...)
	b2[0], b2[1] = 0, 10
	buf = append(buf, b2...)

	// Only provide 3 bytes of key (need 5+10=15)
	buf = append(buf, 'K', 'E', 'Y')

	_, err := Deserialize(buf)
	if err == nil {
		t.Error("expected error for truncated header key+val data")
	}
}

// --- Engine.Set: disk write error path (covered by goroutine) ---
// The disk write error is logged but not returned. We test that Set
// doesn't panic when disk write would fail.

func TestEngineSetDiskWriteError(t *testing.T) {
	log := logger.New("error", "text")
	// Use a path that will cause MkdirAll to fail on write
	// We create an engine with a disk path, then remove the path
	dir := t.TempDir()
	e := NewEngine(context.Background(), 1<<20, dir, 1<<20, log)

	req := httptest.NewRequest("GET", "/disk-error", nil)
	resp := &CachedResponse{
		StatusCode: 200,
		Body:       []byte("test"),
		Created:    time.Now(),
		TTL:        5 * time.Minute,
	}

	// Remove the disk directory to cause write failure
	os.RemoveAll(dir)

	// Set should not panic even when disk write fails in background
	e.Set(req, resp)

	// Wait for async goroutine to complete
	time.Sleep(100 * time.Millisecond)

	// Memory should still have the entry
	got, status := e.Get(req)
	if status != StatusHit {
		t.Errorf("status = %q, want HIT (memory should work despite disk error)", status)
	}
	if got == nil {
		t.Fatal("expected non-nil response from memory")
	}
}

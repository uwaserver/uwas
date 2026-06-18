package cache

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
)

var errPlain = errors.New("plain non-network error")

// --- Engine + Redis (L3) integration via the in-process stub client ---

// syncRedisStub is a concurrency-safe in-memory RedisClient. The shared
// stubRedisClient is not mutex-guarded, so async engine writes would race the
// test goroutine; this one is safe under -race and supports a tiny glob for
// PurgeByTag patterns ("*tag:X*").
type syncRedisStub struct {
	mu   sync.Mutex
	data map[string]string
}

func newSyncRedisStub() *syncRedisStub { return &syncRedisStub{data: map[string]string{}} }

func (s *syncRedisStub) get(k string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.data[k]
	return v, ok
}

func (s *syncRedisStub) Get(_ context.Context, key string) (string, error) {
	v, ok := s.get(key)
	if !ok {
		return "", ErrRedisNotFound
	}
	return v, nil
}

func (s *syncRedisStub) Set(_ context.Context, key, value string, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = value
	return nil
}

func (s *syncRedisStub) Del(_ context.Context, keys ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range keys {
		delete(s.data, k)
	}
	return nil
}

// Keys supports patterns of the form "*mid*" (contains), "pre*" (prefix), and
// "*" (all), which is enough for the engine's tag pattern "*tag:X*".
func (s *syncRedisStub) Keys(_ context.Context, pattern string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []string{}
	for k := range s.data {
		if globMatch(pattern, k) {
			out = append(out, k)
		}
	}
	return out, nil
}

func (s *syncRedisStub) Close() error { return nil }

func (s *syncRedisStub) seed(k, v string) {
	s.mu.Lock()
	s.data[k] = v
	s.mu.Unlock()
}

func (s *syncRedisStub) has(k string) bool {
	_, ok := s.get(k)
	return ok
}

func globMatch(pattern, s string) bool {
	if pattern == "*" {
		return true
	}
	hasPre := strings.HasPrefix(pattern, "*")
	hasSuf := strings.HasSuffix(pattern, "*")
	core := strings.Trim(pattern, "*")
	switch {
	case hasPre && hasSuf:
		return strings.Contains(s, core)
	case hasSuf:
		return strings.HasPrefix(s, core)
	case hasPre:
		return strings.HasSuffix(s, core)
	default:
		return s == core
	}
}

// newRedisStubEngine builds an Engine backed by a sync stub Redis client so the
// L3 branches in engine.go can be exercised without a live Redis.
func newRedisStubEngine(t *testing.T, dir string) (*Engine, *syncRedisStub) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	e := NewEngine(ctx, 1<<20, dir, 1<<30, testLogger())
	stub := newSyncRedisStub()
	e.SetRedis(&RedisCache{client: stub, logger: testLogger()})
	return e, stub
}

// settleDiskPromote waits for the async disk-promote goroutine that engine.Get/
// GetByKey starts on a redis hit to finish writing the file. Without this, that
// goroutine can still be creating a file in the test's t.TempDir() when the
// deferred TempDir cleanup runs RemoveAll, which then fails with "directory not
// empty" and flakes the test.
func settleDiskPromote(t *testing.T, e *Engine, key string) {
	t.Helper()
	if e.disk == nil {
		return
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if r, err := e.disk.Get(key); err == nil && r != nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("disk promote did not settle for key %q", key)
}

func freshResp(body string, ttl time.Duration) *CachedResponse {
	return &CachedResponse{
		StatusCode: 200,
		Headers:    http.Header{"Content-Type": {"text/html"}},
		Body:       []byte(body),
		Created:    time.Now(),
		TTL:        ttl,
		GraceTTL:   time.Hour,
	}
}

// TestSetRedisAttach covers SetRedis (0% before).
func TestSetRedisAttach(t *testing.T) {
	e := NewEngine(context.Background(), 1<<20, "", 0, testLogger())
	if e.redis != nil {
		t.Fatal("expected nil redis before SetRedis")
	}
	e.SetRedis(&RedisCache{client: &stubRedisClient{}})
	if e.redis == nil {
		t.Fatal("expected redis set after SetRedis")
	}
}

// TestEngineGetRedisPromotionFresh covers engine.Get L3 hit + promote to memory
// and disk (lines 64-76).
func TestEngineGetRedisPromotionFresh(t *testing.T) {
	dir := t.TempDir()
	e, stub := newRedisStubEngine(t, dir)

	req := httptest.NewRequest("GET", "http://example.com/redis-fresh", nil)
	key := GenerateKey(req, e.varyKeys())

	data, _ := json.Marshal(freshResp("redis fresh body", time.Hour))
	stub.seed(key, string(data))

	got, status := e.Get(req)
	if got == nil || status != StatusHit {
		t.Fatalf("expected HIT from redis, got status=%q resp=%v", status, got)
	}
	if string(got.Body) != "redis fresh body" {
		t.Fatalf("body = %q", got.Body)
	}
	// Promoted to memory: a second Get should hit memory (still HIT).
	if got2, st2 := e.memory.Get(key); got2 == nil || st2 != StatusHit {
		t.Fatalf("expected memory promotion, got status=%q", st2)
	}
	// Wait for async disk promotion.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if r, err := e.disk.Get(key); err == nil && r != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected disk promotion after redis hit")
}

// TestEngineGetRedisPromotionStale covers engine.Get L3 stale branch (line 75).
func TestEngineGetRedisPromotionStale(t *testing.T) {
	dir := t.TempDir()
	e, stub := newRedisStubEngine(t, dir)

	req := httptest.NewRequest("GET", "http://example.com/redis-stale", nil)
	key := GenerateKey(req, e.varyKeys())

	stale := &CachedResponse{
		StatusCode: 200,
		Headers:    http.Header{},
		Body:       []byte("redis stale"),
		Created:    time.Now().Add(-2 * time.Second),
		TTL:        time.Second,
		GraceTTL:   time.Hour,
	}
	data, _ := json.Marshal(stale)
	stub.seed(key, string(data))

	got, status := e.Get(req)
	if got == nil || status != StatusStale {
		t.Fatalf("expected STALE from redis, got status=%q", status)
	}
	settleDiskPromote(t, e, key)
}

// TestEngineGetRedisExpiredBeyondGrace covers engine.Get when redis returns an
// entry that's neither fresh nor stale (falls through to MISS).
func TestEngineGetRedisExpiredBeyondGrace(t *testing.T) {
	dir := t.TempDir()
	e, stub := newRedisStubEngine(t, dir)

	req := httptest.NewRequest("GET", "http://example.com/redis-expired", nil)
	key := GenerateKey(req, e.varyKeys())

	expired := &CachedResponse{
		StatusCode: 200,
		Headers:    http.Header{},
		Body:       []byte("expired"),
		Created:    time.Now().Add(-time.Hour),
		TTL:        time.Second,
		GraceTTL:   time.Second,
	}
	data, _ := json.Marshal(expired)
	stub.seed(key, string(data))

	if got, status := e.Get(req); got != nil || status != StatusMiss {
		t.Fatalf("expected MISS for expired redis entry, got status=%q", status)
	}
}

// TestEngineGetRedisError covers engine.Get when redis returns an error
// (err != nil branch skipped) — uses errRedisClient.
func TestEngineGetRedisError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := NewEngine(ctx, 1<<20, "", 0, testLogger())
	e.SetRedis(&RedisCache{client: errRedisClient{}, logger: testLogger()})

	req := httptest.NewRequest("GET", "http://example.com/redis-err", nil)
	if got, status := e.Get(req); got != nil || status != StatusMiss {
		t.Fatalf("expected MISS on redis error, got status=%q", status)
	}
}

// TestEngineSetRedisWrite covers engine.Set async redis write (lines 104-110).
func TestEngineSetRedisWrite(t *testing.T) {
	dir := t.TempDir()
	e, stub := newRedisStubEngine(t, dir)

	req := httptest.NewRequest("GET", "http://example.com/set-redis", nil)
	key := GenerateKey(req, e.varyKeys())

	e.Set(req, freshResp("set into redis", time.Hour))

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if stub.has(key) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected redis write from engine.Set")
}

// TestEngineSetRedisWriteError covers the redis write error log path in Set.
func TestEngineSetRedisWriteError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := NewEngine(ctx, 1<<20, "", 0, testLogger())
	e.SetRedis(&RedisCache{client: errRedisClient{}, logger: testLogger()})

	req := httptest.NewRequest("GET", "http://example.com/set-redis-err", nil)
	e.Set(req, freshResp("x", time.Hour))
	// Give the async goroutine a moment to run and log.
	time.Sleep(50 * time.Millisecond)
}

// TestEngineGetByKeyRedis covers GetByKey L3 fresh + stale + memory promote.
func TestEngineGetByKeyRedis(t *testing.T) {
	dir := t.TempDir()
	e, stub := newRedisStubEngine(t, dir)

	// Fresh
	fk := "esi|h|/frag"
	data, _ := json.Marshal(freshResp("frag fresh", time.Hour))
	stub.seed(fk, string(data))
	if got, status := e.GetByKey(fk); got == nil || status != StatusHit {
		t.Fatalf("GetByKey fresh: status=%q", status)
	}
	// Promotion to memory.
	if got, st := e.memory.Get(fk); got == nil || st != StatusHit {
		t.Fatalf("expected memory promote, got %q", st)
	}

	// Stale (separate key not in memory).
	sk := "esi|h|/frag-stale"
	stale := &CachedResponse{
		StatusCode: 200, Headers: http.Header{}, Body: []byte("frag stale"),
		Created: time.Now().Add(-2 * time.Second), TTL: time.Second, GraceTTL: time.Hour,
	}
	sd, _ := json.Marshal(stale)
	stub.seed(sk, string(sd))
	if got, status := e.GetByKey(sk); got == nil || status != StatusStale {
		t.Fatalf("GetByKey stale: status=%q", status)
	}
}

// TestEngineGetByKeyRedisExpired covers GetByKey when redis entry is expired
// beyond grace (falls through to MISS) and the redis-error skip.
func TestEngineGetByKeyRedisMisc(t *testing.T) {
	dir := t.TempDir()
	e, stub := newRedisStubEngine(t, dir)

	ek := "esi|h|/expired"
	expired := &CachedResponse{
		StatusCode: 200, Headers: http.Header{}, Body: []byte("e"),
		Created: time.Now().Add(-time.Hour), TTL: time.Second, GraceTTL: time.Second,
	}
	ed, _ := json.Marshal(expired)
	stub.seed(ek, string(ed))
	if got, status := e.GetByKey(ek); got != nil || status != StatusMiss {
		t.Fatalf("expected MISS for expired, got %q", status)
	}

	// Plain miss (key absent everywhere).
	if got, status := e.GetByKey("esi|h|/nope"); got != nil || status != StatusMiss {
		t.Fatalf("expected MISS for absent key, got %q", status)
	}
}

// TestEngineSetByKeyRedis covers SetByKey async disk + redis writes.
func TestEngineSetByKeyRedis(t *testing.T) {
	dir := t.TempDir()
	e, stub := newRedisStubEngine(t, dir)

	key := "esi|h|/setbykey"
	e.SetByKey(key, freshResp("setbykey body", time.Hour))

	// Memory is synchronous.
	if got, _ := e.memory.Get(key); got == nil {
		t.Fatal("expected memory store from SetByKey")
	}
	// Disk + redis are async.
	deadline := time.Now().Add(2 * time.Second)
	diskOK, redisOK := false, false
	for time.Now().Before(deadline) && (!diskOK || !redisOK) {
		if r, err := e.disk.Get(key); err == nil && r != nil {
			diskOK = true
		}
		if stub.has(key) {
			redisOK = true
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !diskOK || !redisOK {
		t.Fatalf("disk=%v redis=%v after SetByKey", diskOK, redisOK)
	}
}

// TestEngineSetByKeyWriteErrors covers the warn log branches in SetByKey when
// disk + redis writes fail.
func TestEngineSetByKeyWriteErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := NewEngine(ctx, 1<<20, "", 0, testLogger())
	// Disk that always fails: point at a path that is a file.
	bad := t.TempDir() + "/afile"
	if err := writeFile(bad); err != nil {
		t.Fatal(err)
	}
	e.disk = NewDiskCache(bad, 1<<30)
	e.SetRedis(&RedisCache{client: errRedisClient{}, logger: testLogger()})

	e.SetByKey("k-err", freshResp("x", time.Hour))
	time.Sleep(50 * time.Millisecond)
}

// TestEnginePurgeByTagRedis covers engine.PurgeByTag redis branch incl. error.
func TestEnginePurgeByTagRedis(t *testing.T) {
	dir := t.TempDir()
	e, stub := newRedisStubEngine(t, dir)

	// Seed a redis entry whose key matches the tag pattern "*tag:news*".
	stub.seed("x-tag:news-y", "v")
	count := e.PurgeByTag("news")
	_ = count
	if stub.has("x-tag:news-y") {
		t.Fatal("expected redis key purged by tag")
	}

	// Error path: redis PurgeByTag returns an error -> logged, not fatal.
	e.SetRedis(&RedisCache{client: errRedisClient{}, logger: testLogger()})
	_ = e.PurgeByTag("foo")
}

// TestEnginePurgeAllRedis covers engine.PurgeAll redis Close branch.
func TestEnginePurgeAllRedis(t *testing.T) {
	dir := t.TempDir()
	e, _ := newRedisStubEngine(t, dir)
	e.PurgeAll() // exercises memory + disk + redis.Close
}

// TestShouldBypassPHP covers the .php suffix bypass branch (engine.go:245).
func TestShouldBypassPHP(t *testing.T) {
	req := httptest.NewRequest("GET", "http://example.com/index.php", nil)
	if !ShouldBypass(req) {
		t.Fatal("expected bypass for .php request")
	}
}

// TestEngineGetByKeyDiskOnly covers GetByKey's L2 disk branch (no memory, no
// redis): fresh + stale promotion from disk.
func TestEngineGetByKeyDiskOnly(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := NewEngine(ctx, 1<<20, dir, 1<<30, testLogger())

	// Fresh on disk only.
	fk := "esi|h|/disk-fresh"
	if err := e.disk.Set(fk, freshResp("disk fresh", time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got, status := e.GetByKey(fk); got == nil || status != StatusHit {
		t.Fatalf("GetByKey disk fresh: status=%q", status)
	}

	// Stale on disk only.
	sk := "esi|h|/disk-stale"
	stale := &CachedResponse{
		StatusCode: 200, Headers: http.Header{}, Body: []byte("disk stale"),
		Created: time.Now().Add(-2 * time.Second), TTL: time.Second, GraceTTL: time.Hour,
	}
	if err := e.disk.Set(sk, stale); err != nil {
		t.Fatal(err)
	}
	if got, status := e.GetByKey(sk); got == nil || status != StatusStale {
		t.Fatalf("GetByKey disk stale: status=%q", status)
	}
}

// errFetcher returns an error for FetchFragment, exercising the error branches.
type errFetcher struct{}

func (errFetcher) FetchFragment(_, _ string, _ *http.Request) ([]byte, int, http.Header, error) {
	return nil, 0, nil, errors.New("upstream boom")
}

// TestESIFetcherError covers fetchFragment's fetcher-error branch (esi.go:126)
// plus the logger-not-nil warn branch in Process (esi.go:95).
func TestESIFetcherError(t *testing.T) {
	engine := &Engine{memory: NewMemoryCache(1 << 20)}
	p := NewESIProcessor(engine, errFetcher{}, testLogger(), 3)
	body := []byte(`<!--esi <esi:include src="/x" /> -->`)
	req := httptest.NewRequest("GET", "/", nil)
	out, err := p.Process(body, "h", req, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "ESI error: /x") {
		t.Fatalf("expected ESI error marker, got %q", out)
	}
}

// noCCFetcher returns a 200 fragment with no Cache-Control header, forcing the
// ttl<=0 default branch in fetchFragment (esi.go:135).
type noCCFetcher struct{ body []byte }

func (f noCCFetcher) FetchFragment(_, _ string, _ *http.Request) ([]byte, int, http.Header, error) {
	return f.body, 200, make(http.Header), nil
}

// TestESIFetchDefaultTTL covers the ttl<=0 -> 60s default branch.
func TestESIFetchDefaultTTL(t *testing.T) {
	engine := &Engine{memory: NewMemoryCache(1 << 20)}
	p := NewESIProcessor(engine, noCCFetcher{body: []byte("FRAG")}, nil, 3)
	body := []byte(`<!--esi <esi:include src="/noheader" /> -->`)
	req := httptest.NewRequest("GET", "/", nil)
	out, err := p.Process(body, "h", req, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "FRAG" {
		t.Fatalf("got %q, want FRAG", out)
	}
	// Fragment cached with the default TTL (~60s, fresh).
	cached, status := engine.GetByKey(esiFragmentKey("h", "/noheader"))
	if cached == nil || status != StatusHit {
		t.Fatalf("expected cached fragment, status=%q", status)
	}
	if cached.TTL != 60*time.Second {
		t.Fatalf("default TTL = %v, want 60s", cached.TTL)
	}
}

// TestRedisGetUnmarshalError covers redis.Get's json.Unmarshal error branch
// (redis.go:83) by seeding the stub with invalid JSON.
func TestRedisGetUnmarshalError(t *testing.T) {
	stub := newSyncRedisStub()
	stub.seed("k", "{not valid json")
	rc := &RedisCache{client: stub}
	if _, err := rc.Get("k"); err == nil {
		t.Fatal("expected unmarshal error")
	}
}

// TestDeserializeHeaderValueOverflow covers entry.go:167 — header key/val
// length declares more bytes than remain.
func TestDeserializeHeaderValueOverflow(t *testing.T) {
	r := &CachedResponse{
		StatusCode: 200,
		Headers:    http.Header{"X": {"y"}},
		Body:       []byte("b"),
		Created:    time.Now(),
		TTL:        time.Minute,
	}
	data := r.Serialize()
	// Layout: status@0(4) created@4(8) ttl@12(8) grace@20(8) headerCount@28(4)
	//         keyLen@32(4) valLen@36(4) key@40 val...
	// Truncate to 40 so keyLen+valLen are readable (both 1) but the key/val
	// bytes are missing -> errCorrupt at "pos+keyLen+valLen > len(data)".
	if _, err := Deserialize(data[:40]); err != errCorrupt {
		t.Fatalf("expected errCorrupt for header overflow, got %v", err)
	}
}

// TestDeserializeTruncatedTagSectionHeader covers entry.go:195 — the tag
// section begins (pos < len) but there aren't 4 bytes for the tag count.
func TestDeserializeTruncatedTagSectionHeader(t *testing.T) {
	base := buildMinimalSerialized([]byte("body"))
	// Append a single stray byte after the ESI flag: pos < len but pos+4 > len.
	corrupt := append(base, 0x01)
	if _, err := Deserialize(corrupt); err != errCorrupt {
		t.Fatalf("expected errCorrupt for truncated tag header, got %v", err)
	}
}

// TestDiskPurgeByTagUnreadableFile covers disk.go:118 — ReadFile error during
// the walk (a .cache path that is actually a directory).
func TestDiskPurgeByTagUnreadableFile(t *testing.T) {
	dir := t.TempDir()
	dc := NewDiskCache(dir, 1<<30)
	// A normal tagged entry so the walk has something to purge.
	if err := dc.Set("good", &CachedResponse{
		StatusCode: 200, Headers: http.Header{}, Body: []byte("x"),
		Created: time.Now(), TTL: time.Hour, Tags: []string{"news"},
	}); err != nil {
		t.Fatal(err)
	}
	// A ".cache" *file* that cannot be read (0 permissions) -> os.ReadFile
	// returns an error inside the walk, hitting the "return nil" skip branch.
	// (A directory named *.cache would be filtered by d.IsDir() earlier.)
	unreadable := dir + "/locked.cache"
	if err := os.WriteFile(unreadable, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(unreadable, 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(unreadable, 0644) })
	if n := dc.PurgeByTag("news"); n != 1 {
		t.Fatalf("expected 1 purged (skipping unreadable), got %d", n)
	}
}

// TestShouldBypassNoCacheHeader covers the Cache-Control: no-cache branch.
func TestShouldBypassNoCacheHeader(t *testing.T) {
	req := httptest.NewRequest("GET", "http://example.com/page", nil)
	req.Header.Set("Cache-Control", "no-cache")
	if !ShouldBypass(req) {
		t.Fatal("expected bypass for Cache-Control: no-cache")
	}
}

// TestMemoryCacheClose covers MemoryCache.Close (0% before), both with and
// without a started cleanup goroutine.
func TestMemoryCacheClose(t *testing.T) {
	// Without StartCleanup -> cancel is nil, no-op.
	mc := NewMemoryCache(1 << 20)
	mc.Close()

	// With StartCleanup -> cancel is set.
	mc2 := NewMemoryCache(1 << 20)
	mc2.StartCleanup(context.Background(), time.Hour)
	mc2.Close()
}

// TestNewESIProcessorDefaultDepth covers the maxDepth<=0 default branch.
func TestNewESIProcessorDefaultDepth(t *testing.T) {
	p := NewESIProcessor(&Engine{memory: NewMemoryCache(1 << 20)}, &mockFetcher{}, nil, 0)
	if p.maxDepth != ESIMaxDepthDefault {
		t.Fatalf("maxDepth = %d, want %d", p.maxDepth, ESIMaxDepthDefault)
	}
	p2 := NewESIProcessor(&Engine{memory: NewMemoryCache(1 << 20)}, &mockFetcher{}, nil, -5)
	if p2.maxDepth != ESIMaxDepthDefault {
		t.Fatalf("negative maxDepth not defaulted: %d", p2.maxDepth)
	}
}

// TestESIMaxIncludes covers the ESIMaxIncludes cap branch in Process.
func TestESIMaxIncludes(t *testing.T) {
	frags := map[string][]byte{"/f": []byte("F")}
	p := newTestESIProcessor(frags)

	var sb strings.Builder
	// Build a single esi comment containing > ESIMaxIncludes includes.
	sb.WriteString("<!--esi ")
	for i := 0; i < ESIMaxIncludes+5; i++ {
		sb.WriteString(`<esi:include src="/f" />`)
	}
	sb.WriteString(" -->")
	req := httptest.NewRequest("GET", "/", nil)

	result, err := p.Process([]byte(sb.String()), "h", req, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result), "max includes exceeded") {
		t.Fatalf("expected max-includes marker, got %q", result)
	}
}

// TestESIDepthLimitImmediate covers Process returning early when depth>=maxDepth.
func TestESIDepthLimitImmediate(t *testing.T) {
	p := newTestESIProcessor(map[string][]byte{"/x": []byte("X")})
	body := []byte(`<!--esi <esi:include src="/x" /> -->`)
	req := httptest.NewRequest("GET", "/", nil)
	// depth == maxDepth(3) -> returns body unchanged.
	out, err := p.Process(body, "h", req, nil, 3)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(body) {
		t.Fatalf("expected unchanged body at depth limit, got %q", out)
	}
}

// TestESIFetchFragmentCachedRecursive covers fetchFragment's cached-then-ESI
// recursive branch (lines 113-120): a cached fragment that itself contains ESI.
func TestESIFetchFragmentCachedRecursive(t *testing.T) {
	engine := &Engine{memory: NewMemoryCache(1 << 20)}
	fetcher := &mockFetcher{fragments: map[string][]byte{
		"/inner": []byte("INNER"),
	}}
	p := NewESIProcessor(engine, fetcher, nil, 5)

	// Pre-seed the outer fragment in cache with a body that contains ESI.
	outerKey := esiFragmentKey("h", "/outer")
	engine.SetByKey(outerKey, &CachedResponse{
		StatusCode: 200,
		Headers:    http.Header{},
		Body:       []byte(`OUTER<!--esi <esi:include src="/inner" /> -->`),
		Created:    time.Now(),
		TTL:        time.Hour,
	})

	body := []byte(`<!--esi <esi:include src="/outer" /> -->`)
	req := httptest.NewRequest("GET", "/", nil)
	out, err := p.Process(body, "h", req, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "OUTERINNER" {
		t.Fatalf("got %q, want OUTERINNER", out)
	}
}

// TestESIFetchFragmentNon200 covers fetchFragment when sub-request returns a
// non-200 status (line 129-131) -> error reported inline.
func TestESIFetchFragmentNon200(t *testing.T) {
	// mockFetcher returns 404 for unknown paths.
	p := newTestESIProcessor(map[string][]byte{})
	body := []byte(`<!--esi <esi:include src="/missing404" /> -->`)
	req := httptest.NewRequest("GET", "/", nil)
	out, err := p.Process(body, "h", req, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "ESI error: /missing404") {
		t.Fatalf("expected ESI error marker, got %q", out)
	}
}

// TestParseFragmentTTLNoMaxAge covers the case where Cache-Control is present
// but has no max-age directive (loop completes, returns 0).
func TestParseFragmentTTLNoMaxAge(t *testing.T) {
	h := make(http.Header)
	h.Set("Cache-Control", "public, no-transform")
	if ttl := parseFragmentTTL(h); ttl != 0 {
		t.Fatalf("expected 0 ttl, got %v", ttl)
	}
	// max-age=0 -> secs not > 0 -> falls through to 0.
	h2 := make(http.Header)
	h2.Set("Cache-Control", "max-age=0")
	if ttl := parseFragmentTTL(h2); ttl != 0 {
		t.Fatalf("expected 0 ttl for max-age=0, got %v", ttl)
	}
}

// TestGenerateKeyTLSAndPort covers the https branch and host port stripping
// in GenerateKey.
func TestGenerateKeyTLSAndPort(t *testing.T) {
	req := httptest.NewRequest("GET", "http://Example.COM:8443/path?z=1", nil)
	req.Host = "Example.COM:8443"
	req.TLS = &tls.ConnectionState{} // mark request as TLS
	k := GenerateKey(req, []string{"Accept-Encoding"})
	if !strings.HasPrefix(k, "GET|https|example.com|/path|") {
		t.Fatalf("unexpected key: %q", k)
	}
}

// TestWriteSortedQueryOverflow covers the >stackParamsCap overflow fallback
// branch in writeSortedQuery (strings.Split + sort).
func TestWriteSortedQueryOverflow(t *testing.T) {
	// Build a query with more than stackParamsCap params, intentionally
	// out of order so the sort branch is observable.
	parts := make([]string, 0, stackParamsCap+5)
	for i := stackParamsCap + 4; i >= 0; i-- {
		parts = append(parts, "p"+strconv.Itoa(i)+"=v")
	}
	raw := strings.Join(parts, "&")

	r1 := httptest.NewRequest("GET", "http://h/path?"+raw, nil)
	// Reverse order should canonicalize to the same key.
	rev := make([]string, len(parts))
	for i := range parts {
		rev[i] = parts[len(parts)-1-i]
	}
	r2 := httptest.NewRequest("GET", "http://h/path?"+strings.Join(rev, "&"), nil)

	k1 := GenerateKey(r1, nil)
	k2 := GenerateKey(r2, nil)
	if k1 != k2 {
		t.Fatalf("overflow query keys differ:\n%q\n%q", k1, k2)
	}
}

// TestWriteSortedQueryExactlyCap covers the boundary where the number of params
// equals stackParamsCap (the "len(parts)==stackParamsCap" overflow trigger).
func TestWriteSortedQueryExactlyCap(t *testing.T) {
	parts := make([]string, 0, stackParamsCap)
	for i := 0; i < stackParamsCap; i++ {
		parts = append(parts, "k"+strconv.Itoa(i)+"=v")
	}
	raw := strings.Join(parts, "&")
	r := httptest.NewRequest("GET", "http://h/p?"+raw, nil)
	k := GenerateKey(r, nil)
	if k == "" {
		t.Fatal("empty key")
	}
}

// TestDiskPurgeByTagSkipsNonCache covers PurgeByTag skipping non-.cache files.
func TestDiskPurgeByTagSkipsNonCache(t *testing.T) {
	dir := t.TempDir()
	dc := NewDiskCache(dir, 1<<30)

	// A tagged cache entry.
	resp := &CachedResponse{
		StatusCode: 200, Headers: http.Header{}, Body: []byte("x"),
		Created: time.Now(), TTL: time.Hour, Tags: []string{"news"},
	}
	if err := dc.Set("tagged", resp); err != nil {
		t.Fatal(err)
	}
	// A non-.cache file in the tree must be ignored by the walk.
	if err := writeFile(dir + "/random.txt"); err != nil {
		t.Fatal(err)
	}

	if n := dc.PurgeByTag("news"); n != 1 {
		t.Fatalf("expected 1 purged, got %d", n)
	}
	// Non-tagged entry survives.
	resp2 := &CachedResponse{
		StatusCode: 200, Headers: http.Header{}, Body: []byte("y"),
		Created: time.Now(), TTL: time.Hour, Tags: []string{"sports"},
	}
	dc.Set("other", resp2)
	if n := dc.PurgeByTag("news"); n != 0 {
		t.Fatalf("expected 0 purged on second pass, got %d", n)
	}
}

// TestDeserializeTruncatedTagCount covers the corrupt-tag-count branches in
// Deserialize (the tag section of the binary format).
func TestDeserializeTruncatedTagCount(t *testing.T) {
	// Build data with a valid header/body/ESI byte then a tag count of 1 but
	// no tag-length bytes.
	base := buildMinimalSerialized([]byte("body"))
	// append ESI byte already included by builder; now append tag count=1.
	withCount := append(base, 0, 0, 0, 1) // tagCount=1, no tag len follows
	if _, err := Deserialize(withCount); err != errCorrupt {
		t.Fatalf("expected errCorrupt for missing tag len, got %v", err)
	}

	// Tag count=1, tag len declared but data truncated.
	withLen := append(base, 0, 0, 0, 1, 0, 0, 0, 10) // tagLen=10 but no bytes
	if _, err := Deserialize(withLen); err != errCorrupt {
		t.Fatalf("expected errCorrupt for truncated tag data, got %v", err)
	}
}

// --- helpers ---

// buildMinimalSerialized constructs a valid serialized entry (header + body +
// ESI byte) with no tag section, suitable for appending crafted tag bytes.
func buildMinimalSerialized(body []byte) []byte {
	r := &CachedResponse{
		StatusCode: 200,
		Headers:    http.Header{},
		Body:       body,
		Created:    time.Now(),
		TTL:        time.Minute,
	}
	return r.Serialize()
}

func writeFile(path string) error {
	return os.WriteFile(path, []byte("data"), 0644)
}

// --- RESP client wire-level coverage using crafted servers ---

func startRespServer(t *testing.T, handler func(args []string, w *bufio.Writer)) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				r := bufio.NewReader(c)
				w := bufio.NewWriter(c)
				for {
					args, err := readArrayCmd(r)
					if err != nil {
						return
					}
					handler(args, w)
					w.Flush()
				}
			}(c)
		}
	}()
	return l.Addr().String()
}

// TestRespClientSelectDB covers the SELECT branch in dial (db != 0).
func TestRespClientSelectDB(t *testing.T) {
	addr := startRespServer(t, func(args []string, w *bufio.Writer) {
		switch strings.ToUpper(args[0]) {
		case "AUTH", "SELECT":
			w.WriteString("+OK\r\n")
		case "GET":
			w.WriteString("$-1\r\n")
		default:
			w.WriteString("+OK\r\n")
		}
	})
	c, err := newRespClient(config.RedisConfig{Enabled: true, Addr: addr, Password: "pw", DB: 3})
	if err != nil {
		t.Fatalf("newRespClient: %v", err)
	}
	defer c.Close()
	if _, err := c.Get(context.Background(), "k"); err != ErrRedisNotFound {
		t.Fatalf("expected not found, got %v", err)
	}
}

// TestRespClientSelectFailure covers the SELECT error branch in dial.
func TestRespClientSelectFailure(t *testing.T) {
	addr := startRespServer(t, func(args []string, w *bufio.Writer) {
		switch strings.ToUpper(args[0]) {
		case "SELECT":
			w.WriteString("-ERR bad db\r\n")
		default:
			w.WriteString("+OK\r\n")
		}
	})
	_, err := newRespClient(config.RedisConfig{Enabled: true, Addr: addr, DB: 9})
	if err == nil || !strings.Contains(err.Error(), "SELECT") {
		t.Fatalf("expected SELECT failure, got %v", err)
	}
}

// TestRespClientReplyTypes covers readReply integer (+:), error (-), and the
// simple-string and array paths, plus Del's integer reply.
func TestRespClientReplyTypes(t *testing.T) {
	addr := startRespServer(t, func(args []string, w *bufio.Writer) {
		switch strings.ToUpper(args[0]) {
		case "DEL":
			w.WriteString(":2\r\n") // integer reply
		case "SET":
			w.WriteString("+OK\r\n") // simple string
		case "KEYS":
			// array with one bulk string and one nil element (covered as skip).
			w.WriteString("*2\r\n$3\r\nabc\r\n$-1\r\n")
		default:
			w.WriteString("+OK\r\n")
		}
	})
	c, err := newRespClient(config.RedisConfig{Enabled: true, Addr: addr})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()

	if err := c.Set(ctx, "k", "v", 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := c.Del(ctx, "k1", "k2"); err != nil {
		t.Fatalf("Del: %v", err)
	}
	keys, err := c.Keys(ctx, "*")
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}
	if len(keys) != 1 || keys[0] != "abc" {
		t.Fatalf("Keys = %v, want [abc]", keys)
	}
}

// TestRespClientErrorReply covers readReply's '-' error branch surfacing.
func TestRespClientErrorReply(t *testing.T) {
	addr := startRespServer(t, func(args []string, w *bufio.Writer) {
		w.WriteString("-ERR boom\r\n")
	})
	c, err := newRespClient(config.RedisConfig{Enabled: true, Addr: addr})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Get(context.Background(), "k"); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected error reply surfaced, got %v", err)
	}
}

// TestRespClientGetWrongType covers Get's unexpected-reply-type branch when the
// server returns an integer instead of a bulk string.
func TestRespClientGetWrongType(t *testing.T) {
	addr := startRespServer(t, func(args []string, w *bufio.Writer) {
		w.WriteString(":42\r\n")
	})
	c, err := newRespClient(config.RedisConfig{Enabled: true, Addr: addr})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Get(context.Background(), "k"); err == nil || !strings.Contains(err.Error(), "unexpected reply type") {
		t.Fatalf("expected unexpected-type error, got %v", err)
	}
}

// TestRespClientKeysWrongType covers Keys' unexpected-reply-type branch.
func TestRespClientKeysWrongType(t *testing.T) {
	addr := startRespServer(t, func(args []string, w *bufio.Writer) {
		w.WriteString(":1\r\n") // integer, not array
	})
	c, err := newRespClient(config.RedisConfig{Enabled: true, Addr: addr})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Keys(context.Background(), "*"); err == nil || !strings.Contains(err.Error(), "unexpected reply type") {
		t.Fatalf("expected unexpected-type error, got %v", err)
	}
}

// TestRespClientKeysNilReply covers Keys when the reply is RESP nil (*-1).
func TestRespClientKeysNilReply(t *testing.T) {
	addr := startRespServer(t, func(args []string, w *bufio.Writer) {
		w.WriteString("*-1\r\n")
	})
	c, err := newRespClient(config.RedisConfig{Enabled: true, Addr: addr})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	keys, err := c.Keys(context.Background(), "*")
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}
	if keys != nil {
		t.Fatalf("expected nil keys, got %v", keys)
	}
}

// TestRespClientReconnect covers command()'s reconnect-on-net-error path: the
// server closes the connection mid-session, forcing a redial on the next call.
func TestRespClientReconnect(t *testing.T) {
	var connNum int
	var mu sync.Mutex
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			connNum++
			n := connNum
			mu.Unlock()
			go func(c net.Conn, n int) {
				defer c.Close()
				r := bufio.NewReader(c)
				w := bufio.NewWriter(c)
				for {
					args, err := readArrayCmd(r)
					if err != nil {
						return
					}
					if n == 1 && strings.ToUpper(args[0]) == "GET" {
						// First connection: drop it to force a reconnect.
						return
					}
					w.WriteString("$-1\r\n")
					w.Flush()
				}
			}(c, n)
		}
	}()

	c, err := newRespClient(config.RedisConfig{Enabled: true, Addr: l.Addr().String()})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	// First GET triggers the drop + reconnect; should still succeed (NotFound).
	if _, err := c.Get(context.Background(), "k"); err != ErrRedisNotFound {
		t.Fatalf("expected ErrRedisNotFound after reconnect, got %v", err)
	}
}

// TestIsNetErr directly exercises isNetErr's branches.
func TestIsNetErr(t *testing.T) {
	if isNetErr(nil) {
		t.Fatal("nil should not be net err")
	}
	if !isNetErr(io.EOF) {
		t.Fatal("EOF should be net err")
	}
	if !isNetErr(io.ErrUnexpectedEOF) {
		t.Fatal("ErrUnexpectedEOF should be net err")
	}
	if isNetErr(errPlain) {
		t.Fatal("plain error should not be net err")
	}
	// A real net.Error.
	_, derr := net.Dial("tcp", "127.0.0.1:1")
	if derr != nil && !isNetErr(derr) {
		t.Fatalf("dial error should be net err: %v", derr)
	}
}

// TestHostFromAddr covers hostFromAddr (0% before).
func TestHostFromAddr(t *testing.T) {
	if h := hostFromAddr("redis.example.com:6379"); h != "redis.example.com" {
		t.Fatalf("got %q", h)
	}
	if h := hostFromAddr("plainhost"); h != "plainhost" {
		t.Fatalf("got %q", h)
	}
}

// TestNewRedisCacheConnectError covers NewRedisCache when newRedisClient (real
// dial) fails — enabled config pointing at a closed port.
func TestNewRedisCacheConnectError(t *testing.T) {
	_, err := NewRedisCache(config.RedisConfig{Enabled: true, Addr: "127.0.0.1:1"}, testLogger())
	if err == nil {
		t.Fatal("expected connect error for closed port")
	}
}

// TestNewRedisCacheConnect covers NewRedisCache success path through the real
// RESP factory against the fake redis server.
func TestNewRedisCacheConnect(t *testing.T) {
	srv := newFakeRedis(t)
	rc, err := NewRedisCache(config.RedisConfig{Enabled: true, Addr: srv.addr(), Prefix: "p"}, testLogger())
	if err != nil {
		t.Fatalf("NewRedisCache: %v", err)
	}
	if rc == nil {
		t.Fatal("expected non-nil cache")
	}
	defer rc.Close()
	resp := freshResp("hello", time.Minute)
	if err := rc.Set("k", resp, time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := rc.Get("k")
	if err != nil || got == nil {
		t.Fatalf("Get: %v %v", got, err)
	}
	if err := rc.PurgeByTag("nomatch"); err != nil {
		t.Fatalf("PurgeByTag: %v", err)
	}
}

// --- Direct wire-protocol unit tests (writeArray / readReply / readLine) ---

// failWriter fails every write, to exercise writeArray's error branches.
type failWriter struct{ afterN int }

func (f *failWriter) Write(p []byte) (int, error) {
	if f.afterN <= 0 {
		return 0, errPlain
	}
	f.afterN -= len(p)
	if f.afterN < 0 {
		return 0, errPlain
	}
	return len(p), nil
}

// TestWriteArrayErrors covers writeArray's three error branches by failing the
// underlying writer at different points. bufio buffers, so we use a tiny buffer
// and flush to surface the error.
func TestWriteArrayErrors(t *testing.T) {
	// Fail on the very first byte (the "*N\r\n" header write via Flush).
	w := bufio.NewWriterSize(&failWriter{afterN: 0}, 1)
	if err := writeArray(w, []string{"PING"}); err == nil {
		if ferr := w.Flush(); ferr == nil {
			t.Fatal("expected write/flush error")
		}
	}
}

// TestWriteArrayPerArgErrors covers writeArray's per-argument write branches by
// allowing the header write to succeed, then failing on the argument bytes.
func TestWriteArrayPerArgErrors(t *testing.T) {
	// Allow ~6 bytes through (enough for "*1\r\n$1\r\n" header pieces) then fail.
	for _, budget := range []int{4, 7, 9} {
		w := bufio.NewWriterSize(&failWriter{afterN: budget}, 1)
		err := writeArray(w, []string{"AB"})
		if err == nil {
			err = w.Flush()
		}
		if err == nil {
			t.Fatalf("budget %d: expected a write error", budget)
		}
	}
}

// TestCommandLockedNotConnected covers commandLocked's conn==nil guard.
func TestCommandLockedNotConnected(t *testing.T) {
	addr := startRespServer(t, func(args []string, w *bufio.Writer) { w.WriteString("+OK\r\n") })
	c, err := newRespClient(config.RedisConfig{Enabled: true, Addr: addr})
	if err != nil {
		t.Fatal(err)
	}
	c.Close() // conn becomes nil
	if _, err := c.commandLocked("PING"); err == nil {
		t.Fatal("expected 'not connected' error")
	}
}

// TestCommandDialFails covers command()'s "conn == nil -> dial fails" branch by
// pointing a freshly-built (never-dialed) client at a closed port.
func TestCommandDialFails(t *testing.T) {
	c := &respClient{addr: "127.0.0.1:1"} // conn is nil, dial will fail
	if _, err := c.command("PING"); err == nil {
		t.Fatal("expected dial failure on first command")
	}
}

// TestReadReplyVariants covers readReply's parse branches directly.
func TestReadReplyVariants(t *testing.T) {
	read := func(s string) (any, error) {
		return readReply(bufio.NewReader(strings.NewReader(s)))
	}

	// Empty line at EOF -> read error from readLine.
	if _, err := read(""); err == nil {
		t.Fatal("expected error on empty stream")
	}
	// Bad integer.
	if _, err := read(":notanint\r\n"); err == nil {
		t.Fatal("expected bad integer error")
	}
	// Bad bulk length.
	if _, err := read("$xx\r\n"); err == nil {
		t.Fatal("expected bad bulk len error")
	}
	// Bulk with truncated payload (declares 5 bytes, supplies fewer).
	if _, err := read("$5\r\nab"); err == nil {
		t.Fatal("expected truncated bulk error")
	}
	// Bad array length.
	if _, err := read("*xx\r\n"); err == nil {
		t.Fatal("expected bad array len error")
	}
	// Array element parse error (nested bad integer).
	if _, err := read("*1\r\n:bad\r\n"); err == nil {
		t.Fatal("expected nested array element error")
	}
	// Unknown reply type.
	if _, err := read("?weird\r\n"); err == nil {
		t.Fatal("expected unknown reply type error")
	}
	// Simple string OK.
	if v, err := read("+OK\r\n"); err != nil || v != "OK" {
		t.Fatalf("simple string: %v %v", v, err)
	}
	// Integer OK.
	if v, err := read(":7\r\n"); err != nil || v != int64(7) {
		t.Fatalf("integer: %v %v", v, err)
	}
	// RESP nil bulk.
	if v, err := read("$-1\r\n"); err != nil || v != nil {
		t.Fatalf("nil bulk: %v %v", v, err)
	}
	// RESP nil array.
	if v, err := read("*-1\r\n"); err != nil || v != nil {
		t.Fatalf("nil array: %v %v", v, err)
	}
}

// TestReadLineMalformed covers readLine's malformed-line branch (no \r before \n).
func TestReadLineMalformed(t *testing.T) {
	if _, err := readLine(bufio.NewReader(strings.NewReader("noCR\n"))); err == nil {
		t.Fatal("expected malformed line error")
	}
	if _, err := readLine(bufio.NewReader(strings.NewReader("nolf"))); err == nil {
		t.Fatal("expected read error at EOF")
	}
}

// TestReadReplyEmptyLine covers the explicit "empty reply" branch (a bare CRLF).
func TestReadReplyEmptyLine(t *testing.T) {
	if _, err := readReply(bufio.NewReader(strings.NewReader("\r\n"))); err == nil {
		t.Fatal("expected empty reply error")
	}
}

// TestRespClientSetNoTTL / sub-second TTL: covers Set's ttl<=0 plain branch and
// the secs<1 -> 1 clamp.
func TestRespClientSetTTLBranches(t *testing.T) {
	var lastArgs []string
	addr := startRespServer(t, func(args []string, w *bufio.Writer) {
		lastArgs = append([]string(nil), args...)
		w.WriteString("+OK\r\n")
	})
	c, err := newRespClient(config.RedisConfig{Enabled: true, Addr: addr})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()

	// Sub-second positive TTL clamps to "EX 1".
	if err := c.Set(ctx, "k", "v", 10*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if len(lastArgs) != 5 || lastArgs[4] != "1" {
		t.Fatalf("expected EX 1 clamp, got %v", lastArgs)
	}
	// Zero TTL -> plain SET (no EX).
	if err := c.Set(ctx, "k", "v", 0); err != nil {
		t.Fatal(err)
	}
	if len(lastArgs) != 3 {
		t.Fatalf("expected plain SET, got %v", lastArgs)
	}
}

// TestRespClientDelEmpty covers Del's empty-keys short-circuit.
func TestRespClientDelEmpty(t *testing.T) {
	addr := startRespServer(t, func(args []string, w *bufio.Writer) { w.WriteString("+OK\r\n") })
	c, err := newRespClient(config.RedisConfig{Enabled: true, Addr: addr})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Del(context.Background()); err != nil {
		t.Fatalf("Del with no keys should be a no-op, got %v", err)
	}
}

// TestRespClientDelError covers Del's command-error propagation.
func TestRespClientDelError(t *testing.T) {
	addr := startRespServer(t, func(args []string, w *bufio.Writer) { w.WriteString("-ERR nope\r\n") })
	c, err := newRespClient(config.RedisConfig{Enabled: true, Addr: addr})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Del(context.Background(), "k"); err == nil {
		t.Fatal("expected Del error")
	}
}

// TestRespClientSetError covers Set's command-error propagation.
func TestRespClientSetError(t *testing.T) {
	addr := startRespServer(t, func(args []string, w *bufio.Writer) { w.WriteString("-ERR nope\r\n") })
	c, err := newRespClient(config.RedisConfig{Enabled: true, Addr: addr})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Set(context.Background(), "k", "v", time.Minute); err == nil {
		t.Fatal("expected Set error")
	}
}

// TestRespClientKeysError covers Keys' command-error propagation.
func TestRespClientKeysError(t *testing.T) {
	addr := startRespServer(t, func(args []string, w *bufio.Writer) { w.WriteString("-ERR nope\r\n") })
	c, err := newRespClient(config.RedisConfig{Enabled: true, Addr: addr})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Keys(context.Background(), "*"); err == nil {
		t.Fatal("expected Keys error")
	}
}

// TestRespClientCloseTwice covers Close's conn==nil branch on the second call.
func TestRespClientCloseTwice(t *testing.T) {
	addr := startRespServer(t, func(args []string, w *bufio.Writer) { w.WriteString("+OK\r\n") })
	c, err := newRespClient(config.RedisConfig{Enabled: true, Addr: addr})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil { // conn already nil -> no-op
		t.Fatalf("second Close should be no-op, got %v", err)
	}
}

// TestRespClientCommandAfterClose covers command()'s "conn == nil -> dial"
// re-establish branch: Close drops the conn, the next call must redial.
func TestRespClientCommandAfterClose(t *testing.T) {
	addr := startRespServer(t, func(args []string, w *bufio.Writer) {
		if strings.ToUpper(args[0]) == "GET" {
			w.WriteString("$-1\r\n")
			return
		}
		w.WriteString("+OK\r\n")
	})
	c, err := newRespClient(config.RedisConfig{Enabled: true, Addr: addr})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	// Drop the connection, then issue a command -> triggers redial.
	c.Close()
	if _, err := c.Get(context.Background(), "k"); err != ErrRedisNotFound {
		t.Fatalf("expected redial + not found, got %v", err)
	}
}

// TestRespClientReconnectDialFails covers command()'s reconnect path when the
// redial itself fails (server already gone).
func TestRespClientReconnectDialFails(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	accepted := make(chan struct{})
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		close(accepted)
		// Read the first command then drop the connection without replying.
		r := bufio.NewReader(c)
		_, _ = readArrayCmd(r)
		c.Close()
	}()

	c, err := newRespClient(config.RedisConfig{Enabled: true, Addr: addr})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	<-accepted
	l.Close() // server gone; reconnect will fail
	if _, err := c.Get(context.Background(), "k"); err == nil {
		t.Fatal("expected error when reconnect dial fails")
	}
}

// TestNewRespClientTLSDialFails covers the TLS dial branch (cfg.TLS=true). The
// fake server speaks plain TCP, so the TLS handshake fails — which is enough to
// execute the TLS config + tls.DialWithDialer branch.
func TestNewRespClientTLSDialFails(t *testing.T) {
	addr := startRespServer(t, func(args []string, w *bufio.Writer) { w.WriteString("+OK\r\n") })
	_, err := newRespClient(config.RedisConfig{Enabled: true, Addr: addr, TLS: true})
	if err == nil {
		t.Fatal("expected TLS handshake failure against plain server")
	}
}

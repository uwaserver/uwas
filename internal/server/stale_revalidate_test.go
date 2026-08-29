package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/cache"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// staleServer wires a proxy domain in front of a counting origin, so a test
// can tell a background refresh apart from a cache hit by asking the origin
// how many times it was called.
func staleServer(t *testing.T, ttl, graceTTL int, swr bool) (*Server, *atomic.Int64, *atomic.Value) {
	t.Helper()

	var hits atomic.Int64
	var body atomic.Value
	body.Store("v1")

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, body.Load().(string))
	}))
	t.Cleanup(origin.Close)

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1", LogLevel: "error", LogFormat: "text",
			Cache: config.CacheConfig{
				Enabled:              true,
				MemoryLimit:          config.ByteSize(8 << 20),
				DefaultTTL:           ttl,
				GraceTTL:             graceTTL,
				StaleWhileRevalidate: swr,
			},
		},
		Domains: []config.Domain{{
			Host:  "stale.test",
			Type:  "proxy",
			Root:  t.TempDir(),
			SSL:   config.SSLConfig{Mode: "off"},
			Cache: config.DomainCache{Enabled: true, TTL: ttl},
			Proxy: config.ProxyConfig{
				Upstreams: []config.Upstream{{Address: origin.URL, Weight: 1}},
			},
		}},
	}
	s := New(cfg, logger.New("error", "text"))
	t.Cleanup(func() { s.cancel() })
	return s, &hits, &body
}

func fetch(t *testing.T, s *Server) (status, body string) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "stale.test"
	s.handleRequest(rec, req)
	return rec.Header().Get("X-Cache"), rec.Body.String()
}

// waitFor polls until cond holds, so a test never depends on a fixed sleep
// being long enough for a background goroutine.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestStaleWhileRevalidateServesStaleAndRefreshes is the whole feature: past
// its TTL an entry is served stale instead of forcing the visitor to wait,
// and a refresh behind that response makes the next one fresh.
func TestStaleWhileRevalidateServesStaleAndRefreshes(t *testing.T) {
	s, hits, body := staleServer(t, 1, 60, true)

	if st, b := fetch(t, s); b != "v1" {
		t.Fatalf("first request: status=%q body=%q, want v1", st, b)
	}
	if st, _ := fetch(t, s); st != cache.StatusHit {
		t.Fatalf("second request X-Cache = %q, want HIT", st)
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("origin called %d times for two requests, want 1", n)
	}

	body.Store("v2")
	time.Sleep(1100 * time.Millisecond) // past TTL, inside grace

	// The visitor gets the old copy immediately — that is the trade the
	// feature makes — and pays nothing for the refresh.
	st, b := fetch(t, s)
	if st != cache.StatusStale {
		t.Fatalf("X-Cache = %q past TTL, want STALE", st)
	}
	if b != "v1" {
		t.Errorf("stale request served %q, want the old copy v1", b)
	}

	waitFor(t, "the background refresh to reach the origin", func() bool { return hits.Load() >= 2 })
	waitFor(t, "the refreshed entry to be served", func() bool {
		_, b := fetch(t, s)
		return b == "v2"
	})
}

// TestStaleWithoutFlagExpiresNormally pins the gate. grace_ttl defaults to 24
// hours, so an entry must not linger past its TTL unless the operator asked
// for stale-while-revalidate.
func TestStaleWithoutFlagExpiresNormally(t *testing.T) {
	s, hits, _ := staleServer(t, 1, 60, false)

	fetch(t, s)
	if st, _ := fetch(t, s); st != cache.StatusHit {
		t.Fatalf("second request X-Cache = %q, want HIT", st)
	}
	time.Sleep(1100 * time.Millisecond)

	if st, _ := fetch(t, s); st == cache.StatusStale {
		t.Error("entry went stale with stale_while_revalidate off")
	}
	if n := hits.Load(); n < 2 {
		t.Errorf("origin called %d times, want a real re-fetch after expiry", n)
	}
}

// TestStaleRevalidateIsSingleFlight is the reason the guard exists: an entry
// goes stale for every concurrent visitor at once, so without it a popular
// URL sends one origin request per in-flight request — the stampede the cache
// is there to prevent.
func TestStaleRevalidateIsSingleFlight(t *testing.T) {
	s, hits, _ := staleServer(t, 1, 60, true)

	fetch(t, s)
	time.Sleep(1100 * time.Millisecond)
	before := hits.Load()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fetch(t, s)
		}()
	}
	wg.Wait()

	waitFor(t, "a refresh to reach the origin", func() bool { return hits.Load() > before })
	time.Sleep(200 * time.Millisecond) // let any duplicate refreshes land

	if got := hits.Load() - before; got != 1 {
		t.Errorf("50 concurrent stale requests caused %d origin fetches, want exactly 1", got)
	}
}

// TestRevalidateDoesNotPolluteRequestMetrics checks the refresh goes through
// dispatchHandler rather than the full request path: a synthetic fetch must
// not be counted as a visitor.
func TestRevalidateDoesNotPolluteRequestMetrics(t *testing.T) {
	s, hits, _ := staleServer(t, 1, 60, true)

	fetch(t, s)
	time.Sleep(1100 * time.Millisecond)
	fetch(t, s) // stale, schedules a refresh

	waitFor(t, "the background refresh", func() bool { return hits.Load() >= 2 })
	time.Sleep(100 * time.Millisecond)

	// Two requests reached handleRequest. The refresh went through
	// dispatchHandler, so it must not appear here.
	if got := s.metrics.RequestsTotal.Load(); got != 2 {
		t.Errorf("RequestsTotal = %d, want 2 — the background refresh was counted as a visitor request", got)
	}
}

// TestRequestsTotalCountsEachRequestOnce is a regression for a double count
// found while writing the test above: the deferred metrics block incremented
// RequestsTotal directly and then called RecordRequest, which increments it
// again. Every request was counted twice, so uwas_requests_total and the
// panel's request stat read double the real traffic while RequestsByCode —
// updated once, inside RecordRequest — stayed right and disagreed with them.
func TestRequestsTotalCountsEachRequestOnce(t *testing.T) {
	s, _, _ := staleServer(t, 60, 0, false)

	const n = 5
	for i := 0; i < n; i++ {
		fetch(t, s)
	}

	if got := s.metrics.RequestsTotal.Load(); got != n {
		t.Errorf("RequestsTotal = %d after %d requests, want %d", got, n, n)
	}

	var byCode int64
	for i := range s.metrics.RequestsByCode {
		byCode += s.metrics.RequestsByCode[i].Load()
	}
	if total := s.metrics.RequestsTotal.Load(); byCode != total {
		t.Errorf("RequestsByCode sums to %d but RequestsTotal is %d — the two counters disagree", byCode, total)
	}
}

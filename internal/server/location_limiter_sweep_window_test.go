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

func limiterWindowServer(t *testing.T, requests int, window time.Duration) *Server {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "reset"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Global: config.GlobalConfig{WorkerCount: "1", LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{{
			Host: "win.example.com", Type: "static", Root: dir, SSL: config.SSLConfig{Mode: "off"},
			Locations: []config.LocationConfig{{
				Match:     "/reset",
				RateLimit: &config.RateLimitConfig{Requests: requests, Window: config.Duration{Duration: window}},
			}},
		}},
	}
	s := New(cfg, logger.New("error", "text"))
	t.Cleanup(func() { s.cancel(); time.Sleep(50 * time.Millisecond) })
	s.handler = s.buildMiddlewareChain()
	return s
}

func limiterWindowGet(s *Server, remote string) int {
	req := httptest.NewRequest(http.MethodGet, "/reset", nil)
	req.Host = "win.example.com"
	req.RemoteAddr = remote
	req.Header.Set("User-Agent", "uwas-test")
	rec := httptest.NewRecorder()
	s.handler.ServeHTTP(rec, req)
	return rec.Code
}

// F48000: the janitor evicted a per-location counter once its window had
// STARTED more than 30 minutes ago, whatever the window was. A limit such as
// "5 password resets per hour" (window 1h) therefore lost its counter in the
// middle of the window: a client that had used its whole quota got a fresh one
// at the next janitor tick, up to once every few minutes for as long as it kept
// going.
func TestLocationLimiterSweepKeepsCountersOfLiveWindows(t *testing.T) {
	const window = time.Hour
	s := limiterWindowServer(t, 2, window)
	const client = "203.0.113.50:1234"

	// Spend the whole quota.
	for i := 0; i < 2; i++ {
		if c := limiterWindowGet(s, client); c != http.StatusOK {
			t.Fatalf("control: request %d = %d, want 200", i+1, c)
		}
	}
	if c := limiterWindowGet(s, client); c != http.StatusTooManyRequests {
		t.Fatalf("control: request 3 = %d, want 429", c)
	}

	// 31 minutes into the hour-long window the janitor runs. The client is
	// still hitting the limit, so the window is plainly live.
	s.locationLimiters.Range(func(_, v any) bool {
		e := v.(*rateLimitEntry)
		e.mu.Lock()
		e.windowStart = e.windowStart.Add(-31 * time.Minute)
		e.lastAccess = e.lastAccess.Add(-time.Minute)
		e.mu.Unlock()
		return true
	})
	s.sweepLocationLimiters(time.Now(), 30*time.Minute)

	got := limiterWindowGet(s, client)
	t.Logf("EXPECTED: 429 (the 1h window is still running)\nACTUAL: %d", got)
	if got != http.StatusTooManyRequests {
		t.Fatalf("PROBLEM CONFIRMED: the counter of a live 1h window was evicted after 31 minutes; request answered %d, want 429", got)
	}
}

// The janitor still has to reclaim what is really idle: a counter whose window
// ended and that nobody touched for the idle period, and one of a short window
// that went quiet.
func TestLocationLimiterSweepStillEvictsIdleEntries(t *testing.T) {
	s := limiterWindowServer(t, 2, time.Minute)
	old := time.Now().Add(-2 * time.Hour)
	s.locationLimiters.Store("stale-window", &rateLimitEntry{windowStart: old, lastAccess: old, window: time.Minute})
	s.locationLimiters.Store("stale-long", &rateLimitEntry{windowStart: old, lastAccess: old, window: time.Hour})
	s.locationLimiters.Store("recent", &rateLimitEntry{windowStart: time.Now(), lastAccess: time.Now(), window: time.Minute})

	s.sweepLocationLimiters(time.Now(), 30*time.Minute)

	for _, k := range []string{"stale-window", "stale-long"} {
		if _, ok := s.locationLimiters.Load(k); ok {
			t.Errorf("%s: idle entry past its window was not evicted", k)
		}
	}
	if _, ok := s.locationLimiters.Load("recent"); !ok {
		t.Error("recent entry was evicted")
	}
}

// F48001: a request that found a counter idle for more than ten windows reset
// it and then deleted it from the map, which threw away the request it had just
// counted; the next request started a fresh counter. After every long pause one
// extra request got through (limit+1 in the first window).
func TestLocationLimiterStaleResetCountsTheRequest(t *testing.T) {
	s := limiterWindowServer(t, 2, time.Minute)
	const client = "203.0.113.60:1234"
	key := "win.example.com|/reset|203.0.113.60"
	old := time.Now().Add(-time.Hour)
	s.locationLimiters.Store(key, &rateLimitEntry{windowStart: old, lastAccess: old, count: 99, window: time.Minute})

	var codes []int
	for i := 0; i < 4; i++ {
		codes = append(codes, limiterWindowGet(s, client))
	}
	t.Logf("EXPECTED: [200 200 429 429]\nACTUAL: %v", codes)
	for i, want := range []int{200, 200, 429, 429} {
		if codes[i] != want {
			t.Fatalf("PROBLEM CONFIRMED: after a long pause request %d = %d, want %d (all %v)", i+1, codes[i], want, codes)
		}
	}
}

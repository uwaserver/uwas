package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// A flood aimed at the non-canonical host (the apex, when www is primary) was
// answered 301 before the domain's rate limit ran: no request was ever
// refused or counted, so the autoblocker never saw the flooding IPs.
func TestCanonicalRedirectIsRateLimited(t *testing.T) {
	s := statsFixture(t, config.Domain{
		Host: "rl.test", Type: "static", SSL: config.SSLConfig{Mode: "off"},
		CanonicalHost: "www",
		Security:      config.SecurityConfig{RateLimit: config.RateLimitConfig{Requests: 2}},
	})
	h := s.buildMiddlewareChain()

	codes := map[int]int{}
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Host = "rl.test"
		req.RemoteAddr = "203.0.113.7:1111"
		req.Header.Set("User-Agent", "uwas-test")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		codes[rec.Code]++
	}
	if codes[http.StatusMovedPermanently] != 2 || codes[http.StatusTooManyRequests] != 3 {
		t.Fatalf("want 2×301 then 3×429, got %v", codes)
	}
	if got := s.securityStats.RateBlocked.Load(); got != 3 {
		t.Errorf("rate_blocked = %d, want 3: redirect rejections must feed the autoblocker", got)
	}
}

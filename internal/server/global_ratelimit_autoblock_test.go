package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
)

// The global limiter answered 429 without recording it, so it could throttle
// an IP forever but never escalate it to a block.
func TestGlobalRateLimitFeedsSecurityStats(t *testing.T) {
	s := statsFixture(t, config.Domain{
		Host: "grl.test", Type: "static", SSL: config.SSLConfig{Mode: "off"},
	})
	s.config.Global.RateLimit = config.RateLimitConfig{
		Requests: 2, Window: config.Duration{Duration: time.Minute},
	}
	h := s.buildMiddlewareChain()

	rejected := 0
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/index.html", nil)
		req.Host = "grl.test"
		req.RemoteAddr = "203.0.113.8:2222"
		req.Header.Set("User-Agent", "uwas-test")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			rejected++
		}
	}
	if rejected != 3 {
		t.Fatalf("global limiter rejected %d requests, want 3", rejected)
	}
	if got := s.securityStats.RateBlocked.Load(); got != 3 {
		t.Errorf("rate_blocked = %d, want 3: global 429s must feed the autoblocker", got)
	}
}

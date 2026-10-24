package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// F25700: Retry-After is whole seconds rounded up, never 0.
func TestRateLimitRetryAfterRoundsUp(t *testing.T) {
	cases := []struct {
		window time.Duration
		want   string
	}{
		{2 * time.Second, "2"},
		{time.Minute, "60"},
		{time.Second, "1"},
		{500 * time.Millisecond, "1"},
		{1500 * time.Millisecond, "2"},
	}
	for _, c := range cases {
		ctx, cancel := context.WithCancel(context.Background())
		h := RateLimit(ctx, 1, c.window)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		var last *httptest.ResponseRecorder
		for i := 0; i < 2; i++ {
			last = httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = "203.0.113.9:1234"
			h.ServeHTTP(last, r)
		}
		cancel()
		if last.Code != http.StatusTooManyRequests {
			t.Fatalf("window %v: status %d, want 429", c.window, last.Code)
		}
		if got := last.Header().Get("Retry-After"); got != c.want {
			t.Errorf("window %v: Retry-After %q, want %q", c.window, got, c.want)
		}
	}
}

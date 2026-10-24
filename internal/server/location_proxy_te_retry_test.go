package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
)

// F25800: the location proxy dropped "TE: trailers" as hop-by-hop, so a gRPC
// client's trailer request never reached the backend (the domain proxy keeps
// it since F25500).
func TestLocationProxyForwardsTETrailers(t *testing.T) {
	var got []string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got[:0], r.Header.Values("Te")...)
		w.Header().Set("X-Seen", "1")
	}))
	defer up.Close()
	s := locationProxyFixture(t, up.URL)

	for _, tc := range []struct {
		name, te, want string
	}{
		{"trailers", "trailers", "trailers"},
		{"mixed", "gzip, trailers", "trailers"},
		{"gzip only (control)", "gzip", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got = nil
			req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
			req.Host = "a.test"
			req.Header.Set("User-Agent", "uwas-test")
			req.Header.Set("TE", tc.te)
			rec := httptest.NewRecorder()
			s.handler.ServeHTTP(rec, req)
			if rec.Header().Get("X-Seen") != "1" {
				t.Fatalf("upstream not reached: %d", rec.Code)
			}
			joined := ""
			if len(got) > 0 {
				joined = got[0]
			}
			if joined != tc.want {
				t.Errorf("upstream TE = %q, want %q", joined, tc.want)
			}
		})
	}
}

// F25801: the per-location limiter truncated the window to whole seconds, so
// a 1.5s window answered "Retry-After: 1" (client returns before it ends).
func TestLocationRateLimitRetryAfterRoundsUp(t *testing.T) {
	for _, tc := range []struct {
		window time.Duration
		want   string
	}{
		{1500 * time.Millisecond, "2"},
		{500 * time.Millisecond, "1"},
		{2 * time.Second, "2"}, // control: exact seconds unchanged
	} {
		cfg := &config.Config{
			Global: config.GlobalConfig{LogLevel: "error", LogFormat: "text"},
			Domains: []config.Domain{{
				Host: "retry.example.com", Type: "static", Root: t.TempDir(),
				Locations: []config.LocationConfig{{
					Match:     "/api/",
					RateLimit: &config.RateLimitConfig{Requests: 1, Window: config.Duration{Duration: tc.window}},
				}},
			}},
		}
		s := newMinimalServer(cfg)
		var last *httptest.ResponseRecorder
		for i := 0; i < 2; i++ {
			last = httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/api/test", nil)
			req.Host = "retry.example.com"
			req.RemoteAddr = "1.2.3.4:12345"
			s.handleRequest(last, req)
		}
		if last.Code != http.StatusTooManyRequests {
			t.Fatalf("window %v: want 429, got %d", tc.window, last.Code)
		}
		if got := last.Header().Get("Retry-After"); got != tc.want {
			t.Errorf("window %v: Retry-After = %q, want %q", tc.window, got, tc.want)
		}
	}
}

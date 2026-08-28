package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// locations[].request_timeout was dead configuration: defined, merged and
// echoed back by the API, and no runtime path read it. A path configured to
// give up after 100ms waited as long as the upstream took.

func timeoutFixture(t *testing.T, locs []config.LocationConfig) http.Handler {
	t.Helper()

	cfg := &config.Config{
		Global: config.GlobalConfig{WorkerCount: "1", LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{{
			Host:      "loc.test",
			Type:      "static",
			Root:      t.TempDir(),
			SSL:       config.SSLConfig{Mode: "off"},
			Locations: locs,
		}},
	}
	s := New(cfg, logger.New("error", "text"))
	t.Cleanup(func() { s.cancel() })
	return s.buildMiddlewareChain()
}

// slowUpstream blocks until the request context is cancelled or delay passes.
func slowUpstream(t *testing.T, delay time.Duration) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(delay):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("late response"))
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func locationRequest(h http.Handler, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Host = "loc.test"
	req.Header.Set("User-Agent", "uwas-test")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// The timeout must fire, and be reported as a gateway timeout rather than a
// bad gateway: the upstream was reachable, UWAS gave up waiting.
func TestLocationRequestTimeoutFires(t *testing.T) {
	up := slowUpstream(t, 10*time.Second)
	h := timeoutFixture(t, []config.LocationConfig{{
		Match:          "/slow/",
		ProxyPass:      up.URL,
		RequestTimeout: config.Duration{Duration: 150 * time.Millisecond},
	}})

	start := time.Now()
	rec := locationRequest(h, "/slow/x")
	elapsed := time.Since(start)

	if rec.Code != http.StatusGatewayTimeout {
		t.Errorf("status = %d, want 504 — request_timeout is not applied", rec.Code)
	}
	if elapsed > 3*time.Second {
		t.Errorf("the request took %v — a 150ms timeout was expected", elapsed)
	}
}

// A path that responds inside the budget must be served normally.
func TestLocationRequestTimeoutAllowsFastResponse(t *testing.T) {
	up := slowUpstream(t, 10*time.Millisecond)
	h := timeoutFixture(t, []config.LocationConfig{{
		Match:          "/fast/",
		ProxyPass:      up.URL,
		RequestTimeout: config.Duration{Duration: 5 * time.Second},
	}})

	rec := locationRequest(h, "/fast/x")
	if rec.Code != http.StatusOK {
		t.Errorf("durum = %d, want 200", rec.Code)
	}
	if body := rec.Body.String(); body != "late response" {
		t.Errorf("body = %q", body)
	}
}

// No timeout configured must not impose one.
func TestLocationWithoutTimeoutIsUnbounded(t *testing.T) {
	up := slowUpstream(t, 200*time.Millisecond)
	h := timeoutFixture(t, []config.LocationConfig{{Match: "/x/", ProxyPass: up.URL}})

	if rec := locationRequest(h, "/x/y"); rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 — cut off with no timeout configured", rec.Code)
	}
}

// The location loop breaks on its first match (as in nginx), so a later,
// longer timeout on an overlapping path must not apply.
func TestLocationFirstMatchTimeoutWins(t *testing.T) {
	up := slowUpstream(t, 10*time.Second)
	h := timeoutFixture(t, []config.LocationConfig{
		{Match: "/a/", ProxyPass: up.URL, RequestTimeout: config.Duration{Duration: 150 * time.Millisecond}},
		{Match: "/a/b", ProxyPass: up.URL, RequestTimeout: config.Duration{Duration: 30 * time.Second}},
	})

	start := time.Now()
	rec := locationRequest(h, "/a/b")
	elapsed := time.Since(start)

	if elapsed > 3*time.Second {
		t.Errorf("the request took %v — the later location's 30s was applied", elapsed)
	}
	if rec.Code != http.StatusGatewayTimeout {
		t.Errorf("durum = %d, want 504", rec.Code)
	}
}

func TestContextDeadlineMapsToGatewayTimeout(t *testing.T) {
	// Guards the errors.Is branch directly.
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()
	if !isDeadline(ctx.Err()) {
		t.Error("DeadlineExceeded was not recognised")
	}
}

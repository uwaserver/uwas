package monitor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

func init() {
	// httptest binds to loopback, which the SSRF gate rejects by default.
	monitorURLSafetyCheck = func(string) error { return nil }
}

// TestDownRequiresConsecutiveFailures is the regression for healthy sites
// behind a CDN reading as down: one slow or challenged reply used to flip the
// badge on the spot.
func TestDownRequiresConsecutiveFailures(t *testing.T) {
	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	host := srv.Listener.Addr().String()
	m := New([]config.Domain{{Host: host, SSL: config.SSLConfig{Mode: "off"}}}, logger.New("error", "text"))
	ctx := context.Background()

	m.checkDomain(ctx, config.Domain{Host: host})
	if got := status(t, m, host); got != "up" {
		t.Fatalf("after a healthy check status = %q, want up", got)
	}

	fail.Store(true)
	m.checkDomain(ctx, config.Domain{Host: host})
	if got := status(t, m, host); got != "up" {
		t.Errorf("after ONE bad check status = %q, want up — a single blip must not flip it", got)
	}

	m.checkDomain(ctx, config.Domain{Host: host})
	if got := status(t, m, host); got != "down" {
		t.Errorf("after two consecutive bad checks status = %q, want down", got)
	}

	// Recovery is immediate: there is no reason to make an operator wait to
	// learn the site came back.
	fail.Store(false)
	m.checkDomain(ctx, config.Domain{Host: host})
	if got := status(t, m, host); got != "up" {
		t.Errorf("after recovery status = %q, want up", got)
	}
}

// TestFirstCheckReportsImmediately keeps a genuinely dead site from showing as
// blank or up until the second sweep.
func TestFirstCheckReportsImmediately(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	host := srv.Listener.Addr().String()
	m := New(nil, logger.New("error", "text"))
	m.checkDomain(context.Background(), config.Domain{Host: host})

	if got := status(t, m, host); got != "down" {
		t.Errorf("first-ever check status = %q, want down", got)
	}
}

// TestMonitorUserAgent pins the token the request-log filter matches on.
func TestMonitorUserAgent(t *testing.T) {
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.UserAgent()
	}))
	defer srv.Close()

	m := New(nil, logger.New("error", "text"))
	m.checkDomain(context.Background(), config.Domain{Host: srv.Listener.Addr().String()})

	if seen != monitorUserAgent {
		t.Errorf("User-Agent = %q, want %q", seen, monitorUserAgent)
	}
	if !strings.Contains(seen, "UWAS-Monitor") {
		t.Error("User-Agent lost the UWAS-Monitor token the request log filters on")
	}
}

// TestSweepIsConcurrent checks the sweep no longer serializes: several slow
// domains must overlap rather than add up.
func TestSweepIsConcurrent(t *testing.T) {
	const delay = 120 * time.Millisecond
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(delay)
	}))
	defer srv.Close()

	host := srv.Listener.Addr().String()
	domains := make([]config.Domain, checkConcurrency)
	for i := range domains {
		domains[i] = config.Domain{Host: host, SSL: config.SSLConfig{Mode: "off"}}
	}
	m := New(domains, logger.New("error", "text"))

	start := time.Now()
	m.sweep(context.Background())
	elapsed := time.Since(start)

	// Sequential would be checkConcurrency * delay; concurrent is ~one delay.
	if elapsed > delay*3 {
		t.Errorf("sweep of %d slow domains took %v, want well under %v — checks are still serialized",
			len(domains), elapsed, delay*time.Duration(len(domains)))
	}
}

func status(t *testing.T, m *Monitor, host string) string {
	t.Helper()
	m.resultsMu.RLock()
	defer m.resultsMu.RUnlock()
	r, ok := m.results[host]
	if !ok {
		t.Fatalf("no result recorded for %s", host)
	}
	return r.Status
}

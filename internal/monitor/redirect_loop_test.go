package monitor

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// probeTwice runs two checks (failThreshold) against h and returns the result.
func probeTwice(t *testing.T, h http.HandlerFunc) HealthResult {
	t.Helper()
	old := monitorDialControl
	monitorDialControl = nil
	t.Cleanup(func() { monitorDialControl = old })

	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	d := config.Domain{Host: strings.TrimPrefix(srv.URL, "http://"), SSL: config.SSLConfig{Mode: "off"}}
	m := New([]config.Domain{d}, testLogger())
	for i := 0; i < failThreshold; i++ {
		m.checkDomain(context.Background(), d)
	}
	rs := m.Results()
	if len(rs) != 1 {
		t.Fatalf("results = %d, want 1", len(rs))
	}
	return rs[0]
}

// A redirect loop is an outage (browsers stop with "too many redirects"), so
// the probe must not report the last 3xx as the site being up.
func TestRedirectLoopReadsDown(t *testing.T) {
	r := probeTwice(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/loop", http.StatusFound)
	})
	if r.Status != "down" {
		t.Fatalf("status = %q (code %d), want down", r.Status, r.StatusCode)
	}
	last := r.Checks[len(r.Checks)-1]
	if !strings.Contains(last.Error, "redirects") {
		t.Errorf("check error = %q, want it to mention the redirect limit", last.Error)
	}
}

// A long but finite chain still resolves: the limit is not a new way to read
// a healthy site as down.
func TestLongRedirectChainStillUp(t *testing.T) {
	const hops = 6
	r := probeTwice(t, func(w http.ResponseWriter, r *http.Request) {
		var n int
		if _, err := fmt.Sscanf(r.URL.Path, "/hop%d", &n); err != nil {
			n = 0
		}
		if n < hops {
			http.Redirect(w, r, fmt.Sprintf("/hop%d", n+1), http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	if r.Status != "up" || r.StatusCode != http.StatusOK {
		t.Fatalf("status = %q code %d, want up/200", r.Status, r.StatusCode)
	}
}

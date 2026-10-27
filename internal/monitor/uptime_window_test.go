package monitor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// The Uptime field is documented as a percentage over the last 24 hours, but
// it was computed from the retained check list, which holds only the last
// maxChecks results (about 50 minutes at the 30s interval). An outage older
// than that vanished from the figure.
func TestUptimeCoversMoreThanRetainedChecks(t *testing.T) {
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
	m := New(nil, logger.New("error", "text"))
	d := config.Domain{Host: host}
	ctx := context.Background()

	fail.Store(true)
	for i := 0; i < maxChecks/2; i++ {
		m.checkDomain(ctx, d)
	}
	fail.Store(false)
	for i := 0; i < maxChecks; i++ {
		m.checkDomain(ctx, d)
	}

	m.resultsMu.RLock()
	r := m.results[host]
	got, retained := r.Uptime, len(r.Checks)
	m.resultsMu.RUnlock()

	if retained != maxChecks {
		t.Fatalf("retained checks = %d, want %d (control)", retained, maxChecks)
	}
	// 150 checks happened inside the last 24h, 50 of them bad.
	want := 100.0 * float64(maxChecks) / float64(maxChecks+maxChecks/2)
	if got < want-0.01 || got > want+0.01 {
		t.Errorf("EXPECTED: uptime %.2f%% over 24h; ACTUAL: %.2f%%", want, got)
	}
}

// Checks older than 24h must still age out of the figure.
func TestUptimeIgnoresChecksOlderThanADay(t *testing.T) {
	r := &HealthResult{}
	old := time.Now().Add(-25 * time.Hour)
	r.recordUptime(Check{Time: old, StatusCode: 502})
	r.recordUptime(Check{Time: time.Now(), StatusCode: 200})
	if got := r.uptimePercent(time.Now()); got != 100 {
		t.Errorf("uptime = %.2f, want 100 (a 25h-old failure is outside the window)", got)
	}
	if got := (&HealthResult{}).uptimePercent(time.Now()); got != 100 {
		t.Errorf("no checks: uptime = %.2f, want 100", got)
	}
}

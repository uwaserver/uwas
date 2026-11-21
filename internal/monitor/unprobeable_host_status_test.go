package monitor

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// TestReportedHostThatStopsResolvingGoesDown is the regression for F66200: a
// host that was up and then failed the pre-probe check (DNS gone, or now in a
// blocked range) was skipped silently, so the board showed its last "up"
// forever. A host never reported still stays off the board.
func TestReportedHostThatStopsResolvingGoesDown(t *testing.T) {
	var refuse atomic.Bool
	old := monitorURLSafetyCheck
	monitorURLSafetyCheck = func(string) error {
		if refuse.Load() {
			return errors.New("cannot resolve: no such host")
		}
		return nil
	}
	t.Cleanup(func() { monitorURLSafetyCheck = old })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	host := srv.Listener.Addr().String()
	d := config.Domain{Host: host}
	ctx := context.Background()

	m := New([]config.Domain{d}, logger.New("error", "text"))
	m.checkDomain(ctx, d)
	refuse.Store(true)
	m.checkDomain(ctx, d)
	if got := status(t, m, host); got != "up" {
		t.Fatalf("after one refused check status = %q, want up (threshold)", got)
	}
	m.checkDomain(ctx, d)
	if got := status(t, m, host); got != "down" {
		t.Fatalf("after two refused checks status = %q, want down", got)
	}

	refuse.Store(false)
	m.checkDomain(ctx, d)
	if got := status(t, m, host); got != "up" {
		t.Fatalf("after recovery status = %q, want up", got)
	}

	refuse.Store(true)
	fresh := New([]config.Domain{{Host: "never.test"}}, logger.New("error", "text"))
	fresh.checkDomain(ctx, config.Domain{Host: "never.test"})
	if n := len(fresh.Results()); n != 0 {
		t.Fatalf("never-reported refused host produced %d results, want 0", n)
	}
}

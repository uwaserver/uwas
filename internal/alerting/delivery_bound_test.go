package alerting

import (
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/notify"
)

// TestAlertDeliveriesAreBounded: every alert started a goroutine per channel
// with no cap, so a burst of alerts while a channel (SMTP, Slack) hangs piled
// up goroutines, sockets and memory without limit.
func TestAlertDeliveriesAreBounded(t *testing.T) {
	a := New(true, "", []notify.Channel{{Type: "slack", Enabled: true}}, testLogger())
	var inflight atomic.Int64
	gate := make(chan struct{})
	a.sendChannel = func(notify.Channel, notify.Message) error {
		inflight.Add(1)
		defer inflight.Add(-1)
		<-gate
		return nil
	}
	defer close(gate)

	const burst = 300
	for i := 0; i < burst; i++ {
		a.Alert(Alert{Level: "warning", Type: "domain_down", Host: "h", Message: "m"})
	}
	if got := len(a.Alerts()); got != maxAlertHistory && got != burst {
		t.Fatalf("history must still record every alert, got %d", got)
	}

	// The cap must be reached (control: deliveries do run)…
	deadline := time.Now().Add(5 * time.Second)
	for inflight.Load() < int64(maxAlertDeliveries) {
		if time.Now().After(deadline) {
			t.Fatalf("only %d deliveries in flight, want %d", inflight.Load(), maxAlertDeliveries)
		}
		runtime.Gosched()
	}
	// …and never exceeded while the channel is hung.
	until := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(until) {
		if n := inflight.Load(); n > int64(maxAlertDeliveries) {
			t.Fatalf("%d deliveries in flight, cap is %d", n, maxAlertDeliveries)
		}
		runtime.Gosched()
	}
}

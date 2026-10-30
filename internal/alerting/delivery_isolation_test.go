package alerting

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/notify"
)

// TestHungChannelDoesNotStarveOthers: the delivery cap was global, so a burst
// of alerts while one destination hangs (an unreachable SMTP server) filled
// every slot and the healthy channels (Telegram) got no alert at all.
func TestHungChannelDoesNotStarveOthers(t *testing.T) {
	a := New(true, "", []notify.Channel{{Type: "email", Enabled: true}}, testLogger())
	gate := make(chan struct{})
	defer close(gate)
	var telegram atomic.Int64
	a.sendChannel = func(ch notify.Channel, _ notify.Message) error {
		if ch.Type == "email" {
			<-gate
			return nil
		}
		telegram.Add(1)
		return nil
	}
	for i := 0; i < maxAlertDeliveries*2; i++ {
		a.Alert(Alert{Level: "warning", Type: "domain_down", Host: "h", Message: "m"})
	}

	// The healthy channel is configured only now, mid-outage.
	a.mu.Lock()
	a.channels = append(a.channels, notify.Channel{Type: "telegram", Enabled: true})
	a.mu.Unlock()
	a.Alert(Alert{Level: "critical", Type: "domain_down", Host: "h2", Message: "m2"})

	deadline := time.Now().Add(3 * time.Second)
	for telegram.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("EXPECTED: telegram alert delivered while email hangs; ACTUAL: dropped (shared slots exhausted)")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

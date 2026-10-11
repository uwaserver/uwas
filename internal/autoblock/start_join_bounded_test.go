package autoblock

import (
	"context"
	"testing"
	"time"
)

// F2951: Start joins its workers so the final save finishes (F2261), but the
// firewall worker runs ufw with no timeout. One hung call must not hold the
// server's shutdown, so the join is bounded.
func TestStartJoinIsBoundedByHungFirewall(t *testing.T) {
	old := workerJoinTimeout
	workerJoinTimeout = 50 * time.Millisecond
	t.Cleanup(func() { workerJoinTimeout = old })

	run := func(t *testing.T, hang bool) bool {
		t.Helper()
		release := make(chan struct{})
		entered := make(chan struct{}, 1)
		b := New(Config{Enabled: true, BlockDuration: time.Hour}, testLogger())
		b.SetFirewallSync(true)
		b.SetFirewall(func(string, string) error {
			if hang {
				entered <- struct{}{}
				<-release
			}
			return nil
		}, func(string) error { return nil })
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { b.Start(ctx); close(done) }()
		if err := b.Block("203.0.113.95", ReasonConnFlood, time.Hour); err != nil {
			t.Fatal(err)
		}
		if hang {
			select {
			case <-entered:
			case <-time.After(10 * time.Second):
				t.Fatal("firewall op never started")
			}
		}
		cancel()
		returned := false
		select {
		case <-done:
			returned = true
		case <-time.After(10 * time.Second):
		}
		close(release)
		<-done
		return returned
	}

	if !run(t, false) {
		t.Error("control: Start did not return with a healthy firewall hook")
	}
	if !run(t, true) {
		t.Error("Start stayed blocked behind a hung firewall call")
	}
}

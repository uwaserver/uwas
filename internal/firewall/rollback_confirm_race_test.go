package firewall

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

// waitMutexParked polls goroutine stacks until a goroutine is parked acquiring
// a sync.Mutex directly from a frame matching one of firstFrames. It is
// condition-polled (no sleeps), which is what lets the rollback tests below
// pin the order in which competing goroutines take rbMu.
func waitMutexParked(t *testing.T, firstFrames ...string) {
	t.Helper()
	buf := make([]byte, 1<<20)
	for i := 0; i < 20_000; i++ {
		n := runtime.Stack(buf, true)
		for _, g := range strings.Split(string(buf[:n]), "\n\n") {
			if !strings.Contains(g, "sync.runtime_SemacquireMutex") {
				continue
			}
			first := ""
			for _, l := range strings.Split(g, "\n")[1:] {
				if strings.HasPrefix(l, "\t") || strings.HasPrefix(l, "sync.") || strings.HasPrefix(l, "runtime.") || strings.HasPrefix(l, "internal/") {
					continue
				}
				first = l
				break
			}
			for _, f := range firstFrames {
				if strings.Contains(first, f) {
					return
				}
			}
		}
		runtime.Gosched()
	}
	t.Fatalf("no goroutine parked on a mutex from %v", firstFrames)
}

func waitRollbackCallbackDone(t *testing.T) {
	t.Helper()
	buf := make([]byte, 1<<20)
	for i := 0; i < 20_000; i++ {
		n := runtime.Stack(buf, true)
		if !strings.Contains(string(buf[:n]), "firewall.EnableWithRollback.func") {
			return
		}
		runtime.Gosched()
	}
	t.Fatal("rollback callback never finished")
}

func (rs *recordingStub) disables() int {
	n := 0
	for _, c := range rs.got() {
		if c == "ufw disable" {
			n++
		}
	}
	return n
}

// A rollback timer that has fired but not yet taken rbMu must not disable ufw
// once ConfirmEnable has reported the rollback cancelled: the operator was
// told their access was kept, and the firewall then turned itself off.
func TestConfirmEnableBeatsFiredRollbackTimer(t *testing.T) {
	rs := &recordingStub{}
	rs.install(t)
	if err := EnableWithRollback(time.Hour, nil); err != nil {
		t.Fatal(err)
	}

	rbMu.Lock()
	tm := rbTimer
	res := make(chan bool, 1)
	go func() { res <- ConfirmEnable() }()
	waitMutexParked(t, "firewall.ConfirmEnable(")
	tm.Reset(0) // fire now; the callback queues on rbMu behind ConfirmEnable
	waitMutexParked(t, "firewall.cancelRollback(", "firewall.EnableWithRollback.func")
	rbMu.Unlock()

	if !<-res {
		t.Fatal("ConfirmEnable did not take rbMu first; gate order not achieved")
	}
	waitRollbackCallbackDone(t)
	if n := rs.disables(); n != 0 {
		t.Fatalf("confirmed rollback still ran `ufw disable` %d time(s)", n)
	}
}

// A stale timer whose slot was re-armed by a later enable must leave the new
// window alone instead of disabling and cancelling it.
func TestStaleRollbackTimerLeavesReplacementWindow(t *testing.T) {
	rs := &recordingStub{}
	rs.install(t)
	if err := EnableWithRollback(time.Hour, nil); err != nil {
		t.Fatal(err)
	}

	rbMu.Lock()
	stale := rbTimer
	stale.Reset(0)
	waitMutexParked(t, "firewall.cancelRollback(", "firewall.EnableWithRollback.func")
	// What the locked section of a second EnableWithRollback leaves behind.
	stale.Stop()
	rbDeadline = time.Now().Add(time.Hour)
	rbTimer = time.AfterFunc(time.Hour, func() {})
	rbMu.Unlock()

	waitRollbackCallbackDone(t)
	if n := rs.disables(); n != 0 {
		t.Fatalf("stale timer ran `ufw disable` %d time(s)", n)
	}
	if pending, _ := rollbackStatus(); !pending {
		t.Fatal("stale timer cancelled the replacement rollback window")
	}
}

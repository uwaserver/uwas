package phpmanager

// Regression: the per-domain crash-monitor goroutine read m.onCrash WITHOUT
// holding domainMu, while SetOnCrash writes that same field UNDER domainMu.
//
// domain.go:322 (the nil-check) and :323 (the call) both sat inside
// `if shouldRestart`, after the m.domainMu.Unlock(). The same function already
// gets this right for its sibling callback: StartDomain captures
// m.onDomainChange while the lock is still held, commented "Capture callback
// before releasing lock". m.onCrash was that same pattern, missed.
//
// Verified with -race: the detector named the write at manager.go:140 against
// the reads at domain.go:322 and :323. Fixed by capturing the callback under
// the lock and using the local thereafter.

import (
	"fmt"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"
)

// onCrashTestManager builds a Manager whose monitored commands exit non-zero
// immediately, so the crash-monitor goroutine reaches the callback read.
// observed counts callback invocations, which also proves the read executed.
func onCrashTestManager(t *testing.T, domains int) (*Manager, *atomic.Int32) {
	t.Helper()
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4", SAPI: "cgi-fcgi"},
	}
	m.execCommand = func(string, ...string) *exec.Cmd { return exec.Command("sh", "-c", "exit 1") }

	observed := &atomic.Int32{}
	m.SetOnCrash(func(string) { observed.Add(1) })

	for i := 0; i < domains; i++ {
		d := fmt.Sprintf("d%d.example.com", i)
		if _, err := m.AssignDomain(d, "8.4"); err != nil {
			t.Fatalf("AssignDomain(%s): %v", d, err)
		}
		if err := m.StartDomain(d); err != nil {
			t.Fatalf("StartDomain(%s): %v", d, err)
		}
	}
	return m, observed
}

// The monitor's read of the crash callback must be ordered against SetOnCrash.
// Run under -race: an unsynchronized read here is reported as a DATA RACE.
func TestCrashMonitorReadsOnCrashCallbackUnderLock(t *testing.T) {
	const domains = 4
	m, observed := onCrashTestManager(t, domains)
	t.Cleanup(m.StopAll)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 5000; i++ {
			m.SetOnCrash(func(string) { observed.Add(1) })
		}
	}()

	deadline := time.Now().Add(15 * time.Second)
	for observed.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	<-done

	if observed.Load() == 0 {
		t.Fatalf("the crash monitor never invoked the callback, so the read under " +
			"test was not reached and a clean -race run would prove nothing")
	}
}

// Control: concurrent SetOnCrash calls all take domainMu, so the same harness
// that flags the defect above must stay silent here.
func TestConcurrentSetOnCrashIsRaceFree(t *testing.T) {
	m := New(testLogger())

	var ran atomic.Int32
	done := make(chan struct{})
	for g := 0; g < 4; g++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for i := 0; i < 2000; i++ {
				m.SetOnCrash(func(string) {})
				ran.Add(1)
			}
		}()
	}
	for g := 0; g < 4; g++ {
		<-done
	}
	if ran.Load() != 4*2000 {
		t.Fatalf("expected %d SetOnCrash calls, ran %d", 4*2000, ran.Load())
	}
}

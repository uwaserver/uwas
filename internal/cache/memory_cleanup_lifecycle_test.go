package cache

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// cleanupLifecycleParent counts parent cancellation registrations. Its parent
// never cancels, so only child cancellation releases these resources.
type cleanupLifecycleParent struct {
	context.Context
	done             chan struct{}
	active           atomic.Int64
	gateOnce         atomic.Bool
	entered, release chan struct{}
}

func newCleanupLifecycleParent() *cleanupLifecycleParent {
	return &cleanupLifecycleParent{Context: context.Background(), done: make(chan struct{})}
}
func (p *cleanupLifecycleParent) Done() <-chan struct{} { return p.done }
func (p *cleanupLifecycleParent) AfterFunc(f func()) func() bool {
	p.active.Add(1)
	var once sync.Once
	return func() bool {
		stopped := false
		once.Do(func() {
			if p.entered != nil && p.gateOnce.CompareAndSwap(false, true) {
				close(p.entered)
				<-p.release
			}
			p.active.Add(-1)
			stopped = true
		})
		return stopped
	}
}
func awaitCleanupLifecycleSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup lifecycle gate did not complete")
	}
}

func TestMemoryCacheCleanupLifecycle(t *testing.T) {
	control := newCleanupLifecycleParent()
	one := NewMemoryCache(1024)
	one.StartCleanup(control, time.Hour)
	one.Close()
	if n := control.active.Load(); n != 0 {
		t.Fatalf("CONTROL FAILED active=%d", n)
	}
	fmt.Println("CONTROL PASSED")
	p := newCleanupLifecycleParent()
	mc := NewMemoryCache(1024)
	mc.StartCleanup(p, time.Hour)
	mc.StartCleanup(p, time.Hour)
	mc.Close()
	n := p.active.Load()
	fmt.Printf("EXPECTED: 0 retained parent cancellation registrations ACTUAL: %d\n", n)
	if n != 0 {
		fmt.Println("PROBLEM CONFIRMED")
		t.FailNow()
	}
	fmt.Println("PROBLEM NOT REPRODUCED")
	// Repeated Close, close-before-start, and reuse after close.
	for _, starts := range []int{0, 1, 5} {
		parent := newCleanupLifecycleParent()
		cache := NewMemoryCache(1024)
		cache.Close()
		for i := 0; i < starts; i++ {
			cache.StartCleanup(parent, time.Hour)
		}
		cache.Close()
		cache.Close()
		if n := parent.active.Load(); n != 0 {
			t.Fatalf("starts=%d active=%d", starts, n)
		}
		cache.StartCleanup(parent, time.Hour)
		cache.Close()
		if parent.active.Load() != 0 {
			t.Fatal("restart after close retained resource")
		}
	}
	// Force restart to suspend while canceling old owner, then queue Close.
	parent := newCleanupLifecycleParent()
	parent.entered = make(chan struct{})
	parent.release = make(chan struct{})
	cache := NewMemoryCache(1024)
	cache.StartCleanup(parent, time.Hour)
	restarted := make(chan struct{})
	go func() { cache.StartCleanup(parent, time.Hour); close(restarted) }()
	awaitCleanupLifecycleSignal(t, parent.entered)
	closeCalled := make(chan struct{})
	closed := make(chan struct{})
	go func() { close(closeCalled); cache.Close(); close(closed) }()
	awaitCleanupLifecycleSignal(t, closeCalled)
	close(parent.release)
	awaitCleanupLifecycleSignal(t, restarted)
	awaitCleanupLifecycleSignal(t, closed)
	if n := parent.active.Load(); n != 0 {
		t.Fatalf("gated restart/close active=%d", n)
	}
	fmt.Println("FIX VERIFIED")
}

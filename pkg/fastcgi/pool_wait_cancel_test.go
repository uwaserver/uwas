package fastcgi

import (
	"context"
	"net"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// silentFPM accepts connections and never replies.
func silentFPM(t *testing.T) (addr string, stop func()) {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "fpm.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var mu sync.Mutex
	var conns []net.Conn
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()
	stop = func() {
		ln.Close()
		mu.Lock()
		for _, c := range conns {
			c.Close()
		}
		mu.Unlock()
	}
	t.Cleanup(stop)
	return "unix:" + sock, stop
}

// waitForGoroutine polls goroutine stacks until one contains every marker.
func waitForGoroutine(t *testing.T, markers ...string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	buf := make([]byte, 1<<20)
	for time.Now().Before(deadline) {
		n := runtime.Stack(buf, true)
		for _, g := range strings.Split(string(buf[:n]), "\n\n") {
			found := true
			for _, m := range markers {
				if !strings.Contains(g, m) {
					found = false
					break
				}
			}
			if found {
				return
			}
		}
		runtime.Gosched()
	}
	t.Fatalf("no goroutine parked with %v", markers)
}

func recvWithin(t *testing.T, ch <-chan error, what string) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(3 * time.Second): // failure bound, not ordering
		t.Fatalf("%s did not return", what)
		return nil
	}
}

// A Get waiting at MaxOpen must take the slot a Discard releases instead of
// sleeping until the 30s exhaustion timer.
func TestPoolWaiterWokenByDiscard(t *testing.T) {
	addr, _ := silentFPM(t)
	p := NewPool(PoolConfig{Address: addr, MaxIdle: 2, MaxOpen: 2})
	defer p.Close()
	a, err := p.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			c, err := p.Get(context.Background())
			if err == nil {
				defer p.Discard(c)
			}
			done <- err
		}()
	}
	waitForGoroutine(t, "(*Pool).Get(", "select")
	p.Discard(a)
	p.Discard(b)
	for i := 0; i < 2; i++ {
		if err := recvWithin(t, done, "waiter"); err != nil {
			t.Fatalf("waiter %d: %v", i, err)
		}
	}
}

// Cancelling the request context (client gone) must abort a blocked
// Execute and discard the connection, not wait for the 60s deadline.
func TestExecuteAbortsOnContextCancel(t *testing.T) {
	addr, _ := silentFPM(t)
	c := NewClient(PoolConfig{Address: addr})
	defer c.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := c.Execute(ctx, map[string]string{"A": "b"}, nil)
		done <- err
	}()
	waitForGoroutine(t, "(*Client).Execute(", "ReadRecord")
	cancel()
	if err := recvWithin(t, done, "Execute"); err == nil {
		t.Fatal("Execute returned nil error after cancel")
	}
	if active, idle := c.pool.Stats(); active != 0 || idle != 0 {
		t.Fatalf("cancelled conn not discarded: active=%d idle=%d", active, idle)
	}
}

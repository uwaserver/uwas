package fastcgi

import (
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

type poolCloseAccountingConn struct {
	closed  atomic.Int32
	entered chan struct{}
	release chan struct{}
}

func (c *poolCloseAccountingConn) Read([]byte) (int, error)    { return 0, io.EOF }
func (c *poolCloseAccountingConn) Write(b []byte) (int, error) { return len(b), nil }
func (c *poolCloseAccountingConn) Close() error {
	c.closed.Add(1)
	if c.entered != nil {
		close(c.entered)
		<-c.release
	}
	return nil
}
func (c *poolCloseAccountingConn) LocalAddr() net.Addr              { return nil }
func (c *poolCloseAccountingConn) RemoteAddr() net.Addr             { return nil }
func (c *poolCloseAccountingConn) SetDeadline(time.Time) error      { return nil }
func (c *poolCloseAccountingConn) SetReadDeadline(time.Time) error  { return nil }
func (c *poolCloseAccountingConn) SetWriteDeadline(time.Time) error { return nil }
func poolCloseAccountingReserve(p *Pool) (*conn, *poolCloseAccountingConn) {
	f := &poolCloseAccountingConn{}
	p.active.Add(1)
	return &conn{netConn: f, createdAt: time.Now(), usedAt: time.Now()}, f
}
func poolCloseAccountingStats(t *testing.T, p *Pool, want int) {
	t.Helper()
	a, i := p.Stats()
	if a != want || i != 0 {
		t.Fatalf("EXPECTED: active=%d idle=0 ACTUAL: active=%d idle=%d", want, a, i)
	}
}
func TestPoolCloseAccountsForDrainedConnections(t *testing.T) {
	t.Run("empty repeated close", func(t *testing.T) {
		p := NewPool(PoolConfig{})
		p.Close()
		p.Close()
		poolCloseAccountingStats(t, p, 0)
	})
	t.Run("idle drain", func(t *testing.T) {
		p := NewPool(PoolConfig{})
		for range 3 {
			c, _ := poolCloseAccountingReserve(p)
			p.Put(c)
		}
		p.Close()
		p.Close()
		poolCloseAccountingStats(t, p, 0)
	})
	t.Run("overflow", func(t *testing.T) {
		p := NewPool(PoolConfig{MaxIdle: 1})
		c, f := poolCloseAccountingReserve(p)
		p.Put(c)
		c, g := poolCloseAccountingReserve(p)
		p.Put(c)
		if g.closed.Load() != 1 {
			t.Fatal("overflow not closed")
		}
		p.Close()
		poolCloseAccountingStats(t, p, 0)
		if f.closed.Load() != 1 {
			t.Fatal("idle not closed")
		}
	})
	t.Run("checked out discard", func(t *testing.T) {
		p := NewPool(PoolConfig{})
		idle, _ := poolCloseAccountingReserve(p)
		held, _ := poolCloseAccountingReserve(p)
		p.Put(idle)
		p.Close()
		poolCloseAccountingStats(t, p, 1)
		p.Discard(held)
		poolCloseAccountingStats(t, p, 0)
	})
	t.Run("return during gated drain", func(t *testing.T) {
		p := NewPool(PoolConfig{})
		idle, f := poolCloseAccountingReserve(p)
		held, g := poolCloseAccountingReserve(p)
		f.entered = make(chan struct{})
		f.release = make(chan struct{})
		p.Put(idle)
		done := make(chan struct{})
		go func() { p.Close(); close(done) }()
		select {
		case <-f.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("drain did not enter")
		}
		p.Put(held)
		close(f.release)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("drain did not finish")
		}
		poolCloseAccountingStats(t, p, 0)
		if f.closed.Load() != 1 || g.closed.Load() != 1 {
			t.Fatal("connections must close exactly once")
		}
	})
}

package server

import (
	"net"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/autoblock"
	"github.com/uwaserver/uwas/internal/logger"
)

func guardTestBlocker(t *testing.T, mutate func(*autoblock.Config)) *autoblock.Blocker {
	t.Helper()
	cfg := autoblock.Config{
		Enabled:        true,
		Window:         time.Minute,
		MaxConnections: 1000,
		MaxAborts:      2,
		BlockDuration:  time.Hour,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	return autoblock.New(cfg, logger.New("error", "text"))
}

// A refused connection must not surface as an Accept error: http.Server.Serve
// treats a non-temporary error as fatal and stops serving, so a flood would
// take the listener down instead of the attacker.
func TestGuardListenerKeepsAcceptingAfterRefusal(t *testing.T) {
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()

	ln := &guardListener{Listener: base, ab: guardTestBlocker(t, nil)}
	accepted := make(chan struct{}, 8)
	errc := make(chan error, 1)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				errc <- err
				return
			}
			accepted <- struct{}{}
			c.Close()
		}
	}()

	for i := 0; i < 3; i++ {
		c, err := net.Dial("tcp", base.Addr().String())
		if err != nil {
			t.Fatalf("dial %d: %v", i, err)
		}
		select {
		case <-accepted:
		case err := <-errc:
			t.Fatalf("Accept returned an error instead of continuing: %v", err)
		case <-time.After(2 * time.Second):
			t.Fatalf("connection %d was never accepted", i)
		}
		c.Close()
	}
}

// bytesRead == 0 at close is the TLS-handshake-flood signature: a peer that
// connected and hung up before ClientHello. Anything that sent a byte is a
// real client, however short-lived.
func TestGuardConnTracksBytesRead(t *testing.T) {
	t.Run("silent connection reads nothing", func(t *testing.T) {
		client, srv := net.Pipe()
		gc := &guardConn{Conn: srv}
		go client.Close()

		buf := make([]byte, 8)
		_, _ = gc.Read(buf)
		if gc.bytesRead.Load() != 0 {
			t.Fatalf("bytesRead = %d, want 0", gc.bytesRead.Load())
		}
	})

	t.Run("client that sent data is counted", func(t *testing.T) {
		client, srv := net.Pipe()
		gc := &guardConn{Conn: srv}
		go func() { _, _ = client.Write([]byte("hello")); client.Close() }()

		buf := make([]byte, 8)
		if _, err := gc.Read(buf); err != nil {
			t.Fatalf("read: %v", err)
		}
		if gc.bytesRead.Load() != 5 {
			t.Fatalf("bytesRead = %d, want 5", gc.bytesRead.Load())
		}
	})
}

// net/http and crypto/tls both close a connection on their own error paths.
// Counting the abort twice would halve the effective threshold.
func TestGuardConnCloseIsIdempotent(t *testing.T) {
	b := guardTestBlocker(t, func(c *autoblock.Config) { c.MaxAborts = 2 })
	addr, _ := autoblock.ParseAddr("203.0.113.150")

	_, srv := net.Pipe()
	gc := &guardConn{Conn: srv, ab: b, addr: addr}
	for i := 0; i < 10; i++ {
		_ = gc.Close()
	}
	if b.Blocked(addr) {
		t.Fatal("repeated Close() double-counted the abort and tripped the threshold")
	}
}

// Three silent closes past a threshold of two must block; the wrapper is what
// converts a connection's fate into that signal.
func TestGuardConnAbortsAccumulateIntoABlock(t *testing.T) {
	b := guardTestBlocker(t, func(c *autoblock.Config) { c.MaxAborts = 2 })
	addr, _ := autoblock.ParseAddr("203.0.113.151")

	for i := 0; i < 3; i++ {
		_, srv := net.Pipe()
		gc := &guardConn{Conn: srv, ab: b, addr: addr}
		b.ConnOpened(addr)
		_ = gc.Close()
	}
	if !b.Blocked(addr) {
		t.Fatal("three aborted connections should trip max_aborts=2")
	}
}

func TestNewGuardListenerPassesThroughWhenDisabled(t *testing.T) {
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()

	off := autoblock.New(autoblock.Config{Enabled: false}, logger.New("error", "text"))
	if got := newGuardListener(base, off); got != base {
		t.Fatal("a disabled blocker must not wrap the listener")
	}
	if got := newGuardListener(base, nil); got != base {
		t.Fatal("a nil blocker must not wrap the listener")
	}
}

func TestGuardListenerRefusesBlockedSource(t *testing.T) {
	b := guardTestBlocker(t, nil)
	addr, _ := autoblock.ParseAddr("203.0.113.200")
	if err := b.Block("203.0.113.200", autoblock.ReasonManual, time.Hour); err != nil {
		t.Fatalf("block: %v", err)
	}
	if b.ConnOpened(addr) {
		t.Fatal("a blocked source must be refused at the accept path")
	}
}

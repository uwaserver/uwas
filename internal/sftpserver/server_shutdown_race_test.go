package sftpserver

// Regression guard for the acceptLoop registration ordering and Shutdown's
// connection-draining contract.
//
// Shutdown documents that it "waits for all active connections to finish
// before returning". acceptLoop registers each connection goroutine with wg —
// and the test-visible pending counter — BEFORE the blocking Accept, so an
// in-flight accept is never unregistered. acceptSeam sits between Accept
// returning and handleConn spawning, letting this test park the goroutine at
// exactly the point where the old Add-after-Accept order left the counter at
// zero.
//
// Mutation-sensitive: reverting acceptLoop to Add-after-Accept leaves
// pendingCount() at zero while the connection is in flight and while it is
// parked in the seam, and this test fails on that assertion (verified
// MUT_EXIT=1).

import (
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	ssh "golang.org/x/crypto/ssh"
)

// gatedListener signals Accept entry and then returns the connection the test
// placed on handoff, so the accept goroutine parks in acceptSeam.
type gatedListener struct {
	entered chan struct{}
	handoff chan net.Conn
	closed  chan struct{}
	once    sync.Once
}

func (l *gatedListener) Accept() (net.Conn, error) {
	select {
	case l.entered <- struct{}{}:
	case <-l.closed:
		return nil, net.ErrClosed
	}
	select {
	case c := <-l.handoff:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *gatedListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *gatedListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)}
}

// heldConn keeps handleConn alive until the test releases it: its Read blocks
// so the SSH handshake can neither complete nor fail early, and Close reports
// when the server tore the connection down.
type heldConn struct {
	net.Conn
	closed  chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *heldConn) Read(b []byte) (int, error) {
	<-c.release
	return 0, io.EOF
}

func (c *heldConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

// TestShutdownDoesNotRaceInFlightAccept drives Shutdown while the accepted
// connection is parked in acceptSeam, i.e. after Accept returned but before
// handleConn runs. With the old Add-after-Accept order the WaitGroup counter
// was zero at that point, so Shutdown returned immediately and the accept
// goroutine's later Add(1) raced the Wait.
func TestShutdownDoesNotRaceInFlightAccept(t *testing.T) {
	s := New(Config{}, testLogger())
	// handleConn is spawned once the seam is released; a real host key lets the
	// handshake reach its version exchange, which then blocks on the net.Pipe
	// write (the test never reads client), so handleConn stays alive until
	// release. A keyless ServerConfig fails NewServerConn immediately, which
	// would let Shutdown complete and defeat the assertions below.
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{NoClientAuth: true}
	cfg.AddHostKey(signer)
	s.sshCfg = cfg

	client, serverSide := net.Pipe()
	defer client.Close()

	connClosed := make(chan struct{})
	release := make(chan struct{})
	park := make(chan struct{})
	parkEntered := make(chan struct{})

	g := &gatedListener{
		entered: make(chan struct{}),
		handoff: make(chan net.Conn, 1),
		closed:  make(chan struct{}),
	}
	g.handoff <- &heldConn{Conn: serverSide, closed: connClosed, release: release}
	s.listener = g

	// Park the seam hook at the point where old and new orders differ.
	prevSeam := acceptSeam
	defer func() { acceptSeam = prevSeam }()
	acceptSeam = func(net.Conn) {
		close(parkEntered)
		<-park
	}

	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		s.acceptLoop()
	}()

	// The accept goroutine takes the connection and parks in the seam.
	select {
	case <-g.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("accept goroutine never reached Accept")
	}

	// DISCRIMINATOR: the connection is in flight here. The hoisted Add
	// registers it before the blocking Accept, so the count must already be
	// non-zero; the old Add-after-Accept order left it at zero, which is
	// exactly the state that let Shutdown's Wait race the later Add(1).
	if n := s.pendingCount(); n == 0 {
		t.Fatalf("pendingCount()=%d while a connection is in flight: it is not "+
			"registered, so Shutdown's Wait can race the later Add(1)", n)
	}

	select {
	case <-parkEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("accept goroutine never reached the seam")
	}

	// The registration must persist through the seam park.
	if n := s.pendingCount(); n == 0 {
		t.Fatal("pendingCount()=0 while parked in acceptSeam")
	}

	// Drive Shutdown while the accepted connection is parked pre-registration.
	shutdownReturned := make(chan struct{})
	go func() {
		defer close(shutdownReturned)
		s.Shutdown()
	}()

	time.Sleep(200 * time.Millisecond)
	select {
	case <-shutdownReturned:
		t.Fatal("Shutdown returned while the accepted connection was parked " +
			"before registration: the counter was 0, which is the Add(1)-races-" +
			"Wait state (sync: WaitGroup misuse: Add called concurrently with Wait)")
	default:
		// Correct with the hoisted Add: Wait is blocked on the parked conn.
	}

	// Release the seam: handleConn is spawned and holds the connection open,
	// so Shutdown must keep waiting rather than complete early.
	close(park)
	time.Sleep(200 * time.Millisecond)
	select {
	case <-shutdownReturned:
		t.Fatal("Shutdown returned before the accepted connection was handled")
	default:
	}

	// Finish the handshake: closing the pipe's client end fails the version
	// write NewServerConn is parked on (and release ends heldConn.Read), so
	// handleConn returns, Done fires, and the counter reaches zero; the accept
	// loop has already drained via the ErrClosed path.
	close(release)
	client.Close()

	select {
	case <-shutdownReturned:
	case <-time.After(10 * time.Second):
		t.Fatal("Shutdown wedged")
	}
	select {
	case <-connClosed:
	case <-time.After(10 * time.Second):
		t.Fatal("accepted connection was never closed")
	}
	select {
	case <-acceptDone:
	case <-time.After(10 * time.Second):
		t.Fatal("accept goroutine never exited")
	}
}

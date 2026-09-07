package server

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/autoblock"
	"github.com/uwaserver/uwas/internal/logger"
)

// scriptedListener hands out connections the test creates, so each one can
// claim a source address of the test's choosing. Loopback is unconditionally
// exempt from blocking (the watchdog probe depends on it), so a real TCP
// socket on 127.0.0.1 can never exercise the block path.
type scriptedListener struct {
	conns  chan net.Conn
	closed chan struct{}
}

func newScriptedListener() *scriptedListener {
	return &scriptedListener{conns: make(chan net.Conn, 8), closed: make(chan struct{})}
}

func (l *scriptedListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.closed:
		return nil, errors.New("listener closed")
	}
}

func (l *scriptedListener) Close() error {
	select {
	case <-l.closed:
	default:
		close(l.closed)
	}
	return nil
}

func (l *scriptedListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0}
}

// fakeAddrConn presents a chosen peer address to the accept path.
type fakeAddrConn struct {
	net.Conn
	remote net.Addr
}

func (c *fakeAddrConn) RemoteAddr() net.Addr { return c.remote }

func tcpAddr(t *testing.T, s string) net.Addr {
	t.Helper()
	a, err := net.ResolveTCPAddr("tcp", s)
	if err != nil {
		t.Fatalf("resolve %s: %v", s, err)
	}
	return a
}

// End-to-end through a real http.Server: a permitted source reaches the
// handler, a blocked source is dropped at accept and the handler never runs —
// which is the whole point, since a handler running at all means TLS and HTTP
// parsing already happened.
func TestGuardListenerEndToEndThroughHTTPServer(t *testing.T) {
	b := autoblock.New(autoblock.Config{
		Enabled:        true,
		Window:         time.Minute,
		MaxConnections: 1000,
		MaxAborts:      1000,
		BlockDuration:  time.Hour,
	}, logger.New("error", "text"))

	var handlerRuns atomic.Int64
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handlerRuns.Add(1)
			w.WriteHeader(http.StatusNoContent)
		}),
		ReadHeaderTimeout: 2 * time.Second,
	}

	base := newScriptedListener()
	defer base.Close()
	ln := newGuardListener(base, b)
	if ln == base {
		t.Fatal("an enabled blocker must wrap the listener")
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	defer srv.Close()

	// request drives one connection from src and reports whether a response
	// came back.
	request := func(src string) bool {
		t.Helper()
		client, server := net.Pipe()
		base.conns <- &fakeAddrConn{Conn: server, remote: tcpAddr(t, src)}

		_ = client.SetDeadline(time.Now().Add(2 * time.Second))
		if _, err := client.Write([]byte("GET / HTTP/1.1\r\nHost: example.test\r\n\r\n")); err != nil {
			client.Close()
			return false
		}
		resp, err := http.ReadResponse(bufio.NewReader(client), nil)
		client.Close()
		if err != nil {
			return false
		}
		resp.Body.Close()
		return true
	}

	const src = "203.0.113.10:44444"

	if !request(src) {
		t.Fatal("a permitted source should have been served")
	}
	if handlerRuns.Load() != 1 {
		t.Fatalf("handler ran %d times, want 1", handlerRuns.Load())
	}

	if err := b.Block("203.0.113.10", autoblock.ReasonConnFlood, time.Hour); err != nil {
		t.Fatalf("block: %v", err)
	}

	if request(src) {
		t.Fatal("a blocked source should not have been served")
	}
	if handlerRuns.Load() != 1 {
		t.Fatalf("handler ran for a blocked source: %d runs", handlerRuns.Load())
	}

	// Refusing a connection must not stop the server: an unrelated source
	// still gets through afterwards.
	if !request("198.51.100.20:44444") {
		t.Fatal("the listener stopped serving after refusing a connection")
	}
	if handlerRuns.Load() != 2 {
		t.Fatalf("handler ran %d times, want 2", handlerRuns.Load())
	}

	select {
	case err := <-serveErr:
		t.Fatalf("Serve returned early: %v", err)
	default:
	}
}

// The signature from the incident: connect, send nothing, hang up. Repeated
// past the threshold it has to produce a block, with no HTTP request ever
// involved.
func TestSilentConnectionsProduceABlockEndToEnd(t *testing.T) {
	b := autoblock.New(autoblock.Config{
		Enabled:        true,
		Window:         time.Minute,
		MaxConnections: 1000,
		MaxAborts:      5,
		BlockDuration:  time.Hour,
	}, logger.New("error", "text"))

	var handlerRuns atomic.Int64
	srv := &http.Server{
		Handler:           http.HandlerFunc(func(http.ResponseWriter, *http.Request) { handlerRuns.Add(1) }),
		ReadHeaderTimeout: time.Second,
	}

	base := newScriptedListener()
	defer base.Close()
	go func() { _ = srv.Serve(newGuardListener(base, b)) }()
	defer srv.Close()

	addr, _ := autoblock.ParseAddr("203.0.113.99")
	for i := 0; i < 8; i++ {
		client, server := net.Pipe()
		base.conns <- &fakeAddrConn{Conn: server, remote: tcpAddr(t, "203.0.113.99:55555")}
		client.Close() // hang up without sending a byte
	}

	// The abort is recorded when the server side closes, which happens on
	// http.Server's own goroutine, so poll rather than assume it has run.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !b.Blocked(addr) {
		time.Sleep(5 * time.Millisecond)
	}

	if !b.Blocked(addr) {
		t.Fatal("repeated silent connections should have produced a block")
	}
	if handlerRuns.Load() != 0 {
		t.Fatalf("handler ran %d times for connections that carried no request", handlerRuns.Load())
	}
}

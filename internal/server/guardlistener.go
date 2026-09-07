package server

import (
	"net"
	"net/netip"
	"sync/atomic"

	"github.com/uwaserver/uwas/internal/autoblock"
)

// guardListener drops connections from blocked sources before anything else
// touches them — no TLS handshake, no HTTP parse, no goroutine per connection.
//
// This is the only layer that can see a TLS-handshake flood. Once tls.Listener
// has the connection, a peer that hangs up before ClientHello has already cost
// us an accept, a goroutine and a handshake attempt, and produced one
// "TLS handshake error ... EOF" log line, before any middleware exists to say no.
type guardListener struct {
	net.Listener
	ab *autoblock.Blocker
}

func newGuardListener(ln net.Listener, ab *autoblock.Blocker) net.Listener {
	if ab == nil || !ab.Enabled() {
		return ln
	}
	return &guardListener{Listener: ln, ab: ab}
}

// Accept loops rather than returning an error for a refused connection:
// http.Server.Serve treats a non-temporary Accept error as fatal and stops
// serving, so rejecting a flood must never surface as an error here.
func (l *guardListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		addr, ok := autoblock.ParseAddr(c.RemoteAddr().String())
		if !ok {
			return c, nil
		}
		if !l.ab.ConnOpened(addr) {
			_ = c.Close()
			continue
		}
		return &guardConn{Conn: c, ab: l.ab, addr: addr}, nil
	}
}

// guardConn reports the connection's fate back to the detector.
//
// The signal that matters is bytesRead == 0 at close: a client that opened a
// connection and never sent a byte. Browsers, bots, proxies and health checks
// all send at least a ClientHello. Only an abort — the pattern behind the EOF
// handshake errors — does not.
type guardConn struct {
	net.Conn
	ab        *autoblock.Blocker
	addr      netip.Addr
	bytesRead atomic.Int64
	closed    atomic.Bool
}

func (c *guardConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if n > 0 {
		c.bytesRead.Add(int64(n))
	}
	return n, err
}

// Close is idempotent: net/http and crypto/tls both close a connection on
// their own error paths, and double-counting an abort would halve the
// effective threshold.
func (c *guardConn) Close() error {
	if c.closed.CompareAndSwap(false, true) {
		c.ab.ConnClosed(c.addr, c.bytesRead.Load() == 0)
	}
	return c.Conn.Close()
}

package server

import (
	"bufio"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// proxyProtoListener wraps a net.Listener to parse PROXY protocol v1 headers.
// When enabled, the first line of each connection must be a PROXY protocol header
// (e.g., "PROXY TCP4 192.168.1.1 10.0.0.1 56324 443\r\n").
// The real client IP is extracted and attached to the connection.
type proxyProtoListener struct {
	net.Listener
}

func newProxyProtoListener(ln net.Listener) net.Listener {
	return &proxyProtoListener{Listener: ln}
}

func (l *proxyProtoListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &proxyProtoConn{Conn: conn}, nil
}

// proxyHeaderTimeout bounds how long a connection may take to send its PROXY
// header, since RemoteAddr can now be the first thing to read from it.
var proxyHeaderTimeout = 10 * time.Second

// proxyProtoConn wraps a net.Conn to parse the PROXY protocol header before
// the first Read or RemoteAddr, whichever comes first.
type proxyProtoConn struct {
	net.Conn
	once     sync.Once
	reader   *bufio.Reader
	parsed   bool
	parseErr error
	realAddr net.Addr
}

// parseHeader reads the PROXY line once. net/http records RemoteAddr() at the
// top of conn.serve, before it reads anything, so parsing only on the first
// Read left every request with the load balancer's address.
func (c *proxyProtoConn) parseHeader() {
	c.once.Do(func() {
		c.parsed = true
		c.reader = bufio.NewReader(c.Conn)
		_ = c.Conn.SetReadDeadline(time.Now().Add(proxyHeaderTimeout))
		// A v1 header is at most 107 bytes. ReadSlice stops at the reader's
		// buffer size (bufio.ErrBufferFull) instead of buffering without
		// bound until a newline shows up (F2830).
		raw, err := c.reader.ReadSlice('\n')
		_ = c.Conn.SetReadDeadline(time.Time{})
		if err != nil {
			c.parseErr = fmt.Errorf("proxy protocol: %w", err)
			return
		}
		line := strings.TrimRight(string(raw), "\r\n")
		if strings.HasPrefix(line, "PROXY ") {
			parts := strings.Fields(line)
			// PROXY TCP4 <srcIP> <dstIP> <srcPort> <dstPort>
			if len(parts) >= 6 {
				if _, perr := netip.ParseAddr(parts[2]); perr == nil {
					c.realAddr = &proxyAddr{ip: parts[2], port: parts[4]}
				}
			}
		}
	})
}

func (c *proxyProtoConn) Read(b []byte) (int, error) {
	c.parseHeader()
	if c.parseErr != nil {
		// Report the header failure once; the reader is initialized, so
		// subsequent reads work even on header parse failure.
		err := c.parseErr
		c.parseErr = nil
		return 0, err
	}
	return c.reader.Read(b)
}

func (c *proxyProtoConn) RemoteAddr() net.Addr {
	c.parseHeader()
	if c.realAddr != nil {
		return c.realAddr
	}
	return c.Conn.RemoteAddr()
}

type proxyAddr struct {
	ip   string
	port string
}

func (a *proxyAddr) Network() string { return "tcp" }
func (a *proxyAddr) String() string  { return a.ip + ":" + a.port }

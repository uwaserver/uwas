package server

import (
	"bufio"
	"fmt"
	"net"
	"strings"
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

// proxyProtoConn wraps a net.Conn to parse the PROXY protocol header on first read.
type proxyProtoConn struct {
	net.Conn
	reader  *bufio.Reader
	parsed  bool
	realAddr net.Addr
}

func (c *proxyProtoConn) Read(b []byte) (int, error) {
	if !c.parsed {
		c.parsed = true
		c.reader = bufio.NewReader(c.Conn)
		line, err := c.reader.ReadString('\n')
		if err != nil {
			// reader is initialized, so subsequent reads work even on header parse failure
			return 0, fmt.Errorf("proxy protocol: %w", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(line, "PROXY ") {
			parts := strings.Fields(line)
			// PROXY TCP4 <srcIP> <dstIP> <srcPort> <dstPort>
			if len(parts) >= 6 {
				c.realAddr = &proxyAddr{ip: parts[2], port: parts[4]}
			}
		}
	}
	return c.reader.Read(b)
}

func (c *proxyProtoConn) RemoteAddr() net.Addr {
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

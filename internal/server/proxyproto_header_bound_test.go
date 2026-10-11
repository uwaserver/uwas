package server

import (
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// boundedFeedConn serves bytes from src and counts what the PROXY parser takes.
type boundedFeedConn struct {
	net.Conn
	src      io.Reader
	consumed int64
	limit    int64
}

func (c *boundedFeedConn) Read(b []byte) (int, error) {
	if c.consumed >= c.limit {
		return 0, io.EOF
	}
	if rem := c.limit - c.consumed; int64(len(b)) > rem {
		b = b[:rem]
	}
	n, err := c.src.Read(b)
	c.consumed += int64(n)
	return n, err
}
func (c *boundedFeedConn) SetReadDeadline(time.Time) error { return nil }
func (c *boundedFeedConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("10.0.0.2"), Port: 4000}
}

type repeatReader byte

func (r repeatReader) Read(b []byte) (int, error) {
	for i := range b {
		b[i] = byte(r)
	}
	return len(b), nil
}

// A v1 PROXY header is at most 107 bytes. A peer that never sends the newline
// must not be able to make the server buffer everything it can push inside the
// header timeout (F2830).
func TestProxyProtoHeaderReadIsBounded(t *testing.T) {
	// Controls: a normal header and the maximum-length v1 header parse.
	for _, hdr := range []string{
		"PROXY TCP4 192.0.2.9 10.0.0.1 5555 443\r\nGET / HTTP/1.1\r\n",
		"PROXY TCP6 2001:db8:ffff:ffff:ffff:ffff:ffff:ffff 2001:db8:ffff:ffff:ffff:ffff:ffff:fffe 65535 65535\r\n",
	} {
		c := &proxyProtoConn{Conn: &boundedFeedConn{src: strings.NewReader(hdr), limit: 1 << 20}}
		if got := c.RemoteAddr().String(); strings.HasPrefix(got, "10.0.0.2") {
			t.Errorf("valid header %q not parsed: RemoteAddr=%s", hdr, got)
		}
	}

	// A flood without a newline is cut off after the reader's buffer.
	fc := &boundedFeedConn{src: repeatReader('A'), limit: 64 << 20}
	c := &proxyProtoConn{Conn: fc}
	if got := c.RemoteAddr().String(); got != "10.0.0.2:4000" {
		t.Errorf("flooded connection must keep its real address, got %s", got)
	}
	if fc.consumed > 16<<10 {
		t.Errorf("consumed %d bytes of newline-less input, want <= 16 KiB", fc.consumed)
	}
	buf := make([]byte, 16)
	if _, err := c.Read(buf); err == nil || !strings.Contains(err.Error(), "proxy protocol") {
		t.Errorf("first Read after a rejected header = %v, want a proxy protocol error", err)
	}

	// A newline that only arrives past the buffer is also rejected.
	long := strings.Repeat("A", 5000) + "\r\nGET / HTTP/1.1\r\n"
	fc2 := &boundedFeedConn{src: strings.NewReader(long), limit: 1 << 20}
	c2 := &proxyProtoConn{Conn: fc2}
	if got := c2.RemoteAddr().String(); got != "10.0.0.2:4000" {
		t.Errorf("oversize header must not change the address, got %s", got)
	}
}

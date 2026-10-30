//go:build !unix

package testport

import (
	"net"
	"strconv"
	"testing"
)

// reserve falls back to listen-and-close where bound-but-not-listening sockets
// are not available; the port is only "probably" free there.
func reserve(t testing.TB) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("testport: listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

func portOf(addr string) int {
	_, p, _ := net.SplitHostPort(addr)
	n, _ := strconv.Atoi(p)
	return n
}

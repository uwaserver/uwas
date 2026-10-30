//go:build unix

package testport

import (
	"net"
	"strconv"
	"syscall"
	"testing"
)

// reserve binds a TCP socket on 127.0.0.1:0 without listening and keeps it
// until the test's cleanup.
func reserve(t testing.TB) string {
	t.Helper()
	// SOCK_CLOEXEC does not exist on darwin: set close-on-exec under ForkLock
	// so a concurrent fork cannot inherit the reserved socket (F59300).
	syscall.ForkLock.RLock()
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, 0)
	if err == nil {
		syscall.CloseOnExec(fd)
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		t.Fatalf("testport: socket: %v", err)
	}
	if err := syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1); err != nil {
		syscall.Close(fd)
		t.Fatalf("testport: SO_REUSEADDR: %v", err)
	}
	if err := syscall.Bind(fd, &syscall.SockaddrInet4{Addr: [4]byte{127, 0, 0, 1}}); err != nil {
		syscall.Close(fd)
		t.Fatalf("testport: bind: %v", err)
	}
	sa, err := syscall.Getsockname(fd)
	if err != nil {
		syscall.Close(fd)
		t.Fatalf("testport: getsockname: %v", err)
	}
	t.Cleanup(func() { syscall.Close(fd) })
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(sa.(*syscall.SockaddrInet4).Port))
}

func portOf(addr string) int {
	_, p, _ := net.SplitHostPort(addr)
	n, _ := strconv.Atoi(p)
	return n
}

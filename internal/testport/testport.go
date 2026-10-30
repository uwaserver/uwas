// Package testport hands tests loopback addresses that stay theirs.
//
// The usual trick — net.Listen(":0"), read the port, Close — returns a port
// the kernel is free to give to the next binder. Under `go test ./...` many
// packages bind ephemeral ports at once, so a "dead" upstream can start
// answering (the test hangs or sees a live backend) and a "free" port for a
// server can be taken before the server binds it. The addresses here are held
// by a socket that is bound but never listening: connections to it are refused
// with a RST, nothing else can be handed that port, and a server can still bind
// it (SO_REUSEADDR) because no socket is in the LISTEN state.
package testport

import "testing"

// Dead returns "127.0.0.1:PORT" that refuses connections until the test ends.
func Dead(t testing.TB) string {
	t.Helper()
	return reserve(t)
}

// DeadURL is Dead as an http:// URL.
func DeadURL(t testing.TB) string {
	t.Helper()
	return "http://" + reserve(t)
}

// Free returns a loopback TCP port for a server the test is about to start. The
// port stays reserved (and refuses connections) until the server listens on it.
func Free(t testing.TB) int {
	t.Helper()
	return portOf(reserve(t))
}

// FreeAddr is Free as "127.0.0.1:PORT".
func FreeAddr(t testing.TB) string {
	t.Helper()
	return reserve(t)
}

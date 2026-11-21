//go:build linux

package admin

import (
	"bufio"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestTerminalRouteSharesSessionCap: GET /api/v1/terminal built a fresh
// terminal handler per request, so the per-handler session cap (F15202) never
// applied (F67502). All requests must share one handler and its cap.
func TestTerminalRouteSharesSessionCap(t *testing.T) {
	sh := filepath.Join(t.TempDir(), "shell.sh")
	if err := os.WriteFile(sh, []byte("#!/bin/sh\nexec sleep 600\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", sh)
	const limit = 16 // terminal.defaultMaxSessions

	s := &Server{}
	if s.terminalHandler() != s.terminalHandler() {
		t.Fatal("terminalHandler returned a different handler per call")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.terminalHandler().ServeHTTP(w, r) // as the route does
	})}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()
	addr := ln.Addr().String()

	upgraded := 0
	for i := 0; i <= limit; i++ {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer conn.Close()
		req := "GET /api/v1/terminal HTTP/1.1\r\nHost: " + addr + "\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
			"Origin: http://" + addr + "\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n"
		if _, err := conn.Write([]byte(req)); err != nil {
			t.Fatalf("write: %v", err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		status, _ := bufio.NewReader(conn).ReadString('\n')
		if strings.Contains(status, "101") {
			upgraded++
		}
	}
	if upgraded != limit {
		t.Fatalf("%d of %d terminal sessions upgraded through the route; want the cap of %d", upgraded, limit+1, limit)
	}
}

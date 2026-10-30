package proxy

// F34600: the HTTP path counts every request in Backend.ActiveConns/TotalReqs,
// but the raw WebSocket tunnel did not. A tunnel lives for the whole session,
// so least_conn (and the panel's per-backend stats) saw a backend carrying
// thousands of sockets as idle.

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/router"
	"github.com/uwaserver/uwas/internal/testport"
)

func TestWebSocketTunnelCountsActiveConns(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	release := make(chan struct{})
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 4096)
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		_, _ = conn.Read(buf)
		_, _ = conn.Write([]byte("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n"))
		<-release // keep the tunnel open until the test has looked
	}()

	u, _ := url.Parse("http://" + listener.Addr().String())
	backend := &Backend{URL: u, Weight: 1}
	h := New(logger.New("error", "text"))

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	req.RemoteAddr = "1.2.3.4:5678"
	req.Host = "test.com"
	ctx := router.AcquireContext(&covHijackableWriter{conn: serverConn}, req)

	done := make(chan struct{})
	go func() {
		defer close(done)
		h.serveWebSocket(ctx, backend)
	}()

	// The 101 head reaching the client proves the tunnel is established.
	head := make([]byte, 4096)
	_ = clientConn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if n, err := clientConn.Read(head); err != nil || n == 0 {
		t.Fatalf("control: tunnel never established: n=%d err=%v", n, err)
	}

	if got := backend.ActiveConns.Load(); got != 1 {
		t.Errorf("ActiveConns while tunnel open = %d, want 1", got)
	}
	if got := backend.TotalReqs.Load(); got != 1 {
		t.Errorf("TotalReqs after upgrade = %d, want 1", got)
	}

	close(release)
	clientConn.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("tunnel did not close")
	}
	if got := backend.ActiveConns.Load(); got != 0 {
		t.Errorf("ActiveConns after tunnel closed = %d, want 0", got)
	}
}

// A failed dial must not leak a count either.
func TestWebSocketTunnelDialFailureReleasesActiveConns(t *testing.T) {
	addr := testport.Dead(t) // nothing listens here

	u, _ := url.Parse("http://" + addr)
	backend := &Backend{URL: u, Weight: 1}
	h := New(logger.New("error", "text"))

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	go func() {
		b := make([]byte, 4096)
		_ = clientConn.SetReadDeadline(time.Now().Add(3 * time.Second))
		clientConn.Read(b)
	}()

	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	req.Host = "test.com"
	ctx := router.AcquireContext(&covHijackableWriter{conn: serverConn}, req)
	h.serveWebSocket(ctx, backend)

	if got := backend.ActiveConns.Load(); got != 0 {
		t.Errorf("ActiveConns after failed dial = %d, want 0", got)
	}
}

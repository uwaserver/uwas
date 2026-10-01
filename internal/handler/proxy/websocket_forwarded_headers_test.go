package proxy

// Regression: the raw WebSocket tunnel path forwards client-supplied
// X-Forwarded-Proto / X-Forwarded-Host to the backend verbatim.
//
// The HTTP reverse-proxy path (handler.go:322-325) overwrites all four
// forwarded headers via Header.Set, which replaces any client value. The
// tunnel path wrote its headers by hand and only stripped X-Forwarded-For and
// X-Real-IP, so a client could claim `X-Forwarded-Proto: https` on a plain
// HTTP upgrade — and a backend that trusts it (PHP's HTTP_X_FORWARDED_PROTO,
// used for secure-cookie and HTTPS-redirect decisions) would be misled.

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/router"
)

// tunnelBackendCapture runs the real serveWebSocket tunnel and returns the
// exact bytes the backend received.
//
// The tunnel issues one Write() per header, so a single Read can return only
// the first TCP segment; it reads until the header-block terminator so a
// partial read can never masquerade as a missing header.
func tunnelBackendCapture(t *testing.T, headers map[string]string) string {
	t.Helper()

	captured := make(chan string, 1)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		var acc []byte
		chunk := make([]byte, 4096)
		deadline := time.Now().Add(3 * time.Second)
		_ = conn.SetReadDeadline(deadline)
		for !bytes.Contains(acc, []byte("\r\n\r\n")) && time.Now().Before(deadline) {
			n, err := conn.Read(chunk)
			if n > 0 {
				acc = append(acc, chunk[:n]...)
			}
			if err != nil {
				break
			}
		}
		captured <- string(acc)

		conn.Write([]byte("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n"))
		time.Sleep(100 * time.Millisecond)
	}()

	u, _ := url.Parse("http://" + listener.Addr().String())
	backend := &Backend{URL: u, Weight: 1}
	h := New(logger.New("error", "text"))

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	go func() {
		b := make([]byte, 4096)
		_ = clientConn.SetReadDeadline(time.Now().Add(3 * time.Second))
		clientConn.Read(b)
		clientConn.Close()
	}()

	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.RemoteAddr = "1.2.3.4:5678"
	req.Host = "test.com"

	ctx := router.AcquireContext(&covHijackableWriter{conn: serverConn}, req)
	h.serveWebSocket(ctx, backend)

	select {
	case got := <-captured:
		return got
	case <-time.After(3 * time.Second):
		t.Fatal("backend never received the tunnelled request — harness broken")
		return ""
	}
}

// tunnelHeaderValues returns every value the backend saw for a header name.
func tunnelHeaderValues(raw, name string) []string {
	var out []string
	head := raw
	if i := strings.Index(raw, "\r\n\r\n"); i >= 0 {
		head = raw[:i]
	}
	for _, line := range strings.Split(head, "\r\n") {
		if k, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(k), name) {
			out = append(out, strings.TrimSpace(v))
		}
	}
	return out
}

func TestWebSocketTunnelReplacesClientForwardedProto(t *testing.T) {
	raw := tunnelBackendCapture(t, map[string]string{"X-Forwarded-Proto": "https"})

	// Control: the authoritative client IP is set, so the tunnel really ran.
	if got := tunnelHeaderValues(raw, "X-Forwarded-For"); len(got) == 0 || got[0] != "1.2.3.4" {
		t.Fatalf("control: X-Forwarded-For not set authoritatively, backend saw %q", raw)
	}

	for _, v := range tunnelHeaderValues(raw, "X-Forwarded-Proto") {
		if v == "https" {
			t.Fatalf("backend received client-supplied X-Forwarded-Proto: https over a plain-HTTP upgrade; "+
				"the tunnel must replace it with the authoritative value. Backend saw: %q", raw)
		}
	}
	// The authoritative value must be present instead.
	if got := tunnelHeaderValues(raw, "X-Forwarded-Proto"); len(got) == 0 || got[0] != "http" {
		t.Fatalf("X-Forwarded-Proto = %v, want authoritative \"http\"; backend saw %q", got, raw)
	}
}

func TestWebSocketTunnelReplacesClientForwardedHost(t *testing.T) {
	raw := tunnelBackendCapture(t, map[string]string{"X-Forwarded-Host": "evil.example"})

	if got := tunnelHeaderValues(raw, "X-Forwarded-For"); len(got) == 0 || got[0] != "1.2.3.4" {
		t.Fatalf("control: X-Forwarded-For not set authoritatively, backend saw %q", raw)
	}

	for _, v := range tunnelHeaderValues(raw, "X-Forwarded-Host") {
		if v == "evil.example" {
			t.Fatalf("backend received client-supplied X-Forwarded-Host: evil.example; "+
				"the tunnel must replace it with the authoritative Host. Backend saw: %q", raw)
		}
	}
	if got := tunnelHeaderValues(raw, "X-Forwarded-Host"); len(got) == 0 || got[0] != "test.com" {
		t.Fatalf("X-Forwarded-Host = %v, want authoritative \"test.com\"; backend saw %q", got, raw)
	}
}

// Control: the tunnel must keep working normally — upgrade passthrough, the
// authoritative Host/X-Real-IP, and the request line all stay intact.
func TestWebSocketTunnelStillForwardsUpgradeAndHostHeaders(t *testing.T) {
	raw := tunnelBackendCapture(t, nil)

	if !strings.HasPrefix(raw, "GET /ws HTTP/1.1") {
		t.Fatalf("request line not preserved, backend saw %q", raw)
	}
	if got := tunnelHeaderValues(raw, "Upgrade"); len(got) == 0 || got[0] != "websocket" {
		t.Fatalf("Upgrade header missing, backend saw %q", raw)
	}
	if got := tunnelHeaderValues(raw, "Host"); len(got) == 0 || got[0] != "test.com" {
		t.Fatalf("Host = %v, want \"test.com\"; backend saw %q", got, raw)
	}
	if got := tunnelHeaderValues(raw, "X-Real-IP"); len(got) == 0 || got[0] != "1.2.3.4" {
		t.Fatalf("X-Real-IP = %v, want \"1.2.3.4\"; backend saw %q", got, raw)
	}
	// Both forwarded headers must now always be present, even when the client
	// sent neither — they are authoritative, not client echoes.
	if got := tunnelHeaderValues(raw, "X-Forwarded-Proto"); len(got) == 0 || got[0] != "http" {
		t.Fatalf("X-Forwarded-Proto absent without a client value, backend saw %q", raw)
	}
	if got := tunnelHeaderValues(raw, "X-Forwarded-Host"); len(got) == 0 || got[0] != "test.com" {
		t.Fatalf("X-Forwarded-Host absent without a client value, backend saw %q", raw)
	}
}

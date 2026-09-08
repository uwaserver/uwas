package middleware

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Rate-limit 429 must not send Connection: close; the same TCP connection
// stays reusable for further requests (HTTP keepalive).
func TestRateLimitKeepsAliveAfter429(t *testing.T) {
	ctx := context.Background()
	h := RateLimit(ctx, 3, time.Minute)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	srv := &http.Server{Handler: h, IdleTimeout: 30 * time.Second}
	go srv.Serve(ln)
	defer srv.Close()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	readResp := func(i int) (status string, connClose bool) {
		t.Helper()
		fmtWrite := "GET / HTTP/1.1\r\nHost: t\r\nConnection: keep-alive\r\n\r\n"
		if _, err := io.WriteString(conn, fmtWrite); err != nil {
			t.Fatalf("req %d write: %v", i, err)
		}
		buf := make([]byte, 4096)
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, err := conn.Read(buf)
		if err != nil {
			t.Fatalf("req %d read: %v", i, err)
		}
		raw := string(buf[:n])
		status = strings.SplitN(raw, "\r\n", 2)[0]
		for _, line := range strings.Split(raw, "\r\n") {
			if strings.EqualFold(line, "Connection: close") {
				connClose = true
			}
		}
		return status, connClose
	}

	for i := 1; i <= 3; i++ {
		status, closeHdr := readResp(i)
		if !strings.Contains(status, "200") {
			t.Fatalf("req %d: want 200, got %q", i, status)
		}
		if closeHdr {
			t.Fatalf("req %d: unexpected Connection: close on 200", i)
		}
	}
	for i := 4; i <= 5; i++ {
		status, closeHdr := readResp(i)
		if !strings.Contains(status, "429") {
			t.Fatalf("req %d: want 429, got %q", i, status)
		}
		if closeHdr {
			t.Fatalf("req %d: rate-limit 429 set Connection: close — keepalive broken", i)
		}
	}
	// Connection must still accept another request after 429s.
	status, closeHdr := readResp(6)
	if !strings.Contains(status, "429") {
		t.Fatalf("req 6: want 429 on same conn, got %q (conn dropped?)", status)
	}
	if closeHdr {
		t.Fatal("req 6: Connection: close after prior 429s")
	}
}

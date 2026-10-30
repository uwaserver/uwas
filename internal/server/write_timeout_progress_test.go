package server

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/testport"
)

// F8700: global.timeouts.write is http.Server.WriteTimeout, an absolute
// deadline on the whole response. A response that keeps making progress (an SSE
// stream, a long download to a slow client) is cut when it elapses even though
// every write succeeded, unlike nginx's per-write send_timeout.
func TestWriteTimeoutIsPerWriteProgress(t *testing.T) {
	const events = 6
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/quick" {
			fmt.Fprint(w, "event 0\n")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		for i := 0; i < events; i++ {
			fmt.Fprintf(w, "event %d\n", i)
			fl.Flush()
			time.Sleep(400 * time.Millisecond)
		}
	}))
	t.Cleanup(upstream.Close)

	addr := testport.FreeAddr(t)

	cfg := &config.Config{
		Global: config.GlobalConfig{WorkerCount: "1", LogLevel: "error", LogFormat: "text",
			HTTPListen: addr,
			Timeouts: config.TimeoutConfig{
				Read:       config.Duration{Duration: 30 * time.Second},
				ReadHeader: config.Duration{Duration: 10 * time.Second},
				Write:      config.Duration{Duration: 1 * time.Second},
				Idle:       config.Duration{Duration: 30 * time.Second},
			}},
		Domains: []config.Domain{{Host: "w.test", Type: "proxy", Root: t.TempDir(), SSL: config.SSLConfig{Mode: "off"},
			Proxy: config.ProxyConfig{Upstreams: []config.Upstream{{Address: upstream.URL, Weight: 1}}}}},
	}
	s := New(cfg, logger.New("error", "text"))
	t.Cleanup(func() { s.cancel(); time.Sleep(50 * time.Millisecond) })
	if err := s.startHTTP(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.httpSrv.Close() })

	get := func(path string) int {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(15 * time.Second))
		fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: w.test\r\nUser-Agent: t\r\nConnection: close\r\n\r\n", path)
		sc := bufio.NewScanner(conn)
		n := 0
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), "event ") {
				n++
			}
		}
		return n
	}
	control := get("/quick")
	got := get("/stream")
	if control != 1 {
		t.Fatalf("control request returned %d events, want 1", control)
	}
	if got != events {
		t.Errorf("stream delivered %d of %d events: the write timeout cut a response that kept progressing", got, events)
	}
}

// A hijacked connection (WebSocket upgrade) and Flush must still reach the
// server's own writer through the progress wrapper.
func TestWriteProgressKeepsHijackAndFlush(t *testing.T) {
	h := writeProgress(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := w.(http.Flusher); !ok {
			t.Error("wrapper lost http.Flusher")
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("wrapper lost http.Hijacker")
			return
		}
		conn, rw, err := hj.Hijack()
		if err != nil {
			t.Errorf("Hijack: %v", err)
			return
		}
		defer conn.Close()
		rw.WriteString("HTTP/1.1 101 Switching Protocols\r\n\r\nhello")
		rw.Flush()
	}), time.Second)
	srv := httptest.NewServer(h)
	defer srv.Close()
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprint(conn, "GET / HTTP/1.1\r\nHost: x\r\n\r\n")
	buf := make([]byte, 256)
	n, _ := conn.Read(buf)
	if !strings.Contains(string(buf[:n]), "101 Switching Protocols") {
		t.Errorf("hijacked response = %q", buf[:n])
	}
}

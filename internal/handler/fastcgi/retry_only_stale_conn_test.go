package fastcgi

import (
	"bufio"
	"encoding/binary"
	"net"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/router"
	"github.com/uwaserver/uwas/pkg/fastcgi"
)

// retryTestBackend serves one request per connection: after reading the full
// request it counts an execution and either closes with no output (die) or
// sends a complete response and closes (so a pooled reuse of it is stale).
func retryTestBackend(t *testing.T, die bool) (addr string, execs *atomic.Int32) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	execs = new(atomic.Int32)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				br := bufio.NewReader(c)
				for {
					rec, err := fastcgi.ReadRecord(br)
					if err != nil {
						return
					}
					if rec.Type == fastcgi.TypeStdin && rec.ContentLength == 0 {
						break
					}
				}
				execs.Add(1)
				if die {
					return
				}
				bw := bufio.NewWriter(c)
				fastcgi.WriteRecord(bw, fastcgi.TypeStdout, 1, []byte("Content-Type: text/html\r\n\r\nok"))
				end := make([]byte, 8)
				binary.BigEndian.PutUint32(end[0:4], 0)
				fastcgi.WriteRecord(bw, fastcgi.TypeEndRequest, 1, end)
				bw.Flush()
			}(c)
		}
	}()
	return "tcp:" + ln.Addr().String(), execs
}

func retryTestServe(h *Handler, addr string) int {
	domain := &config.Domain{Host: "php.test", Root: "/var/www", Type: "php",
		PHP: config.PHPConfig{FPMAddress: addr, IndexFiles: []string{"index.php"}}}
	rec := httptest.NewRecorder()
	ctx := router.AcquireContext(rec, httptest.NewRequest("GET", "/cron.php", nil))
	defer router.ReleaseContext(ctx)
	ctx.DocumentRoot = "/var/www"
	ctx.ResolvedPath = "/var/www/cron.php"
	ctx.OriginalURI = "/cron.php"
	h.ServeWith(ctx, domain, addr, nil)
	return rec.Code
}

// A GET whose worker dies after receiving the request on a freshly dialed
// connection must not be re-executed: the request was delivered (F815).
func TestFastCGIDeliveredRequestNotRetried(t *testing.T) {
	addr, execs := retryTestBackend(t, true)
	h := New(logger.New("error", "text"))
	defer h.getClient(addr).Close()
	if code := retryTestServe(h, addr); code != 502 {
		t.Fatalf("status = %d, want 502", code)
	}
	if n := execs.Load(); n != 1 {
		t.Fatalf("script executed %d times, want 1", n)
	}
}

// A stale pooled connection (backend closed it while idle) is still retried.
func TestFastCGIStalePooledConnRetried(t *testing.T) {
	addr, execs := retryTestBackend(t, false)
	h := New(logger.New("error", "text"))
	defer h.getClient(addr).Close()
	for i := 0; i < 2; i++ {
		if code := retryTestServe(h, addr); code != 200 {
			t.Fatalf("request %d status = %d, want 200", i+1, code)
		}
	}
	if n := execs.Load(); n != 2 {
		t.Fatalf("script executed %d times, want 2", n)
	}
}

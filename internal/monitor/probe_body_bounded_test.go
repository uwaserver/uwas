package monitor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// F49301: a probe needs the status line, not the page, but checkDomain read
// the response body to its end. The probed site is tenant-controlled, so a
// tenant whose "/" is a multi-gigabyte file or an endless stream made every
// sweep read for the whole 10 s check timeout (8 domains at a time, every
// 30 s) — reading and, since the domain resolves to this very host, serving
// that traffic. The read is now capped.

func TestCheckDomainDoesNotDrainAnEndlessBody(t *testing.T) {
	const serveCap = 64 << 20 // the handler stops by itself so a missing fix ends
	var served atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		chunk := make([]byte, 32<<10)
		for served.Load() < serveCap {
			n, err := w.Write(chunk)
			served.Add(int64(n))
			if err != nil {
				return
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))

	host := srv.Listener.Addr().String()
	m := New(nil, testLogger())
	m.checkDomain(context.Background(), config.Domain{Host: host})
	srv.Close() // waits for the handler, which ends once the probe hangs up

	// The cap plus whatever the kernel buffered on both sockets.
	const ceiling = 16 << 20
	got := served.Load()
	t.Logf("EXPECTED: at most %d bytes of the body leave the server for one probe\nACTUAL: %d bytes", ceiling, got)
	if got > ceiling {
		t.Fatalf("PROBLEM CONFIRMED: one probe pulled %d bytes of an endless body", got)
	}
	t.Log("PROBLEM NOT REPRODUCED")

	if st := status(t, m, host); st != "up" {
		t.Errorf("status of a 200 with a big body = %q, want up", st)
	}
}

// Control: a body that ends early (declared longer than sent) is still a
// failed probe; the cap must not hide a truncated reply.
func TestCheckDomainStillFlagsATruncatedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("short"))
		// returning with 995 bytes unwritten makes net/http close the conn
	}))
	defer srv.Close()

	host := srv.Listener.Addr().String()
	m := New(nil, testLogger())
	m.checkDomain(context.Background(), config.Domain{Host: host})
	if st := status(t, m, host); st != "down" {
		t.Errorf("truncated body status = %q, want down", st)
	}
}

// Control: an ordinary small page is up.
func TestCheckDomainSmallBodyIsUp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html>ok</html>"))
	}))
	defer srv.Close()

	host := srv.Listener.Addr().String()
	m := New(nil, testLogger())
	m.checkDomain(context.Background(), config.Domain{Host: host})
	if st := status(t, m, host); st != "up" {
		t.Errorf("small body status = %q, want up", st)
	}
}

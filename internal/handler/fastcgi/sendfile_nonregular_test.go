//go:build linux || darwin

package fastcgi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/router"
)

// X-Sendfile / X-Accel-Redirect served whatever os.Open accepted. A directory
// went to http.ServeContent (a 200 whose body read fails), and a FIFO made
// os.Open block forever on the request goroutine: the tenant's own PHP can
// create one with posix_mkfifo(). The static handler only serves regular
// files; so must the sendfile path (F26100).
func TestSendfileRefusesNonRegularFiles(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "ok.txt"), []byte("PLAIN"), 0o644)
	if err := os.Mkdir(filepath.Join(root, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(root, "pipe")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	t.Cleanup(func() {
		// Unblock a reader stuck in open(2) so a failing run does not leak it.
		if w, err := os.OpenFile(fifo, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			w.Close()
		}
	})

	h := New(logger.New("error", "text"))
	type result struct {
		ok   bool
		code int
		body string
	}
	serve := func(header, path string) (result, bool) {
		done := make(chan result, 1)
		go func() {
			rec := httptest.NewRecorder()
			ctx := &router.RequestContext{
				Request:  httptest.NewRequest("GET", "http://sf.test/x.php", nil),
				Response: router.NewResponseWriter(rec),
			}
			domain := &config.Domain{Host: "sf.test", Root: root}
			ok := h.tryServeFile(ctx, domain, http.Header{header: []string{path}})
			done <- result{ok, rec.Code, rec.Body.String()}
		}()
		select {
		case r := <-done:
			return r, true
		case <-time.After(3 * time.Second): // failure detector only
			return result{}, false
		}
	}

	failed := false
	for _, c := range []struct {
		name, header, path string
		served             bool
	}{
		{"control regular sendfile", "X-Sendfile", filepath.Join(root, "ok.txt"), true},
		{"control regular accel", "X-Accel-Redirect", "/ok.txt", true},
		{"directory sendfile", "X-Sendfile", filepath.Join(root, "dir"), false},
		{"directory accel", "X-Accel-Redirect", "/dir", false},
		{"fifo sendfile", "X-Sendfile", fifo, false},
		{"fifo accel", "X-Accel-Redirect", "/pipe", false},
	} {
		r, returned := serve(c.header, c.path)
		t.Logf("%s: EXPECTED served=%v ACTUAL returned=%v served=%v code=%d body=%q", c.name, c.served, returned, r.ok, r.code, r.body)
		if !returned || r.ok != c.served {
			failed = true
			t.Errorf("%s: returned=%v served=%v, want served=%v", c.name, returned, r.ok, c.served)
		}
	}
	if failed {
		t.Log("PROBLEM CONFIRMED")
	} else {
		t.Log("PROBLEM NOT REPRODUCED")
	}
}

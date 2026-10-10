package static

// Regression test for F860/F861: non-regular files (FIFO, socket, symlink to FIFO) are never
// opened by Serve — as the file or as a pre-compressed variant — and the
// Accept-Encoding q parameter name is matched case-insensitively.

import (
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/router"
)

type nonRegularResult struct {
	code      int
	body, enc string
	stuck     bool
}

func serveNonRegularProbe(t *testing.T, h *Handler, domain *config.Domain, uri, ae, rng string, fifos []string, statInServe bool) nonRegularResult {
	t.Helper()
	r := httptest.NewRequest("GET", "http://example.test"+uri, nil)
	if ae != "" {
		r.Header.Set("Accept-Encoding", ae)
	}
	if rng != "" {
		r.Header.Set("Range", rng)
	}
	rec := httptest.NewRecorder()
	ctx := &router.RequestContext{Request: r, Response: router.NewResponseWriter(rec)}
	if !ResolveRequest(ctx, domain) {
		return nonRegularResult{code: 404}
	}
	if statInServe {
		ctx.FileInfo = nil
	}
	done := make(chan struct{})
	go func() { defer close(done); h.Serve(ctx) }()
	stuck := false
	deadline := time.Now().Add(10 * time.Second)
	for {
		select {
		case <-done:
			return nonRegularResult{rec.Code, rec.Body.String(), rec.Header().Get("Content-Encoding"), stuck}
		default:
		}
		for _, f := range fifos {
			if w, err := os.OpenFile(f, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
				stuck = true
				_ = w.Close()
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: serve neither returned nor parked on a FIFO", uri)
		}
		runtime.Gosched()
	}
}

func TestServeSkipsNonRegularFilesAndHonorsQCase(t *testing.T) {
	root := t.TempDir()
	old, newer := time.Unix(1700000000, 0), time.Unix(1700000100, 0)
	wr := func(p, b string, ts time.Time) {
		if err := os.WriteFile(filepath.Join(root, p), []byte(b), 0o644); err != nil {
			t.Fatal(err)
		}
		_ = os.Chtimes(filepath.Join(root, p), ts, ts)
	}
	fifo := func(p string, ts time.Time) string {
		fp := filepath.Join(root, p)
		if err := syscall.Mkfifo(fp, 0o644); err != nil {
			t.Fatal(err)
		}
		_ = os.Chtimes(fp, ts, ts)
		return fp
	}
	wr("ok.txt", "hello world", old)
	wr("app.js", "plain", old)
	wr("both.js", "plainboth", old)
	wr("both.js.gz", "GZBOTH", newer)
	wr("lib.js", "plainlib", old)
	wr("lib.js.gz", "GZLIB", newer)
	fifos := []string{
		fifo("pipe.txt", old),
		fifo("app.js.gz", newer),
		fifo("both.js.br", newer),
	}
	if err := os.Symlink(filepath.Join(root, "pipe.txt"), filepath.Join(root, "link.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "dirvar.js.gz"), 0o755); err != nil {
		t.Fatal(err)
	}
	wr("dirvar.js", "plaindir", old)
	sockPath := filepath.Join(root, "s.sock")
	if ln, err := net.Listen("unix", sockPath); err == nil {
		defer ln.Close()
	} else {
		t.Logf("unix socket unavailable: %v", err)
		sockPath = ""
	}

	domain := &config.Domain{Host: "example.test", Type: "static", Root: root}
	h := New()
	ok, bad := 0, 0
	check := func(name string, cond bool, got any) {
		if cond {
			ok++
		} else {
			bad++
			t.Errorf("%s: %+v", name, got)
		}
	}

	for i := 1; i <= 2; i++ {
		r := serveNonRegularProbe(t, h, domain, "/pipe.txt", "", "", fifos, false)
		check(fmt.Sprintf("FIFO file #%d -> 404, not parked", i), r.code == 404 && !r.stuck, r)
	}
	r := serveNonRegularProbe(t, h, domain, "/pipe.txt", "", "", fifos, true)
	check("FIFO file, Serve stats itself -> 404", r.code == 404 && !r.stuck, r)
	r = serveNonRegularProbe(t, h, domain, "/link.txt", "", "", fifos, false)
	check("in-root symlink to FIFO -> 404", r.code == 404 && !r.stuck, r)
	if sockPath != "" {
		r = serveNonRegularProbe(t, h, domain, "/s.sock", "", "", fifos, false)
		check("unix socket -> 404", r.code == 404 && !r.stuck, r)
	}
	r = serveNonRegularProbe(t, h, domain, "/app.js", "gzip", "", fifos, false)
	check("FIFO .gz variant skipped -> identity", r.code == 200 && r.body == "plain" && r.enc == "" && !r.stuck, r)
	r = serveNonRegularProbe(t, h, domain, "/both.js", "br, gzip", "", fifos, false)
	check("FIFO .br skipped, regular .gz served", r.code == 200 && r.body == "GZBOTH" && r.enc == "gzip" && !r.stuck, r)
	r = serveNonRegularProbe(t, h, domain, "/dirvar.js", "gzip", "", fifos, false)
	check("directory variant skipped", r.body == "plaindir" && r.enc == "", r)
	r = serveNonRegularProbe(t, h, domain, "/ok.txt", "", "", fifos, false)
	check("regular file served", r.code == 200 && r.body == "hello world", r)
	r = serveNonRegularProbe(t, h, domain, "/ok.txt", "", "bytes=0-4", fifos, false)
	check("range on regular file", r.code == 206 && r.body == "hello", r)
	r = serveNonRegularProbe(t, h, domain, "/lib.js", "gzip", "", fifos, false)
	check("regular .gz variant served", r.enc == "gzip" && r.body == "GZLIB", r)

	for _, c := range []struct {
		h, coding string
		want      bool
	}{
		{"gzip;Q=0", "gzip", false},
		{"gzip;q=0", "gzip", false},
		{"gzip; Q=0.000", "gzip", false},
		{"GZIP;Q=0.5", "gzip", true},
		{"gzip;Q=1", "gzip", true},
		{"br;Q=0, gzip", "br", false},
		{"br;Q=0, gzip", "gzip", true},
		{"gzip", "gzip", true},
		{"identity", "gzip", false},
	} {
		got := acceptsEncoding(c.h, c.coding)
		check(fmt.Sprintf("acceptsEncoding(%q,%q)=%v", c.h, c.coding, c.want), got == c.want, got)
	}
	r = serveNonRegularProbe(t, h, domain, "/lib.js", "gzip;Q=0", "", fifos, false)
	check("end-to-end gzip;Q=0 -> identity", r.enc == "" && r.body == "plainlib", r)

	// 16 concurrent FIFO requests released together.
	start := make(chan struct{})
	var wg sync.WaitGroup
	res := make([]nonRegularResult, 16)
	for i := range res {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			uri := "/pipe.txt"
			if i%2 == 1 {
				uri = "/app.js"
			}
			ae := ""
			if i%2 == 1 {
				ae = "gzip"
			}
			res[i] = serveNonRegularProbe(t, h, domain, uri, ae, "", fifos, false)
		}(i)
	}
	close(start)
	wg.Wait()
	allOK := true
	for i, x := range res {
		if x.stuck || (i%2 == 0 && x.code != 404) || (i%2 == 1 && x.body != "plain") {
			allOK = false
		}
	}
	check("16 concurrent FIFO requests none parked", allOK, res)

	t.Logf("ok=%d mismatch=%d", ok, bad)
}

package server

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// Hot-path cost attribution.
//
// These benchmarks drive the *real* middleware chain (s.handler) with a
// throwaway ResponseWriter, so what they measure is server-side CPU per
// request with no kernel, no socket and no client in the way.
//
// The point is not the absolute ns/op — it is the delta between the
// baseline and each variant that has one subsystem switched off. That
// delta is the price of the subsystem, and the most expensive one is the
// improvement worth making. Run:
//
//	go test ./internal/server -run '^$' -bench 'HotPath' -benchmem -count 10 > new.txt
//	benchstat old.txt new.txt
//
// and to see where the time actually goes:
//
//	go test ./internal/server -run '^$' -bench 'HotPathStatic$' -cpuprofile cpu.out
//	go tool pprof -http=: server.test cpu.out

// benchWriter is an allocation-free http.ResponseWriter. Each goroutine owns
// one; the header map is allocated once and cleared between iterations so the
// benchmark measures the server, not the recorder.
type benchWriter struct {
	hdr    http.Header
	status int
	n      int64
}

func (w *benchWriter) Header() http.Header {
	if w.hdr == nil {
		w.hdr = make(http.Header, 16)
	}
	return w.hdr
}

func (w *benchWriter) Write(b []byte) (int, error) {
	w.n += int64(len(b))
	return len(b), nil
}

func (w *benchWriter) WriteHeader(code int) { w.status = code }

func (w *benchWriter) reset() {
	for k := range w.hdr {
		delete(w.hdr, k)
	}
	w.status = 0
	w.n = 0
}

// benchServer builds a server with one static domain over a temp docroot
// holding a file of the requested size.
func benchServer(tb testing.TB, host string, size int, cacheOn, compressOn bool) *Server {
	tb.Helper()

	dir := tb.TempDir()
	body := strings.Repeat("x", size)
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(body), 0644); err != nil {
		tb.Fatal(err)
	}

	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "auto",
			LogLevel:    "error",
			LogFormat:   "text",
			Cache: config.CacheConfig{
				Enabled:     cacheOn,
				MemoryLimit: 256 << 20,
				DefaultTTL:  300,
			},
		},
		Domains: []config.Domain{{
			Host:        host,
			Root:        dir,
			Type:        "static",
			SSL:         config.SSLConfig{Mode: "off"},
			Cache:       config.DomainCache{Enabled: cacheOn, TTL: 300},
			Compression: config.CompressionConfig{Enabled: &compressOn, Algorithms: []string{"gzip"}, MinSize: 256},
			IndexFiles:  []string{"index.html"},
		}},
	}

	s := New(cfg, logger.New("error", "text"))
	tb.Cleanup(func() { s.cancel() })
	return s
}

// driveHotPath runs the shared request loop against s.handler.
func driveHotPath(b *testing.B, s *Server, host, path, acceptEncoding string) {
	u := &url.URL{Path: path}

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		w := &benchWriter{}
		hdr := make(http.Header, 8)
		hdr.Set("User-Agent", "uwas-bench/1.0")
		if acceptEncoding != "" {
			hdr.Set("Accept-Encoding", acceptEncoding)
		}
		for pb.Next() {
			req := &http.Request{
				Method:     http.MethodGet,
				URL:        u,
				Proto:      "HTTP/1.1",
				ProtoMajor: 1,
				ProtoMinor: 1,
				Header:     hdr,
				Host:       host,
				RemoteAddr: "192.0.2.10:44321",
			}
			s.handler.ServeHTTP(w, req)
			w.reset()
		}
	})
}

// BenchmarkHotPathStatic is the baseline: everything a shipped server runs.
func BenchmarkHotPathStatic(b *testing.B) {
	s := benchServer(b, "bench.perf", 1024, false, false)
	driveHotPath(b, s, "bench.perf", "/index.html", "")
}

// BenchmarkHotPathCached measures the L1 memory cache hit path.
func BenchmarkHotPathCached(b *testing.B) {
	s := benchServer(b, "bench.perf", 1024, true, false)
	// Prime the cache so every measured iteration is a hit.
	driveOnce(b, s, "bench.perf", "/index.html")
	driveHotPath(b, s, "bench.perf", "/index.html", "")
}

// BenchmarkHotPathGzip measures the compression middleware.
func BenchmarkHotPathGzip(b *testing.B) {
	s := benchServer(b, "bench.perf", 16<<10, false, true)
	driveHotPath(b, s, "bench.perf", "/index.html", "gzip")
}

// ── Cost attribution ────────────────────────────────────────────────────
//
// Each of these is the baseline with exactly one per-request bookkeeping
// subsystem removed. baseline − variant = what that subsystem costs.

// BenchmarkHotPathNoAnalytics drops analytics.RecordFull, which takes a
// per-domain mutex and does six map writes on every request.
func BenchmarkHotPathNoAnalytics(b *testing.B) {
	s := benchServer(b, "bench.perf", 1024, false, false)
	s.analytics = nil
	driveHotPath(b, s, "bench.perf", "/index.html", "")
}

// BenchmarkHotPathNoAdminLog drops the admin ring-buffer LogEntry, which
// formats a duration string and takes a global mutex per request.
func BenchmarkHotPathNoAdminLog(b *testing.B) {
	s := benchServer(b, "bench.perf", 1024, false, false)
	s.admin = nil
	driveHotPath(b, s, "bench.perf", "/index.html", "")
}

// BenchmarkHotPathNoBandwidth drops the per-request bandwidth accounting.
func BenchmarkHotPathNoBandwidth(b *testing.B) {
	s := benchServer(b, "bench.perf", 1024, false, false)
	s.bwMgr = nil
	driveHotPath(b, s, "bench.perf", "/index.html", "")
}

// BenchmarkHotPathNoAlerter drops the error-spike recorder.
func BenchmarkHotPathNoAlerter(b *testing.B) {
	s := benchServer(b, "bench.perf", 1024, false, false)
	s.alerter = nil
	driveHotPath(b, s, "bench.perf", "/index.html", "")
}

// BenchmarkHotPathBare drops every optional per-request recorder at once.
// The gap to BenchmarkHotPathStatic is the total bookkeeping budget — the
// ceiling on what any amount of tuning in this area can buy.
func BenchmarkHotPathBare(b *testing.B) {
	s := benchServer(b, "bench.perf", 1024, false, false)
	s.analytics = nil
	s.admin = nil
	s.bwMgr = nil
	s.alerter = nil
	driveHotPath(b, s, "bench.perf", "/index.html", "")
}

// driveOnce sends a single request, used to prime caches before measuring.
func driveOnce(b *testing.B, s *Server, host, path string) {
	b.Helper()
	w := &benchWriter{}
	req := &http.Request{
		Method:     http.MethodGet,
		URL:        &url.URL{Path: path},
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     make(http.Header),
		Host:       host,
		RemoteAddr: "192.0.2.10:44321",
	}
	s.handler.ServeHTTP(w, req)
}

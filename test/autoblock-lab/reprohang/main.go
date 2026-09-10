// Command repro_hang reproduces the dgnteknoloji full-page symptom locally:
// browser opens many parallel connections for HTML/CSS/JS/img; some fail with
// no HTTP status (connection reset / timeout class), not clean 429s.
//
// Loopback is always Safe for autoblock, so every accepted connection is
// rewritten to look like TEST-NET peer 203.0.113.50 — the same class of
// address a real public client has.
//
//	go run ./test/autoblock-lab/repro_hang.go
package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/uwaserver/uwas/internal/autoblock"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/middleware"
)

const spoofIP = "203.0.113.50"

func main() {
	root, err := os.MkdirTemp("", "uwas-hang-repro-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(root)
	writeSite(root)

	fmt.Println("=== Local repro: full-page parallel asset storm ===")
	fmt.Println("Peer IP spoofed to", spoofIP, "(not Safe — autoblock applies)")
	fmt.Println()

	type scen struct {
		name     string
		ab       *autoblock.Config
		rl       int
		preblock bool
	}
	scenarios := []scen{
		{name: "A) no limits", ab: &autoblock.Config{Enabled: false}},
		{name: "B) rate_limit only 30/min", ab: &autoblock.Config{Enabled: false}, rl: 30},
		{name: "B2) rate_limit only 5/min", ab: &autoblock.Config{Enabled: false}, rl: 5},
		{name: "C) autoblock max_concurrent=8", ab: &autoblock.Config{
			Enabled: true, Window: time.Minute, MaxConcurrent: 8, MaxConnections: 0,
			MaxAborts: 1000, BlockDuration: time.Hour, FeedRateHits: false,
		}},
		{name: "D) autoblock max_connections=25/min", ab: &autoblock.Config{
			Enabled: true, Window: time.Minute, MaxConcurrent: 0, MaxConnections: 25,
			MaxAborts: 1000, BlockDuration: time.Hour, FeedRateHits: false,
		}},
		{name: "E) autoblock defaults 600/150", ab: &autoblock.Config{
			Enabled: true, Window: time.Minute, MaxConcurrent: 150, MaxConnections: 600,
			MaxAborts: 60, BlockDuration: time.Hour, FeedRateHits: false,
		}},
		{name: "F) IP already blocked (DROP/reset class)", ab: &autoblock.Config{
			Enabled: true, Window: time.Minute, MaxConcurrent: 150, MaxConnections: 600,
			MaxAborts: 60, BlockDuration: time.Hour, FeedRateHits: false,
		}, preblock: true},
		{name: "G) two page loads under max_concurrent=10", ab: &autoblock.Config{
			Enabled: true, Window: time.Minute, MaxConcurrent: 10, MaxConnections: 0,
			MaxAborts: 1000, BlockDuration: time.Hour, FeedRateHits: false,
		}},
	}

	for _, sc := range scenarios {
		fmt.Printf("--- %s ---\n", sc.name)
		loads := 1
		if strings.HasPrefix(sc.name, "G)") {
			loads = 2
		}
		res := runScenario(root, sc.ab, sc.rl, sc.preblock, loads)
		printResult(res)
		fmt.Println()
	}

	fmt.Println("=== Verdict ===")
	fmt.Println("B2 rate_limit → HTTP 429 (page still gets statuses; NOT hang).")
	fmt.Println("C/F/G autoblock trip or pre-block → TCP reset, no HTTP (browser ERR_* hang class).")
	fmt.Println("Keepalive sockets hold max_concurrent slots until IdleTimeout closes them.")
	fmt.Println("E defaults → one browser page storm survives — until the IP is blocked once;")
	fmt.Println("after that (F), with firewall_sync DROP, every asset times out.")

	fmt.Println()
	fmt.Println("--- H) keepalive holds consume max_concurrent ---")
	keepaliveConcurrentDemo()
}

func keepaliveConcurrentDemo() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	ab := autoblock.New(autoblock.Config{
		Enabled: true, Window: time.Minute, MaxConcurrent: 6, MaxConnections: 0,
		MaxAborts: 10000, BlockDuration: time.Hour,
	}, logger.New("error", "text"))
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	ln := &guardWrap{Listener: &spoofListener{Listener: base, ip: spoofIP}, ab: ab}
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	defer srv.Close()
	addr := base.Addr().String()

	var holds []net.Conn
	for i := 0; i < 5; i++ {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			panic(err)
		}
		fmt.Fprintf(c, "GET / HTTP/1.1\r\nHost: t\r\nConnection: keep-alive\r\n\r\n")
		buf := make([]byte, 512)
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, _ = c.Read(buf)
		holds = append(holds, c)
	}
	fmt.Printf("  held 5 keepalive; blocked=%v\n", ab.BlockedAddr(spoofIP))

	var wg sync.WaitGroup
	var okN, noN int
	var mu sync.Mutex
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st, err := fetchOnce(addr, "/", 2*time.Second)
			mu.Lock()
			defer mu.Unlock()
			if err != "" {
				noN++
				return
			}
			if st == 200 {
				okN++
			}
		}()
	}
	wg.Wait()
	fmt.Printf("  burst4 while held: ok=%d no_http=%d blocked=%v\n", okN, noN, ab.BlockedAddr(spoofIP))
	for _, c := range holds {
		_ = c.Close()
	}
	if noN > 0 && ab.BlockedAddr(spoofIP) {
		fmt.Println("  → MATCHES: keepalive + max_concurrent trips IP; later assets reset/timeout")
	}
}

type stormResult struct {
	HTMLStatus    int
	AssetOK       int
	AssetHTTPFail int
	AssetNoHTTP   int
	Asset429      int
	Statuses      map[int]int
	Errors        []string
	BlockedAfter  bool
}

func runScenario(root string, abCfg *autoblock.Config, rateLimit int, preblock bool, loads int) stormResult {
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.Dir(root)))

	var handler http.Handler = mux
	if rateLimit > 0 {
		handler = middleware.RateLimit(context.Background(), rateLimit, time.Minute).Middleware()(mux)
	}

	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}

	var ab *autoblock.Blocker
	var ln net.Listener = &spoofListener{Listener: base, ip: spoofIP}
	if abCfg != nil && abCfg.Enabled {
		ab = autoblock.New(*abCfg, logger.New("error", "text"))
		if preblock {
			_ = ab.Block(spoofIP, autoblock.ReasonConcurrent, time.Hour)
		}
		ln = &guardWrap{Listener: ln, ab: ab}
	}

	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	go srv.Serve(ln)
	defer srv.Close()

	addr := base.Addr().String()
	assets := []string{
		"/", "/a.css", "/b.css", "/c.css", "/d.css", "/e.css",
		"/app.js", "/nav.js", "/theme.js", "/vendor.js",
		"/logo.svg", "/icons.svg", "/hero.webp", "/band.webp", "/about.webp",
	}

	res := stormResult{Statuses: map[int]int{}}
	for load := 0; load < loads; load++ {
		if load > 0 {
			// Keep previous connections briefly open to pressure max_concurrent,
			// then open a second page storm (multi-tab / navigation).
			time.Sleep(50 * time.Millisecond)
		}
		type one struct {
			path   string
			status int
			err    string
		}
		out := make([]one, len(assets))
		var wg sync.WaitGroup
		// Hold first N connections open during the storm when testing concurrent.
		holdN := 0
		if abCfg != nil && abCfg.MaxConcurrent > 0 && abCfg.MaxConcurrent <= 12 {
			holdN = abCfg.MaxConcurrent - 2
			if holdN < 0 {
				holdN = 0
			}
		}
		holders := make([]net.Conn, 0, holdN)
		for i := 0; i < holdN; i++ {
			c, err := net.DialTimeout("tcp", addr, time.Second)
			if err == nil {
				holders = append(holders, c)
			}
		}
		for i, path := range assets {
			wg.Add(1)
			go func(i int, path string) {
				defer wg.Done()
				status, err := fetchOnce(addr, path, 2*time.Second)
				out[i] = one{path: path, status: status, err: err}
			}(i, path)
		}
		wg.Wait()
		for _, c := range holders {
			_ = c.Close()
		}

		for _, o := range out {
			if o.err != "" {
				res.AssetNoHTTP++
				if len(res.Errors) < 8 {
					res.Errors = append(res.Errors, fmt.Sprintf("load%d %s: %s", load+1, o.path, o.err))
				}
				continue
			}
			res.Statuses[o.status]++
			if o.path == "/" {
				res.HTMLStatus = o.status
			}
			switch {
			case o.status == 200:
				res.AssetOK++
			case o.status == 429:
				res.Asset429++
				res.AssetHTTPFail++
			default:
				res.AssetHTTPFail++
			}
		}
	}
	if ab != nil {
		res.BlockedAfter = ab.BlockedAddr(spoofIP)
	}
	return res
}

func printResult(r stormResult) {
	fmt.Printf("  html=%d ok=%d no_http=%d http_fail=%d 429=%d blocked_after=%v statuses=%v\n",
		r.HTMLStatus, r.AssetOK, r.AssetNoHTTP, r.AssetHTTPFail, r.Asset429, r.BlockedAfter, r.Statuses)
	for _, e := range r.Errors {
		fmt.Printf("    ! %s\n", e)
	}
	switch {
	case r.AssetNoHTTP > 0 && r.Asset429 == 0:
		fmt.Println("  → MATCHES browser hang class (no HTTP status)")
	case r.Asset429 > 0 && r.AssetNoHTTP == 0:
		fmt.Println("  → soft rate limit (429), not hang")
	case r.AssetNoHTTP == 0 && r.Asset429 == 0:
		fmt.Println("  → healthy")
	default:
		fmt.Println("  → mixed")
	}
}

func fetchOnce(addr, path string, timeout time.Duration) (int, string) {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return 0, "dial: " + err.Error()
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	req := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: lab.local\r\nConnection: close\r\n\r\n", path)
	if _, err := io.WriteString(conn, req); err != nil {
		return 0, "write: " + err.Error()
	}
	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if n == 0 {
		if err != nil {
			return 0, "read: " + err.Error()
		}
		return 0, "read: empty"
	}
	line := string(buf[:n])
	if i := strings.Index(line, "\r\n"); i >= 0 {
		line = line[:i]
	}
	var proto string
	var code int
	if _, err := fmt.Sscanf(line, "%s %d", &proto, &code); err != nil {
		return 0, "bad status line: " + line
	}
	return code, ""
}

func writeSite(root string) {
	index := `<!doctype html><html><head>
<link rel="stylesheet" href="/a.css"><link rel="stylesheet" href="/b.css">
<link rel="stylesheet" href="/c.css"><link rel="stylesheet" href="/d.css">
<link rel="stylesheet" href="/e.css">
<script src="/app.js"></script><script src="/nav.js"></script>
<script src="/theme.js"></script><script src="/vendor.js"></script>
</head><body>
<img src="/logo.svg"><img src="/icons.svg"><img src="/hero.webp">
<img src="/band.webp"><img src="/about.webp"><h1>lab</h1>
</body></html>`
	mustWrite(filepath.Join(root, "index.html"), index)
	for _, name := range []string{"a.css", "b.css", "c.css", "d.css", "e.css"} {
		mustWrite(filepath.Join(root, name), "body{color:#111}")
	}
	for _, name := range []string{"app.js", "nav.js", "theme.js", "vendor.js"} {
		mustWrite(filepath.Join(root, name), "console.log('ok')")
	}
	for _, name := range []string{"logo.svg", "icons.svg"} {
		mustWrite(filepath.Join(root, name), `<svg xmlns="http://www.w3.org/2000/svg"/>`)
	}
	for _, name := range []string{"hero.webp", "band.webp", "about.webp"} {
		mustWrite(filepath.Join(root, name), "WEBP")
	}
}

func mustWrite(path, body string) {
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		panic(err)
	}
}

type spoofListener struct {
	net.Listener
	ip string
}

func (l *spoofListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &spoofConn{Conn: c, ip: l.ip}, nil
}

type spoofConn struct {
	net.Conn
	ip string
}

func (c *spoofConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP(c.ip), Port: 40000}
}

type guardWrap struct {
	net.Listener
	ab *autoblock.Blocker
}

func (l *guardWrap) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		addr, ok := autoblock.ParseAddr(c.RemoteAddr().String())
		if !ok {
			return c, nil
		}
		if !l.ab.ConnOpened(addr) {
			_ = c.Close()
			continue
		}
		return &countingConn{Conn: c, ab: l.ab, addr: addr}, nil
	}
}

type countingConn struct {
	net.Conn
	ab        *autoblock.Blocker
	addr      netip.Addr
	bytesRead atomic.Int64
	closed    atomic.Bool
}

func (c *countingConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if n > 0 {
		c.bytesRead.Add(int64(n))
	}
	return n, err
}

func (c *countingConn) Close() error {
	if c.closed.CompareAndSwap(false, true) {
		c.ab.ConnClosed(c.addr, c.bytesRead.Load() == 0)
	}
	return c.Conn.Close()
}

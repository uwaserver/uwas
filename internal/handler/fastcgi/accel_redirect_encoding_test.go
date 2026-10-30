package fastcgi

// F48601: X-Accel-Redirect carries a URI, and nginx percent-decodes it (and
// drops its query string) before mapping it to a file. UWAS joined the raw
// value to the document root, so the usual spelling of a file with a space or
// a non-ASCII name ("/my%20file.pdf") never matched, the file was not served
// and the request fell through to PHP's own (empty) body, which the WSOD
// check then turned into a 500.

import (
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/router"
	"github.com/uwaserver/uwas/pkg/fastcgi"
)

// serveAccel runs one PHP request whose response carries the given
// X-Accel-Redirect value and returns the recorded response.
func serveAccel(t *testing.T, docRoot, accel string) *httptest.ResponseRecorder {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var wg sync.WaitGroup
	wg.Add(1)
	stdout := "Status: 200 OK\r\nX-Accel-Redirect: " + accel + "\r\nContent-Type: text/html\r\n\r\nPHPBODY"
	go mockFCGIServerCustom(t, ln, stdout, "", &wg)

	h := New(logger.New("error", "text"))
	h.clients.Store(ln.Addr().String(), fastcgi.NewClient(fastcgi.PoolConfig{
		Address: "tcp:" + ln.Addr().String(), MaxIdle: 1, MaxOpen: 1,
	}))
	domain := &config.Domain{
		Host: "php.test", Root: docRoot, Type: "php",
		PHP: config.PHPConfig{FPMAddress: ln.Addr().String(), IndexFiles: []string{"index.php"}},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/download.php", nil)
	ctx := router.AcquireContext(rec, req)
	defer router.ReleaseContext(ctx)
	ctx.DocumentRoot = docRoot
	ctx.ResolvedPath = filepath.Join(docRoot, "download.php")
	ctx.OriginalURI = "/download.php"
	h.Serve(ctx, domain)
	wg.Wait()
	return rec
}

func TestXAccelRedirectIsPercentDecoded(t *testing.T) {
	outer := t.TempDir()
	docRoot := filepath.Join(outer, "site")
	write := func(rel, body string) {
		p := filepath.Join(docRoot, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("plain.txt", "PLAIN")
	write("my file.txt", "SPACED")
	write("dir/ünï.txt", "UNICODE")
	write("a%20b.txt", "LITERAL-PERCENT")
	if err := os.WriteFile(filepath.Join(outer, "secret.txt"), []byte("OUTSIDE"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name, accel, want string
	}{
		{"control: unencoded name", "/plain.txt", "PLAIN"},
		{"space as %20", "/my%20file.txt", "SPACED"},
		{"non-ASCII as UTF-8 percent bytes", "/dir/%C3%BCn%C3%AF.txt", "UNICODE"},
		{"query string is not part of the path", "/my%20file.txt?download=1", "SPACED"},
		{"control: a file whose name really contains %20 still works", "/a%20b.txt", "LITERAL-PERCENT"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := serveAccel(t, docRoot, c.accel)
			t.Logf("EXPECTED: %q\nACTUAL: code=%d body=%q", c.want, rec.Code, rec.Body.String())
			if rec.Body.String() != c.want {
				t.Fatalf("PROBLEM CONFIRMED: X-Accel-Redirect %q served %q (status %d), want %q", c.accel, rec.Body.String(), rec.Code, c.want)
			}
		})
	}

	// Decoding must not open a way out of the document root.
	for _, accel := range []string{
		"/%2e%2e/secret.txt", "/..%2fsecret.txt", "/%2e%2e%2fsecret.txt", "/dir/%2e%2e/%2e%2e/secret.txt",
		"/..%5csecret.txt", "/%00/../../secret.txt",
	} {
		rec := serveAccel(t, docRoot, accel)
		if rec.Body.String() == "OUTSIDE" {
			t.Errorf("X-Accel-Redirect %q served a file outside the document root", accel)
		}
	}
}

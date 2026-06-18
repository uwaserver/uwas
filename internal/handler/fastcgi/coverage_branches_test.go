package fastcgi

import (
	"bufio"
	"encoding/binary"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/router"
	"github.com/uwaserver/uwas/pkg/fastcgi"
)

// --- env.go: PHP_ header injection is dropped (env.go:66-67) ---

func TestBuildEnvDropsPHPHeaderInjection(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/index.php", nil)
	req.Header.Set("PHP_VALUE", "auto_prepend_file=/etc/passwd")
	req.Header.Set("PHP_ADMIN_VALUE", "disable_functions=")
	req.Header.Set("X-Safe", "ok")
	ctx := router.AcquireContext(rec, req)
	defer router.ReleaseContext(ctx)
	ctx.DocumentRoot = ""

	env := BuildEnv(ctx, "/var/www/index.php", "/index.php", "", nil)

	if _, ok := env["HTTP_PHP_VALUE"]; ok {
		t.Error("PHP_VALUE header must not be forwarded as HTTP_PHP_VALUE")
	}
	if _, ok := env["HTTP_PHP_ADMIN_VALUE"]; ok {
		t.Error("PHP_ADMIN_VALUE header must not be forwarded")
	}
	if env["HTTP_X_SAFE"] != "ok" {
		t.Errorf("HTTP_X_SAFE = %q, want ok", env["HTTP_X_SAFE"])
	}
}

// --- env.go: merge existing PHP_ADMIN_VALUE from customEnv (env.go:99-101) ---

func TestBuildEnvMergesExistingPHPAdminValue(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/index.php", nil)
	ctx := router.AcquireContext(rec, req)
	defer router.ReleaseContext(ctx)
	ctx.DocumentRoot = "/var/www/site/public"

	custom := map[string]string{
		"PHP_ADMIN_VALUE": "memory_limit = 256M",
	}
	env := BuildEnv(ctx, "/var/www/site/public/index.php", "/index.php", "", custom)

	val := env["PHP_ADMIN_VALUE"]
	if !strings.Contains(val, "open_basedir = ") {
		t.Errorf("PHP_ADMIN_VALUE missing open_basedir: %q", val)
	}
	if !strings.Contains(val, "memory_limit = 256M") {
		t.Errorf("PHP_ADMIN_VALUE did not merge existing value: %q", val)
	}
	if !strings.Contains(val, "\n") {
		t.Errorf("PHP_ADMIN_VALUE should join with newline: %q", val)
	}
}

// --- handler.go: empty FPM address returns 502 (handler.go:40-43) ---

func TestServeWithEmptyFPMAddress(t *testing.T) {
	log := logger.New("error", "text")
	h := New(log)

	domain := &config.Domain{
		Host: "php.test",
		Root: "/var/www",
		Type: "php",
		PHP:  config.PHPConfig{IndexFiles: []string{"index.php"}},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/index.php", nil)
	ctx := router.AcquireContext(rec, req)
	defer router.ReleaseContext(ctx)
	ctx.DocumentRoot = "/var/www"
	ctx.OriginalURI = "/index.php"

	h.ServeWith(ctx, domain, "", nil)

	if rec.Code != 502 {
		t.Errorf("status = %d, want 502 for empty FPM address", rec.Code)
	}
}

// --- handler.go: PHP timeout context branch (handler.go:68-72) ---

func TestServeWithPHPTimeout(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	go mockFCGIServer(t, ln, "Status: 200 OK\r\nContent-Type: text/plain", "ok", &wg)

	log := logger.New("error", "text")
	h := New(log)
	client := fastcgi.NewClient(fastcgi.PoolConfig{
		Address: "tcp:" + ln.Addr().String(),
		MaxIdle: 1, MaxOpen: 1,
	})
	h.clients.Store(ln.Addr().String(), client)

	domain := &config.Domain{
		Host: "php.test",
		Root: "/var/www",
		Type: "php",
		PHP: config.PHPConfig{
			FPMAddress: ln.Addr().String(),
			IndexFiles: []string{"index.php"},
			Timeout:    config.Duration{Duration: 30 * time.Second},
		},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/index.php", nil)
	ctx := router.AcquireContext(rec, req)
	defer router.ReleaseContext(ctx)
	ctx.DocumentRoot = "/var/www"
	ctx.ResolvedPath = "/var/www/index.php"
	ctx.OriginalURI = "/index.php"

	h.Serve(ctx, domain)
	wg.Wait()

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// --- handler.go: retry on stale connection for GET (handler.go:79-86) ---

func TestServeRetriesOnStaleConnection(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		// First connection: accept then close immediately to force a read/write
		// error (stale connection), which is retriable for a GET.
		c1, err := ln.Accept()
		if err != nil {
			return
		}
		c1.Close()

		// Second connection: serve a normal response.
		c2, err := ln.Accept()
		if err != nil {
			return
		}
		defer c2.Close()
		br := bufio.NewReader(c2)
		bw := bufio.NewWriter(c2)
		for {
			r, err := fastcgi.ReadRecord(br)
			if err != nil {
				return
			}
			if r.Type == fastcgi.TypeStdin && r.ContentLength == 0 {
				break
			}
		}
		fastcgi.WriteRecord(bw, fastcgi.TypeStdout, 1, []byte("Status: 200 OK\r\nContent-Type: text/plain\r\n\r\nretried"))
		fastcgi.WriteRecord(bw, fastcgi.TypeStdout, 1, nil)
		end := make([]byte, 8)
		fastcgi.WriteRecord(bw, fastcgi.TypeEndRequest, 1, end)
		bw.Flush()
	}()

	log := logger.New("error", "text")
	h := New(log)
	// MaxOpen 2 so the retry can dial a fresh connection.
	client := fastcgi.NewClient(fastcgi.PoolConfig{
		Address: "tcp:" + ln.Addr().String(),
		MaxIdle: 2, MaxOpen: 2,
	})
	h.clients.Store(ln.Addr().String(), client)

	domain := &config.Domain{
		Host: "php.test",
		Root: "/var/www",
		Type: "php",
		PHP: config.PHPConfig{
			FPMAddress: ln.Addr().String(),
			IndexFiles: []string{"index.php"},
		},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/index.php", nil)
	ctx := router.AcquireContext(rec, req)
	defer router.ReleaseContext(ctx)
	ctx.DocumentRoot = "/var/www"
	ctx.ResolvedPath = "/var/www/index.php"
	ctx.OriginalURI = "/index.php"

	h.Serve(ctx, domain)
	wg.Wait()

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200 after retry", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "retried") {
		t.Errorf("body = %q, want retried response", rec.Body.String())
	}
}

// --- handler.go: WSOD detection (handler.go:154-172) ---

func TestServeWSODDetection(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	// 200 text/html with non-empty stdout (headers) but EMPTY body.
	go mockFCGIServerCustom(t, ln, "Status: 200 OK\r\nContent-Type: text/html\r\n\r\n", "", &wg)

	log := logger.New("error", "text")
	h := New(log)
	client := fastcgi.NewClient(fastcgi.PoolConfig{
		Address: "tcp:" + ln.Addr().String(),
		MaxIdle: 1, MaxOpen: 1,
	})
	h.clients.Store(ln.Addr().String(), client)

	domain := &config.Domain{
		Host: "php.test",
		Root: "/var/www",
		Type: "php",
		PHP: config.PHPConfig{
			FPMAddress: ln.Addr().String(),
			IndexFiles: []string{"index.php"},
		},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/page.php", nil)
	ctx := router.AcquireContext(rec, req)
	defer router.ReleaseContext(ctx)
	ctx.DocumentRoot = "/var/www"
	ctx.ResolvedPath = "/var/www/page.php"
	ctx.OriginalURI = "/page.php"

	h.Serve(ctx, domain)
	wg.Wait()

	if rec.Code != 500 {
		t.Fatalf("status = %d, want 500 for WSOD", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "headers but no content") {
		t.Errorf("body = %q, want WSOD message", rec.Body.String())
	}
}

// TestServeWSODKnownEmptyPathNotFlagged ensures wp-cron style paths are not
// flagged as WSOD even with empty body (exercises isKnownEmpty true branch).
func TestServeWSODKnownEmptyPathNotFlagged(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	go mockFCGIServerCustom(t, ln, "Status: 200 OK\r\nContent-Type: text/html\r\n\r\n", "", &wg)

	log := logger.New("error", "text")
	h := New(log)
	client := fastcgi.NewClient(fastcgi.PoolConfig{
		Address: "tcp:" + ln.Addr().String(),
		MaxIdle: 1, MaxOpen: 1,
	})
	h.clients.Store(ln.Addr().String(), client)

	domain := &config.Domain{
		Host: "php.test",
		Root: "/var/www",
		Type: "php",
		PHP: config.PHPConfig{
			FPMAddress: ln.Addr().String(),
			IndexFiles: []string{"index.php"},
		},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/wp-cron.php", nil)
	ctx := router.AcquireContext(rec, req)
	defer router.ReleaseContext(ctx)
	ctx.DocumentRoot = "/var/www"
	ctx.ResolvedPath = "/var/www/wp-cron.php"
	ctx.OriginalURI = "/wp-cron.php"

	h.Serve(ctx, domain)
	wg.Wait()

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200 (wp-cron not WSOD)", rec.Code)
	}
}

// --- handler.go: X-Sendfile with ".." surviving Clean (handler.go:215-222) ---

func TestServeXSendfileDotDotSurvivesClean(t *testing.T) {
	docRoot := t.TempDir()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	// Relative path with leading ".." that filepath.Clean cannot eliminate,
	// so cleanPath still contains "..".
	stdout := "Status: 200 OK\r\nX-Sendfile: foo/../../etc/passwd\r\nContent-Type: text/plain\r\n\r\nfallback"
	go mockFCGIServerCustom(t, ln, stdout, "", &wg)

	log := logger.New("error", "text")
	h := New(log)
	client := fastcgi.NewClient(fastcgi.PoolConfig{
		Address: "tcp:" + ln.Addr().String(),
		MaxIdle: 1, MaxOpen: 1,
	})
	h.clients.Store(ln.Addr().String(), client)

	domain := &config.Domain{
		Host: "php.test",
		Root: docRoot,
		Type: "php",
		PHP: config.PHPConfig{
			FPMAddress: ln.Addr().String(),
			IndexFiles: []string{"index.php"},
		},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/evil.php", nil)
	ctx := router.AcquireContext(rec, req)
	defer router.ReleaseContext(ctx)
	ctx.DocumentRoot = docRoot
	ctx.ResolvedPath = filepath.Join(docRoot, "evil.php")
	ctx.OriginalURI = "/evil.php"

	h.Serve(ctx, domain)
	wg.Wait()

	if strings.Contains(rec.Body.String(), "root:") {
		t.Error("must not serve traversal path")
	}
	if !strings.Contains(rec.Body.String(), "fallback") {
		t.Errorf("expected fallthrough to PHP body, got %q", rec.Body.String())
	}
}

// --- handler.go: X-Accel-Redirect to symlink escaping root (handler.go:228-230) ---

func TestServeXAccelRedirectSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	docRoot := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("root:topsecret"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Symlink inside docRoot pointing to a file outside docRoot.
	link := filepath.Join(docRoot, "leak")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	// IsWithinBase passes lexically (docRoot/leak/secret.txt) but
	// IsWithinBaseResolved fails because the symlink resolves outside.
	stdout := "Status: 200 OK\r\nX-Accel-Redirect: /leak/secret.txt\r\nContent-Type: text/plain\r\n\r\nfallback"
	go mockFCGIServerCustom(t, ln, stdout, "", &wg)

	log := logger.New("error", "text")
	h := New(log)
	client := fastcgi.NewClient(fastcgi.PoolConfig{
		Address: "tcp:" + ln.Addr().String(),
		MaxIdle: 1, MaxOpen: 1,
	})
	h.clients.Store(ln.Addr().String(), client)

	domain := &config.Domain{
		Host: "php.test",
		Root: docRoot,
		Type: "php",
		PHP: config.PHPConfig{
			FPMAddress: ln.Addr().String(),
			IndexFiles: []string{"index.php"},
		},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/evil.php", nil)
	ctx := router.AcquireContext(rec, req)
	defer router.ReleaseContext(ctx)
	ctx.DocumentRoot = docRoot
	ctx.ResolvedPath = filepath.Join(docRoot, "evil.php")
	ctx.OriginalURI = "/evil.php"

	h.Serve(ctx, domain)
	wg.Wait()

	if strings.Contains(rec.Body.String(), "topsecret") {
		t.Error("symlink escape must not serve outside file")
	}
	if !strings.Contains(rec.Body.String(), "fallback") {
		t.Errorf("expected fallthrough to PHP body, got %q", rec.Body.String())
	}
}

// --- handler.go: isRetriable nil and matrix (handler.go:289-301) ---

func TestIsRetriable(t *testing.T) {
	if isRetriable(nil) {
		t.Error("isRetriable(nil) must be false")
	}
	retriable := []string{
		"read record: EOF",
		"write begin: broken pipe",
		"write params: connection reset",
		"flush: boom",
		"something broken pipe here",
		"connection reset by peer",
		"unexpected EOF",
	}
	for _, msg := range retriable {
		if !isRetriable(errString(msg)) {
			t.Errorf("isRetriable(%q) = false, want true", msg)
		}
	}
	if isRetriable(errString("permission denied")) {
		t.Error("non-network error should not be retriable")
	}
}

type errString string

func (e errString) Error() string { return string(e) }

// compile-time check the embedded helpers are referenced.
var _ = binary.BigEndian
var _ = http.StatusOK

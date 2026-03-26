package fastcgi

import (
	"bufio"
	"encoding/binary"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/router"
	"github.com/uwaserver/uwas/pkg/fastcgi"
)

// mockFCGIServerCustom starts a mock FastCGI server that sends custom stdout and stderr.
func mockFCGIServerCustom(t *testing.T, ln net.Listener, stdout, stderr string, wg *sync.WaitGroup) {
	t.Helper()
	defer wg.Done()

	c, err := ln.Accept()
	if err != nil {
		return
	}
	defer c.Close()

	br := bufio.NewReader(c)
	bw := bufio.NewWriter(c)

	for {
		rec, err := fastcgi.ReadRecord(br)
		if err != nil {
			return
		}
		if rec.Type == fastcgi.TypeStdin && rec.ContentLength == 0 {
			break
		}
	}

	requestID := uint16(1)

	if stdout != "" {
		if err := fastcgi.WriteRecord(bw, fastcgi.TypeStdout, requestID, []byte(stdout)); err != nil {
			return
		}
	}
	fastcgi.WriteRecord(bw, fastcgi.TypeStdout, requestID, nil)

	if stderr != "" {
		fastcgi.WriteRecord(bw, fastcgi.TypeStderr, requestID, []byte(stderr))
		fastcgi.WriteRecord(bw, fastcgi.TypeStderr, requestID, nil)
	}

	endBody := make([]byte, 8)
	binary.BigEndian.PutUint32(endBody[0:4], 0)
	fastcgi.WriteRecord(bw, fastcgi.TypeEndRequest, requestID, endBody)
	bw.Flush()
}

// --- Serve: empty PHP output with stderr ---

func TestServeEmptyOutputWithStderr(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	// Send empty stdout but with stderr
	go mockFCGIServerCustom(t, ln, "", "PHP Fatal error: something broke", &wg)

	log := logger.New("error", "text")
	h := New(log)

	client := fastcgi.NewClient(fastcgi.PoolConfig{
		Address: "tcp:" + ln.Addr().String(),
		MaxIdle: 1,
		MaxOpen: 1,
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
	req := httptest.NewRequest("GET", "/broken.php", nil)
	ctx := router.AcquireContext(rec, req)
	defer router.ReleaseContext(ctx)
	ctx.DocumentRoot = "/var/www"
	ctx.ResolvedPath = "/var/www/broken.php"
	ctx.OriginalURI = "/broken.php"

	h.Serve(ctx, domain)
	wg.Wait()

	if rec.Code != 500 {
		t.Errorf("status = %d, want 500 for empty output with stderr", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "500 Internal Server Error") {
		t.Errorf("body should contain '500 Internal Server Error', got %q", body)
	}
}

// --- Serve: X-Accel-Redirect ---

func TestServeXAccelRedirect(t *testing.T) {
	// Create a temporary document root with a file to serve
	docRoot := t.TempDir()
	testFile := filepath.Join(docRoot, "files", "download.bin")
	os.MkdirAll(filepath.Dir(testFile), 0755)
	os.WriteFile(testFile, []byte("binary content here"), 0644)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	// PHP returns X-Accel-Redirect header pointing to the file
	stdout := "Status: 200 OK\r\nX-Accel-Redirect: /files/download.bin\r\nContent-Type: application/octet-stream\r\n\r\n"
	go mockFCGIServerCustom(t, ln, stdout, "", &wg)

	log := logger.New("error", "text")
	h := New(log)

	client := fastcgi.NewClient(fastcgi.PoolConfig{
		Address: "tcp:" + ln.Addr().String(),
		MaxIdle: 1,
		MaxOpen: 1,
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
	req := httptest.NewRequest("GET", "/download.php", nil)
	ctx := router.AcquireContext(rec, req)
	defer router.ReleaseContext(ctx)
	ctx.DocumentRoot = docRoot
	ctx.ResolvedPath = filepath.Join(docRoot, "download.php")
	ctx.OriginalURI = "/download.php"

	h.Serve(ctx, domain)
	wg.Wait()

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "binary content here") {
		t.Errorf("body should contain file content, got %q", body)
	}
}

// --- Serve: X-Accel-Redirect with path traversal attempt ---

func TestServeXAccelRedirectPathTraversal(t *testing.T) {
	docRoot := t.TempDir()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	// PHP returns X-Accel-Redirect with path traversal
	stdout := "Status: 200 OK\r\nX-Accel-Redirect: /../../../etc/passwd\r\nContent-Type: text/plain\r\n\r\nfallback body"
	go mockFCGIServerCustom(t, ln, stdout, "", &wg)

	log := logger.New("error", "text")
	h := New(log)

	client := fastcgi.NewClient(fastcgi.PoolConfig{
		Address: "tcp:" + ln.Addr().String(),
		MaxIdle: 1,
		MaxOpen: 1,
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

	// Should fall through to normal response (not serve /etc/passwd)
	body := rec.Body.String()
	if strings.Contains(body, "root:") {
		t.Error("should not serve files outside document root")
	}
}

// --- Serve: X-Accel-Redirect with nonexistent file ---

func TestServeXAccelRedirectNonexistentFile(t *testing.T) {
	docRoot := t.TempDir()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	// PHP returns X-Accel-Redirect pointing to a file that doesn't exist
	stdout := "Status: 200 OK\r\nX-Accel-Redirect: /nonexistent.bin\r\nContent-Type: text/plain\r\n\r\nfallback body"
	go mockFCGIServerCustom(t, ln, stdout, "", &wg)

	log := logger.New("error", "text")
	h := New(log)

	client := fastcgi.NewClient(fastcgi.PoolConfig{
		Address: "tcp:" + ln.Addr().String(),
		MaxIdle: 1,
		MaxOpen: 1,
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
	req := httptest.NewRequest("GET", "/dl.php", nil)
	ctx := router.AcquireContext(rec, req)
	defer router.ReleaseContext(ctx)
	ctx.DocumentRoot = docRoot
	ctx.ResolvedPath = filepath.Join(docRoot, "dl.php")
	ctx.OriginalURI = "/dl.php"

	h.Serve(ctx, domain)
	wg.Wait()

	// Should fall through to normal response since file doesn't exist
	body := rec.Body.String()
	if !strings.Contains(body, "fallback body") {
		t.Errorf("should fall through to normal response, got %q", body)
	}
}

// --- Serve: X-Sendfile ---

func TestServeXSendfile(t *testing.T) {
	docRoot := t.TempDir()
	testFile := filepath.Join(docRoot, "sendme.txt")
	os.WriteFile(testFile, []byte("sendfile content"), 0644)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	// Use forward slashes in the header value (the code uses filepath.Abs)
	sendfilePath := filepath.ToSlash(testFile)
	stdout := "Status: 200 OK\r\nX-Sendfile: " + sendfilePath + "\r\nContent-Type: text/plain\r\n\r\n"
	go mockFCGIServerCustom(t, ln, stdout, "", &wg)

	log := logger.New("error", "text")
	h := New(log)

	client := fastcgi.NewClient(fastcgi.PoolConfig{
		Address: "tcp:" + ln.Addr().String(),
		MaxIdle: 1,
		MaxOpen: 1,
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
	req := httptest.NewRequest("GET", "/file.php", nil)
	ctx := router.AcquireContext(rec, req)
	defer router.ReleaseContext(ctx)
	ctx.DocumentRoot = docRoot
	ctx.ResolvedPath = filepath.Join(docRoot, "file.php")
	ctx.OriginalURI = "/file.php"

	h.Serve(ctx, domain)
	wg.Wait()

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "sendfile content") {
		t.Errorf("body should contain sendfile content, got %q", body)
	}
}

// --- Serve: X-Sendfile with path traversal ---

func TestServeXSendfilePathTraversal(t *testing.T) {
	docRoot := t.TempDir()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	// Try to send a file outside document root
	stdout := "Status: 200 OK\r\nX-Sendfile: /etc/passwd\r\nContent-Type: text/plain\r\n\r\nfallback"
	go mockFCGIServerCustom(t, ln, stdout, "", &wg)

	log := logger.New("error", "text")
	h := New(log)

	client := fastcgi.NewClient(fastcgi.PoolConfig{
		Address: "tcp:" + ln.Addr().String(),
		MaxIdle: 1,
		MaxOpen: 1,
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

	// Should fall through to normal response
	body := rec.Body.String()
	if strings.Contains(body, "root:") {
		t.Error("should not serve files outside document root via X-Sendfile")
	}
}

// --- Serve: X-Sendfile with nonexistent file ---

func TestServeXSendfileNonexistent(t *testing.T) {
	docRoot := t.TempDir()
	nonExistPath := filepath.Join(docRoot, "nofile.txt")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	stdout := "Status: 200 OK\r\nX-Sendfile: " + filepath.ToSlash(nonExistPath) + "\r\nContent-Type: text/plain\r\n\r\nfallback body"
	go mockFCGIServerCustom(t, ln, stdout, "", &wg)

	log := logger.New("error", "text")
	h := New(log)

	client := fastcgi.NewClient(fastcgi.PoolConfig{
		Address: "tcp:" + ln.Addr().String(),
		MaxIdle: 1,
		MaxOpen: 1,
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
	req := httptest.NewRequest("GET", "/dl.php", nil)
	ctx := router.AcquireContext(rec, req)
	defer router.ReleaseContext(ctx)
	ctx.DocumentRoot = docRoot
	ctx.ResolvedPath = filepath.Join(docRoot, "dl.php")
	ctx.OriginalURI = "/dl.php"

	h.Serve(ctx, domain)
	wg.Wait()

	// Should fall through to normal response since file doesn't exist
	body := rec.Body.String()
	if !strings.Contains(body, "fallback body") {
		t.Errorf("should fall through to normal response, got %q", body)
	}
}

// --- Serve: HEAD request (no body forwarded) ---

func TestServeHEADRequest(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	responseHeaders := "Status: 200 OK\r\nContent-Type: text/html"
	responseBody := "<h1>HEAD response</h1>"

	var wg sync.WaitGroup
	wg.Add(1)
	go mockFCGIServer(t, ln, responseHeaders, responseBody, &wg)

	log := logger.New("error", "text")
	h := New(log)

	client := fastcgi.NewClient(fastcgi.PoolConfig{
		Address: "tcp:" + ln.Addr().String(),
		MaxIdle: 1,
		MaxOpen: 1,
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
	req := httptest.NewRequest("HEAD", "/index.php", nil)
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

// --- Serve: DELETE request (body is forwarded) ---

func TestServeDELETERequest(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	responseHeaders := "Status: 204 No Content\r\nContent-Type: text/plain"
	responseBody := ""

	var wg sync.WaitGroup
	wg.Add(1)
	go mockFCGIServer(t, ln, responseHeaders, responseBody, &wg)

	log := logger.New("error", "text")
	h := New(log)

	client := fastcgi.NewClient(fastcgi.PoolConfig{
		Address: "tcp:" + ln.Addr().String(),
		MaxIdle: 1,
		MaxOpen: 1,
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
	req := httptest.NewRequest("DELETE", "/api.php?id=5", strings.NewReader(""))
	ctx := router.AcquireContext(rec, req)
	defer router.ReleaseContext(ctx)
	ctx.DocumentRoot = "/var/www"
	ctx.ResolvedPath = "/var/www/api.php"
	ctx.OriginalURI = "/api.php?id=5"

	h.Serve(ctx, domain)
	wg.Wait()

	if rec.Code != 204 {
		t.Errorf("status = %d, want 204", rec.Code)
	}
}

// --- BuildEnv: with PATH_INFO ---

func TestBuildEnvWithPathInfo(t *testing.T) {
	r := httptest.NewRequest("GET", "/index.php/api/users", nil)
	w := httptest.NewRecorder()
	ctx := router.AcquireContext(w, r)
	defer router.ReleaseContext(ctx)
	ctx.DocumentRoot = "/var/www"

	env := BuildEnv(ctx, "/var/www/index.php", "/index.php", "/api/users", nil)

	if env["PATH_INFO"] != "/api/users" {
		t.Errorf("PATH_INFO = %q, want /api/users", env["PATH_INFO"])
	}
	if env["PATH_TRANSLATED"] != "/var/www/api/users" {
		t.Errorf("PATH_TRANSLATED = %q, want /var/www/api/users", env["PATH_TRANSLATED"])
	}
}

// --- BuildEnv: with custom port in Host header ---

func TestBuildEnvCustomPort(t *testing.T) {
	r := httptest.NewRequest("GET", "/index.php", nil)
	r.Host = "example.com:8080"
	w := httptest.NewRecorder()
	ctx := router.AcquireContext(w, r)
	defer router.ReleaseContext(ctx)
	ctx.DocumentRoot = "/var/www"

	env := BuildEnv(ctx, "/var/www/index.php", "/index.php", "", nil)

	if env["SERVER_PORT"] != "8080" {
		t.Errorf("SERVER_PORT = %q, want 8080", env["SERVER_PORT"])
	}
	if env["HTTP_HOST"] != "example.com:8080" {
		t.Errorf("HTTP_HOST = %q, want example.com:8080", env["HTTP_HOST"])
	}
}

// --- BuildEnv: without Host header ---

func TestBuildEnvNoHost(t *testing.T) {
	r := httptest.NewRequest("GET", "/index.php", nil)
	r.Host = ""
	w := httptest.NewRecorder()
	ctx := router.AcquireContext(w, r)
	defer router.ReleaseContext(ctx)
	ctx.DocumentRoot = "/var/www"

	env := BuildEnv(ctx, "/var/www/index.php", "/index.php", "", nil)

	if _, ok := env["HTTP_HOST"]; ok {
		t.Error("HTTP_HOST should not be set when Host is empty")
	}
}

// --- SplitScriptPath: edge cases ---

func TestSplitScriptPathWithQueryInPhp(t *testing.T) {
	// Request to /index.php?page=1 with resolved path
	script, pathInfo := SplitScriptPath("/index.php?page=1", "/var/www/index.php", "/var/www", []string{"index.php"})
	if script != "/index.php" {
		t.Errorf("script = %q, want /index.php", script)
	}
	if pathInfo != "" {
		t.Errorf("pathInfo = %q, want empty", pathInfo)
	}
}

func TestSplitScriptPathFallbackNoResolvedNoIndex(t *testing.T) {
	// No resolved path, no .php in URI, no index files
	script, pathInfo := SplitScriptPath("/some/path", "", "/var/www", nil)
	if script != "/some/path" {
		t.Errorf("script = %q, want /some/path", script)
	}
	if pathInfo != "" {
		t.Errorf("pathInfo = %q, want empty", pathInfo)
	}
}

func TestSplitScriptPathFallbackWithIndex(t *testing.T) {
	// No resolved path, no .php in URI, with index files
	script, pathInfo := SplitScriptPath("/api/endpoint", "", "/var/www", []string{"index.php"})
	if script != "/index.php" {
		t.Errorf("script = %q, want /index.php", script)
	}
	if pathInfo != "/api/endpoint" {
		t.Errorf("pathInfo = %q, want /api/endpoint", pathInfo)
	}
}

func TestSplitScriptPathRootWithIndex(t *testing.T) {
	// Root request with no resolved path but with index files
	script, pathInfo := SplitScriptPath("/", "", "/var/www", []string{"index.php"})
	if script != "/index.php" {
		t.Errorf("script = %q, want /index.php", script)
	}
	if pathInfo != "" {
		t.Errorf("pathInfo = %q, want empty", pathInfo)
	}
}

// --- ScriptFilenameFromResolved: various paths ---

func TestScriptFilenameFromResolvedExists(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.php")
	os.WriteFile(filePath, []byte("<?php echo 1;"), 0644)

	got := ScriptFilenameFromResolved(filePath, dir, "/index.php")
	if got != filePath {
		t.Errorf("got %q, want %q (resolved path should be used)", got, filePath)
	}
}

func TestScriptFilenameFromResolvedNotExists(t *testing.T) {
	dir := t.TempDir()
	nonExist := filepath.Join(dir, "nope.php")

	got := ScriptFilenameFromResolved(nonExist, dir, "/index.php")
	want := strings.TrimRight(dir, "/") + "/index.php"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestScriptFilenameFromResolvedEmpty(t *testing.T) {
	got := ScriptFilenameFromResolved("", "/var/www", "/index.php")
	if got != "/var/www/index.php" {
		t.Errorf("got %q, want /var/www/index.php", got)
	}
}

// --- Serve: PUT request with body ---

func TestServePUTRequest(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	responseHeaders := "Status: 200 OK\r\nContent-Type: application/json"
	responseBody := `{"updated":true}`

	var wg sync.WaitGroup
	wg.Add(1)
	go mockFCGIServer(t, ln, responseHeaders, responseBody, &wg)

	log := logger.New("error", "text")
	h := New(log)

	client := fastcgi.NewClient(fastcgi.PoolConfig{
		Address: "tcp:" + ln.Addr().String(),
		MaxIdle: 1,
		MaxOpen: 1,
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

	bodyStr := `{"name":"updated"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api.php", strings.NewReader(bodyStr))
	req.Header.Set("Content-Type", "application/json")

	ctx := router.AcquireContext(rec, req)
	defer router.ReleaseContext(ctx)
	ctx.DocumentRoot = "/var/www"
	ctx.ResolvedPath = "/var/www/api.php"
	ctx.OriginalURI = "/api.php"

	h.Serve(ctx, domain)
	wg.Wait()

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// --- Serve: PATCH request with body ---

func TestServePATCHRequest(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	responseHeaders := "Status: 200 OK\r\nContent-Type: application/json"
	responseBody := `{"patched":true}`

	var wg sync.WaitGroup
	wg.Add(1)
	go mockFCGIServer(t, ln, responseHeaders, responseBody, &wg)

	log := logger.New("error", "text")
	h := New(log)

	client := fastcgi.NewClient(fastcgi.PoolConfig{
		Address: "tcp:" + ln.Addr().String(),
		MaxIdle: 1,
		MaxOpen: 1,
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
	req := httptest.NewRequest("PATCH", "/api.php", strings.NewReader(`{"field":"val"}`))
	req.Header.Set("Content-Type", "application/json")

	ctx := router.AcquireContext(rec, req)
	defer router.ReleaseContext(ctx)
	ctx.DocumentRoot = "/var/www"
	ctx.ResolvedPath = "/var/www/api.php"
	ctx.OriginalURI = "/api.php"

	h.Serve(ctx, domain)
	wg.Wait()

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// --- Serve: response with multiple custom headers ---

func TestServeMultipleHeaders(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	stdout := "Status: 200 OK\r\nContent-Type: text/html\r\nX-Custom-A: valueA\r\nX-Custom-B: valueB\r\nSet-Cookie: sid=abc\r\n\r\nOK"
	go mockFCGIServerCustom(t, ln, stdout, "", &wg)

	log := logger.New("error", "text")
	h := New(log)

	client := fastcgi.NewClient(fastcgi.PoolConfig{
		Address: "tcp:" + ln.Addr().String(),
		MaxIdle: 1,
		MaxOpen: 1,
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
	req := httptest.NewRequest("GET", "/multi.php", nil)
	ctx := router.AcquireContext(rec, req)
	defer router.ReleaseContext(ctx)
	ctx.DocumentRoot = "/var/www"
	ctx.ResolvedPath = "/var/www/multi.php"
	ctx.OriginalURI = "/multi.php"

	h.Serve(ctx, domain)
	wg.Wait()

	if rec.Header().Get("X-Custom-A") != "valueA" {
		t.Errorf("X-Custom-A = %q, want valueA", rec.Header().Get("X-Custom-A"))
	}
	if rec.Header().Get("X-Custom-B") != "valueB" {
		t.Errorf("X-Custom-B = %q, want valueB", rec.Header().Get("X-Custom-B"))
	}
}

// --- BuildEnv: HTTPS with custom port ---

func TestBuildEnvHTTPSWithPort(t *testing.T) {
	r := httptest.NewRequest("GET", "/index.php", nil)
	r.Host = "example.com:8443"
	w := httptest.NewRecorder()
	ctx := router.AcquireContext(w, r)
	defer router.ReleaseContext(ctx)
	ctx.IsHTTPS = true
	ctx.DocumentRoot = "/var/www"

	env := BuildEnv(ctx, "/var/www/index.php", "/index.php", "", nil)

	if env["HTTPS"] != "on" {
		t.Errorf("HTTPS = %q, want on", env["HTTPS"])
	}
	if env["SERVER_PORT"] != "8443" {
		t.Errorf("SERVER_PORT = %q, want 8443", env["SERVER_PORT"])
	}
}

// --- BuildEnv: with trailing slash on document root ---

func TestBuildEnvTrailingSlashDocRoot(t *testing.T) {
	r := httptest.NewRequest("GET", "/index.php/sub", nil)
	w := httptest.NewRecorder()
	ctx := router.AcquireContext(w, r)
	defer router.ReleaseContext(ctx)
	ctx.DocumentRoot = "/var/www/"

	env := BuildEnv(ctx, "/var/www/index.php", "/index.php", "/sub", nil)

	if env["PATH_TRANSLATED"] != "/var/www/sub" {
		t.Errorf("PATH_TRANSLATED = %q, want /var/www/sub", env["PATH_TRANSLATED"])
	}
}

// --- ScriptFilename: with trailing slash in docRoot ---

func TestScriptFilenameTrailingSlash(t *testing.T) {
	got := ScriptFilename("/var/www/", "/test.php")
	if got != "/var/www/test.php" {
		t.Errorf("ScriptFilename = %q, want /var/www/test.php", got)
	}
}

// --- SplitScriptPath: resolvedPath same as docRoot (rel is empty, no leading slash) ---

func TestSplitScriptPathResolvedSameAsDocRoot(t *testing.T) {
	// When resolvedPath equals docRoot, rel will be empty string.
	// After ToSlash it's still "", which doesn't start with "/".
	// The code adds "/" prefix, making scriptName = "/".
	// Since "/" doesn't end with .php and origPath is "/test",
	// it falls through to the index files fallback.
	dir := t.TempDir()
	script, pathInfo := SplitScriptPath("/test", dir, dir, []string{"index.php"})
	if script != "/index.php" {
		t.Errorf("script = %q, want /index.php", script)
	}
	if pathInfo != "/test" {
		t.Errorf("pathInfo = %q, want /test", pathInfo)
	}
}

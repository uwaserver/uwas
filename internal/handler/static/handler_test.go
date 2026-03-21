package static

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/router"
)

func TestStaticFileServing(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "index.html", "<h1>Hello</h1>")

	ctx := makeCtx(t, "GET", "/index.html")
	ctx.ResolvedPath = filepath.Join(dir, "index.html")

	h := New()
	h.Serve(ctx)

	if ctx.Response.StatusCode() != 200 {
		t.Errorf("status = %d, want 200", ctx.Response.StatusCode())
	}
}

func TestStaticFile304(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "style.css", "body{}")

	// First request to get ETag
	ctx1 := makeCtx(t, "GET", "/style.css")
	ctx1.ResolvedPath = filepath.Join(dir, "style.css")
	h := New()
	h.Serve(ctx1)

	etag := ctx1.Response.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag header in response")
	}

	// Second request with If-None-Match
	ctx2 := makeCtxWithHeader(t, "GET", "/style.css", "If-None-Match", etag)
	ctx2.ResolvedPath = filepath.Join(dir, "style.css")
	h.Serve(ctx2)

	if ctx2.Response.StatusCode() != 304 {
		t.Errorf("status = %d, want 304", ctx2.Response.StatusCode())
	}
}

func TestDotfileBlocked(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".env", "SECRET=123")

	ctx := makeCtx(t, "GET", "/.env")
	ctx.ResolvedPath = filepath.Join(dir, ".env")

	h := New()
	h.Serve(ctx)

	if ctx.Response.StatusCode() != 403 {
		t.Errorf("status = %d, want 403 for dotfile", ctx.Response.StatusCode())
	}
}

func TestPreCompressedBrotli(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "app.js", "console.log('hello')")
	writeFile(t, dir, "app.js.br", "compressed-brotli-data")

	ctx := makeCtxWithHeader(t, "GET", "/app.js", "Accept-Encoding", "br, gzip")
	ctx.ResolvedPath = filepath.Join(dir, "app.js")

	h := New()
	h.Serve(ctx)

	if enc := ctx.Response.Header().Get("Content-Encoding"); enc != "br" {
		t.Errorf("Content-Encoding = %q, want br", enc)
	}
}

func TestPreCompressedGzip(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "app.js", "console.log('hello')")
	writeFile(t, dir, "app.js.gz", "compressed-gzip-data")

	ctx := makeCtxWithHeader(t, "GET", "/app.js", "Accept-Encoding", "gzip")
	ctx.ResolvedPath = filepath.Join(dir, "app.js")

	h := New()
	h.Serve(ctx)

	if enc := ctx.Response.Header().Get("Content-Encoding"); enc != "gzip" {
		t.Errorf("Content-Encoding = %q, want gzip", enc)
	}
}

func TestResolveRequestStatic(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "index.html", "<h1>Home</h1>")
	writeFile(t, dir, "about.html", "<h1>About</h1>")

	domain := &config.Domain{
		Host: "example.com",
		Root: dir,
		Type: "static",
	}

	ctx := makeCtx(t, "GET", "/about.html")
	if !ResolveRequest(ctx, domain) {
		t.Fatal("ResolveRequest should find about.html")
	}
	if ctx.ResolvedPath != filepath.Join(dir, "about.html") {
		t.Errorf("ResolvedPath = %q, want %q", ctx.ResolvedPath, filepath.Join(dir, "about.html"))
	}
}

func TestResolveRequestIndexFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "index.html", "<h1>Home</h1>")

	domain := &config.Domain{
		Host: "example.com",
		Root: dir,
		Type: "static",
	}

	ctx := makeCtx(t, "GET", "/")
	if !ResolveRequest(ctx, domain) {
		t.Fatal("ResolveRequest should find index.html for /")
	}
	if !filepath.IsAbs(ctx.ResolvedPath) {
		t.Errorf("ResolvedPath should be absolute: %q", ctx.ResolvedPath)
	}
}

func TestResolveRequestSPAMode(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "index.html", "<div id=app></div>")

	domain := &config.Domain{
		Host:    "spa.com",
		Root:    dir,
		Type:    "static",
		SPAMode: true,
	}

	ctx := makeCtx(t, "GET", "/some/deep/route")
	if !ResolveRequest(ctx, domain) {
		t.Fatal("SPA mode should fallback to index.html")
	}
}

func TestResolveRequestPathTraversal(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "secret.txt", "should not be served")

	// Create a subdirectory
	subDir := filepath.Join(dir, "sub")
	os.MkdirAll(subDir, 0755)

	domain := &config.Domain{
		Host: "example.com",
		Root: subDir,
		Type: "static",
	}

	// Try to escape via ../
	ctx := makeCtx(t, "GET", "/../secret.txt")
	resolved := ResolveRequest(ctx, domain)

	// Either not resolved or resolved path is within root
	if resolved && ctx.ResolvedPath != "" {
		absRoot, _ := filepath.Abs(subDir)
		absResolved, _ := filepath.Abs(ctx.ResolvedPath)
		if !strings.HasPrefix(absResolved, absRoot) {
			t.Errorf("path traversal not blocked: resolved to %q (root: %q)", absResolved, absRoot)
		}
	}
}

func TestMIMELookup(t *testing.T) {
	m := NewMIMERegistry(nil)

	tests := []struct {
		file string
		want string
	}{
		{"style.css", "text/css; charset=utf-8"},
		{"app.js", "application/javascript; charset=utf-8"},
		{"image.png", "image/png"},
		{"font.woff2", "font/woff2"},
		{"data.wasm", "application/wasm"},
		{"photo.avif", "image/avif"},
		{"unknown.xyz", "application/octet-stream"},
	}

	for _, tt := range tests {
		got := m.Lookup(tt.file)
		if got != tt.want {
			t.Errorf("Lookup(%q) = %q, want %q", tt.file, got, tt.want)
		}
	}
}

func TestMIMECustomOverride(t *testing.T) {
	custom := map[string]string{
		".custom": "application/x-custom",
	}
	m := NewMIMERegistry(custom)

	if got := m.Lookup("file.custom"); got != "application/x-custom" {
		t.Errorf("custom MIME = %q, want application/x-custom", got)
	}
}

// Helpers

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	os.MkdirAll(filepath.Dir(path), 0755)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func makeCtx(t *testing.T, method, path string) *router.RequestContext {
	t.Helper()
	r := httptest.NewRequest(method, path, nil)
	w := httptest.NewRecorder()
	return router.AcquireContext(w, r)
}

func makeCtxWithHeader(t *testing.T, method, path, hdrKey, hdrVal string) *router.RequestContext {
	t.Helper()
	r := httptest.NewRequest(method, path, nil)
	r.Header.Set(hdrKey, hdrVal)
	w := httptest.NewRecorder()
	return router.AcquireContext(w, r)
}

// --- Additional coverage tests ---

func TestHandlerName(t *testing.T) {
	h := New()
	if got := h.Name(); got != "static" {
		t.Errorf("Name() = %q, want %q", got, "static")
	}
}

func TestHandlerDescription(t *testing.T) {
	h := New()
	if got := h.Description(); got != "Serves static files from disk" {
		t.Errorf("Description() = %q, want %q", got, "Serves static files from disk")
	}
}

func TestCanHandleTrue(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "hello.txt", "hi")

	ctx := makeCtx(t, "GET", "/hello.txt")
	ctx.ResolvedPath = filepath.Join(dir, "hello.txt")

	h := New()
	if !h.CanHandle(ctx) {
		t.Error("CanHandle should return true for an existing file")
	}
}

func TestCanHandleFalseEmpty(t *testing.T) {
	ctx := makeCtx(t, "GET", "/nothing")
	ctx.ResolvedPath = ""

	h := New()
	if h.CanHandle(ctx) {
		t.Error("CanHandle should return false when ResolvedPath is empty")
	}
}

func TestCanHandleFalseNoFile(t *testing.T) {
	ctx := makeCtx(t, "GET", "/missing.txt")
	ctx.ResolvedPath = filepath.Join(t.TempDir(), "nonexistent.txt")

	h := New()
	if h.CanHandle(ctx) {
		t.Error("CanHandle should return false when file does not exist")
	}
}

func TestCanHandleFalseDir(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "subdir")
	os.MkdirAll(sub, 0755)

	ctx := makeCtx(t, "GET", "/subdir")
	ctx.ResolvedPath = sub

	h := New()
	if h.CanHandle(ctx) {
		t.Error("CanHandle should return false for a directory")
	}
}

func TestServeDirListing(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file1.txt", "content1")
	writeFile(t, dir, "file2.txt", "content2 is longer")
	os.MkdirAll(filepath.Join(dir, "subdir"), 0755)

	ctx := makeCtx(t, "GET", "/")

	ServeDirListing(ctx, dir, "/")

	rec := ctx.Response.ResponseWriter.(*httptest.ResponseRecorder)
	body := rec.Body.String()

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if !strings.Contains(body, "file1.txt") {
		t.Error("listing should contain file1.txt")
	}
	if !strings.Contains(body, "subdir/") {
		t.Error("listing should contain subdir/")
	}
}

func TestServeDirListingNonRoot(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "aaa")

	ctx := makeCtx(t, "GET", "/somepath/")

	ServeDirListing(ctx, dir, "/somepath/")

	rec := ctx.Response.ResponseWriter.(*httptest.ResponseRecorder)
	body := rec.Body.String()

	if !strings.Contains(body, "../") {
		t.Error("non-root listing should contain parent link ../")
	}
}

func TestServeDirListingBadDir(t *testing.T) {
	ctx := makeCtx(t, "GET", "/")

	ServeDirListing(ctx, filepath.Join(t.TempDir(), "nonexistent"), "/")

	rec := ctx.Response.ResponseWriter.(*httptest.ResponseRecorder)
	if rec.Code != 500 {
		t.Errorf("status = %d, want 500 for unreadable dir", rec.Code)
	}
}

func TestFormatSizeGB(t *testing.T) {
	got := formatSize(2 * (1 << 30)) // 2 GB
	if !strings.HasSuffix(got, "GB") {
		t.Errorf("formatSize(2GB) = %q, want GB suffix", got)
	}
	if got != "2.0 GB" {
		t.Errorf("formatSize(2GB) = %q, want %q", got, "2.0 GB")
	}
}

func TestFormatSizeMB(t *testing.T) {
	got := formatSize(5 * (1 << 20)) // 5 MB
	if got != "5.0 MB" {
		t.Errorf("formatSize(5MB) = %q, want %q", got, "5.0 MB")
	}
}

func TestFormatSizeKB(t *testing.T) {
	got := formatSize(3 * (1 << 10)) // 3 KB
	if got != "3.0 KB" {
		t.Errorf("formatSize(3KB) = %q, want %q", got, "3.0 KB")
	}
}

func TestFormatSizeBytes(t *testing.T) {
	got := formatSize(512)
	if got != "512 B" {
		t.Errorf("formatSize(512) = %q, want %q", got, "512 B")
	}
}

func TestFormatSizeZero(t *testing.T) {
	got := formatSize(0)
	if got != "0 B" {
		t.Errorf("formatSize(0) = %q, want %q", got, "0 B")
	}
}

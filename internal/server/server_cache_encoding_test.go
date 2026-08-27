package server

import (
	"bytes"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andybalholm/brotli"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// A handler can emit an ALREADY-ENCODED body and declare it with
// Content-Encoding: the static handler does this whenever a .br/.gz sibling
// exists (servePreCompressed), and a reverse-proxy upstream can do the same.
// The compress middleware honours that declaration and leaves the body alone.
//
// The cache capture records what the handler wrote — the encoded bytes — so the
// stored entry has to keep the encoding that describes them. Dropping it makes
// every later hit look like a plaintext body to the compress middleware, which
// encodes it a second time while the response still advertises one layer. The
// client decodes once and renders compressed bytes as text.
//
// These tests pin the invariant from the client's side: a body served from
// cache decodes in exactly one step, on the first request and every one after.

func newCacheTestChain(t *testing.T, root string) http.Handler {
	t.Helper()
	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
			// A memory limit is required: with the default 0 the L1 store keeps
			// nothing, every request is a MISS and these tests pass vacuously.
			Cache: config.CacheConfig{Enabled: true, MemoryLimit: config.ByteSize(64 << 20)},
		},
		Domains: []config.Domain{{
			Host:  "cache.test",
			Type:  "static",
			Root:  root,
			SSL:   config.SSLConfig{Mode: "off"},
			Cache: config.DomainCache{Enabled: true, TTL: 60},
		}},
	}
	s := New(cfg, logger.New("error", "text"))
	t.Cleanup(func() { s.cancel() })
	return s.buildMiddlewareChain()
}

func cacheTestGet(t *testing.T, h http.Handler, path, acceptEncoding string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	req.Host = "cache.test"
	req.Header.Set("Accept-Encoding", acceptEncoding)
	// The bot guard answers an empty User-Agent with 403.
	req.Header.Set("User-Agent", "uwas-test")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func brotliBytes(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := brotli.NewWriter(&buf)
	if _, err := w.Write(b); err != nil {
		t.Fatalf("brotli write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("brotli close: %v", err)
	}
	return buf.Bytes()
}

// decodeLayers reports how many brotli passes the body needs to become want.
// 1 is correct; 2 means the body was encoded twice but declared once.
func decodeLayers(body, want []byte) int {
	cur := body
	for layer := 1; layer <= 3; layer++ {
		out, err := io.ReadAll(brotli.NewReader(bytes.NewReader(cur)))
		if err != nil {
			return -1
		}
		if bytes.Equal(out, want) {
			return layer
		}
		cur = out
	}
	return -1
}

// hardToCompress returns printable bytes with enough entropy that their brotli
// form still exceeds the compress middleware's 1KB threshold — otherwise the
// second encoding never triggers and the regression hides.
func hardToCompress(n int) []byte {
	r := rand.New(rand.NewSource(42))
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(32 + r.Intn(95))
	}
	return b
}

func TestCachedPreCompressedBodyKeepsSingleEncoding(t *testing.T) {
	root := t.TempDir()
	plain := hardToCompress(8192)
	encoded := brotliBytes(t, plain)
	if len(encoded) < 1024 {
		t.Fatalf("fixture compresses too well (%d bytes); the second encoding would not trigger", len(encoded))
	}

	if err := os.WriteFile(filepath.Join(root, "data.js"), plain, 0o644); err != nil {
		t.Fatalf("write data.js: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "data.js.br"), encoded, 0o644); err != nil {
		t.Fatalf("write data.js.br: %v", err)
	}

	h := newCacheTestChain(t, root)

	miss := cacheTestGet(t, h, "/data.js", "br")
	if miss.Code != http.StatusOK {
		t.Fatalf("miss: status %d want 200", miss.Code)
	}
	if enc := miss.Header().Get("Content-Encoding"); enc != "br" {
		t.Fatalf("miss: Content-Encoding = %q want br", enc)
	}
	if n := decodeLayers(miss.Body.Bytes(), plain); n != 1 {
		t.Fatalf("miss: body needed %d brotli passes, want 1", n)
	}

	hit := cacheTestGet(t, h, "/data.js", "br")
	if hit.Code != http.StatusOK {
		t.Fatalf("hit: status %d want 200", hit.Code)
	}
	// Guard against a vacuous pass: without a real cache hit this test says
	// nothing about the store/replay path.
	if got := hit.Header().Get("X-Cache"); got != "HIT" {
		t.Fatalf("second request was not served from cache (X-Cache = %q)", got)
	}
	if enc := hit.Header().Get("Content-Encoding"); enc != "br" {
		t.Fatalf("hit: Content-Encoding = %q want br", enc)
	}
	if n := decodeLayers(hit.Body.Bytes(), plain); n != 1 {
		t.Fatalf("hit: body needed %d brotli passes, want 1 — the cached body was encoded again", n)
	}
	if !bytes.Equal(hit.Body.Bytes(), miss.Body.Bytes()) {
		t.Errorf("hit body differs from miss body (%d vs %d bytes)", hit.Body.Len(), miss.Body.Len())
	}
}

// The plaintext path keeps its existing behaviour: the cache stores unencoded
// bytes and the compress middleware encodes them once per hit.
func TestCachedPlainBodyStillEncodedOncePerHit(t *testing.T) {
	root := t.TempDir()
	plain := []byte(strings.Repeat("plain body, encoded by the middleware. ", 64))
	if err := os.WriteFile(filepath.Join(root, "page.html"), plain, 0o644); err != nil {
		t.Fatalf("write page.html: %v", err)
	}

	h := newCacheTestChain(t, root)

	miss := cacheTestGet(t, h, "/page.html", "br")
	if miss.Code != http.StatusOK {
		t.Fatalf("miss: status %d want 200", miss.Code)
	}
	if n := decodeLayers(miss.Body.Bytes(), plain); n != 1 {
		t.Fatalf("miss: body needed %d brotli passes, want 1", n)
	}

	hit := cacheTestGet(t, h, "/page.html", "br")
	if got := hit.Header().Get("X-Cache"); got != "HIT" {
		t.Fatalf("second request was not served from cache (X-Cache = %q)", got)
	}
	if enc := hit.Header().Get("Content-Encoding"); enc != "br" {
		t.Fatalf("hit: Content-Encoding = %q want br", enc)
	}
	if n := decodeLayers(hit.Body.Bytes(), plain); n != 1 {
		t.Fatalf("hit: body needed %d brotli passes, want 1", n)
	}
}

// A client that accepts no encoding must still receive a readable body. The
// pre-compressed variant is only selected when the request advertises support
// for it, so the entry stored for this client is plaintext.
func TestCachedBodyReadableWithoutAcceptEncoding(t *testing.T) {
	root := t.TempDir()
	plain := []byte(strings.Repeat("identity clients read this verbatim. ", 64))
	if err := os.WriteFile(filepath.Join(root, "page.html"), plain, 0o644); err != nil {
		t.Fatalf("write page.html: %v", err)
	}

	h := newCacheTestChain(t, root)

	for _, label := range []string{"miss", "hit"} {
		rec := cacheTestGet(t, h, "/page.html", "identity")
		if enc := rec.Header().Get("Content-Encoding"); enc != "" {
			t.Fatalf("%s: Content-Encoding = %q want empty", label, enc)
		}
		if !bytes.Equal(rec.Body.Bytes(), plain) {
			t.Fatalf("%s: body was not served verbatim (%d bytes)", label, rec.Body.Len())
		}
	}
}

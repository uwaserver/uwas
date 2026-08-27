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

// The static handler serves a .br/.gz sibling verbatim and declares it with
// Content-Encoding (servePreCompressed). That body must never reach the cache
// as if it were plaintext: a later hit would hand the compress middleware an
// already-encoded body, which it would encode again while the response still
// advertises a single layer — the client decodes once and renders compressed
// bytes as text.
//
// 3ac3d34 fixed this by refusing to cache an encoded response, and covers the
// reverse-proxy path in TestPrecompressedResponseIsNotCachedOrDoubleCompressed.
// The static handler reaches the same place through different code, so it gets
// its own test.
//
// The assertion is made from the client's side — a body must decode in exactly
// one step — so it holds regardless of how the cache decides to treat the
// entry.

func newStaticCacheChain(t *testing.T, root string) http.Handler {
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

func staticCacheGet(t *testing.T, h http.Handler, path, acceptEncoding string) *httptest.ResponseRecorder {
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

func staticBrotliBytes(t *testing.T, b []byte) []byte {
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
func staticDecodeLayers(body, want []byte) int {
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
func staticHardToCompress(n int) []byte {
	r := rand.New(rand.NewSource(42))
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(32 + r.Intn(95))
	}
	return b
}

func TestCachedPreCompressedBodyKeepsSingleEncoding(t *testing.T) {
	root := t.TempDir()
	plain := staticHardToCompress(8192)
	encoded := staticBrotliBytes(t, plain)
	if len(encoded) < 1024 {
		t.Fatalf("fixture compresses too well (%d bytes); the second encoding would not trigger", len(encoded))
	}

	if err := os.WriteFile(filepath.Join(root, "data.js"), plain, 0o644); err != nil {
		t.Fatalf("write data.js: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "data.js.br"), encoded, 0o644); err != nil {
		t.Fatalf("write data.js.br: %v", err)
	}

	h := newStaticCacheChain(t, root)

	miss := staticCacheGet(t, h, "/data.js", "br")
	if miss.Code != http.StatusOK {
		t.Fatalf("miss: status %d want 200", miss.Code)
	}
	if enc := miss.Header().Get("Content-Encoding"); enc != "br" {
		t.Fatalf("miss: Content-Encoding = %q want br", enc)
	}
	if n := staticDecodeLayers(miss.Body.Bytes(), plain); n != 1 {
		t.Fatalf("miss: body needed %d brotli passes, want 1", n)
	}

	hit := staticCacheGet(t, h, "/data.js", "br")
	if hit.Code != http.StatusOK {
		t.Fatalf("hit: status %d want 200", hit.Code)
	}
	// An encoded response is deliberately kept out of the cache, so the second
	// request is a miss too. Asserting that here pins the current contract: if
	// a later change starts storing these entries, this test still demands a
	// single decode layer below.
	if got := hit.Header().Get("X-Cache"); got != "" {
		t.Fatalf("encoded response must not be cached (X-Cache = %q)", got)
	}
	if enc := hit.Header().Get("Content-Encoding"); enc != "br" {
		t.Fatalf("hit: Content-Encoding = %q want br", enc)
	}
	if n := staticDecodeLayers(hit.Body.Bytes(), plain); n != 1 {
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

	h := newStaticCacheChain(t, root)

	miss := staticCacheGet(t, h, "/page.html", "br")
	if miss.Code != http.StatusOK {
		t.Fatalf("miss: status %d want 200", miss.Code)
	}
	if n := staticDecodeLayers(miss.Body.Bytes(), plain); n != 1 {
		t.Fatalf("miss: body needed %d brotli passes, want 1", n)
	}

	hit := staticCacheGet(t, h, "/page.html", "br")
	if got := hit.Header().Get("X-Cache"); got != "HIT" {
		t.Fatalf("second request was not served from cache (X-Cache = %q)", got)
	}
	if enc := hit.Header().Get("Content-Encoding"); enc != "br" {
		t.Fatalf("hit: Content-Encoding = %q want br", enc)
	}
	if n := staticDecodeLayers(hit.Body.Bytes(), plain); n != 1 {
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

	h := newStaticCacheChain(t, root)

	for _, label := range []string{"miss", "hit"} {
		rec := staticCacheGet(t, h, "/page.html", "identity")
		if enc := rec.Header().Get("Content-Encoding"); enc != "" {
			t.Fatalf("%s: Content-Encoding = %q want empty", label, enc)
		}
		if !bytes.Equal(rec.Body.Bytes(), plain) {
			t.Fatalf("%s: body was not served verbatim (%d bytes)", label, rec.Body.Len())
		}
	}
}

package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// domain.Compression was dead configuration: the chain hardcoded
// middleware.Gzip(1024) and read none of enabled, min_size, algorithms or
// types. An operator could set them, see them echoed back by the API, and get
// no change. These tests assert the Content-Encoding a client actually
// receives, so they fail if the block goes back to being ignored.

const compressibleBodySize = 3000 // bytes; above every min_size used below

func compressionFixture(t *testing.T, c config.CompressionConfig) http.Handler {
	t.Helper()

	root := t.TempDir()
	// Highly compressible so the encoded body is unmistakably smaller.
	body := strings.Repeat("dgn ", compressibleBodySize/4)
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	cfg := &config.Config{
		Global: config.GlobalConfig{WorkerCount: "1", LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{{
			Host:        "zip.test",
			Type:        "static",
			Root:        root,
			SSL:         config.SSLConfig{Mode: "off"},
			Compression: c,
		}},
	}

	s := New(cfg, logger.New("error", "text"))
	t.Cleanup(func() { s.cancel() })
	return s.buildMiddlewareChain()
}

// encodingFor issues one request and returns the Content-Encoding it got back.
// The bot guard answers an empty User-Agent with 403, so it must be set.
func encodingFor(t *testing.T, h http.Handler, acceptEncoding string) (string, int) {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/index.html", nil)
	req.Host = "zip.test"
	req.Header.Set("User-Agent", "uwas-test")
	req.Header.Set("Accept-Encoding", acceptEncoding)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("returned %d, want 200", rec.Code)
	}
	return rec.Header().Get("Content-Encoding"), rec.Body.Len()
}

// A domain that never mentions compression must keep it. Enabled is a pointer
// precisely so this case does not zero-value into "disabled".
func TestCompressionDefaultsOnWhenUnconfigured(t *testing.T) {
	h := compressionFixture(t, config.CompressionConfig{})

	enc, n := encodingFor(t, h, "br, gzip")
	if enc != "br" {
		t.Errorf("Content-Encoding = %q, want br", enc)
	}
	if n >= compressibleBodySize {
		t.Errorf("body is %d bytes — it does not look compressed", n)
	}
}

func TestCompressionDisabledForDomain(t *testing.T) {
	h := compressionFixture(t, config.CompressionConfig{Enabled: config.BoolPtr(false)})

	enc, n := encodingFor(t, h, "br, gzip")
	if enc != "" {
		t.Errorf("Content-Encoding = %q, want empty (compression disabled)", enc)
	}
	if n < compressibleBodySize {
		t.Errorf("body is %d bytes — it looks compressed while disabled", n)
	}
}

// min_size above the body size must leave the response uncompressed.
func TestCompressionMinSizeIsHonoured(t *testing.T) {
	h := compressionFixture(t, config.CompressionConfig{MinSize: compressibleBodySize * 10})

	if enc, _ := encodingFor(t, h, "br, gzip"); enc != "" {
		t.Errorf("Content-Encoding = %q, want empty (body under min_size)", enc)
	}
}

// Restricting algorithms to gzip must produce gzip even though the client
// offers brotli and brotli is otherwise preferred.
func TestCompressionAlgorithmRestriction(t *testing.T) {
	h := compressionFixture(t, config.CompressionConfig{Algorithms: []string{"gzip"}})

	if enc, _ := encodingFor(t, h, "br, gzip"); enc != "gzip" {
		t.Errorf("Content-Encoding = %q, want gzip (algorithms: [gzip])", enc)
	}
}

// When only brotli is allowed and the client offers only gzip, nothing is
// compressed rather than something being served under the wrong encoding.
func TestCompressionAlgorithmRestrictionNoOverlap(t *testing.T) {
	h := compressionFixture(t, config.CompressionConfig{Algorithms: []string{"br"}})

	if enc, _ := encodingFor(t, h, "gzip"); enc != "" {
		t.Errorf("Content-Encoding = %q, want empty (no algorithm in common)", enc)
	}
}

// A types list that excludes text/html must leave the HTML uncompressed.
func TestCompressionTypesRestriction(t *testing.T) {
	h := compressionFixture(t, config.CompressionConfig{Types: []string{"application/json"}})

	if enc, _ := encodingFor(t, h, "br, gzip"); enc != "" {
		t.Errorf("Content-Encoding = %q, want empty (text/html not in types)", enc)
	}
}

func TestCompressionTypesIncludingHTML(t *testing.T) {
	h := compressionFixture(t, config.CompressionConfig{Types: []string{"text/html"}})

	if enc, _ := encodingFor(t, h, "br, gzip"); enc != "br" {
		t.Errorf("Content-Encoding = %q, want br (types listesinde text/html var)", enc)
	}
}

package middleware

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing/iotest"

	"testing"
)

// TestCompressWriteContractSmallChunks is the regression for the io.Writer
// contract violation in compressResponseWriter.Write. When a handler streams a
// body in chunks that individually stay under minSize but cross it in
// aggregate (e.g. io.Copy from a slow upstream), the buffer-flush path used to
// return the whole buffer length — larger than the current chunk — which trips
// io.Copy's errInvalidWrite and truncates the response. Write must report only
// the bytes it consumed from the current call.
func TestCompressWriteContractSmallChunks(t *testing.T) {
	payload := bytes.Repeat([]byte("abcdefgh"), 500) // 4000 bytes, compressible

	var copyErr error
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		// OneByteReader forces io.Copy to call Write one byte at a time, so the
		// minSize boundary is crossed mid-stream on a 1-byte Write.
		_, copyErr = io.Copy(w, iotest.OneByteReader(bytes.NewReader(payload)))
	})

	compressed := Compress(1024)(handler)
	req := httptest.NewRequest("GET", "/stream", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	compressed.ServeHTTP(rec, req)

	if copyErr != nil {
		t.Fatalf("io.Copy through compress writer failed: %v", copyErr)
	}
	if ce := rec.Header().Get("Content-Encoding"); ce != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", ce)
	}
	gr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	got, err := io.ReadAll(gr)
	if err != nil {
		t.Fatalf("gzip read: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("round-trip mismatch: got %d bytes, want %d (response truncated?)", len(got), len(payload))
	}
}

// hijackRecorder is an httptest.ResponseRecorder that also satisfies
// http.Hijacker, so a handler can assert the writer it received is hijackable.
type hijackRecorder struct {
	*httptest.ResponseRecorder
	hijacked bool
}

func (h *hijackRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h.hijacked = true
	c1, _ := net.Pipe()
	return c1, bufio.NewReadWriter(bufio.NewReader(c1), bufio.NewWriter(c1)), nil
}

// TestCompressSkipsUpgradeHijack is the regression for WebSocket proxying
// breaking behind the compress middleware: browsers send Accept-Encoding on the
// upgrade request, and compressResponseWriter is not an http.Hijacker, so the
// hijack failed. Upgrade requests must reach the underlying writer unwrapped.
func TestCompressSkipsUpgradeHijack(t *testing.T) {
	var hijackable bool
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hijackable = w.(http.Hijacker)
	})

	compressed := Compress(1024)(handler)
	req := httptest.NewRequest("GET", "/ws", nil)
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	rec := &hijackRecorder{ResponseRecorder: httptest.NewRecorder()}
	compressed.ServeHTTP(rec, req)

	if !hijackable {
		t.Fatal("handler did not receive an http.Hijacker for a WebSocket upgrade — compress middleware wrapped it")
	}
}

// TestCompressFlushStreams verifies that a Flush on the compress writer pushes
// buffered bytes to the client instead of holding them until minSize is
// reached, so SSE/streaming handlers are not stalled.
func TestCompressFlushStreams(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("event: ping\n\n")) // well under minSize
		f, ok := w.(http.Flusher)
		if !ok {
			t.Error("compress writer does not implement http.Flusher")
			return
		}
		f.Flush()
	})

	compressed := Compress(1024)(handler)
	req := httptest.NewRequest("GET", "/sse", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	compressed.ServeHTTP(rec, req)

	if !rec.Flushed {
		t.Fatal("underlying writer was never flushed")
	}
	if rec.Body.Len() == 0 {
		t.Fatal("Flush did not push buffered bytes to the client")
	}
}

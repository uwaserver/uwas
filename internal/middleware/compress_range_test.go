package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCompressSkipsRangeRequests: 206 responses carry a Content-Range that
// describes uncompressed bytes; compressing the partial body corrupts the
// download, so Range requests must bypass the compressor entirely.
func TestCompressSkipsRangeRequests(t *testing.T) {
	body := strings.Repeat("a", 8192)
	h := Compress(1024)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css")
		w.Header().Set("Content-Range", "bytes 0-8191/100000")
		w.WriteHeader(http.StatusPartialContent)
		w.Write([]byte(body))
	}))

	req := httptest.NewRequest("GET", "/big.css", nil)
	req.Header.Set("Accept-Encoding", "br, gzip")
	req.Header.Set("Range", "bytes=0-8191")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", rec.Code)
	}
	if enc := rec.Header().Get("Content-Encoding"); enc != "" {
		t.Fatalf("206 response must not be compressed, got Content-Encoding=%q", enc)
	}
	if rec.Body.String() != body {
		t.Fatal("partial body was altered")
	}
}

// TestCompressRefuses206WithoutRangeHeader: defense in depth — even if a
// handler emits 206/Content-Range without the request Range header, the
// writer must flush uncompressed.
func TestCompressRefuses206WithoutRangeHeader(t *testing.T) {
	body := strings.Repeat("b", 8192)
	h := Compress(1024)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css")
		w.Header().Set("Content-Range", "bytes 0-8191/100000")
		w.WriteHeader(http.StatusPartialContent)
		w.Write([]byte(body))
	}))

	req := httptest.NewRequest("GET", "/big.css", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if enc := rec.Header().Get("Content-Encoding"); enc != "" {
		t.Fatalf("206 response must not be compressed, got Content-Encoding=%q", enc)
	}
	if rec.Body.String() != body {
		t.Fatal("partial body was altered")
	}
}

package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestCompressRedirect302 simulates exactly what happens when WordPress
// does wp_redirect() after a POST: handler sends 302 + Location + empty body.
// The compression middleware must NOT swallow the 302 status code.
func TestCompressRedirect302(t *testing.T) {
	// Handler that simulates PHP wp_redirect(): 302 + Location, no body
	phpRedirect := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://example.com/wp-admin/options-permalink.php?settings-saved=true")
		w.Header().Set("Content-Type", "text/html; charset=UTF-8")
		w.WriteHeader(302)
		// No body — WordPress redirect sends empty body
	})

	// Wrap with compression middleware (same as production)
	compressed := Compress(1024)(phpRedirect)

	// Simulate browser POST with Accept-Encoding (brotli + gzip)
	req := httptest.NewRequest("POST", "/wp-admin/options-permalink.php", nil)
	req.Header.Set("Accept-Encoding", "gzip, deflate, br, zstd")
	rec := httptest.NewRecorder()

	compressed.ServeHTTP(rec, req)

	// MUST be 302, NOT 200
	if rec.Code != 302 {
		t.Fatalf("CRITICAL: status = %d, want 302. Compression middleware is swallowing redirects!", rec.Code)
	}

	// Location header must be present
	loc := rec.Header().Get("Location")
	if loc == "" {
		t.Fatal("CRITICAL: Location header missing from redirect response")
	}
	if loc != "https://example.com/wp-admin/options-permalink.php?settings-saved=true" {
		t.Errorf("Location = %q, want the redirect URL", loc)
	}

	// Body should be empty (no compression trailer bytes)
	if rec.Body.Len() > 0 {
		t.Errorf("Body should be empty for redirect, got %d bytes: %x", rec.Body.Len(), rec.Body.Bytes())
	}

	// Content-Encoding should NOT be set for redirects
	if ce := rec.Header().Get("Content-Encoding"); ce != "" {
		t.Errorf("Content-Encoding should not be set for redirect, got %q", ce)
	}

	t.Logf("OK: 302 redirect with Location=%q, body=%d bytes", loc, rec.Body.Len())
}

// TestCompressRedirect301 tests permanent redirect
func TestCompressRedirect301(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://example.com/new-url")
		w.WriteHeader(301)
	})

	compressed := Compress(1024)(handler)
	req := httptest.NewRequest("GET", "/old-url", nil)
	req.Header.Set("Accept-Encoding", "br, gzip")
	rec := httptest.NewRecorder()

	compressed.ServeHTTP(rec, req)

	if rec.Code != 301 {
		t.Fatalf("status = %d, want 301", rec.Code)
	}
}

// TestCompressNormalHTML tests that normal HTML is still compressed
func TestCompressNormalHTMLStillWorks(t *testing.T) {
	bigHTML := make([]byte, 2048)
	for i := range bigHTML {
		bigHTML[i] = 'A' // compressible content
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=UTF-8")
		w.WriteHeader(200)
		w.Write(bigHTML)
	})

	compressed := Compress(1024)(handler)
	req := httptest.NewRequest("GET", "/page", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()

	compressed.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	ce := rec.Header().Get("Content-Encoding")
	if ce != "gzip" {
		t.Errorf("Content-Encoding = %q, want gzip (compression should still work for normal HTML)", ce)
	}

	// Compressed body should be smaller than original
	if rec.Body.Len() >= len(bigHTML) {
		t.Errorf("compressed body (%d) should be smaller than original (%d)", rec.Body.Len(), len(bigHTML))
	}

	t.Logf("OK: 200 HTML compressed from %d to %d bytes", len(bigHTML), rec.Body.Len())
}

// TestCompress204NoContent tests no-content response
func TestCompress204NoContent(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	})

	compressed := Compress(1024)(handler)
	req := httptest.NewRequest("DELETE", "/resource", nil)
	req.Header.Set("Accept-Encoding", "br, gzip")
	rec := httptest.NewRecorder()

	compressed.ServeHTTP(rec, req)

	if rec.Code != 204 {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if rec.Body.Len() > 0 {
		t.Errorf("body should be empty for 204, got %d bytes", rec.Body.Len())
	}
}

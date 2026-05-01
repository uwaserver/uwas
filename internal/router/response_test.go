package router

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResponseWriterStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	w := NewResponseWriter(rec)

	w.WriteHeader(404)
	if w.StatusCode() != 404 {
		t.Errorf("StatusCode() = %d, want 404", w.StatusCode())
	}
}

func TestResponseWriterDefaultStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	w := NewResponseWriter(rec)

	w.Write([]byte("hello"))
	if w.StatusCode() != 200 {
		t.Errorf("StatusCode() = %d, want 200 (implicit)", w.StatusCode())
	}
}

func TestResponseWriterBytesWritten(t *testing.T) {
	rec := httptest.NewRecorder()
	w := NewResponseWriter(rec)

	w.Write([]byte("hello"))
	w.Write([]byte(" world"))

	if w.BytesWritten() != 11 {
		t.Errorf("BytesWritten() = %d, want 11", w.BytesWritten())
	}
}

func TestResponseWriterTTFB(t *testing.T) {
	rec := httptest.NewRecorder()
	w := NewResponseWriter(rec)

	w.Write([]byte("data"))
	if w.TTFB() < 0 {
		t.Error("TTFB should be >= 0 after Write")
	}
	if w.StatusCode() == 0 {
		t.Error("StatusCode should be set after Write")
	}
}

func TestResponseWriterDoubleWriteHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	w := NewResponseWriter(rec)

	w.WriteHeader(200)
	w.WriteHeader(500) // should be ignored

	if w.StatusCode() != 200 {
		t.Errorf("StatusCode() = %d, should be 200 (first call wins)", w.StatusCode())
	}
}

// --- Tests for AcquireContext, ReleaseContext, generateID, Error, Flush, Hijack, Unwrap ---

func TestAcquireAndReleaseContext(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/hello?foo=bar", nil)

	ctx := AcquireContext(rec, req)
	if ctx == nil {
		t.Fatal("AcquireContext returned nil")
	}
	if ctx.ID() == "" {
		t.Error("AcquireContext should set a non-empty ID")
	}
	if ctx.Request != req {
		t.Error("AcquireContext should set Request")
	}
	if ctx.Response == nil {
		t.Error("AcquireContext should set Response")
	}
	if ctx.OriginalURI != "/hello?foo=bar" {
		t.Errorf("OriginalURI = %q, want /hello?foo=bar", ctx.OriginalURI)
	}
	if ctx.StartTime.IsZero() {
		t.Error("StartTime should be set")
	}
	// All reset fields should be zero values
	if ctx.RewrittenURI != "" || ctx.DocumentRoot != "" || ctx.ResolvedPath != "" ||
		ctx.ScriptName != "" || ctx.PathInfo != "" || ctx.CacheStatus != "" ||
		ctx.Upstream != "" || ctx.VHostName != "" || ctx.RemoteIP != "" ||
		ctx.RemotePort != "" || ctx.ServerPort != "" {
		t.Error("AcquireContext should reset all string fields to empty")
	}
	if ctx.BytesSent != 0 || ctx.Duration != 0 || ctx.TTFBDur != 0 {
		t.Error("AcquireContext should reset numeric fields to zero")
	}
	if ctx.IsHTTPS {
		t.Error("AcquireContext should reset IsHTTPS to false")
	}

	// Release and verify cleanup
	ReleaseContext(ctx)
	if ctx.Request != nil {
		t.Error("ReleaseContext should nil out Request")
	}
	if ctx.Response != nil {
		t.Error("ReleaseContext should nil out Response")
	}
}

func TestAcquireContextPoolReuse(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/a", nil)

	ctx1 := AcquireContext(rec, req)
	ctx1.VHostName = "dirty"
	ctx1.BytesSent = 999
	ReleaseContext(ctx1)

	ctx2 := AcquireContext(rec, req)
	// After re-acquire, fields should be reset
	if ctx2.VHostName != "" {
		t.Errorf("VHostName should be reset, got %q", ctx2.VHostName)
	}
	if ctx2.BytesSent != 0 {
		t.Errorf("BytesSent should be reset, got %d", ctx2.BytesSent)
	}
	ReleaseContext(ctx2)
}

func TestGenerateID(t *testing.T) {
	id1 := generateID()
	id2 := generateID()

	// Basic format check: UUID-like with dashes
	if len(id1) == 0 {
		t.Fatal("generateID returned empty string")
	}
	// Should be unique
	if id1 == id2 {
		t.Errorf("generateID returned duplicate IDs: %q", id1)
	}
	// Check it contains dashes (UUID format)
	parts := 0
	for _, c := range id1 {
		if c == '-' {
			parts++
		}
	}
	if parts != 4 {
		t.Errorf("generateID format has %d dashes, want 4 (UUID format): %q", parts, id1)
	}
}

func TestResponseWriterError(t *testing.T) {
	rec := httptest.NewRecorder()
	w := NewResponseWriter(rec)

	w.Error(500, "Internal Server Error")

	if w.StatusCode() != 500 {
		t.Errorf("StatusCode() = %d, want 500", w.StatusCode())
	}
	if rec.Body.Len() == 0 {
		t.Error("Error should write a body")
	}
}

func TestResponseWriterFlush(t *testing.T) {
	rec := httptest.NewRecorder()
	w := NewResponseWriter(rec)

	// httptest.ResponseRecorder implements http.Flusher, so Flush should not panic
	w.Flush()
	if !rec.Flushed {
		t.Error("Flush should call underlying Flusher.Flush()")
	}
}

// nonHijackWriter is a minimal ResponseWriter that does NOT support Hijack.
type nonHijackWriter struct {
	http.ResponseWriter
}

func TestResponseWriterHijackNotSupported(t *testing.T) {
	w := NewResponseWriter(&nonHijackWriter{})

	conn, buf, err := w.Hijack()
	if err == nil {
		t.Fatal("Hijack should return error for non-Hijacker")
	}
	if conn != nil || buf != nil {
		t.Error("Hijack should return nil conn and buf when not supported")
	}
	if err.Error() != "hijack not supported" {
		t.Errorf("Hijack error = %q, want %q", err.Error(), "hijack not supported")
	}
}

// fakeHijackWriter implements http.ResponseWriter and http.Hijacker.
type fakeHijackWriter struct {
	headerMap http.Header
}

func (w *fakeHijackWriter) Header() http.Header         { return w.headerMap }
func (w *fakeHijackWriter) Write(b []byte) (int, error) { return len(b), nil }
func (w *fakeHijackWriter) WriteHeader(int)             {}
func (w *fakeHijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, nil // simulate successful hijack
}

func TestResponseWriterHijackSupported(t *testing.T) {
	hw := &fakeHijackWriter{headerMap: make(http.Header)}
	w := NewResponseWriter(hw)

	_, _, err := w.Hijack()
	if err != nil {
		t.Fatalf("Hijack should succeed for Hijacker: %v", err)
	}
}

func TestResponseWriterUnwrap(t *testing.T) {
	rec := httptest.NewRecorder()
	w := NewResponseWriter(rec)

	inner := w.Unwrap()
	if inner != rec {
		t.Error("Unwrap should return the original underlying ResponseWriter")
	}
}

// nonFlusherWriter is a minimal ResponseWriter that does NOT support Flusher.
type nonFlusherWriter struct {
	headerMap http.Header
}

func (w *nonFlusherWriter) Header() http.Header         { return w.headerMap }
func (w *nonFlusherWriter) Write(b []byte) (int, error) { return len(b), nil }
func (w *nonFlusherWriter) WriteHeader(int)             {}

func TestResponseWriterFlushNoFlusher(t *testing.T) {
	nf := &nonFlusherWriter{headerMap: make(http.Header)}
	w := NewResponseWriter(nf)

	// Should not panic even though underlying writer doesn't support Flush
	w.Flush()
}

// === Additional coverage tests ===

func TestGenerateIDUniquenessOverManyCalls(t *testing.T) {
	const n = 10000
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		id := generateID()
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate ID at iteration %d: %q", i, id)
		}
		seen[id] = struct{}{}
	}
	if len(seen) != n {
		t.Errorf("expected %d unique IDs, got %d", n, len(seen))
	}
}

func TestGenerateIDFormat(t *testing.T) {
	for i := 0; i < 100; i++ {
		id := generateID()
		// Should be 36 chars: 8-4-4-4-12
		if len(id) != 36 {
			t.Errorf("generateID length = %d, want 36: %q", len(id), id)
			break
		}
		// Check version nibble (byte 14 should be '7')
		if id[14] != '7' {
			t.Errorf("version nibble = %c, want 7 in %q", id[14], id)
			break
		}
		// Check variant nibble (byte 19 should be 8, 9, a, or b)
		variant := id[19]
		if variant != '8' && variant != '9' && variant != 'a' && variant != 'b' {
			t.Errorf("variant nibble = %c, want 8/9/a/b in %q", variant, id)
			break
		}
	}
}

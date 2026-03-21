package router

import (
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
	if !w.Written() {
		t.Error("Written() should be true after Write")
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

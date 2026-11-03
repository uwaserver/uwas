package server

import (
	"net/http"
	"testing"

	"github.com/uwaserver/uwas/internal/router"
)

type captureFlushWriter struct {
	header  http.Header
	status  int
	flushes int
}

func (w *captureFlushWriter) Header() http.Header         { return w.header }
func (w *captureFlushWriter) WriteHeader(code int)        { w.status = code }
func (w *captureFlushWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w *captureFlushWriter) Flush()                      { w.flushes++ }

func TestResponseCapturePreservesFlush(t *testing.T) {
	underlying := &captureFlushWriter{header: make(http.Header)}
	capture := newResponseCapture(underlying)
	w := router.NewResponseWriter(capture)
	defer router.ReleaseResponseWriter(w)

	w.Write([]byte("event: update\n\n"))
	w.Flush()

	if underlying.flushes != 1 {
		t.Fatalf("underlying flushes = %d, want 1", underlying.flushes)
	}
	if capture.statusCode != http.StatusOK || capture.body.String() != "event: update\n\n" {
		t.Fatalf("capture status/body = %d/%q", capture.statusCode, capture.body.String())
	}
}

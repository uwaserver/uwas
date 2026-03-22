package router

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"time"
)

type ResponseWriter struct {
	http.ResponseWriter
	statusCode    int
	bytesWritten  int64
	headerWritten bool
	startTime     time.Time
	ttfb          time.Duration
}

func NewResponseWriter(w http.ResponseWriter) *ResponseWriter {
	return &ResponseWriter{
		ResponseWriter: w,
		statusCode:     http.StatusOK,
		startTime:      time.Now(),
	}
}

func (w *ResponseWriter) WriteHeader(code int) {
	if w.headerWritten {
		return
	}
	w.statusCode = code
	w.headerWritten = true
	w.ttfb = time.Since(w.startTime)
	w.ResponseWriter.WriteHeader(code)
}

func (w *ResponseWriter) Write(b []byte) (int, error) {
	if !w.headerWritten {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytesWritten += int64(n)
	return n, err
}

func (w *ResponseWriter) StatusCode() int    { return w.statusCode }
func (w *ResponseWriter) BytesWritten() int64 { return w.bytesWritten }
func (w *ResponseWriter) TTFB() time.Duration { return w.ttfb }

// Hijack support for WebSocket proxy.
func (w *ResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := w.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, fmt.Errorf("hijack not supported")
}

// Flush support for streaming responses.
func (w *ResponseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap returns the underlying ResponseWriter (for http.ResponseController).
func (w *ResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// Error writes an error response.
func (w *ResponseWriter) Error(code int, msg string) {
	http.Error(w, msg, code)
}

package server

import (
	"bytes"
	"net/http"
)

// responseCapture wraps an http.ResponseWriter to record the status code,
// headers, and body so the response can be stored in the cache after the
// handler returns.
type responseCapture struct {
	http.ResponseWriter
	statusCode int
	headers    http.Header
	body       bytes.Buffer
	written    bool
}

func newResponseCapture(w http.ResponseWriter) *responseCapture {
	return &responseCapture{
		ResponseWriter: w,
		statusCode:     http.StatusOK,
		headers:        make(http.Header),
	}
}

func (rc *responseCapture) WriteHeader(code int) {
	if rc.written {
		return
	}
	rc.statusCode = code
	rc.written = true
	rc.ResponseWriter.WriteHeader(code)
}

func (rc *responseCapture) Write(b []byte) (int, error) {
	if !rc.written {
		rc.WriteHeader(http.StatusOK)
	}
	rc.body.Write(b)
	return rc.ResponseWriter.Write(b)
}

func (rc *responseCapture) Header() http.Header {
	return rc.ResponseWriter.Header()
}

// capturedHeaders snapshots the current response headers. Call after the
// handler has finished writing so all headers are present.
func (rc *responseCapture) capturedHeaders() http.Header {
	h := make(http.Header)
	for k, v := range rc.ResponseWriter.Header() {
		h[k] = v
	}
	return h
}

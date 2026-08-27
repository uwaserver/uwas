package server

import (
	"bytes"
	"net/http"
)

// maxCacheableBody is the maximum response body size (10 MB) that will be
// captured for caching. Responses exceeding this limit are still sent to
// the client but are not stored in the cache.
const maxCacheableBody = 10 * 1024 * 1024

// responseCapture wraps an http.ResponseWriter to record the status code,
// headers, and body so the response can be stored in the cache after the
// handler returns.
type responseCapture struct {
	http.ResponseWriter
	statusCode int
	headers    http.Header
	body       bytes.Buffer
	written    bool
	overflow   bool // true if body exceeded maxCacheableBody

	// handlerEncoding records the Content-Encoding the HANDLER set, sampled
	// before the first write reaches the compress middleware below us.
	//
	// Two different writers can put Content-Encoding on the shared header map:
	// the handler itself, when it serves an already-encoded body (the static
	// handler's .br/.gz sibling, a reverse-proxy upstream), and the compress
	// middleware, when it encodes a plaintext body on the way out. After the
	// handler returns, the header alone can no longer tell them apart — yet the
	// distinction decides whether the captured bytes are encoded or not.
	//
	// Sampling at the first Write settles it: the compress middleware only sets
	// the header from inside the delegated write, so whatever is present when
	// we are called came from the handler.
	handlerEncoding string
	sampled         bool
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
	if !rc.sampled {
		rc.sampled = true
		rc.handlerEncoding = rc.ResponseWriter.Header().Get("Content-Encoding")
	}
	if !rc.overflow {
		if rc.body.Len()+len(b) > maxCacheableBody {
			rc.overflow = true
			rc.body.Reset()
		} else {
			rc.body.Write(b)
		}
	}
	return rc.ResponseWriter.Write(b)
}

func (rc *responseCapture) Header() http.Header {
	return rc.ResponseWriter.Header()
}

// bodyIsEncoded reports whether the captured bytes are already content-coded,
// i.e. the handler declared an encoding for the body it wrote.
func (rc *responseCapture) bodyIsEncoded() bool {
	return rc.handlerEncoding != ""
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

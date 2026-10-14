package server

import (
	"bytes"
	"net/http"
	"strconv"
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
	// upstreamEncoded is captured before the outer compression middleware can
	// add its own Content-Encoding. Cache entries contain canonical plaintext;
	// storing an upstream-encoded body as though it were plaintext makes a
	// later cache hit compress those bytes again.
	upstreamEncoded bool
	// cacheControl, when set, replaces whatever Cache-Control the handler
	// wrote. A cache rule's cache_control was set on the response before the
	// handler ran, and a proxy copies the upstream's headers over it, so for
	// proxied domains the rule never reached the client. Error responses keep
	// the handler's header, as stampCacheControl does.
	cacheControl string
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
	if code >= 100 && code < 200 && code != http.StatusSwitchingProtocols {
		rc.ResponseWriter.WriteHeader(code)
		return
	}
	rc.statusCode = code
	rc.written = true
	if rc.cacheControl != "" && code < 400 {
		rc.Header().Set("Cache-Control", rc.cacheControl)
	}
	rc.upstreamEncoded = rc.Header().Get("Content-Encoding") != ""
	// A body that declares itself larger than the cache takes is never stored,
	// so it is not copied for it either. Without this every request for a large
	// download buffered up to the limit, and grew and dropped that buffer, before
	// finding out (F14705).
	if n, err := strconv.ParseInt(rc.Header().Get("Content-Length"), 10, 64); err == nil && n > maxCacheableBody {
		rc.overflow = true
	}
	rc.ResponseWriter.WriteHeader(code)
}

func (rc *responseCapture) Write(b []byte) (int, error) {
	if !rc.written {
		rc.WriteHeader(http.StatusOK)
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

// capturedHeaders snapshots the current response headers. Call after the
// handler has finished writing so all headers are present.
func (rc *responseCapture) capturedHeaders() http.Header {
	return rc.ResponseWriter.Header().Clone()
}

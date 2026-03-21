package middleware

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"sync"
)

var gzipPool = sync.Pool{
	New: func() any {
		w, _ := gzip.NewWriterLevel(io.Discard, gzip.DefaultCompression)
		return w
	},
}

// compressible content types (prefix match)
var compressibleTypes = []string{
	"text/",
	"application/json",
	"application/javascript",
	"application/xml",
	"application/xhtml+xml",
	"application/rss+xml",
	"application/atom+xml",
	"application/manifest+json",
	"image/svg+xml",
}

// Gzip returns middleware that compresses responses with gzip.
func Gzip(minSize int) Middleware {
	if minSize <= 0 {
		minSize = 1024
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip compression for conditional requests (let ServeContent handle 304)
			if r.Header.Get("If-None-Match") != "" || r.Header.Get("If-Modified-Since") != "" {
				next.ServeHTTP(w, r)
				return
			}

			// Check Accept-Encoding
			if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
				next.ServeHTTP(w, r)
				return
			}

			gw := &gzipResponseWriter{
				ResponseWriter: w,
				minSize:        minSize,
			}
			defer gw.Close()

			w.Header().Set("Vary", "Accept-Encoding")
			next.ServeHTTP(gw, r)
		})
	}
}

type gzipResponseWriter struct {
	http.ResponseWriter
	gzWriter    *gzip.Writer
	minSize     int
	buf         []byte
	wroteHeader bool
	statusCode  int
	compressed  bool
}

func (w *gzipResponseWriter) WriteHeader(code int) {
	w.statusCode = code
	// Defer actual WriteHeader until we know if we're compressing
	w.wroteHeader = true
}

func (w *gzipResponseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.statusCode = http.StatusOK
		w.wroteHeader = true
	}

	// Buffer until we have enough to decide
	if w.gzWriter == nil && !w.compressed {
		w.buf = append(w.buf, b...)

		if len(w.buf) < w.minSize {
			return len(b), nil
		}

		// Decide: compress or not
		ct := w.Header().Get("Content-Type")
		if ct == "" {
			ct = http.DetectContentType(w.buf)
		}

		if isCompressible(ct) {
			return w.startCompression()
		}

		// Not compressible, flush buffer
		return w.flushUncompressed()
	}

	if w.gzWriter != nil {
		return w.gzWriter.Write(b)
	}

	return w.ResponseWriter.Write(b)
}

func (w *gzipResponseWriter) startCompression() (int, error) {
	w.compressed = true
	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Del("Content-Length") // unknown after compression
	w.ResponseWriter.WriteHeader(w.statusCode)

	gz := gzipPool.Get().(*gzip.Writer)
	gz.Reset(w.ResponseWriter)
	w.gzWriter = gz

	n, err := gz.Write(w.buf)
	w.buf = nil
	return n, err
}

func (w *gzipResponseWriter) flushUncompressed() (int, error) {
	w.ResponseWriter.WriteHeader(w.statusCode)
	n, err := w.ResponseWriter.Write(w.buf)
	w.buf = nil
	w.compressed = true // prevent re-evaluation
	return n, err
}

func (w *gzipResponseWriter) Close() {
	// Flush remaining buffer
	if len(w.buf) > 0 {
		ct := w.Header().Get("Content-Type")
		if ct == "" {
			ct = http.DetectContentType(w.buf)
		}
		if isCompressible(ct) && len(w.buf) >= w.minSize {
			w.startCompression()
		} else {
			w.flushUncompressed()
		}
	}

	if w.gzWriter != nil {
		w.gzWriter.Close()
		gzipPool.Put(w.gzWriter)
	}

	// If nothing was written at all
	if !w.wroteHeader {
		w.ResponseWriter.WriteHeader(http.StatusOK)
	}
}

func isCompressible(ct string) bool {
	ct = strings.ToLower(ct)
	for _, prefix := range compressibleTypes {
		if strings.HasPrefix(ct, prefix) {
			return true
		}
	}
	return false
}

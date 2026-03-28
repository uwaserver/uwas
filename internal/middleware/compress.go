package middleware

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/andybalholm/brotli"
)

// gzipPool reuses gzip.Writer instances. This is safe because gzip.Writer.Reset
// reinitializes all internal state (including the compressor, hasher, and
// buffered data) before any new writes, so a previously-used writer returned
// to the pool cannot leak data from an earlier response.
var gzipPool = sync.Pool{
	New: func() any {
		w, _ := gzip.NewWriterLevel(io.Discard, gzip.DefaultCompression)
		return w
	},
}

// brotliPool reuses brotli.Writer instances. Like gzip.Writer, brotli.Writer.Reset
// reinitializes internal state before new writes, making pooling safe.
var brotliPool = sync.Pool{
	New: func() any {
		return brotli.NewWriterLevel(io.Discard, brotli.DefaultCompression)
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

// encodingType represents the compression encoding to use.
type encodingType int

const (
	encodingNone   encodingType = iota
	encodingBrotli              // br
	encodingGzip                // gzip
)

// selectEncoding inspects the Accept-Encoding header and returns the best
// supported encoding. Brotli is preferred over gzip when both are present.
func selectEncoding(acceptEncoding string) encodingType {
	hasBr := strings.Contains(acceptEncoding, "br")
	hasGzip := strings.Contains(acceptEncoding, "gzip")

	if hasBr {
		return encodingBrotli
	}
	if hasGzip {
		return encodingGzip
	}
	return encodingNone
}

// Compress returns middleware that compresses responses with brotli or gzip.
// Brotli is preferred when the client supports it; gzip is used as fallback.
func Compress(minSize int) Middleware {
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

			enc := selectEncoding(r.Header.Get("Accept-Encoding"))
			if enc == encodingNone {
				next.ServeHTTP(w, r)
				return
			}

			cw := &compressResponseWriter{
				ResponseWriter: w,
				minSize:        minSize,
				encoding:       enc,
			}
			defer cw.Close()

			w.Header().Add("Vary", "Accept-Encoding")
			next.ServeHTTP(cw, r)
		})
	}
}

// Gzip returns middleware that compresses responses with brotli (preferred) or gzip.
// It is kept for backward compatibility; it delegates to Compress internally.
func Gzip(minSize int) Middleware {
	return Compress(minSize)
}

// compressResponseWriter buffers output until enough data is available to decide
// whether compression is worthwhile, then delegates to brotli or gzip.
type compressResponseWriter struct {
	http.ResponseWriter
	writer      io.WriteCloser // brotli or gzip writer (nil until compression starts)
	encoding    encodingType
	minSize     int
	buf         []byte
	wroteHeader bool
	statusCode  int
	compressed  bool
}

func (w *compressResponseWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.statusCode = code
	w.wroteHeader = true

	// Redirect, No Content, Not Modified: no body expected.
	// Flush status immediately — don't buffer, don't compress.
	if code == http.StatusNoContent || code == http.StatusNotModified ||
		(code >= 300 && code < 400) || code < 200 {
		w.compressed = true // prevent future compression attempts
		w.ResponseWriter.WriteHeader(code)
	}
	// For 200 etc., defer WriteHeader until Write decides on compression
}

func (w *compressResponseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.statusCode = http.StatusOK
		w.wroteHeader = true
	}

	// Buffer until we have enough to decide
	if w.writer == nil && !w.compressed {
		w.buf = append(w.buf, b...)

		if len(w.buf) < w.minSize {
			return len(b), nil
		}

		// Decide: compress or not
		ct := w.Header().Get("Content-Type")
		if ct == "" {
			ct = http.DetectContentType(w.buf)
		}

		// Skip if already compressed by upstream (PHP ob_gzip_handler, etc.)
		if ce := w.Header().Get("Content-Encoding"); ce != "" {
			return w.flushUncompressed()
		}

		if isCompressible(ct) {
			return w.startCompression()
		}

		// Not compressible, flush buffer
		return w.flushUncompressed()
	}

	if w.writer != nil {
		return w.writer.Write(b)
	}

	return w.ResponseWriter.Write(b)
}

func (w *compressResponseWriter) startCompression() (int, error) {
	w.compressed = true

	switch w.encoding {
	case encodingBrotli:
		w.Header().Set("Content-Encoding", "br")
		w.Header().Del("Content-Length")
		w.ResponseWriter.WriteHeader(w.statusCode)

		br := brotliPool.Get().(*brotli.Writer)
		br.Reset(w.ResponseWriter)
		w.writer = br
	default:
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Del("Content-Length")
		w.ResponseWriter.WriteHeader(w.statusCode)

		gz := gzipPool.Get().(*gzip.Writer)
		gz.Reset(w.ResponseWriter)
		w.writer = gz
	}

	n, err := w.writer.Write(w.buf)
	w.buf = nil
	return n, err
}

func (w *compressResponseWriter) flushUncompressed() (int, error) {
	w.ResponseWriter.WriteHeader(w.statusCode)
	n, err := w.ResponseWriter.Write(w.buf)
	w.buf = nil
	w.compressed = true // prevent re-evaluation
	return n, err
}

func (w *compressResponseWriter) Close() {
	// Flush remaining buffer
	if len(w.buf) > 0 {
		ct := w.Header().Get("Content-Type")
		if ct == "" {
			ct = http.DetectContentType(w.buf)
		}
		if isCompressible(ct) && len(w.buf) >= w.minSize && w.statusCode == http.StatusOK {
			w.startCompression()
		} else {
			w.flushUncompressed()
		}
	}

	if w.writer != nil {
		w.writer.Close()
		// Return the writer to the appropriate pool
		switch w.encoding {
		case encodingBrotli:
			brotliPool.Put(w.writer)
		default:
			gzipPool.Put(w.writer)
		}
	}

	// If WriteHeader was called but the real ResponseWriter never got it
	// (no Write happened, e.g. empty redirect), flush the status now.
	if w.wroteHeader && w.writer == nil && len(w.buf) == 0 && !w.compressed {
		w.ResponseWriter.WriteHeader(w.statusCode)
	} else if !w.wroteHeader {
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

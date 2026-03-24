package middleware

import (
	"net"
	"net/http"
	"strings"

	"github.com/uwaserver/uwas/internal/config"
)

// HeaderTransform applies request and response header transformations
// based on per-domain configuration. Supports variable substitution
// in header values using $remote_addr, $host, $uri, and $request_id.
func HeaderTransform(cfg config.HeadersConfig) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// --- Request header transforms ---
			for k, v := range cfg.RequestAdd {
				r.Header.Set(k, substituteVars(v, r))
			}
			for _, k := range cfg.RequestRemove {
				r.Header.Del(k)
			}

			// --- Response header transforms (set before calling next) ---
			// We need to wrap the writer to apply response transforms
			// after the handler writes headers.
			tw := &transformWriter{
				ResponseWriter: w,
				cfg:            cfg,
				req:            r,
			}
			next.ServeHTTP(tw, r)
		})
	}
}

// transformWriter wraps http.ResponseWriter to apply response header
// transforms when WriteHeader is called.
type transformWriter struct {
	http.ResponseWriter
	cfg           config.HeadersConfig
	req           *http.Request
	headerWritten bool
}

func (tw *transformWriter) WriteHeader(code int) {
	if tw.headerWritten {
		tw.ResponseWriter.WriteHeader(code)
		return
	}
	tw.headerWritten = true
	tw.applyResponseTransforms()
	tw.ResponseWriter.WriteHeader(code)
}

func (tw *transformWriter) Write(b []byte) (int, error) {
	if !tw.headerWritten {
		tw.headerWritten = true
		tw.applyResponseTransforms()
	}
	return tw.ResponseWriter.Write(b)
}

func (tw *transformWriter) applyResponseTransforms() {
	h := tw.ResponseWriter.Header()
	for k, v := range tw.cfg.ResponseAdd {
		h.Set(k, substituteVars(v, tw.req))
	}
	for _, k := range tw.cfg.ResponseRemove {
		h.Del(k)
	}
}

// Unwrap returns the underlying ResponseWriter.
func (tw *transformWriter) Unwrap() http.ResponseWriter {
	return tw.ResponseWriter
}

// substituteVars replaces supported variables in a header value string.
// Supported variables: $remote_addr, $host, $uri, $request_id.
func substituteVars(value string, r *http.Request) string {
	if !strings.Contains(value, "$") {
		return value
	}

	result := value
	result = strings.ReplaceAll(result, "$remote_addr", extractRemoteAddr(r))
	result = strings.ReplaceAll(result, "$host", r.Host)
	result = strings.ReplaceAll(result, "$uri", r.URL.RequestURI())
	result = strings.ReplaceAll(result, "$request_id", r.Header.Get("X-Request-ID"))

	return result
}

// extractRemoteAddr extracts the IP from the request's RemoteAddr,
// stripping the port if present.
func extractRemoteAddr(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

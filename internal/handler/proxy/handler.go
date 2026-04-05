package proxy

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/router"
)

// Handler handles reverse proxy requests with load balancing.
type Handler struct {
	logger     *logger.Logger
	transports sync.Map // domain host → *http.Transport
}

const (
	maxRetryBodyBytes        int64 = 8 << 20  // 8MB cap for retryable request buffering
	maxBufferedResponseBytes int64 = 16 << 20 // 16MB cap for buffer_response mode
)

func New(log *logger.Logger) *Handler {
	return &Handler{logger: log}
}

// getTransport returns a per-domain transport with configured timeouts.
func (h *Handler) getTransport(domain *config.Domain) *http.Transport {
	if t, ok := h.transports.Load(domain.Host); ok {
		return t.(*http.Transport)
	}

	connectTimeout := 5 * time.Second
	if domain.Proxy.Timeouts.Connect.Duration > 0 {
		connectTimeout = domain.Proxy.Timeouts.Connect.Duration
	}

	headerTimeout := 60 * time.Second
	if domain.Proxy.Timeouts.Read.Duration > 0 {
		headerTimeout = domain.Proxy.Timeouts.Read.Duration
	}

	writeTimeout := 60 * time.Second
	if domain.Proxy.Timeouts.Write.Duration > 0 {
		writeTimeout = domain.Proxy.Timeouts.Write.Duration
	}

	t := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   connectTimeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ResponseHeaderTimeout: headerTimeout,
		WriteBufferSize:       64 * 1024,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: writeTimeout,
	}

	// gRPC/h2c: allow HTTP/2 cleartext to upstream
	if domain.Proxy.GRPC {
		t.ForceAttemptHTTP2 = true
	}

	actual, _ := h.transports.LoadOrStore(domain.Host, t)
	return actual.(*http.Transport)
}

// Serve proxies the request to an upstream backend.
func (h *Handler) Serve(ctx *router.RequestContext, domain *config.Domain, pool *UpstreamPool, balancer Balancer) {
	backends := pool.Healthy()
	if len(backends) == 0 {
		ctx.Response.Error(http.StatusBadGateway, "502 Bad Gateway — no healthy upstreams")
		return
	}

	// WebSocket: tunnel raw TCP instead of HTTP round-trip
	if domain.Proxy.WebSocket && IsWebSocketUpgrade(ctx.Request) {
		backend := balancer.Select(backends, ctx.Request)
		if backend == nil {
			ctx.Response.Error(http.StatusBadGateway, "502 Bad Gateway — no backend selected")
			return
		}
		h.serveWebSocket(ctx, backend)
		return
	}

	maxRetries := domain.Proxy.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 2
	}
	if maxRetries > len(backends) {
		maxRetries = len(backends)
	}

	// Buffer the request body so we can retry
	var bodyBytes []byte
	if ctx.Request.Body != nil {
		// For unknown/large bodies, avoid unbounded memory usage and disable retries.
		if ctx.Request.ContentLength < 0 || ctx.Request.ContentLength > maxRetryBodyBytes {
			maxRetries = 0
		} else {
			limited := io.LimitReader(ctx.Request.Body, maxRetryBodyBytes+1)
			var err error
			bodyBytes, err = io.ReadAll(limited)
			if err != nil {
				ctx.Response.Error(http.StatusBadGateway, "502 Bad Gateway")
				return
			}
			if int64(len(bodyBytes)) > maxRetryBodyBytes {
				ctx.Request.Body.Close()
				ctx.Response.Error(http.StatusRequestEntityTooLarge, "413 Request Entity Too Large")
				return
			}
			ctx.Request.Body.Close()
		}
	}

	tried := make(map[*Backend]bool)

	for attempt := 0; attempt <= maxRetries; attempt++ {
		backend := balancer.Select(backends, ctx.Request)
		if backend == nil {
			ctx.Response.Error(http.StatusBadGateway, "502 Bad Gateway — no backend selected")
			return
		}

		// On retries, try to pick a different backend
		if attempt > 0 {
			var found bool
			for _, b := range backends {
				if !tried[b] {
					backend = b
					found = true
					break
				}
			}
			if !found {
				// All backends tried, give up
				break
			}
			h.logger.Warn("retrying upstream request",
				"attempt", attempt,
				"backend", backend.URL.String(),
				"path", ctx.Request.URL.Path,
			)
		}
		tried[backend] = true

		backend.ActiveConns.Add(1)
		backend.TotalReqs.Add(1)

		ctx.Upstream = backend.URL.String()

		// Build upstream request
		upstreamURL := *backend.URL
		upstreamURL.Path = ctx.Request.URL.Path
		upstreamURL.RawQuery = ctx.Request.URL.RawQuery

		// Per-backend timeout via request context
		readTimeout := 60 * time.Second
		if domain.Proxy.Timeouts.Read.Duration > 0 {
			readTimeout = domain.Proxy.Timeouts.Read.Duration
		}

		reqCtx := ctx.Request.Context()
		reqCtx, cancel := context.WithTimeout(reqCtx, readTimeout)

		var body io.Reader
		if bodyBytes != nil {
			body = bytes.NewReader(bodyBytes)
		} else if attempt == 0 && ctx.Request.Body != nil {
			body = ctx.Request.Body
		}

		proxyReq, err := http.NewRequestWithContext(
			reqCtx,
			ctx.Request.Method,
			upstreamURL.String(),
			body,
		)
		if err != nil {
			cancel()
			backend.ActiveConns.Add(-1)
			backend.TotalFails.Add(1)
			ctx.Response.Error(http.StatusBadGateway, "502 Bad Gateway")
			return
		}

		// Copy headers
		for key, vals := range ctx.Request.Header {
			for _, v := range vals {
				proxyReq.Header.Add(key, v)
			}
		}

		// Add proxy headers
		proxyReq.Header.Set("X-Forwarded-For", clientIP(ctx.Request))
		proxyReq.Header.Set("X-Forwarded-Proto", forwardedProto(ctx))
		proxyReq.Header.Set("X-Forwarded-Host", ctx.Request.Host)
		proxyReq.Header.Set("X-Real-IP", clientIP(ctx.Request))

		// W3C Trace Context: propagate or generate traceparent
		if proxyReq.Header.Get("Traceparent") == "" {
			proxyReq.Header.Set("Traceparent", generateTraceparent())
		}

		// Remove hop-by-hop headers
		removeHopByHop(proxyReq.Header)

		// Execute
		resp, err := h.getTransport(domain).RoundTrip(proxyReq)
		if err != nil {
			cancel()
			backend.ActiveConns.Add(-1)
			backend.TotalFails.Add(1)
			h.logger.Error("upstream error",
				"backend", backend.URL.String(),
				"error", err,
			)

			// Don't retry if the original client request context is done
			if ctx.Request.Context().Err() == context.DeadlineExceeded {
				ctx.Response.Error(http.StatusGatewayTimeout, "504 Gateway Timeout")
				return
			}
			if ctx.Request.Context().Err() != nil {
				ctx.Response.Error(http.StatusBadGateway, "502 Bad Gateway")
				return
			}

			// If there are more retries available, continue to next backend
			if attempt < maxRetries && isRetryableError(err) {
				cancel() // release context from this failed attempt
				continue
			}

			ctx.Response.Error(http.StatusBadGateway, "502 Bad Gateway")
			return
		}

		// NOTE: cancel() must be called AFTER resp.Body is fully read.
		// Calling it before io.Copy truncates large responses because the
		// canceled context closes the underlying connection mid-stream.
		backend.ActiveConns.Add(-1)

		// Copy response headers
		for key, vals := range resp.Header {
			for _, v := range vals {
				ctx.Response.Header().Add(key, v)
			}
		}
		removeHopByHop(ctx.Response.Header())

		// Set sticky session cookie if the balancer is sticky
		if sb, ok := balancer.(*StickyBalancer); ok {
			SetStickyCookie(ctx.Response, sb.CookieName, backend.URL.Host, sb.TTL)
		}

		// Write status + body
		ctx.Response.WriteHeader(resp.StatusCode)
		useBufferedResponse := domain.Proxy.BufferResponse &&
			resp.ContentLength >= 0 &&
			resp.ContentLength <= maxBufferedResponseBytes
		if useBufferedResponse {
			// Buffered mode: read entire upstream response, then write to client.
			// Frees upstream connection faster for slow clients.
			body, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			cancel()
			if readErr != nil {
				h.logger.Error("error reading upstream response body", "backend", backend.URL.String(), "error", readErr)
				// Do not write partial body — upstream connection was broken.
				// Headers already sent; cannot change status code.
				return
			}
			if len(body) > 0 {
				ctx.Response.Write(body)
			}
		} else {
			// Streaming mode (default): pipe upstream → client directly.
			if _, err := io.Copy(ctx.Response, resp.Body); err != nil {
				h.logger.Error("error copying upstream response body",
					"backend", backend.URL.String(),
					"error", err,
				)
			}
			resp.Body.Close()
			cancel() // safe now — body fully consumed
		}
		return
	}

	// All retries exhausted
	ctx.Response.Error(http.StatusBadGateway, "502 Bad Gateway — all backends failed")
}

// isRetryableError checks if the error is a connection-level error worth retrying.
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	// Connection refused, timeout, etc. are retryable
	if _, ok := err.(net.Error); ok {
		return true
	}
	errStr := err.Error()
	return strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "no such host") ||
		strings.Contains(errStr, "connection reset")
}

var hopByHopHeaders = []string{
	"Connection", "Keep-Alive", "Proxy-Authenticate",
	"Proxy-Authorization", "Te", "Trailers",
	"Transfer-Encoding", "Upgrade",
}

func removeHopByHop(h http.Header) {
	for _, key := range hopByHopHeaders {
		h.Del(key)
	}
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func forwardedProto(ctx *router.RequestContext) string {
	if ctx.IsHTTPS {
		return "https"
	}
	return "http"
}

// generateTraceparent creates a W3C Trace Context traceparent header.
// Format: 00-<trace-id>-<span-id>-01
// See https://www.w3.org/TR/trace-context/
func generateTraceparent() string {
	var traceID [16]byte
	var spanID [8]byte
	rand.Read(traceID[:])
	rand.Read(spanID[:])

	// Manual hex encoding: 00- (3) + 32 trace + - (1) + 16 span + -01 (3) = 55 bytes
	const hexChars = "0123456789abcdef"
	var buf [55]byte
	// 00-
	buf[0], buf[1], buf[2] = '0', '0', '-'
	// trace-id (16 bytes → 32 hex chars)
	for i, j := 0, 3; i < 16; i++ {
		buf[j] = hexChars[traceID[i]>>4]
		buf[j+1] = hexChars[traceID[i]&0xF]
		j += 2
	}
	// -
	buf[35] = '-'
	// span-id (8 bytes → 16 hex chars)
	for i, j := 0, 36; i < 8; i++ {
		buf[j] = hexChars[spanID[i]>>4]
		buf[j+1] = hexChars[spanID[i]&0xF]
		j += 2
	}
	// -01
	buf[52], buf[53], buf[54] = '-', '0', '1'
	return string(buf[:])
}

// IsWebSocketUpgrade checks if the request is a WebSocket upgrade.
func IsWebSocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket") &&
		strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade")
}

// serveWebSocket tunnels a WebSocket connection by hijacking the client
// connection and establishing a raw TCP connection to the backend. Both
// directions are piped concurrently until one side closes.
func (h *Handler) serveWebSocket(ctx *router.RequestContext, backend *Backend) {
	// Hijack the client connection (ResponseWriter implements Hijack)
	clientConn, clientBuf, err := ctx.Response.Hijack()
	if err != nil {
		h.logger.Error("websocket hijack failed", "error", err)
		ctx.Response.Error(http.StatusInternalServerError, "WebSocket hijack not supported")
		return
	}
	defer clientConn.Close()

	// Connect to upstream
	backendAddr := backend.URL.Host
	if !strings.Contains(backendAddr, ":") {
		if backend.URL.Scheme == "https" || backend.URL.Scheme == "wss" {
			backendAddr += ":443"
		} else {
			backendAddr += ":80"
		}
	}

	upstreamConn, err := net.DialTimeout("tcp", backendAddr, 5*time.Second)
	if err != nil {
		h.logger.Error("websocket upstream connect failed", "backend", backendAddr, "error", err)
		clientConn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		return
	}
	defer upstreamConn.Close()

	// Forward the original HTTP request (including Upgrade headers) to the backend
	upstreamURL := *backend.URL
	upstreamURL.Path = ctx.Request.URL.Path
	upstreamURL.RawQuery = ctx.Request.URL.RawQuery

	// Write the request line
	reqLine := ctx.Request.Method + " " + upstreamURL.RequestURI() + " HTTP/1.1\r\n"
	upstreamConn.Write([]byte(reqLine))

	// Write headers (including Upgrade and Connection)
	for key, vals := range ctx.Request.Header {
		for _, v := range vals {
			upstreamConn.Write([]byte(key + ": " + v + "\r\n"))
		}
	}
	// Add proxy headers
	upstreamConn.Write([]byte("X-Forwarded-For: " + clientIP(ctx.Request) + "\r\n"))
	upstreamConn.Write([]byte("X-Real-IP: " + clientIP(ctx.Request) + "\r\n"))
	upstreamConn.Write([]byte("Host: " + ctx.Request.Host + "\r\n"))
	upstreamConn.Write([]byte("\r\n"))

	// Bidirectional copy
	done := make(chan struct{}, 2)

	go func() {
		io.Copy(upstreamConn, clientBuf)
		done <- struct{}{}
	}()

	go func() {
		io.Copy(clientConn, upstreamConn)
		done <- struct{}{}
	}()

	// Wait for either direction to finish
	<-done

	h.logger.Debug("websocket connection closed", "backend", backendAddr, "path", ctx.Request.URL.Path)
}

package proxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
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
	transports sync.Map // proxyTransportKey -> http.RoundTripper
}

type proxyTransportKey struct {
	host               string
	connectTimeout     time.Duration
	readTimeout        time.Duration
	writeTimeout       time.Duration
	allowPrivate       bool
	insecureSkipVerify bool
	grpc               bool
}

const maxBufferedResponseBytes int64 = 16 << 20 // 16MB cap for buffer_response mode

func New(log *logger.Logger) *Handler {
	return &Handler{logger: log}
}

// getTransport returns a per-domain transport with configured timeouts.
func (h *Handler) getTransport(domain *config.Domain) http.RoundTripper {
	connectTimeout := 5 * time.Second
	if domain.Proxy.Timeouts.Connect.Duration > 0 {
		connectTimeout = domain.Proxy.Timeouts.Connect.Duration
	}

	// ResponseHeaderTimeout: default 30s, prevents slowloris on upstream connections
	headerTimeout := 30 * time.Second
	if domain.Proxy.Timeouts.Read.Duration > 0 {
		headerTimeout = domain.Proxy.Timeouts.Read.Duration
	}

	writeTimeout := 60 * time.Second
	if domain.Proxy.Timeouts.Write.Duration > 0 {
		writeTimeout = domain.Proxy.Timeouts.Write.Duration
	}
	key := proxyTransportKey{
		host:               domain.Host,
		connectTimeout:     connectTimeout,
		readTimeout:        headerTimeout,
		writeTimeout:       writeTimeout,
		allowPrivate:       domain.Proxy.AllowPrivateUpstreams,
		insecureSkipVerify: domain.Proxy.InsecureSkipVerify,
		grpc:               domain.Proxy.GRPC,
	}
	if t, ok := h.transports.Load(key); ok {
		return t.(http.RoundTripper)
	}

	dialer := &net.Dialer{
		Timeout:   connectTimeout,
		KeepAlive: 30 * time.Second,
		Control:   config.ProxyDialControl(domain.Proxy.AllowPrivateUpstreams),
	}
	t := &http.Transport{
		DialContext:           dialer.DialContext,
		ResponseHeaderTimeout: headerTimeout,
		WriteBufferSize:       64 * 1024,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: writeTimeout,
		// Go disables HTTP/2 when a custom DialContext is set; opt back in
		// explicitly so modern HTTPS origins (Cloudflare, h2-preferred CDNs)
		// negotiate h2 via ALPN. Falls back to HTTP/1.1 transparently when
		// the upstream doesn't advertise h2 — safe to leave on for every
		// HTTPS upstream. gRPC/h2c still relies on the same flag.
		ForceAttemptHTTP2: true,
	}

	// TLS config — only override the default when the operator opts into
	// skipping cert verification (self-signed origin, private CA, hostname
	// mismatch). Leaving TLSClientConfig nil otherwise keeps Go's secure
	// defaults (system roots, verify peer, SNI from URL.Host).
	if domain.Proxy.InsecureSkipVerify {
		t.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402 — opt-in via per-domain config
		// Surface this MITM-exposing opt-in so it can't be enabled silently.
		// Cached per domain, so this logs once per domain, not per request.
		if h.logger != nil {
			h.logger.Warn("proxy upstream TLS verification DISABLED — MITM possible on the UWAS→backend hop",
				"domain", domain.Host)
		}
	}

	var rt http.RoundTripper = t

	// proxy.grpc was dead configuration: nothing read the field. The comment
	// above claims "gRPC/h2c still relies on the same flag", but
	// ForceAttemptHTTP2 only negotiates h2 over TLS via ALPN. A cleartext
	// http:// upstream still gets HTTP/1.1, and gRPC does not run on
	// HTTP/1.1 — so a domain configured for gRPC could not proxy it.
	//
	// h2c has no negotiation: the client must decide to speak HTTP/2 on a
	// plaintext connection. That is what the flag now selects, for cleartext
	// upstreams only; https:// keeps the standard transport, which already
	// reaches h2 through ALPN.
	if domain.Proxy.GRPC {
		// A clone keeps the dialer (with its SSRF Control) and the timeouts.
		// net/http uses unencrypted HTTP/2 for http:// URLs when the protocol
		// set includes UnencryptedHTTP2 and excludes HTTP1, so this transport
		// is h2c-only and is used only for cleartext upstreams.
		h2c := t.Clone()
		var protos http.Protocols
		protos.SetUnencryptedHTTP2(true)
		h2c.Protocols = &protos
		rt = &grpcRoundTripper{std: t, h2c: h2c}
	}

	actual, _ := h.transports.LoadOrStore(key, rt)
	return actual.(http.RoundTripper)
}

// grpcRoundTripper sends cleartext requests over h2c and everything else over
// the standard transport, so a domain with both http:// and https:// upstreams
// keeps working when proxy.grpc is on.
type grpcRoundTripper struct {
	std *http.Transport
	h2c *http.Transport
}

func (g *grpcRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL != nil && r.URL.Scheme == "http" {
		return g.h2c.RoundTrip(r)
	}
	return g.std.RoundTrip(r)
}

func (g *grpcRoundTripper) CloseIdleConnections() {
	g.std.CloseIdleConnections()
	g.h2c.CloseIdleConnections()
}

// ResetTransports closes idle upstream connections and removes cached
// transports after a config reload. Active requests retain their transport;
// subsequent requests rebuild one from the current domain policy.
func (h *Handler) ResetTransports() {
	h.transports.Range(func(key, value any) bool {
		if c, ok := value.(interface{ CloseIdleConnections() }); ok {
			c.CloseIdleConnections()
		}
		h.transports.Delete(key)
		return true
	})
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
		if err := proxyUpstreamSafetyCheck(domain, backend.URL.String()); err != nil {
			h.logger.Warn("websocket proxy SSRF blocked", "upstream", backend.URL.String(), "error", err)
			ctx.Response.Error(http.StatusForbidden, "403 Forbidden — upstream blocked (SSRF protection)")
			return
		}
		h.serveWebSocketWithOptions(ctx, backend, websocketDialOptions{
			insecureSkipVerify: domain.Proxy.InsecureSkipVerify,
			allowPrivate:       domain.Proxy.AllowPrivateUpstreams,
		})
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
			ctx.Request.Body.Close()
			if err != nil {
				ctx.Response.Error(http.StatusBadGateway, "502 Bad Gateway")
				return
			}
			if int64(len(bodyBytes)) > maxRetryBodyBytes {
				ctx.Response.Error(http.StatusRequestEntityTooLarge, "413 Request Entity Too Large")
				return
			}
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
				"request_id", ctx.Request.Header.Get("X-Request-ID"),
			)
		}
		tried[backend] = true

		backend.ActiveConns.Add(1)
		backend.TotalReqs.Add(1)

		ctx.Upstream = backend.URL.String()

		// Build upstream request. RawPath must be copied alongside Path so the
		// original percent-encoding (e.g. %2F, %2e%2e%2f) is preserved when the
		// URL is re-serialized — otherwise the backend sees a decoded/normalized
		// path the front-end guards never inspected.
		upstreamURL := *backend.URL
		upstreamURL.Path = ctx.Request.URL.Path
		upstreamURL.RawPath = ctx.Request.URL.RawPath
		upstreamURL.RawQuery = ctx.Request.URL.RawQuery

		// SSRF protection: local app upstreams are allowed, but metadata and
		// link-local ranges remain blocked even when private upstreams are enabled.
		if err := proxyUpstreamSafetyCheck(domain, upstreamURL.String()); err != nil {
			backend.ActiveConns.Add(-1)
			backend.TotalFails.Add(1)
			h.logger.Warn("proxy SSRF blocked", "upstream", upstreamURL.String(), "error", err)
			ctx.Response.Error(http.StatusForbidden, "403 Forbidden — upstream blocked (SSRF protection)")
			return
		}

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

		// Remove hop-by-hop headers BEFORE adding the proxy headers: the
		// client's Connection header may name X-Forwarded-For / X-Real-IP,
		// and laundering after the Set calls would delete the authoritative
		// values UWAS just wrote (the location proxy in internal/server
		// already uses this order).
		removeHopByHop(proxyReq.Header)
		stripClientForwarded(proxyReq.Header)

		// Add proxy headers
		proxyReq.Header.Set("X-Forwarded-For", clientIP(ctx.Request))
		proxyReq.Header.Set("X-Forwarded-Proto", forwardedProto(ctx))
		proxyReq.Header.Set("X-Forwarded-Host", ctx.Request.Host)
		proxyReq.Header.Set("X-Real-IP", clientIP(ctx.Request))

		// W3C Trace Context: propagate or generate traceparent
		if proxyReq.Header.Get("Traceparent") == "" {
			proxyReq.Header.Set("Traceparent", generateTraceparent())
		}

		// Execute
		resp, err := h.getTransport(domain).RoundTrip(proxyReq)
		if err != nil {
			cancel()
			backend.ActiveConns.Add(-1)
			backend.TotalFails.Add(1)
			h.logger.Error("upstream error",
				"backend", backend.URL.String(),
				"request_id", ctx.Request.Header.Get("X-Request-ID"),
				"error", err,
				"error_class", classifyUpstreamErr(err),
			)

			// Don't retry if the original client request context is done.
			// A deadline — observed on the client context OR surfaced directly
			// by the transport (this attempt's read-timeout) — is a gateway
			// timeout, not a bad gateway.
			if ctx.Request.Context().Err() == context.DeadlineExceeded || isTimeoutErr(err) {
				ctx.Response.Error(http.StatusGatewayTimeout, "504 Gateway Timeout")
				return
			}
			if ctx.Request.Context().Err() != nil {
				ctx.Response.Error(http.StatusBadGateway, "502 Bad Gateway — "+classifyUpstreamErr(err))
				return
			}

			// If there are more retries available, continue to next backend
			if attempt < maxRetries && isRetryableError(err) && canReplayRequest(ctx.Request, err) {
				cancel() // release context from this failed attempt
				continue
			}

			// Final failure: a timed-out upstream is a 504, everything else 502.
			if isTimeoutErr(err) {
				ctx.Response.Error(http.StatusGatewayTimeout, "504 Gateway Timeout")
				return
			}
			ctx.Response.Error(http.StatusBadGateway, "502 Bad Gateway — "+classifyUpstreamErr(err))
			return
		}

		// NOTE: cancel() must be called AFTER resp.Body is fully read.
		// Calling it before io.Copy truncates large responses because the
		// canceled context closes the underlying connection mid-stream.

		// Copy response headers. Assignment replaces UWAS defaults when the
		// upstream owns a header (notably security headers), while preserving
		// multi-value headers such as Set-Cookie.
		for key, vals := range resp.Header {
			ctx.Response.Header()[key] = append([]string(nil), vals...)
		}
		removeHopByHop(ctx.Response.Header())

		// Set sticky session cookie if the balancer is sticky
		if sb, ok := balancer.(*StickyBalancer); ok {
			SetStickyCookie(ctx.Response, sb.CookieName, backend.URL.Host, sb.TTL, ctx.Request.TLS != nil)
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
			backend.ActiveConns.Add(-1)
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
			copyTrailers(ctx.Response.Header(), resp.Trailer)
		} else {
			// Streaming mode (default): pipe upstream → client directly.
			// Unknown-length and event-stream bodies are flushed per write,
			// as httputil.ReverseProxy does, so SSE/long-poll data is not
			// held in the server's write buffer while the upstream is open.
			var dst io.Writer = ctx.Response
			if resp.ContentLength == -1 || isEventStream(resp.Header) {
				dst = flushWriter{ctx.Response}
			}
			if _, err := io.Copy(dst, resp.Body); err != nil {
				h.logger.Error("error copying upstream response body",
					"backend", backend.URL.String(),
					"error", err,
				)
			}
			// Trailers (e.g. gRPC's grpc-status) are only populated once
			// the body has been read to EOF.
			copyTrailers(ctx.Response.Header(), resp.Trailer)
			resp.Body.Close()
			backend.ActiveConns.Add(-1)
			cancel() // safe now — body fully consumed
		}
		return
	}

	// All retries exhausted
	ctx.Response.Error(http.StatusBadGateway, "502 Bad Gateway — all backends failed")
}

func proxyUpstreamSafetyCheck(domain *config.Domain, rawURL string) error {
	if domain.Proxy.AllowPrivateUpstreams {
		return config.IsPrivateProxyUpstreamSafe(rawURL)
	}
	return config.IsProxyUpstreamSafe(rawURL)
}

// isRetryableError checks if the error is a connection-level error worth retrying.
// isTimeoutErr reports whether an upstream error is a deadline/timeout —
// either a context deadline (the per-attempt read-timeout or the client
// request deadline) or a network-level timeout. Such errors map to 504
// Gateway Timeout rather than 502 Bad Gateway.
func isTimeoutErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	return false
}

// canReplayRequest reports whether a failed attempt may be sent to another
// backend. A dial failure means the request never left UWAS; otherwise the
// upstream may already have acted on it, so only idempotent methods (RFC 9110
// §9.2.2) or requests carrying an idempotency key are replayed.
func canReplayRequest(r *http.Request, err error) bool {
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "dial" {
		return true
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace,
		http.MethodPut, http.MethodDelete:
		return true
	}
	return r.Header.Get("Idempotency-Key") != "" || r.Header.Get("X-Idempotency-Key") != ""
}

// copyTrailers forwards upstream response trailers to the client using the
// net/http TrailerPrefix convention, which works after WriteHeader for both
// HTTP/1.1 chunked and HTTP/2 responses.
func copyTrailers(dst http.Header, trailer http.Header) {
	for key, vals := range trailer {
		for _, v := range vals {
			dst.Add(http.TrailerPrefix+key, v)
		}
	}
}

func isEventStream(h http.Header) bool {
	ct := h.Get("Content-Type")
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	return strings.EqualFold(strings.TrimSpace(ct), "text/event-stream")
}

// flushWriter flushes after every successful write.
type flushWriter struct{ w *router.ResponseWriter }

func (f flushWriter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	if err == nil {
		f.w.Flush()
	}
	return n, err
}

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

// RemoveHopByHop strips hop-by-hop headers (RFC 9110 §7.6.1) from h,
// including fields NAMED by Connection. Exported for the location proxy in
// internal/server, which must apply the same laundering as the domain proxy.
func RemoveHopByHop(h http.Header) { removeHopByHop(h) }

func removeHopByHop(h http.Header) {
	// RFC 7230 §6.1: "Connection" may name *additional* headers that are
	// hop-by-hop for this connection ("Connection: X-Secret"). Those must be
	// dropped too, or a client chooses which header gets laundered to the
	// upstream. Read the names before deleting Connection itself. Header field
	// names are case-insensitive and http.Header.Del canonicalizes the key, so
	// a lowercase name in Connection still removes the canonical header.
	for _, value := range h.Values("Connection") {
		for _, name := range strings.Split(value, ",") {
			if name = strings.TrimSpace(name); name != "" {
				h.Del(name)
			}
		}
	}
	for _, key := range hopByHopHeaders {
		h.Del(key)
	}
}

// classifyUpstreamErr maps a transport-layer error to a short, stable label
// suitable for the 502 response body. The goal is to give an operator
// reading `curl -v` enough hint to know whether to look at DNS, TLS,
// timeouts, or the origin itself — without leaking the full Go error
// string (which can include internal addresses or stack-ish noise).
// The matching is intentionally pattern-based: Go's net/http does not
// expose typed errors for most transport failures, so we sniff the
// canonical error substrings.
func classifyUpstreamErr(err error) string {
	if err == nil {
		return "no upstream error"
	}
	s := err.Error()
	switch {
	case strings.Contains(s, "x509:") || strings.Contains(s, "certificate"):
		return "TLS certificate verification failed (consider proxy.insecure_skip_verify)"
	case strings.Contains(s, "tls:"):
		return "TLS handshake failed"
	case strings.Contains(s, "no such host"):
		return "DNS lookup failed for upstream"
	case strings.Contains(s, "connection refused"):
		return "upstream refused connection"
	case strings.Contains(s, "connection reset"):
		return "upstream reset the connection"
	case strings.Contains(s, "i/o timeout") || strings.Contains(s, "deadline exceeded"):
		return "upstream timed out"
	case strings.Contains(s, "network is unreachable") || strings.Contains(s, "no route to host"):
		return "upstream unreachable from this network"
	case strings.Contains(s, "http2:"):
		return "HTTP/2 protocol error from upstream"
	case strings.Contains(s, "EOF"):
		return "upstream closed the connection prematurely"
	}
	return "upstream connection failed"
}

// clientForwardedHeaders are forwarding headers UWAS never writes itself.
// Backends such as Spring's ForwardedHeaderFilter prefer them over the
// X-Forwarded-For/Proto/Host values UWAS sets, so a client-supplied copy would
// let the client choose the address, scheme, host or path prefix the app sees.
var clientForwardedHeaders = []string{
	"Forwarded", "X-Forwarded-Port", "X-Forwarded-Prefix",
	"X-Forwarded-Server", "X-Forwarded-Ssl", "X-Forwarded-Scheme",
}

func isClientForwardedHeader(key string) bool {
	for _, n := range clientForwardedHeaders {
		if strings.EqualFold(key, n) {
			return true
		}
	}
	return false
}

func stripClientForwarded(h http.Header) {
	for _, n := range clientForwardedHeaders {
		h.Del(n)
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
	if _, err := rand.Read(traceID[:]); err != nil {
		// Fallback: use timestamp-based prefix so trace ID is at least unique per-second
		ms := uint64(time.Now().UnixMilli())
		traceID[0] = byte(ms >> 40)
		traceID[1] = byte(ms >> 32)
		traceID[2] = byte(ms >> 24)
		traceID[3] = byte(ms >> 16)
		traceID[4] = byte(ms >> 8)
		traceID[5] = byte(ms)
	}
	if _, err := rand.Read(spanID[:]); err != nil {
		spanID[0] = 0x01
	}

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
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return false
	}
	for _, value := range r.Header.Values("Connection") {
		for _, token := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(token), "upgrade") {
				return true
			}
		}
	}
	return false
}

// serveWebSocket tunnels a WebSocket connection by hijacking the client
// connection and establishing a raw TCP connection to the backend. Both
// directions are piped concurrently until one side closes.
type websocketDialOptions struct {
	insecureSkipVerify bool
	allowPrivate       bool
}

func (h *Handler) serveWebSocket(ctx *router.RequestContext, backend *Backend) {
	h.serveWebSocketWithOptions(ctx, backend, websocketDialOptions{})
}

func (h *Handler) serveWebSocketWithOptions(ctx *router.RequestContext, backend *Backend, options websocketDialOptions) {
	// Hijack the client connection (ResponseWriter implements Hijack)
	clientConn, clientBuf, err := ctx.Response.Hijack()
	if err != nil {
		h.logger.Error("websocket hijack failed", "error", err)
		ctx.Response.Error(http.StatusInternalServerError, "WebSocket hijack not supported")
		return
	}

	// Connect to upstream
	backendAddr := websocketBackendAddress(backend.URL)

	reqID := ctx.Request.Header.Get("X-Request-ID")

	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
		Control: config.ProxyDialControl(options.allowPrivate),
	}
	upstreamConn, err := dialer.DialContext(ctx.Request.Context(), "tcp", backendAddr)
	if err != nil {
		h.logger.Error("websocket upstream connect failed", "backend", backendAddr, "request_id", reqID, "error", err)
		clientConn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		clientConn.Close()
		return
	}
	if backend.URL.Scheme == "https" || backend.URL.Scheme == "wss" {
		tlsConn := tls.Client(upstreamConn, &tls.Config{
			ServerName:         backend.URL.Hostname(),
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: options.insecureSkipVerify, // #nosec G402 -- explicit per-domain operator opt-in
		})
		handshakeCtx, cancel := context.WithTimeout(ctx.Request.Context(), 5*time.Second)
		err = tlsConn.HandshakeContext(handshakeCtx)
		cancel()
		if err != nil {
			h.logger.Error("websocket upstream TLS handshake failed", "backend", backendAddr, "request_id", reqID, "error", err)
			upstreamConn.Close()
			clientConn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
			clientConn.Close()
			return
		}
		upstreamConn = tlsConn
	}

	// Forward the original HTTP request (including Upgrade headers) to the backend.
	// RawPath preserves the original percent-encoding in RequestURI().
	upstreamURL := *backend.URL
	upstreamURL.Path = ctx.Request.URL.Path
	upstreamURL.RawPath = ctx.Request.URL.RawPath
	upstreamURL.RawQuery = ctx.Request.URL.RawQuery

	// Write the request line
	reqLine := ctx.Request.Method + " " + upstreamURL.RequestURI() + " HTTP/1.1\r\n"
	upstreamConn.Write([]byte(reqLine))

	// Write headers (including Upgrade and Connection). Skip every
	// client-supplied forwarded header so we can set authoritative values
	// below — the same set the HTTP reverse-proxy path overwrites via
	// Header.Set. Leaving X-Forwarded-Proto / X-Forwarded-Host in would let a
	// client claim https (or an arbitrary host) on a plain-HTTP upgrade, and
	// backends that trust those headers (PHP's HTTP_X_FORWARDED_PROTO) would
	// be misled.
	for key, vals := range ctx.Request.Header {
		lk := strings.ToLower(key)
		if lk == "x-forwarded-for" || lk == "x-real-ip" ||
			lk == "x-forwarded-proto" || lk == "x-forwarded-host" ||
			isClientForwardedHeader(key) {
			continue
		}
		for _, v := range vals {
			upstreamConn.Write([]byte(key + ": " + v + "\r\n"))
		}
	}
	// Add proxy headers
	upstreamConn.Write([]byte("X-Forwarded-For: " + clientIP(ctx.Request) + "\r\n"))
	upstreamConn.Write([]byte("X-Real-IP: " + clientIP(ctx.Request) + "\r\n"))
	upstreamConn.Write([]byte("X-Forwarded-Proto: " + forwardedProto(ctx) + "\r\n"))
	upstreamConn.Write([]byte("X-Forwarded-Host: " + ctx.Request.Host + "\r\n"))
	upstreamConn.Write([]byte("Host: " + ctx.Request.Host + "\r\n"))
	upstreamConn.Write([]byte("\r\n"))

	// Tunnel only after the backend has actually switched protocols. If it
	// answers anything but 101 the connection is still plain keep-alive
	// HTTP, and piping it raw would let the client send further requests
	// straight to the backend, past every check UWAS applies in front of
	// the proxy. Relay that response with Connection: close and stop.
	upstreamReader := bufio.NewReader(upstreamConn)
	_ = upstreamConn.SetReadDeadline(time.Now().Add(websocketUpgradeTimeout))
	head, status, err := readUpgradeResponseHead(upstreamReader)
	if err != nil {
		h.logger.Error("websocket upstream handshake response invalid", "backend", backendAddr, "request_id", reqID, "error", err)
		upstreamConn.Close()
		clientConn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\nConnection: close\r\n\r\n"))
		clientConn.Close()
		return
	}
	if status != http.StatusSwitchingProtocols {
		if resp, err := http.ReadResponse(bufio.NewReader(io.MultiReader(bytes.NewReader(head), upstreamReader)), ctx.Request); err == nil {
			resp.Close = true
			_ = resp.Write(clientConn)
			resp.Body.Close()
		} else {
			clientConn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\nConnection: close\r\n\r\n"))
		}
		upstreamConn.Close()
		clientConn.Close()
		return
	}
	_ = upstreamConn.SetReadDeadline(time.Time{})
	if _, err := clientConn.Write(head); err != nil {
		upstreamConn.Close()
		clientConn.Close()
		return
	}

	// Bidirectional copy with WaitGroup for graceful shutdown on error.
	// sync.Once guarantees that closeBoth only fires once — closing a net.Conn
	// twice is technically safe in stdlib, but the Once also avoids racing
	// SetDeadline calls and gives us a clean place to bound the *other*
	// direction's blocked Read when one side returns without closing the TCP
	// layer cleanly (e.g. half-open / load-balancer idle timeout).
	var (
		wg      sync.WaitGroup
		closeMu sync.Once
	)
	wg.Add(2)

	closeBoth := func() {
		closeMu.Do(func() {
			// A near-immediate read deadline kicks any goroutine blocked in
			// io.Copy out of its Read() so we never wedge on a half-closed
			// peer. Close() then completes the teardown.
			now := time.Now()
			_ = clientConn.SetDeadline(now)
			_ = upstreamConn.SetDeadline(now)
			clientConn.Close()
			upstreamConn.Close()
		})
	}

	go func() {
		defer wg.Done()
		defer closeBoth()
		if _, err := io.Copy(upstreamConn, clientBuf); err != nil {
			h.logger.Debug("websocket client→backend copy error", "request_id", reqID, "error", err)
		}
	}()

	go func() {
		defer wg.Done()
		defer closeBoth()
		if _, err := io.Copy(clientConn, upstreamReader); err != nil {
			h.logger.Debug("websocket backend→client copy error", "request_id", reqID, "error", err)
		}
	}()

	// Wait for both directions to finish
	wg.Wait()
	h.logger.Debug("websocket connection closed", "backend", backendAddr, "request_id", reqID, "path", ctx.Request.URL.Path)
}

// websocketUpgradeTimeout bounds how long the backend may take to answer the
// upgrade request (matches the HTTP path's default ResponseHeaderTimeout).
const websocketUpgradeTimeout = 30 * time.Second

// maxUpgradeResponseHead caps the backend's upgrade response head.
const maxUpgradeResponseHead = 64 << 10

// readUpgradeResponseHead reads the backend's response head verbatim (status
// line through the blank line) and returns it with the parsed status code.
func readUpgradeResponseHead(br *bufio.Reader) ([]byte, int, error) {
	var head []byte
	for {
		line, err := br.ReadSlice('\n')
		head = append(head, line...)
		if err != nil {
			return nil, 0, err
		}
		if len(head) > maxUpgradeResponseHead {
			return nil, 0, errors.New("upgrade response head too large")
		}
		if l := len(line); l == 1 || (l == 2 && line[0] == '\r') {
			if len(head) == l {
				return nil, 0, errors.New("empty upgrade response")
			}
			break
		}
	}
	statusLine, _, _ := strings.Cut(string(head), "\n")
	fields := strings.Fields(statusLine)
	if len(fields) < 2 || !strings.HasPrefix(fields[0], "HTTP/") {
		return nil, 0, errors.New("malformed upgrade response status line")
	}
	code, err := strconv.Atoi(fields[1])
	if err != nil || code < 100 || code > 999 {
		return nil, 0, errors.New("malformed upgrade response status code")
	}
	return head, code, nil
}

func websocketBackendAddress(backendURL *url.URL) string {
	if backendURL.Port() != "" {
		return backendURL.Host
	}
	port := "80"
	if backendURL.Scheme == "https" || backendURL.Scheme == "wss" {
		port = "443"
	}
	return net.JoinHostPort(backendURL.Hostname(), port)
}

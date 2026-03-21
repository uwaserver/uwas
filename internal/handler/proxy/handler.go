package proxy

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/router"
)

// Handler handles reverse proxy requests with load balancing.
type Handler struct {
	logger *logger.Logger
}

func New(log *logger.Logger) *Handler {
	return &Handler{logger: log}
}

func (h *Handler) Name() string        { return "proxy" }
func (h *Handler) Description() string  { return "Reverse proxy with load balancing" }

// Serve proxies the request to an upstream backend.
func (h *Handler) Serve(ctx *router.RequestContext, domain *config.Domain, pool *UpstreamPool, balancer Balancer) {
	backends := pool.Healthy()
	if len(backends) == 0 {
		ctx.Response.Error(http.StatusBadGateway, "502 Bad Gateway — no healthy upstreams")
		return
	}

	backend := balancer.Select(backends, ctx.Request)
	if backend == nil {
		ctx.Response.Error(http.StatusBadGateway, "502 Bad Gateway — no backend selected")
		return
	}

	backend.ActiveConns.Add(1)
	defer backend.ActiveConns.Add(-1)
	backend.TotalReqs.Add(1)

	ctx.Upstream = backend.URL.String()

	// Build upstream request
	upstreamURL := *backend.URL
	upstreamURL.Path = ctx.Request.URL.Path
	upstreamURL.RawQuery = ctx.Request.URL.RawQuery

	// Timeouts
	connectTimeout := 5 * time.Second
	readTimeout := 60 * time.Second
	if domain.Proxy.Timeouts.Connect.Duration > 0 {
		connectTimeout = domain.Proxy.Timeouts.Connect.Duration
	}
	if domain.Proxy.Timeouts.Read.Duration > 0 {
		readTimeout = domain.Proxy.Timeouts.Read.Duration
	}

	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout: connectTimeout,
		}).DialContext,
		ResponseHeaderTimeout: readTimeout,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
	}

	proxyReq, err := http.NewRequestWithContext(
		ctx.Request.Context(),
		ctx.Request.Method,
		upstreamURL.String(),
		ctx.Request.Body,
	)
	if err != nil {
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

	// Remove hop-by-hop headers
	removeHopByHop(proxyReq.Header)

	// Execute
	resp, err := transport.RoundTrip(proxyReq)
	if err != nil {
		backend.TotalFails.Add(1)
		h.logger.Error("upstream error",
			"backend", backend.URL.String(),
			"error", err,
		)
		if ctx.Request.Context().Err() == context.DeadlineExceeded {
			ctx.Response.Error(http.StatusGatewayTimeout, "504 Gateway Timeout")
		} else {
			ctx.Response.Error(http.StatusBadGateway, "502 Bad Gateway")
		}
		return
	}
	defer resp.Body.Close()

	// Copy response headers
	for key, vals := range resp.Header {
		for _, v := range vals {
			ctx.Response.Header().Add(key, v)
		}
	}
	removeHopByHop(ctx.Response.Header())

	// Write status + body
	ctx.Response.WriteHeader(resp.StatusCode)
	io.Copy(ctx.Response, resp.Body)
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

// IsWebSocketUpgrade checks if the request is a WebSocket upgrade.
func IsWebSocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket") &&
		strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade")
}

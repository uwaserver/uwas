// Package httpx provides small helpers around the standard net/http
// package that are easy to mis-apply, in particular: HTTP clients with
// sensible timeouts and the drain-then-close idiom for response bodies.
//
// Background: http.DefaultClient has no Timeout, so a slow or
// half-dead upstream parks the calling goroutine indefinitely. Even
// when the caller threads a context with deadline through to
// NewRequestWithContext, DefaultClient's Transport may block on the
// connection setup phase before the request-scoped deadline applies.
// Using a *http.Client with both a Timeout AND a properly tuned
// Transport eliminates both failure modes.
//
// Likewise, closing a response body without first draining whatever
// the server has already written into the kernel buffer prevents the
// Transport from reusing the underlying TCP connection, leading to
// connection-pool exhaustion under bursty load.
package httpx

import (
	"io"
	"net"
	"net/http"
	"time"
)

// NewClient returns an *http.Client configured with the supplied
// total timeout (header + body) and a Transport tuned for an outbound
// REST/JSON call profile (small bodies, short request lifetime). The
// returned client is safe for concurrent use and should be reused —
// constructing a fresh client per request defeats the connection pool.
func NewClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: newTransport(),
	}
}

func newTransport() *http.Transport {
	return &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		MaxConnsPerHost:       50,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}
}

// drainLimit caps how many bytes DrainAndClose reads before giving up.
// 4KB covers the vast majority of remaining response bodies after the
// caller has read what it wanted; for anything larger the goroutine
// cost of fully draining usually outweighs the connection-pool
// benefit.
const drainLimit = 4 << 10

// DrainAndClose discards up to 4KB from the body and then closes it.
// Always safe to defer right after an http.Client.Do; safe to call
// with a nil body (no-op). The drain + close pattern lets the
// Transport return the connection to the idle pool for reuse —
// without it, the underlying TCP connection is closed and a fresh
// handshake is required for the next request.
func DrainAndClose(body io.ReadCloser) {
	if body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(body, drainLimit))
	_ = body.Close()
}

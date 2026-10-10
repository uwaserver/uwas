package proxy

import (
	"bytes"
	"context"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// MirrorConfig configures request mirroring for a proxy domain.
type MirrorConfig struct {
	Enabled               bool   `yaml:"enabled"`
	Backend               string `yaml:"backend"`        // mirror backend URL
	Percent               int    `yaml:"percent"`        // percentage of requests to mirror (0-100)
	MaxBodyBytes          int    `yaml:"max_body_bytes"` // max body size for mirroring (default 2MB)
	AllowPrivateUpstreams bool   `yaml:"allow_private_upstreams"`
}

// Mirror handles fire-and-forget request mirroring to a secondary backend.
type Mirror struct {
	backend               string
	percent               int
	maxBytes              int
	allowPrivateUpstreams bool
	logger                *logger.Logger
	transport             *http.Transport

	// sem bounds the copies in flight: mirroring is best effort, so when the
	// shadow backend is slower than live traffic the excess is shed instead of
	// piling up goroutines, connections and buffered bodies.
	sem     chan struct{}
	dropped atomic.Int64
}

// maxMirrorInflight is the most mirrored copies a Mirror keeps in flight.
const maxMirrorInflight = 64

// NewMirror creates a new Mirror instance.
func NewMirror(cfg MirrorConfig, log *logger.Logger) *Mirror {
	maxBytes := cfg.MaxBodyBytes
	if maxBytes <= 0 {
		maxBytes = 2 << 20 // default 2MB
	}
	return &Mirror{
		backend:               config.NormalizeProxyUpstreamAddress(cfg.Backend),
		percent:               cfg.Percent,
		maxBytes:              maxBytes,
		allowPrivateUpstreams: cfg.AllowPrivateUpstreams,
		logger:                log,
		sem:                   make(chan struct{}, maxMirrorInflight),
		transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout: 5 * time.Second,
				Control: config.ProxyDialControl(cfg.AllowPrivateUpstreams),
			}).DialContext,
			MaxIdleConns:          50,
			IdleConnTimeout:       30 * time.Second,
			ResponseHeaderTimeout: 10 * time.Second,
		},
	}
}

// ShouldMirror returns true if this request should be mirrored based on
// the configured percentage.
func (m *Mirror) ShouldMirror() bool {
	if m.percent <= 0 {
		return false
	}
	if m.percent >= 100 {
		return true
	}
	return rand.IntN(100) < m.percent
}

// MaxBodyBytes returns the maximum request body size for mirroring.
func (m *Mirror) MaxBodyBytes() int {
	return m.maxBytes
}

// Send sends a copy of the request to the mirror backend.
// It is fire-and-forget: the mirror response is discarded and errors
// are logged at debug level. The original request body must be provided
// as bodyBytes since the original body has already been consumed.
func (m *Mirror) Send(originalReq *http.Request, bodyBytes []byte) {
	if m.backend == "" {
		return
	}

	select {
	case m.sem <- struct{}{}:
	default:
		m.dropped.Add(1)
		return
	}
	go func() {
		defer func() { <-m.sem }()
		m.doMirror(originalReq, bodyBytes)
	}()
}

// Dropped returns how many copies were shed because maxMirrorInflight were
// already in flight.
func (m *Mirror) Dropped() int64 {
	return m.dropped.Load()
}

func (m *Mirror) doMirror(originalReq *http.Request, bodyBytes []byte) {
	// Build mirror URL
	// RequestURI has no leading "/" after a relative rewrite target; without
	// one the concatenation would change the host ("http://shadow" + "@h/x").
	uri := originalReq.URL.RequestURI()
	if !strings.HasPrefix(uri, "/") {
		uri = "/" + uri
	}
	mirrorURL := strings.TrimRight(m.backend, "/") + uri
	if err := m.validateBackendURL(mirrorURL); err != nil {
		m.logger.Warn("mirror: upstream blocked by SSRF protection", "url", mirrorURL, "error", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var body io.Reader
	if bodyBytes != nil {
		body = bytes.NewReader(bodyBytes)
	}

	req, err := http.NewRequestWithContext(ctx, originalReq.Method, mirrorURL, body)
	if err != nil {
		m.logger.Debug("mirror: failed to create request", "error", err, "url", mirrorURL)
		return
	}

	// Copy headers from original request
	for key, vals := range originalReq.Header {
		for _, v := range vals {
			req.Header.Add(key, v)
		}
	}

	// Remove hop-by-hop headers first: they include any header the client
	// names in Connection, which would otherwise delete the marker below.
	removeHopByHop(req.Header)

	// Mark as mirrored so the mirror backend can identify these
	req.Header.Set("X-Mirror", "true")

	resp, err := m.transport.RoundTrip(req)
	if err != nil {
		m.logger.Debug("mirror: request failed", "error", err, "url", mirrorURL)
		return
	}
	// Discard the body and close
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if resp.StatusCode >= 500 {
		m.logger.Debug("mirror: backend error", "status", resp.StatusCode, "url", mirrorURL)
	}
}

func (m *Mirror) validateBackendURL(rawURL string) error {
	ssrfCheck := config.IsProxyUpstreamSafe
	if m.allowPrivateUpstreams {
		ssrfCheck = config.IsPrivateProxyUpstreamSafe
	}
	return ssrfCheck(rawURL)
}

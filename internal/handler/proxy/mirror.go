package proxy

import (
	"bytes"
	"context"
	"io"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/uwaserver/uwas/internal/logger"
)

// MirrorConfig configures request mirroring for a proxy domain.
type MirrorConfig struct {
	Enabled      bool   `yaml:"enabled"`
	Backend      string `yaml:"backend"`               // mirror backend URL
	Percent      int    `yaml:"percent"`               // percentage of requests to mirror (0-100)
	MaxBodyBytes int    `yaml:"max_body_bytes"`        // max body size for mirroring (default 2MB)
}

// Mirror handles fire-and-forget request mirroring to a secondary backend.
type Mirror struct {
	backend   string
	percent   int
	maxBytes  int
	logger    *logger.Logger
	transport *http.Transport
}

// NewMirror creates a new Mirror instance.
func NewMirror(cfg MirrorConfig, log *logger.Logger) *Mirror {
	maxBytes := cfg.MaxBodyBytes
	if maxBytes <= 0 {
		maxBytes = 2 << 20 // default 2MB
	}
	return &Mirror{
		backend: cfg.Backend,
		percent: cfg.Percent,
		maxBytes: maxBytes,
		logger:  log,
		transport: &http.Transport{
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

	go m.doMirror(originalReq, bodyBytes)
}

func (m *Mirror) doMirror(originalReq *http.Request, bodyBytes []byte) {
	// Build mirror URL
	mirrorURL := m.backend + originalReq.URL.RequestURI()

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

	// Mark as mirrored so the mirror backend can identify these
	req.Header.Set("X-Mirror", "true")

	// Remove hop-by-hop headers
	removeHopByHop(req.Header)

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

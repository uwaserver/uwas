package proxy

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// HealthChecker periodically checks backend health.
type HealthChecker struct {
	pool         *UpstreamPool
	path         string
	interval     time.Duration
	timeout      time.Duration
	threshold    int // consecutive failures before unhealthy
	rise         int // consecutive successes before healthy
	logger       *logger.Logger
	client       *http.Client
	mu           sync.Mutex
	failures     map[*Backend]int
	successes    map[*Backend]int
	cancel       context.CancelFunc // cancels the derived context that stops the background goroutine
	allowPrivate bool
}

// HealthConfig configures health checking.
type HealthConfig struct {
	Path      string
	Interval  time.Duration
	Timeout   time.Duration
	Threshold int
	Rise      int

	// AllowPrivate mirrors the domain's allow_private_upstreams opt-in:
	// probes may then target private (non-loopback) backends the proxy path
	// already serves. Metadata/link-local/documentation ranges stay blocked
	// either way.
	AllowPrivate bool

	// InsecureSkipVerify mirrors the domain's proxy.insecure_skip_verify:
	// a self-signed HTTPS upstream the proxy serves must not be probed with
	// verification, or it is marked unhealthy (F4500).
	InsecureSkipVerify bool
}

func NewHealthChecker(pool *UpstreamPool, cfg HealthConfig, log *logger.Logger) *HealthChecker {
	if cfg.Interval <= 0 {
		cfg.Interval = 10 * time.Second
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}
	if cfg.Threshold <= 0 {
		cfg.Threshold = 3
	}
	if cfg.Rise <= 0 {
		cfg.Rise = 2
	}
	if cfg.Path == "" {
		cfg.Path = "/"
	} else if cfg.Path[0] != '/' && cfg.Path[0] != '?' {
		// The path is appended to the backend address: without its leading slash
		// "host:port"+"health" is not a URL and every probe fails (F20200).
		cfg.Path = "/" + cfg.Path
	}

	// A redirect is an answer from the backend, not an instruction to probe
	// elsewhere: only the configured backend is checked against the SSRF
	// policy, so following Location would reach any address (F3700).
	// The dial is guarded as well: the backend hostname is resolved once for
	// the URL check and again by the dialer, so a rebinding name could pass
	// the first and reach a blocked address on the second (F4003).
	client := &http.Client{
		Timeout:       cfg.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout: cfg.Timeout,
				Control: config.ProxyDialControl(cfg.AllowPrivate),
			}).DialContext,
			DisableKeepAlives:      true,
			MaxResponseHeaderBytes: MaxResponseHeaderBytes,
		},
	}
	if cfg.InsecureSkipVerify {
		client.Transport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402 -- mirrors the per-domain proxy.insecure_skip_verify opt-in
	}

	return &HealthChecker{
		pool:         pool,
		path:         cfg.Path,
		allowPrivate: cfg.AllowPrivate,
		interval:     cfg.Interval,
		timeout:      cfg.Timeout,
		threshold:    cfg.Threshold,
		rise:         cfg.Rise,
		logger:       log,
		client:       client,
		failures:     make(map[*Backend]int),
		successes:    make(map[*Backend]int),
	}
}

// Start begins periodic health checking.
// The parent ctx controls the goroutine lifetime; call Stop() to cancel
// it early (e.g. on config reload). Safe to call multiple times — subsequent
// calls are no-ops until the previous goroutine exits.
func (hc *HealthChecker) Start(ctx context.Context) {
	hc.mu.Lock()
	if hc.cancel != nil {
		hc.mu.Unlock()
		return // already running
	}
	derivedCtx, cancel := context.WithCancel(ctx)
	hc.cancel = cancel
	hc.mu.Unlock()

	go func() {
		ticker := time.NewTicker(hc.interval)
		defer ticker.Stop()

		for {
			select {
			case <-derivedCtx.Done():
				return
			case <-ticker.C:
				hc.checkAll()
			}
		}
	}()
}

// Stop cancels the background goroutine and resets internal maps.
// Safe to call multiple times; subsequent calls are no-ops.
func (hc *HealthChecker) Stop() {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	if hc.cancel != nil {
		hc.cancel()
		hc.cancel = nil
	}
	// Reset failure/success maps to prevent memory leak
	hc.failures = make(map[*Backend]int)
	hc.successes = make(map[*Backend]int)
}

// maxConcurrentProbes bounds the probes one round runs at once.
const maxConcurrentProbes = 32

// checkAll probes the backends concurrently: a round that probed them one by
// one let each unresponsive backend delay the others by a full timeout, so a
// few dead backends made detection of the rest slower than the interval
// (F58000). record* serialize on hc.mu.
func (hc *HealthChecker) checkAll() {
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxConcurrentProbes)
	for _, b := range hc.pool.All() {
		if b.GetState() == StateDraining {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(b *Backend) {
			defer wg.Done()
			defer func() { <-sem }()
			hc.checkOne(b)
		}(b)
	}
	wg.Wait()
}

func (hc *HealthChecker) checkOne(b *Backend) {
	// Trim the address's trailing slash so "http://host/"+"/health" probes
	// "/health", not "//health" (F20200).
	urlStr := strings.TrimRight(b.URL.String(), "/") + hc.path

	// Reject private/loopback upstreams before dialing to prevent timing
	// leakage of internal network topology via health-check latency — unless
	// the domain opted into private upstreams, in which case only metadata,
	// link-local, and documentation ranges stay blocked (mirroring the
	// proxy path's proxyUpstreamSafetyCheck selection).
	validate := config.IsProxyUpstreamSafe
	if hc.allowPrivate {
		validate = config.IsPrivateProxyUpstreamSafe
	}
	if err := validate(b.URL.String()); err != nil {
		hc.recordFailure(b)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), hc.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	if err != nil {
		hc.recordFailure(b)
		return
	}
	req.Header.Set("User-Agent", "UWAS-HealthCheck/1.0")

	resp, err := hc.client.Do(req)
	if err != nil {
		hc.recordFailure(b)
		return
	}
	_, bodyErr := io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if bodyErr != nil {
		hc.recordFailure(b)
		return
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		hc.recordSuccess(b)
	} else {
		hc.recordFailure(b)
	}
}

func (hc *HealthChecker) recordFailure(b *Backend) {
	hc.mu.Lock()
	defer hc.mu.Unlock()

	hc.successes[b] = 0
	hc.failures[b]++

	if hc.failures[b] >= hc.threshold && b.IsHealthy() {
		b.SetState(StateUnhealthy)
		hc.logger.Warn("backend unhealthy",
			"backend", b.URL.String(),
			"failures", hc.failures[b],
		)
	}
}

func (hc *HealthChecker) recordSuccess(b *Backend) {
	hc.mu.Lock()
	defer hc.mu.Unlock()

	hc.failures[b] = 0
	hc.successes[b]++

	if hc.successes[b] >= hc.rise && b.State.CompareAndSwap(int32(StateUnhealthy), int32(StateHealthy)) {
		hc.logger.Info("backend recovered",
			"backend", b.URL.String(),
			"successes", hc.successes[b],
		)
	}
}

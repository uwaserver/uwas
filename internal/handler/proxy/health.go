package proxy

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/uwaserver/uwas/internal/logger"
)

// HealthChecker periodically checks backend health.
type HealthChecker struct {
	pool      *UpstreamPool
	path      string
	interval  time.Duration
	timeout   time.Duration
	threshold int // consecutive failures before unhealthy
	rise      int // consecutive successes before healthy
	logger    *logger.Logger
	client    *http.Client
	mu        sync.Mutex
	failures  map[*Backend]int
	successes map[*Backend]int
}

// HealthConfig configures health checking.
type HealthConfig struct {
	Path      string
	Interval  time.Duration
	Timeout   time.Duration
	Threshold int
	Rise      int
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
	}

	return &HealthChecker{
		pool:      pool,
		path:      cfg.Path,
		interval:  cfg.Interval,
		timeout:   cfg.Timeout,
		threshold: cfg.Threshold,
		rise:      cfg.Rise,
		logger:    log,
		client:    &http.Client{Timeout: cfg.Timeout},
		failures:  make(map[*Backend]int),
		successes: make(map[*Backend]int),
	}
}

// Start begins periodic health checking.
func (hc *HealthChecker) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(hc.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				hc.checkAll()
			}
		}
	}()
}

func (hc *HealthChecker) checkAll() {
	for _, b := range hc.pool.All() {
		if b.GetState() == StateDraining {
			continue
		}
		hc.checkOne(b)
	}
}

func (hc *HealthChecker) checkOne(b *Backend) {
	url := b.URL.String() + hc.path

	ctx, cancel := context.WithTimeout(context.Background(), hc.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
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
	resp.Body.Close()

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

	if hc.successes[b] >= hc.rise && !b.IsHealthy() {
		b.SetState(StateHealthy)
		hc.logger.Info("backend recovered",
			"backend", b.URL.String(),
			"successes", hc.successes[b],
		)
	}
}

package metrics

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Collector gathers Prometheus-compatible metrics.
type Collector struct {
	RequestsTotal  atomic.Int64
	RequestsByCode [6]atomic.Int64 // index 0=1xx, 1=2xx, 2=3xx, 3=4xx, 4=5xx, 5=other
	CacheHits      atomic.Int64
	CacheMisses    atomic.Int64
	CacheStales    atomic.Int64
	ActiveConns    atomic.Int64
	BytesSent      atomic.Int64
	StartTime      time.Time

	// Latency tracking: rolling window of recent request durations.
	latencyMu     sync.Mutex
	latencyBuf    []float64 // ring buffer of last N durations (seconds)
	latencyPos    int
	latencyFull   bool
	SlowThreshold time.Duration // log requests slower than this
	SlowRequests  atomic.Int64  // count of slow requests

	// Per-handler type counters
	StaticRequests   atomic.Int64
	PHPRequests      atomic.Int64
	ProxyRequests    atomic.Int64
	RedirectRequests atomic.Int64
}

const latencyBufSize = 10000 // track last 10K requests for percentile calculation

func New() *Collector {
	return &Collector{
		StartTime:     time.Now(),
		latencyBuf:    make([]float64, latencyBufSize),
		SlowThreshold: 5 * time.Second, // default: 5s
	}
}

func (c *Collector) RecordRequest(statusCode int) {
	c.RequestsTotal.Add(1)
	idx := statusCode/100 - 1
	if idx < 0 || idx > 4 {
		idx = 5
	}
	c.RequestsByCode[idx].Add(1)
}

// RecordLatency records the duration of a completed request.
func (c *Collector) RecordLatency(d time.Duration) {
	secs := d.Seconds()

	c.latencyMu.Lock()
	c.latencyBuf[c.latencyPos] = secs
	c.latencyPos = (c.latencyPos + 1) % latencyBufSize
	if c.latencyPos == 0 {
		c.latencyFull = true
	}
	c.latencyMu.Unlock()

	if c.SlowThreshold > 0 && d >= c.SlowThreshold {
		c.SlowRequests.Add(1)
	}
}

// Percentiles returns the p50, p95, p99, and max latencies in seconds.
func (c *Collector) Percentiles() (p50, p95, p99, max float64) {
	c.latencyMu.Lock()
	var n int
	if c.latencyFull {
		n = latencyBufSize
	} else {
		n = c.latencyPos
	}
	if n == 0 {
		c.latencyMu.Unlock()
		return
	}
	// Copy the data to avoid holding the lock during sort
	data := make([]float64, n)
	copy(data, c.latencyBuf[:n])
	c.latencyMu.Unlock()

	sort.Float64s(data)

	p50 = percentile(data, 0.50)
	p95 = percentile(data, 0.95)
	p99 = percentile(data, 0.99)
	max = data[len(data)-1]
	return
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := p * float64(len(sorted)-1)
	lower := int(math.Floor(idx))
	upper := int(math.Ceil(idx))
	if lower == upper || upper >= len(sorted) {
		return sorted[lower]
	}
	frac := idx - float64(lower)
	return sorted[lower]*(1-frac) + sorted[upper]*frac
}

// RecordHandlerType records which handler type served the request.
func (c *Collector) RecordHandlerType(handlerType string) {
	switch handlerType {
	case "static":
		c.StaticRequests.Add(1)
	case "php":
		c.PHPRequests.Add(1)
	case "proxy":
		c.ProxyRequests.Add(1)
	case "redirect":
		c.RedirectRequests.Add(1)
	}
}

func (c *Collector) RecordCache(status string) {
	switch status {
	case "HIT":
		c.CacheHits.Add(1)
	case "MISS":
		c.CacheMisses.Add(1)
	case "STALE":
		c.CacheStales.Add(1)
	}
}

// Handler returns an HTTP handler that serves Prometheus text format metrics.
func (c *Collector) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		var b strings.Builder

		fmt.Fprintf(&b, "# HELP uwas_requests_total Total HTTP requests.\n")
		fmt.Fprintf(&b, "# TYPE uwas_requests_total counter\n")
		fmt.Fprintf(&b, "uwas_requests_total %d\n", c.RequestsTotal.Load())

		fmt.Fprintf(&b, "# HELP uwas_requests_by_status HTTP requests by status code class.\n")
		fmt.Fprintf(&b, "# TYPE uwas_requests_by_status counter\n")
		codes := []string{"1xx", "2xx", "3xx", "4xx", "5xx", "other"}
		for i, label := range codes {
			fmt.Fprintf(&b, "uwas_requests_by_status{code=%q} %d\n", label, c.RequestsByCode[i].Load())
		}

		fmt.Fprintf(&b, "# HELP uwas_cache_hits_total Cache hits.\n")
		fmt.Fprintf(&b, "# TYPE uwas_cache_hits_total counter\n")
		fmt.Fprintf(&b, "uwas_cache_hits_total %d\n", c.CacheHits.Load())

		fmt.Fprintf(&b, "# HELP uwas_cache_misses_total Cache misses.\n")
		fmt.Fprintf(&b, "# TYPE uwas_cache_misses_total counter\n")
		fmt.Fprintf(&b, "uwas_cache_misses_total %d\n", c.CacheMisses.Load())

		fmt.Fprintf(&b, "# HELP uwas_connections_active Active connections.\n")
		fmt.Fprintf(&b, "# TYPE uwas_connections_active gauge\n")
		fmt.Fprintf(&b, "uwas_connections_active %d\n", c.ActiveConns.Load())

		fmt.Fprintf(&b, "# HELP uwas_bytes_sent_total Total bytes sent.\n")
		fmt.Fprintf(&b, "# TYPE uwas_bytes_sent_total counter\n")
		fmt.Fprintf(&b, "uwas_bytes_sent_total %d\n", c.BytesSent.Load())

		fmt.Fprintf(&b, "# HELP uwas_uptime_seconds Server uptime.\n")
		fmt.Fprintf(&b, "# TYPE uwas_uptime_seconds gauge\n")
		fmt.Fprintf(&b, "uwas_uptime_seconds %.0f\n", time.Since(c.StartTime).Seconds())

		// Latency percentiles
		p50, p95, p99, max := c.Percentiles()
		fmt.Fprintf(&b, "# HELP uwas_request_duration_seconds Request latency percentiles.\n")
		fmt.Fprintf(&b, "# TYPE uwas_request_duration_seconds summary\n")
		fmt.Fprintf(&b, "uwas_request_duration_seconds{quantile=\"0.5\"} %.6f\n", p50)
		fmt.Fprintf(&b, "uwas_request_duration_seconds{quantile=\"0.95\"} %.6f\n", p95)
		fmt.Fprintf(&b, "uwas_request_duration_seconds{quantile=\"0.99\"} %.6f\n", p99)
		fmt.Fprintf(&b, "uwas_request_duration_seconds{quantile=\"1.0\"} %.6f\n", max)

		fmt.Fprintf(&b, "# HELP uwas_slow_requests_total Requests exceeding slow threshold.\n")
		fmt.Fprintf(&b, "# TYPE uwas_slow_requests_total counter\n")
		fmt.Fprintf(&b, "uwas_slow_requests_total %d\n", c.SlowRequests.Load())

		fmt.Fprintf(&b, "# HELP uwas_requests_by_handler Requests by handler type.\n")
		fmt.Fprintf(&b, "# TYPE uwas_requests_by_handler counter\n")
		fmt.Fprintf(&b, "uwas_requests_by_handler{handler=\"static\"} %d\n", c.StaticRequests.Load())
		fmt.Fprintf(&b, "uwas_requests_by_handler{handler=\"php\"} %d\n", c.PHPRequests.Load())
		fmt.Fprintf(&b, "uwas_requests_by_handler{handler=\"proxy\"} %d\n", c.ProxyRequests.Load())
		fmt.Fprintf(&b, "uwas_requests_by_handler{handler=\"redirect\"} %d\n", c.RedirectRequests.Load())

		w.Write([]byte(b.String()))
	})
}

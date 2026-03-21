package metrics

import (
	"fmt"
	"net/http"
	"strings"
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
}

func New() *Collector {
	return &Collector{StartTime: time.Now()}
}

func (c *Collector) RecordRequest(statusCode int) {
	c.RequestsTotal.Add(1)
	idx := statusCode/100 - 1
	if idx < 0 || idx > 4 {
		idx = 5
	}
	c.RequestsByCode[idx].Add(1)
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

		w.Write([]byte(b.String()))
	})
}

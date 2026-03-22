package metrics

import (
	"math"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRecordRequest(t *testing.T) {
	c := New()
	c.RecordRequest(200)
	c.RecordRequest(200)
	c.RecordRequest(404)
	c.RecordRequest(500)

	if c.RequestsTotal.Load() != 4 {
		t.Errorf("total = %d, want 4", c.RequestsTotal.Load())
	}
	if c.RequestsByCode[1].Load() != 2 { // 2xx
		t.Errorf("2xx = %d, want 2", c.RequestsByCode[1].Load())
	}
	if c.RequestsByCode[3].Load() != 1 { // 4xx
		t.Errorf("4xx = %d, want 1", c.RequestsByCode[3].Load())
	}
	if c.RequestsByCode[4].Load() != 1 { // 5xx
		t.Errorf("5xx = %d, want 1", c.RequestsByCode[4].Load())
	}
}

func TestRecordCache(t *testing.T) {
	c := New()
	c.RecordCache("HIT")
	c.RecordCache("HIT")
	c.RecordCache("MISS")
	c.RecordCache("STALE")

	if c.CacheHits.Load() != 2 {
		t.Errorf("hits = %d, want 2", c.CacheHits.Load())
	}
	if c.CacheMisses.Load() != 1 {
		t.Errorf("misses = %d, want 1", c.CacheMisses.Load())
	}
	if c.CacheStales.Load() != 1 {
		t.Errorf("stales = %d, want 1", c.CacheStales.Load())
	}
}

func TestMetricsHandler(t *testing.T) {
	c := New()
	c.RequestsTotal.Store(100)
	c.CacheHits.Store(50)
	c.BytesSent.Store(1024000)

	rec := httptest.NewRecorder()
	c.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))

	body := rec.Body.String()

	if !strings.Contains(body, "uwas_requests_total 100") {
		t.Error("missing requests_total")
	}
	if !strings.Contains(body, "uwas_cache_hits_total 50") {
		t.Error("missing cache_hits")
	}
	if !strings.Contains(body, "uwas_bytes_sent_total 1024000") {
		t.Error("missing bytes_sent")
	}
	if !strings.Contains(body, "uwas_uptime_seconds") {
		t.Error("missing uptime")
	}

	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain", ct)
	}
}

func TestRecordRequestEdgeStatusCode(t *testing.T) {
	c := New()
	// Status code 600 is outside normal 1xx-5xx range, should go to "other" bucket (index 5)
	c.RecordRequest(600)
	c.RecordRequest(0)
	c.RecordRequest(-1)

	if c.RequestsTotal.Load() != 3 {
		t.Errorf("total = %d, want 3", c.RequestsTotal.Load())
	}
	if c.RequestsByCode[5].Load() != 3 {
		t.Errorf("other = %d, want 3", c.RequestsByCode[5].Load())
	}
}

func TestRecordLatency(t *testing.T) {
	c := New()
	// Record 100 requests with increasing durations (1ms to 100ms)
	for i := 1; i <= 100; i++ {
		c.RecordLatency(time.Duration(i) * time.Millisecond)
	}

	p50, p95, p99, max := c.Percentiles()

	// p50 should be around 50ms
	if math.Abs(p50-0.050) > 0.01 {
		t.Errorf("p50 = %.4f, want ~0.050", p50)
	}
	// p95 should be around 95ms
	if math.Abs(p95-0.095) > 0.01 {
		t.Errorf("p95 = %.4f, want ~0.095", p95)
	}
	// p99 should be around 99ms
	if math.Abs(p99-0.099) > 0.01 {
		t.Errorf("p99 = %.4f, want ~0.099", p99)
	}
	// max should be 100ms
	if math.Abs(max-0.100) > 0.001 {
		t.Errorf("max = %.4f, want 0.100", max)
	}
}

func TestPercentilesEmpty(t *testing.T) {
	c := New()
	p50, p95, p99, max := c.Percentiles()
	if p50 != 0 || p95 != 0 || p99 != 0 || max != 0 {
		t.Errorf("empty percentiles should be 0, got p50=%.4f p95=%.4f p99=%.4f max=%.4f", p50, p95, p99, max)
	}
}

func TestSlowRequests(t *testing.T) {
	c := New()
	c.SlowThreshold = 100 * time.Millisecond

	c.RecordLatency(50 * time.Millisecond)  // fast
	c.RecordLatency(200 * time.Millisecond) // slow
	c.RecordLatency(150 * time.Millisecond) // slow
	c.RecordLatency(80 * time.Millisecond)  // fast

	if c.SlowRequests.Load() != 2 {
		t.Errorf("slow = %d, want 2", c.SlowRequests.Load())
	}
}

func TestLatencyRingBufferWrap(t *testing.T) {
	c := New()
	// Fill buffer beyond capacity to test ring wrap
	for i := 0; i < latencyBufSize+100; i++ {
		c.RecordLatency(time.Millisecond)
	}

	p50, _, _, _ := c.Percentiles()
	if p50 == 0 {
		t.Error("p50 should be non-zero after filling buffer")
	}
}

func TestMetricsHandlerIncludesLatency(t *testing.T) {
	c := New()
	c.RecordLatency(50 * time.Millisecond)
	c.RecordLatency(100 * time.Millisecond)
	c.SlowThreshold = 80 * time.Millisecond
	c.RecordLatency(90 * time.Millisecond)

	rec := httptest.NewRecorder()
	c.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()

	if !strings.Contains(body, "uwas_request_duration_seconds") {
		t.Error("missing request_duration_seconds")
	}
	if !strings.Contains(body, `quantile="0.5"`) {
		t.Error("missing p50 quantile")
	}
	if !strings.Contains(body, `quantile="0.95"`) {
		t.Error("missing p95 quantile")
	}
	if !strings.Contains(body, `quantile="0.99"`) {
		t.Error("missing p99 quantile")
	}
	if !strings.Contains(body, "uwas_slow_requests_total") {
		t.Error("missing slow_requests_total")
	}
}

func TestPercentileCalculation(t *testing.T) {
	// Test with a single value
	c := New()
	c.RecordLatency(42 * time.Millisecond)

	p50, p95, p99, max := c.Percentiles()
	expected := 0.042
	if math.Abs(p50-expected) > 0.001 {
		t.Errorf("p50 = %.4f, want %.4f", p50, expected)
	}
	if math.Abs(p95-expected) > 0.001 {
		t.Errorf("p95 = %.4f, want %.4f", p95, expected)
	}
	if math.Abs(p99-expected) > 0.001 {
		t.Errorf("p99 = %.4f, want %.4f", p99, expected)
	}
	if math.Abs(max-expected) > 0.001 {
		t.Errorf("max = %.4f, want %.4f", max, expected)
	}
}

package metrics

import (
	"net/http/httptest"
	"strings"
	"testing"
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

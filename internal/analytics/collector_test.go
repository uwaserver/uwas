package analytics

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRecordAndSnapshot(t *testing.T) {
	c := New()

	c.Record("example.com", "/", "1.2.3.4:1234", 200, 1024)
	c.Record("example.com", "/about", "1.2.3.4:1234", 200, 512)
	c.Record("example.com", "/about", "5.6.7.8:5678", 404, 256)

	snap := c.GetHost("example.com")
	if snap == nil {
		t.Fatal("expected snapshot for example.com")
	}

	if snap.PageViews != 3 {
		t.Errorf("PageViews = %d, want 3", snap.PageViews)
	}
	if snap.UniqueIPs != 2 {
		t.Errorf("UniqueIPs = %d, want 2", snap.UniqueIPs)
	}
	if snap.BytesSent != 1792 {
		t.Errorf("BytesSent = %d, want 1792", snap.BytesSent)
	}
	if snap.StatusCodes[200] != 2 {
		t.Errorf("StatusCodes[200] = %d, want 2", snap.StatusCodes[200])
	}
	if snap.StatusCodes[404] != 1 {
		t.Errorf("StatusCodes[404] = %d, want 1", snap.StatusCodes[404])
	}
	if snap.TopPaths["/about"] != 2 {
		t.Errorf("TopPaths[/about] = %d, want 2", snap.TopPaths["/about"])
	}
}

func TestGetHostNotFound(t *testing.T) {
	c := New()
	snap := c.GetHost("nonexistent.com")
	if snap != nil {
		t.Error("expected nil for unknown domain")
	}
}

func TestGetAll(t *testing.T) {
	c := New()
	c.Record("a.com", "/", "1.1.1.1:1", 200, 100)
	c.Record("b.com", "/", "2.2.2.2:2", 200, 200)

	all := c.GetAll()
	if len(all) != 2 {
		t.Errorf("GetAll returned %d domains, want 2", len(all))
	}

	hosts := map[string]bool{}
	for _, s := range all {
		hosts[s.Host] = true
	}
	if !hosts["a.com"] || !hosts["b.com"] {
		t.Error("expected both a.com and b.com in results")
	}
}

func TestHourlyViews(t *testing.T) {
	c := New()
	c.Record("example.com", "/", "1.2.3.4:1234", 200, 100)

	snap := c.GetHost("example.com")
	hour := time.Now().Hour()
	if snap.HourlyViews[hour] != 1 {
		t.Errorf("HourlyViews[%d] = %d, want 1", hour, snap.HourlyViews[hour])
	}
}

func TestRollingWindow(t *testing.T) {
	c := New()

	// Record some requests
	for i := 0; i < 10; i++ {
		c.Record("example.com", "/", "1.2.3.4:1234", 200, 100)
	}

	// Views in last hour should include our requests
	views := requestsInWindow(c, "example.com", time.Hour)
	if views != 10 {
		t.Errorf("views in last hour = %d, want 10", views)
	}
}

func TestActiveDomains(t *testing.T) {
	c := New()
	c.Record("a.com", "/", "1.1.1.1:1", 200, 0)
	c.Record("b.com", "/", "2.2.2.2:2", 200, 0)
	c.Record("c.com", "/", "3.3.3.3:3", 200, 0)

	if count := c.ActiveDomains(); count != 3 {
		t.Errorf("ActiveDomains = %d, want 3", count)
	}
}

func TestHandlerAll(t *testing.T) {
	c := New()
	c.Record("example.com", "/", "1.2.3.4:1234", 200, 100)

	allHandler, _ := c.Handler()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/analytics", nil)
	allHandler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var result []Snapshot
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode JSON: %v", err)
	}
	if len(result) != 1 {
		t.Errorf("got %d snapshots, want 1", len(result))
	}
}

func TestHandlerHost(t *testing.T) {
	c := New()
	c.Record("example.com", "/", "1.2.3.4:1234", 200, 100)

	_, hostHandler := c.Handler()

	// Existing domain
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/analytics/example.com", nil)
	req.SetPathValue("host", "example.com")
	hostHandler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	var snap Snapshot
	if err := json.NewDecoder(rec.Body).Decode(&snap); err != nil {
		t.Fatalf("failed to decode JSON: %v", err)
	}
	if snap.Host != "example.com" {
		t.Errorf("host = %q, want example.com", snap.Host)
	}
}

func TestHandlerHostNotFound(t *testing.T) {
	c := New()
	_, hostHandler := c.Handler()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/analytics/unknown.com", nil)
	req.SetPathValue("host", "unknown.com")
	hostHandler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestTopNPaths(t *testing.T) {
	c := New()

	// Record many different paths, some more than others
	for i := 0; i < 100; i++ {
		c.Record("example.com", "/popular", "1.2.3.4:1234", 200, 10)
	}
	for i := 0; i < 50; i++ {
		c.Record("example.com", "/medium", "1.2.3.4:1234", 200, 10)
	}
	for i := 0; i < 10; i++ {
		c.Record("example.com", "/rare", "1.2.3.4:1234", 200, 10)
	}

	snap := c.GetHost("example.com")
	if snap.TopPaths["/popular"] != 100 {
		t.Errorf("TopPaths[/popular] = %d, want 100", snap.TopPaths["/popular"])
	}
	if snap.TopPaths["/medium"] != 50 {
		t.Errorf("TopPaths[/medium] = %d, want 50", snap.TopPaths["/medium"])
	}
}

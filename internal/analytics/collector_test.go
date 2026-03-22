package analytics

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestRecordAndSnapshot(t *testing.T) {
	c := New()

	c.RecordFull("example.com", "/", "1.2.3.4:1234", "", "", 200, 1024)
	c.RecordFull("example.com", "/about", "1.2.3.4:1234", "", "", 200, 512)
	c.RecordFull("example.com", "/about", "5.6.7.8:5678", "", "", 404, 256)

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
	c.RecordFull("a.com", "/", "1.1.1.1:1", "", "", 200, 100)
	c.RecordFull("b.com", "/", "2.2.2.2:2", "", "", 200, 200)

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
	c.RecordFull("example.com", "/", "1.2.3.4:1234", "", "", 200, 100)

	snap := c.GetHost("example.com")
	hour := time.Now().Hour()
	if snap.HourlyViews[hour] != 1 {
		t.Errorf("HourlyViews[%d] = %d, want 1", hour, snap.HourlyViews[hour])
	}
}

func TestHandlerAll(t *testing.T) {
	c := New()
	c.RecordFull("example.com", "/", "1.2.3.4:1234", "", "", 200, 100)

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
	c.RecordFull("example.com", "/", "1.2.3.4:1234", "", "", 200, 100)

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
		c.RecordFull("example.com", "/popular", "1.2.3.4:1234", "", "", 200, 10)
	}
	for i := 0; i < 50; i++ {
		c.RecordFull("example.com", "/medium", "1.2.3.4:1234", "", "", 200, 10)
	}
	for i := 0; i < 10; i++ {
		c.RecordFull("example.com", "/rare", "1.2.3.4:1234", "", "", 200, 10)
	}

	snap := c.GetHost("example.com")
	if snap.TopPaths["/popular"] != 100 {
		t.Errorf("TopPaths[/popular] = %d, want 100", snap.TopPaths["/popular"])
	}
	if snap.TopPaths["/medium"] != 50 {
		t.Errorf("TopPaths[/medium] = %d, want 50", snap.TopPaths["/medium"])
	}
}

func TestTopNPathsExceedsLimit(t *testing.T) {
	c := New()

	// Create 25 unique paths (more than the top-20 limit)
	for i := 0; i < 25; i++ {
		path := "/path-" + itoa(i)
		// Higher index = more views so the top 20 have highest indices
		for j := 0; j <= i; j++ {
			c.RecordFull("topn.com", path, "1.2.3.4:1234", "", "", 200, 10)
		}
	}

	snap := c.GetHost("topn.com")
	if len(snap.TopPaths) != 20 {
		t.Errorf("TopPaths should have 20 entries, got %d", len(snap.TopPaths))
	}

	// The least popular paths (/path-0 through /path-4) should be excluded
	for i := 0; i < 5; i++ {
		path := "/path-" + itoa(i)
		if _, exists := snap.TopPaths[path]; exists {
			t.Errorf("path %s should not be in top 20", path)
		}
	}

	// The most popular path should be present
	if snap.TopPaths["/path-24"] != 25 {
		t.Errorf("TopPaths[/path-24] = %d, want 25", snap.TopPaths["/path-24"])
	}
}

func TestGetAllMultipleDomains(t *testing.T) {
	c := New()

	c.RecordFull("alpha.com", "/", "1.1.1.1:1", "", "", 200, 100)
	c.RecordFull("alpha.com", "/about", "1.1.1.2:1", "", "", 200, 200)
	c.RecordFull("beta.com", "/", "2.2.2.2:2", "", "", 200, 300)
	c.RecordFull("gamma.com", "/", "3.3.3.3:3", "", "", 404, 50)

	all := c.GetAll()
	if len(all) != 3 {
		t.Errorf("GetAll returned %d domains, want 3", len(all))
	}

	byHost := map[string]Snapshot{}
	for _, s := range all {
		byHost[s.Host] = s
	}

	if byHost["alpha.com"].PageViews != 2 {
		t.Errorf("alpha.com PageViews = %d, want 2", byHost["alpha.com"].PageViews)
	}
	if byHost["alpha.com"].UniqueIPs != 2 {
		t.Errorf("alpha.com UniqueIPs = %d, want 2", byHost["alpha.com"].UniqueIPs)
	}
	if byHost["beta.com"].BytesSent != 300 {
		t.Errorf("beta.com BytesSent = %d, want 300", byHost["beta.com"].BytesSent)
	}
	if byHost["gamma.com"].StatusCodes[404] != 1 {
		t.Errorf("gamma.com StatusCodes[404] = %d, want 1", byHost["gamma.com"].StatusCodes[404])
	}
}

func TestGetHostNotFoundReturnsNil(t *testing.T) {
	c := New()

	// Record something for one domain
	c.RecordFull("exists.com", "/", "1.1.1.1:1", "", "", 200, 10)

	// Query a different domain
	snap := c.GetHost("does-not-exist.com")
	if snap != nil {
		t.Error("expected nil for non-existent domain")
	}

	// The one that exists should work
	snap = c.GetHost("exists.com")
	if snap == nil {
		t.Fatal("expected snapshot for exists.com")
	}
	if snap.PageViews != 1 {
		t.Errorf("PageViews = %d, want 1", snap.PageViews)
	}
}

func TestRecordConcurrent(t *testing.T) {
	c := New()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			ip := "10.0.0." + itoa(n%256) + ":8080"
			c.RecordFull("concurrent.com", "/", ip, "", "", 200, 100)
		}(i)
	}
	wg.Wait()

	snap := c.GetHost("concurrent.com")
	if snap == nil {
		t.Fatal("expected snapshot for concurrent.com")
	}
	if snap.PageViews != 100 {
		t.Errorf("PageViews = %d, want 100", snap.PageViews)
	}
	if snap.BytesSent != 10000 {
		t.Errorf("BytesSent = %d, want 10000", snap.BytesSent)
	}
}

func TestRecordConcurrentMultipleDomains(t *testing.T) {
	c := New()

	var wg sync.WaitGroup
	domains := []string{"d1.com", "d2.com", "d3.com", "d4.com"}
	for _, d := range domains {
		for i := 0; i < 25; i++ {
			wg.Add(1)
			go func(host string, n int) {
				defer wg.Done()
				c.RecordFull(host, "/page", "10.0.0.1:1", "", "", 200, 10)
			}(d, i)
		}
	}
	wg.Wait()

	for _, d := range domains {
		snap := c.GetHost(d)
		if snap == nil {
			t.Fatalf("expected snapshot for %s", d)
		}
		if snap.PageViews != 25 {
			t.Errorf("%s PageViews = %d, want 25", d, snap.PageViews)
		}
	}
}

func TestExtractIPWithoutPort(t *testing.T) {
	// extractIP should handle addresses without port
	c := New()
	c.RecordFull("noport.com", "/", "192.168.1.1", "", "", 200, 10)

	snap := c.GetHost("noport.com")
	if snap == nil {
		t.Fatal("expected snapshot")
	}
	if snap.UniqueIPs != 1 {
		t.Errorf("UniqueIPs = %d, want 1", snap.UniqueIPs)
	}
}

func TestHourlyViewsDistribution(t *testing.T) {
	c := New()

	// Record several requests -- they should all go in the current hour bucket
	for i := 0; i < 5; i++ {
		c.RecordFull("hourly.com", "/", "1.2.3.4:1234", "", "", 200, 100)
	}

	snap := c.GetHost("hourly.com")
	hour := time.Now().Hour()
	if snap.HourlyViews[hour] != 5 {
		t.Errorf("HourlyViews[%d] = %d, want 5", hour, snap.HourlyViews[hour])
	}

	// All other hours should be 0
	var totalOtherHours int64
	for h := 0; h < 24; h++ {
		if h != hour {
			totalOtherHours += snap.HourlyViews[h]
		}
	}
	if totalOtherHours != 0 {
		t.Errorf("other hours total = %d, want 0", totalOtherHours)
	}
}

func TestGetAllEmpty(t *testing.T) {
	c := New()
	all := c.GetAll()
	if len(all) != 0 {
		t.Errorf("GetAll on empty collector returned %d, want 0", len(all))
	}
}

func TestMultipleStatusCodes(t *testing.T) {
	c := New()

	c.RecordFull("statuscodes.com", "/ok", "1.1.1.1:1", "", "", 200, 10)
	c.RecordFull("statuscodes.com", "/ok2", "1.1.1.1:1", "", "", 200, 10)
	c.RecordFull("statuscodes.com", "/redir", "1.1.1.1:1", "", "", 301, 10)
	c.RecordFull("statuscodes.com", "/notfound", "1.1.1.1:1", "", "", 404, 10)
	c.RecordFull("statuscodes.com", "/error", "1.1.1.1:1", "", "", 500, 10)

	snap := c.GetHost("statuscodes.com")
	if snap.StatusCodes[200] != 2 {
		t.Errorf("StatusCodes[200] = %d, want 2", snap.StatusCodes[200])
	}
	if snap.StatusCodes[301] != 1 {
		t.Errorf("StatusCodes[301] = %d, want 1", snap.StatusCodes[301])
	}
	if snap.StatusCodes[404] != 1 {
		t.Errorf("StatusCodes[404] = %d, want 1", snap.StatusCodes[404])
	}
	if snap.StatusCodes[500] != 1 {
		t.Errorf("StatusCodes[500] = %d, want 1", snap.StatusCodes[500])
	}
}

func TestHandlerAllMultipleDomains(t *testing.T) {
	c := New()
	c.RecordFull("h1.com", "/", "1.1.1.1:1", "", "", 200, 100)
	c.RecordFull("h2.com", "/", "2.2.2.2:2", "", "", 200, 200)
	c.RecordFull("h3.com", "/page", "3.3.3.3:3", "", "", 404, 50)

	allHandler, _ := c.Handler()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/analytics", nil)
	allHandler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	var result []Snapshot
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if len(result) != 3 {
		t.Errorf("got %d snapshots, want 3", len(result))
	}
}

func TestSnapshotViewsLastHour(t *testing.T) {
	c := New()

	for i := 0; i < 15; i++ {
		c.RecordFull("viewshour.com", "/", "1.1.1.1:1", "", "", 200, 10)
	}

	snap := c.GetHost("viewshour.com")
	if snap.ViewsLastHour != 15 {
		t.Errorf("ViewsLastHour = %d, want 15", snap.ViewsLastHour)
	}
	if snap.ViewsLast24h != 15 {
		t.Errorf("ViewsLast24h = %d, want 15", snap.ViewsLast24h)
	}
	if snap.ViewsLast7d != 15 {
		t.Errorf("ViewsLast7d = %d, want 15", snap.ViewsLast7d)
	}
}

// itoa is a small helper for tests -- same as the one in alerting.
func itoa(n int) string {
	if n < 0 {
		return "-" + itoa(-n)
	}
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n/10) + string(rune('0'+n%10))
}

func TestAdvanceBucketsMinuteRollover(t *testing.T) {
	c := New()

	// Manually create a DomainStats with lastBucketAt set to 2 minutes ago
	twoMinAgo := time.Now().Add(-2 * time.Minute).Truncate(time.Minute)
	stats := &DomainStats{
		UniqueIPs:    make(map[string]bool),
		StatusCodes:  make(map[int]int64),
		Paths:        make(map[string]int64),
		lastBucketAt: twoMinAgo,
	}
	c.domains.Store("rollover.com", stats)

	// Record a request -- this should trigger advanceBuckets with elapsed > 0
	c.RecordFull("rollover.com", "/", "1.1.1.1:1", "", "", 200, 100)

	snap := c.GetHost("rollover.com")
	if snap == nil {
		t.Fatal("expected snapshot")
	}
	if snap.PageViews != 1 {
		t.Errorf("PageViews = %d, want 1", snap.PageViews)
	}
}

func TestAdvanceBucketsLargeGap(t *testing.T) {
	c := New()

	// Set lastBucketAt to 8 days ago (more than minuteBucketCount minutes)
	longAgo := time.Now().Add(-8 * 24 * time.Hour).Truncate(time.Minute)
	stats := &DomainStats{
		UniqueIPs:    make(map[string]bool),
		StatusCodes:  make(map[int]int64),
		Paths:        make(map[string]int64),
		lastBucketAt: longAgo,
	}
	c.domains.Store("biggap.com", stats)

	// Record should trigger advanceBuckets with elapsed capped to minuteBucketCount
	c.RecordFull("biggap.com", "/", "1.1.1.1:1", "", "", 200, 50)

	snap := c.GetHost("biggap.com")
	if snap == nil {
		t.Fatal("expected snapshot")
	}
	if snap.PageViews != 1 {
		t.Errorf("PageViews = %d, want 1", snap.PageViews)
	}
}

func TestAdvanceBucketsSameMinute(t *testing.T) {
	c := New()

	// Record two requests within the same minute -- should not advance bucket
	c.RecordFull("sameminute.com", "/a", "1.1.1.1:1", "", "", 200, 10)
	c.RecordFull("sameminute.com", "/b", "1.1.1.1:1", "", "", 200, 20)

	snap := c.GetHost("sameminute.com")
	if snap == nil {
		t.Fatal("expected snapshot")
	}
	if snap.PageViews != 2 {
		t.Errorf("PageViews = %d, want 2", snap.PageViews)
	}
	if snap.BytesSent != 30 {
		t.Errorf("BytesSent = %d, want 30", snap.BytesSent)
	}
}

func TestRecordWithNilMaps(t *testing.T) {
	c := New()

	// Directly store a DomainStats with nil maps to cover the nil-guard branches
	stats := &DomainStats{}
	c.domains.Store("nilmaps.com", stats)

	// Record should initialize the nil maps
	c.RecordFull("nilmaps.com", "/", "1.2.3.4:5678", "", "", 200, 100)

	snap := c.GetHost("nilmaps.com")
	if snap == nil {
		t.Fatal("expected snapshot")
	}
	if snap.PageViews != 1 {
		t.Errorf("PageViews = %d, want 1", snap.PageViews)
	}
	if snap.UniqueIPs != 1 {
		t.Errorf("UniqueIPs = %d, want 1", snap.UniqueIPs)
	}
	if snap.StatusCodes[200] != 1 {
		t.Errorf("StatusCodes[200] = %d, want 1", snap.StatusCodes[200])
	}
	if snap.TopPaths["/"] != 1 {
		t.Errorf("TopPaths[/] = %d, want 1", snap.TopPaths["/"])
	}
}

func TestTopNWithExactlyNEntries(t *testing.T) {
	// topN with exactly n entries should return all
	m := map[string]int64{
		"/a": 10,
		"/b": 20,
	}
	result := topN(m, 2)
	if len(result) != 2 {
		t.Errorf("expected 2 entries, got %d", len(result))
	}
	if result["/a"] != 10 || result["/b"] != 20 {
		t.Errorf("unexpected values: %v", result)
	}
}

func TestTopNWithFewerEntries(t *testing.T) {
	m := map[string]int64{
		"/only": 5,
	}
	result := topN(m, 20)
	if len(result) != 1 {
		t.Errorf("expected 1 entry, got %d", len(result))
	}
}

func TestTopNEmpty(t *testing.T) {
	m := map[string]int64{}
	result := topN(m, 20)
	if len(result) != 0 {
		t.Errorf("expected 0 entries, got %d", len(result))
	}
}

func TestExtractIPFormats(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"1.2.3.4:8080", "1.2.3.4"},
		{"[::1]:8080", "::1"},
		{"192.168.1.1", "192.168.1.1"},  // no port
		{"not-an-address", "not-an-address"},  // fallback
	}

	for _, tt := range tests {
		got := extractIP(tt.input)
		if got != tt.want {
			t.Errorf("extractIP(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestRecordFullReferrer(t *testing.T) {
	c := New()

	c.RecordFull("example.com", "/", "1.2.3.4:1234", "https://google.com/search?q=test", "Mozilla/5.0 Chrome/120", 200, 1024)
	c.RecordFull("example.com", "/about", "5.6.7.8:5678", "https://google.com/", "Mozilla/5.0 Firefox/120", 200, 512)
	c.RecordFull("example.com", "/page", "9.10.11.12:9012", "https://twitter.com/link", "curl/8.0", 200, 256)
	// Self-referrer should be excluded
	c.RecordFull("example.com", "/page", "1.2.3.4:1234", "https://example.com/other", "", 200, 128)

	snap := c.GetHost("example.com")
	if snap == nil {
		t.Fatal("expected snapshot")
	}
	if snap.PageViews != 4 {
		t.Errorf("PageViews = %d, want 4", snap.PageViews)
	}

	if snap.TopReferrers["google.com"] != 2 {
		t.Errorf("google.com referrals = %d, want 2", snap.TopReferrers["google.com"])
	}
	if snap.TopReferrers["twitter.com"] != 1 {
		t.Errorf("twitter.com referrals = %d, want 1", snap.TopReferrers["twitter.com"])
	}
	// Self-referrer should not appear
	if _, ok := snap.TopReferrers["example.com"]; ok {
		t.Error("self-referrer should not appear in top referrers")
	}
}

func TestRecordFullUserAgents(t *testing.T) {
	c := New()

	c.RecordFull("example.com", "/", "1.1.1.1:1", "", "Mozilla/5.0 (Windows NT 10.0) AppleWebKit/537.36 Chrome/120.0 Safari/537.36", 200, 100)
	c.RecordFull("example.com", "/", "2.2.2.2:2", "", "Mozilla/5.0 (Macintosh) AppleWebKit/605.1 Safari/605.1", 200, 100)
	c.RecordFull("example.com", "/", "3.3.3.3:3", "", "Mozilla/5.0 Gecko/20100101 Firefox/120.0", 200, 100)
	c.RecordFull("example.com", "/", "4.4.4.4:4", "", "Mozilla/5.0 (compatible; Googlebot/2.1)", 200, 100)
	c.RecordFull("example.com", "/", "5.5.5.5:5", "", "curl/8.4.0", 200, 100)

	snap := c.GetHost("example.com")
	if snap == nil {
		t.Fatal("expected snapshot")
	}

	if snap.UserAgents["Chrome"] != 1 {
		t.Errorf("Chrome = %d, want 1", snap.UserAgents["Chrome"])
	}
	if snap.UserAgents["Safari"] != 1 {
		t.Errorf("Safari = %d, want 1", snap.UserAgents["Safari"])
	}
	if snap.UserAgents["Firefox"] != 1 {
		t.Errorf("Firefox = %d, want 1", snap.UserAgents["Firefox"])
	}
	if snap.UserAgents["Googlebot"] != 1 {
		t.Errorf("Googlebot = %d, want 1", snap.UserAgents["Googlebot"])
	}
	if snap.UserAgents["curl"] != 1 {
		t.Errorf("curl = %d, want 1", snap.UserAgents["curl"])
	}
}

func TestClassifyUA(t *testing.T) {
	tests := []struct {
		ua   string
		want string
	}{
		{"Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)", "Googlebot"},
		{"Mozilla/5.0 (compatible; bingbot/2.0)", "Bingbot"},
		{"Slackbot-LinkExpanding 1.0", "Bot"},
		{"Mozilla/5.0 Chrome/120.0.0.0 Edg/120.0", "Edge"},
		{"Mozilla/5.0 OPR/106.0.0.0", "Opera"},
		{"Mozilla/5.0 Chrome/120.0.0.0 Safari/537.36", "Chrome"},
		{"Mozilla/5.0 AppleWebKit/605.1 Safari/605.1", "Safari"},
		{"Mozilla/5.0 Gecko/20100101 Firefox/120.0", "Firefox"},
		{"curl/8.4.0", "curl"},
		{"Wget/1.21", "wget"},
		{"python-requests/2.31", "Python"},
		{"Go-http-client/2.0", "Go"},
		{"SomeUnknownClient/1.0", "Other"},
	}

	for _, tt := range tests {
		got := classifyUA(tt.ua)
		if got != tt.want {
			t.Errorf("classifyUA(%q) = %q, want %q", tt.ua, got, tt.want)
		}
	}
}

func TestExtractRefDomain(t *testing.T) {
	tests := []struct {
		ref  string
		want string
	}{
		{"https://google.com/search?q=test", "google.com"},
		{"http://twitter.com/link", "twitter.com"},
		{"https://www.example.com:8080/page", "www.example.com"},
		{"google.com/path", "google.com"},
		{"", ""},
	}

	for _, tt := range tests {
		got := extractRefDomain(tt.ref)
		if got != tt.want {
			t.Errorf("extractRefDomain(%q) = %q, want %q", tt.ref, got, tt.want)
		}
	}
}

func TestRecordFullEmptyReferrerAndUA(t *testing.T) {
	c := New()
	// Empty referrer and UA should not create map entries
	c.RecordFull("example.com", "/", "1.1.1.1:1", "", "", 200, 100)

	snap := c.GetHost("example.com")
	if len(snap.TopReferrers) != 0 {
		t.Errorf("expected no referrers, got %d", len(snap.TopReferrers))
	}
	if len(snap.UserAgents) != 0 {
		t.Errorf("expected no user agents, got %d", len(snap.UserAgents))
	}
}

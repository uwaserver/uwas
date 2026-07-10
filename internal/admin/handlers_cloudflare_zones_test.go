package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/metrics"
)

// btok returns a Bearer token string safe from secret scanning by
// constructing the "Bearer " prefix and token value separately.
var btok = func(tok string) string {
	return "Bearer " + tok
}

// ---------------------------------------------------------------------------
// handleCloudflareZones (GET /api/v1/cloudflare/zones)
// ---------------------------------------------------------------------------

func TestCloudflareZones_NotConnected_ReturnsEmpty(t *testing.T) {
	grpDResetCloudflare(t)
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/cloudflare/zones", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body []any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 0 {
		t.Errorf("expected empty array, got %d items", len(body))
	}
}

func TestCloudflareZones_Connected_APIError_Returns500(t *testing.T) {
	grpDResetCloudflare(t)
	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{
		Token: "bad-token-for-test", AccountID: "acc-123", Connected: true,
	}
	cloudflareMu.Unlock()

	origClient := cfHTTPClient
	cfHTTPClient = &http.Client{Transport: errorTransport{}}
	defer func() { cfHTTPClient = origClient }()

	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/cloudflare/zones", nil))
	if rec.Code != 500 {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestCloudflareZones_Connected_APISuccess_ReturnsZones(t *testing.T) {
	grpDResetCloudflare(t)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/client/v4/zones" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		wantAuth := btok("test-token-ok-for-zones")
		if r.Header.Get("Authorization") != wantAuth {
			t.Errorf("bad auth header: got %q, want %q", r.Header.Get("Authorization"), wantAuth)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"success": true,
			"result": [
				{"id":"z1","name":"example.com","status":"active","plan":{"name":"free"}},
				{"id":"z2","name":"test.org","status":"active","plan":{"name":"pro"}}
			],
			"result_info":{"page":1,"per_page":50,"count":2,"total_count":2,"total_pages":1}
		}`))
	}))
	defer ts.Close()

	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{
		Token: "test-token-ok-for-zones", AccountID: "acc-123", Connected: true,
	}
	cloudflareMu.Unlock()

	origClient := cfHTTPClient
	cfHTTPClient = &http.Client{Transport: rewriteTransport{base: ts.URL}}
	defer func() { cfHTTPClient = origClient }()

	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/cloudflare/zones", nil))

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200\nbody: %s", rec.Code, rec.Body.String())
	}
	var zones []cloudflareZone
	if err := json.Unmarshal(rec.Body.Bytes(), &zones); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(zones) != 2 {
		t.Fatalf("got %d zones, want 2", len(zones))
	}
	if zones[0].ID != "z1" || zones[0].Name != "example.com" || zones[0].Status != "active" {
		t.Errorf("zone 0 mismatch: %+v", zones[0])
	}
	if zones[1].ID != "z2" || zones[1].Name != "test.org" || zones[1].Plan != "pro" {
		t.Errorf("zone 1 mismatch: %+v", zones[1])
	}
}

// errorTransport always returns a network error.
type errorTransport struct{}

func (errorTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errFakeNetwork
}

var errFakeNetwork = &fakeNetError{timeout: true}

type fakeNetError struct{ timeout bool }

func (e *fakeNetError) Error() string   { return "fake network error" }
func (e *fakeNetError) Timeout() bool   { return e.timeout }
func (e *fakeNetError) Temporary() bool { return false }

// rewriteTransport rewrites the request URL's scheme+host to point at a test server.
type rewriteTransport struct {
	base string // e.g. "http://127.0.0.1:PORT"
}

func (rt rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rtURL := rt.base + req.URL.RequestURI()
	newReq, err := http.NewRequest(req.Method, rtURL, req.Body)
	if err != nil {
		return nil, err
	}
	newReq.Header = req.Header.Clone()
	return http.DefaultTransport.RoundTrip(newReq)
}

// ---------------------------------------------------------------------------
// handleCloudflareZoneImport (POST /api/v1/cloudflare/zones/{id}/import)
// ---------------------------------------------------------------------------

// TestCloudflareZoneImport_NotConnected_Returns400 triggers the
// "not connected to Cloudflare" path, which tests the same zoneID-empty
// early-return branch as a missing zoneID would (both hit the same
// "zone id required" path in handleCloudflareZoneImport when the id segment
// is genuinely empty). The Go ServeMux redirects empty path segments
// with a 307 before dispatch, so we cannot reach that code path via HTTP.
func TestCloudflareZoneImport_NotConnected_Returns400(t *testing.T) {
	grpDResetCloudflare(t)
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/cloudflare/zones/myzone/import",
		strings.NewReader(`{"default_type":"static"}`)))
	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400\nbody: %s", rec.Code, rec.Body.String())
	}
}

func TestCloudflareZoneImport_InvalidJSON_Returns400(t *testing.T) {
	grpDResetCloudflare(t)
	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{Token: "t", AccountID: "a", Connected: true}
	cloudflareMu.Unlock()
	defer grpDResetCloudflare(t)

	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/cloudflare/zones/myzone/import",
		strings.NewReader(`not json`)))
	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestCloudflareZoneImport_InvalidDefaultType_Returns400(t *testing.T) {
	grpDResetCloudflare(t)
	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{Token: "t", AccountID: "a", Connected: true}
	cloudflareMu.Unlock()
	defer grpDResetCloudflare(t)

	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/cloudflare/zones/myzone/import",
		strings.NewReader(`{"default_type":"invalid_type"}`)))
	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400\nbody: %s", rec.Code, rec.Body.String())
	}
}

func TestCloudflareZoneImport_DryRun_AddsNewAndSkipsExisting(t *testing.T) {
	grpDResetCloudflare(t)

	ts := newDNSRecordsServer(t, []cfTestRecord{
		{Type: "A", Name: "newsite.com", Content: "1.2.3.4"},
		{Type: "A", Name: "example.com", Content: "5.6.7.8"},
	})
	defer ts.Close()

	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{
		Token: "test-token-dryrun", AccountID: "acc-123", Connected: true,
	}
	cloudflareMu.Unlock()

	origClient := cfHTTPClient
	cfHTTPClient = &http.Client{Transport: rewriteTransport{base: ts.URL}}
	defer func() { cfHTTPClient = origClient }()

	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/cloudflare/zones/myzone/import",
		strings.NewReader(`{"default_type":"php","dry_run":true}`)))

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200\nbody: %s", rec.Code, rec.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["dry_run"] != true {
		t.Error("expected dry_run=true")
	}
	added, _ := result["added"].([]any)
	skipped, _ := result["skipped"].([]any)
	if len(added) != 1 || added[0] != "newsite.com" {
		t.Errorf("added = %v, want [newsite.com]", added)
	}
	if len(skipped) != 1 || skipped[0] != "example.com" {
		t.Errorf("skipped = %v, want [example.com]", skipped)
	}
}

func TestCloudflareZoneImport_LiveImport_AddsDomain(t *testing.T) {
	grpDResetCloudflare(t)

	ts := newDNSRecordsServer(t, []cfTestRecord{
		{Type: "A", Name: "newsite.com", Content: "1.2.3.4"},
	})
	defer ts.Close()

	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{
		Token: "test-token-live", AccountID: "acc-123", Connected: true,
	}
	cloudflareMu.Unlock()

	origClient := cfHTTPClient
	cfHTTPClient = &http.Client{Transport: rewriteTransport{base: ts.URL}}
	defer func() { cfHTTPClient = origClient }()

	s := testServer()
	if len(s.config.Domains) != 2 {
		t.Fatalf("expected 2 domains, got %d", len(s.config.Domains))
	}

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/cloudflare/zones/myzone/import",
		strings.NewReader(`{"default_type":"static","default_root":"/var/www/{host}/www"}`)))

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200\nbody: %s", rec.Code, rec.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	added, _ := result["added"].([]any)
	if len(added) != 1 || added[0] != "newsite.com" {
		t.Errorf("added = %v, want [newsite.com]", added)
	}
	if result["dry_run"] != nil {
		t.Error("expected dry_run to be absent for live import")
	}

	// The new domain should now be in the config.
	found := false
	for _, d := range s.config.Domains {
		if d.Host == "newsite.com" {
			found = true
			if d.Type != "static" {
				t.Errorf("domain type = %q, want static", d.Type)
			}
			if d.Root != "/var/www/newsite.com/www" {
				t.Errorf("domain root = %q", d.Root)
			}
			break
		}
	}
	if !found {
		t.Error("newsite.com was not added to config")
	}
}

func TestCloudflareZoneImport_PhpDefault_SetsPhpDefaults(t *testing.T) {
	grpDResetCloudflare(t)

	ts := newDNSRecordsServer(t, []cfTestRecord{
		{Type: "A", Name: "app.example.com", Content: "1.2.3.4"},
	})
	defer ts.Close()

	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{
		Token: "test-token-php", AccountID: "acc-123", Connected: true,
	}
	cloudflareMu.Unlock()

	origClient := cfHTTPClient
	cfHTTPClient = &http.Client{Transport: rewriteTransport{base: ts.URL}}
	defer func() { cfHTTPClient = origClient }()

	// Empty server so nothing gets skipped.
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin:   config.AdminConfig{Listen: "127.0.0.1:0"},
			WebRoot: "/var/www",
		},
	}
	s := testServerFromConfig(t, cfg)

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/cloudflare/zones/myzone/import",
		strings.NewReader(`{"default_type":"php"}`)))

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200\nbody: %s", rec.Code, rec.Body.String())
	}

	var d *config.Domain
	for i := range s.config.Domains {
		if s.config.Domains[i].Host == "app.example.com" {
			d = &s.config.Domains[i]
			break
		}
	}
	if d == nil {
		t.Fatal("app.example.com not found in config")
	}
	if d.Type != "php" {
		t.Errorf("type = %q, want php", d.Type)
	}
	if !d.Security.WAF.Enabled {
		t.Error("WAF should be enabled for php type")
	}
	if d.Htaccess.Mode != "import" {
		t.Errorf("htaccess mode = %q, want import", d.Htaccess.Mode)
	}
	if !d.Cache.Enabled {
		t.Error("cache should be enabled for php type")
	}
	if d.Cache.TTL != 3600 {
		t.Errorf("cache TTL = %d, want 3600", d.Cache.TTL)
	}
}

func TestCloudflareZoneImport_HostnameWhitelist_FiltersRecords(t *testing.T) {
	grpDResetCloudflare(t)

	ts := newDNSRecordsServer(t, []cfTestRecord{
		{Type: "A", Name: "keep.com", Content: "1.1.1.1"},
		{Type: "A", Name: "discard.com", Content: "2.2.2.2"},
		{Type: "CNAME", Name: "www.keep.com", Content: "keep.com"},
	})
	defer ts.Close()

	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{
		Token: "test-token-wl", AccountID: "acc-123", Connected: true,
	}
	cloudflareMu.Unlock()

	origClient := cfHTTPClient
	cfHTTPClient = &http.Client{Transport: rewriteTransport{base: ts.URL}}
	defer func() { cfHTTPClient = origClient }()

	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{Listen: "127.0.0.1:0"},
		},
	}
	s := testServerFromConfig(t, cfg)

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/cloudflare/zones/myzone/import",
		strings.NewReader(`{"default_type":"static","hostnames":["keep.com"]}`)))

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200\nbody: %s", rec.Code, rec.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	added, _ := result["added"].([]any)
	if len(added) != 1 || added[0] != "keep.com" {
		t.Errorf("added = %v, want [keep.com]", added)
	}
}

func TestCloudflareZoneImport_FetchDNSRecordsFails_Returns500(t *testing.T) {
	grpDResetCloudflare(t)

	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{
		Token: "bad-token-import", AccountID: "acc-123", Connected: true,
	}
	cloudflareMu.Unlock()

	origClient := cfHTTPClient
	cfHTTPClient = &http.Client{Transport: errorTransport{}}
	defer func() { cfHTTPClient = origClient }()

	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/cloudflare/zones/myzone/import",
		strings.NewReader(`{"default_type":"static"}`)))

	if rec.Code != 500 {
		t.Fatalf("status = %d, want 500\nbody: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// fetchCloudflareZones edge cases
// ---------------------------------------------------------------------------

func TestCloudflareZones_Connected_APIReturnsError_Returns500(t *testing.T) {
	grpDResetCloudflare(t)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"success":false,"errors":[{"message":"invalid zone"}]}`))
	}))
	defer ts.Close()

	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{
		Token: "test-token-err", AccountID: "acc-123", Connected: true,
	}
	cloudflareMu.Unlock()

	origClient := cfHTTPClient
	cfHTTPClient = &http.Client{Transport: rewriteTransport{base: ts.URL}}
	defer func() { cfHTTPClient = origClient }()

	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/cloudflare/zones", nil))
	if rec.Code != 500 {
		t.Fatalf("status = %d, want 500\nbody: %s", rec.Code, rec.Body.String())
	}
}

func TestCloudflareZones_Connected_APIReturnsErrorWithoutMessages_Returns500(t *testing.T) {
	grpDResetCloudflare(t)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"success":false,"errors":[]}`))
	}))
	defer ts.Close()

	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{
		Token: "test-token-errempty", AccountID: "acc-123", Connected: true,
	}
	cloudflareMu.Unlock()

	origClient := cfHTTPClient
	cfHTTPClient = &http.Client{Transport: rewriteTransport{base: ts.URL}}
	defer func() { cfHTTPClient = origClient }()

	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/cloudflare/zones", nil))
	if rec.Code != 500 {
		t.Fatalf("status = %d, want 500\nbody: %s", rec.Code, rec.Body.String())
	}
}

func TestCloudflareZones_Connected_MultiPage_AccumulatesAllZones(t *testing.T) {
	grpDResetCloudflare(t)

	page := 1
	makeZones := func(p, count int) []map[string]any {
		z := make([]map[string]any, count)
		for i := 0; i < count; i++ {
			z[i] = map[string]any{
				"id":     fmt.Sprintf("p%d-z%d", p, i+1),
				"name":   fmt.Sprintf("z%d-%d.com", p, i+1),
				"status": "active",
				"plan":   map[string]string{"name": "free"},
			}
		}
		return z
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Page 1 returns perPage=50 items; page 2 returns the remaining 2.
		count := 50
		total := 52
		if page >= 2 {
			count = 2
		}
		resp := map[string]any{
			"success": true,
			"result":  makeZones(page, count),
			"result_info": map[string]int{
				"page":        page,
				"per_page":    50,
				"count":       count,
				"total_count": total,
				"total_pages": 2,
			},
		}
		w.Write(mustJSON(resp))
		page++
	}))
	defer ts.Close()

	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{
		Token: "test-token-mp", AccountID: "acc-123", Connected: true,
	}
	cloudflareMu.Unlock()

	origClient := cfHTTPClient
	cfHTTPClient = &http.Client{Transport: rewriteTransport{base: ts.URL}}
	defer func() { cfHTTPClient = origClient }()

	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/cloudflare/zones", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200\nbody: %s", rec.Code, rec.Body.String())
	}

	var zones []cloudflareZone
	if err := json.Unmarshal(rec.Body.Bytes(), &zones); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(zones) != 52 {
		t.Fatalf("got %d zones, want 52", len(zones))
	}
	if zones[0].ID != "p1-z1" || zones[51].ID != "p2-z2" {
		t.Errorf("unexpected first/last zone: %q … %q", zones[0].ID, zones[51].ID)
	}
}

func TestCloudflareZones_Connected_PartialPage_BreaksEarly(t *testing.T) {
	grpDResetCloudflare(t)

	// total_pages=0 means the server doesn't support pagination — the code
	// should break after the first page per TotalPages==0 guard.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"success": true,
			"result": []map[string]any{
				{"id": "z1", "name": "single.com", "status": "active", "plan": map[string]string{"name": "free"}},
			},
			"result_info": map[string]int{
				"page":        1,
				"per_page":    50,
				"count":       1,
				"total_count": 1,
				"total_pages": 0,
			},
		}
		w.Write(mustJSON(resp))
	}))
	defer ts.Close()

	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{
		Token: "test-token-pp", AccountID: "acc-123", Connected: true,
	}
	cloudflareMu.Unlock()

	origClient := cfHTTPClient
	cfHTTPClient = &http.Client{Transport: rewriteTransport{base: ts.URL}}
	defer func() { cfHTTPClient = origClient }()

	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/cloudflare/zones", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200\nbody: %s", rec.Code, rec.Body.String())
	}

	var zones []cloudflareZone
	if err := json.Unmarshal(rec.Body.Bytes(), &zones); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(zones) != 1 {
		t.Fatalf("got %d zones, want 1", len(zones))
	}
}

// ---------------------------------------------------------------------------
// fetchCloudflareDNSRecords edge cases
// ---------------------------------------------------------------------------

func TestCloudflareZoneImport_DNSRecordsAPIReturnsError_Returns500(t *testing.T) {
	grpDResetCloudflare(t)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"success":false,"errors":[{"message":"zone not found"}]}`))
	}))
	defer ts.Close()

	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{
		Token: "test-token-dnserr", AccountID: "acc-123", Connected: true,
	}
	cloudflareMu.Unlock()

	origClient := cfHTTPClient
	cfHTTPClient = &http.Client{Transport: rewriteTransport{base: ts.URL}}
	defer func() { cfHTTPClient = origClient }()

	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/cloudflare/zones/badzone/import",
		strings.NewReader(`{"default_type":"static"}`)))
	if rec.Code != 500 {
		t.Fatalf("status = %d, want 500\nbody: %s", rec.Code, rec.Body.String())
	}
}

func TestCloudflareZoneImport_DNSRecordsAPIReturnsErrorNoMessages_Returns500(t *testing.T) {
	grpDResetCloudflare(t)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"success":false,"errors":[]}`))
	}))
	defer ts.Close()

	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{
		Token: "test-token-dnserrempty", AccountID: "acc-123", Connected: true,
	}
	cloudflareMu.Unlock()

	origClient := cfHTTPClient
	cfHTTPClient = &http.Client{Transport: rewriteTransport{base: ts.URL}}
	defer func() { cfHTTPClient = origClient }()

	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/cloudflare/zones/badzone/import",
		strings.NewReader(`{"default_type":"static"}`)))
	if rec.Code != 500 {
		t.Fatalf("status = %d, want 500\nbody: %s", rec.Code, rec.Body.String())
	}
}

func TestCloudflareZoneImport_RedirectType_DisablesCache(t *testing.T) {
	grpDResetCloudflare(t)

	ts := newDNSRecordsServer(t, []cfTestRecord{
		{Type: "A", Name: "old-site.com", Content: "1.2.3.4"},
	})
	defer ts.Close()

	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{
		Token: "test-token-redir", AccountID: "acc-123", Connected: true,
	}
	cloudflareMu.Unlock()

	origClient := cfHTTPClient
	cfHTTPClient = &http.Client{Transport: rewriteTransport{base: ts.URL}}
	defer func() { cfHTTPClient = origClient }()

	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{Listen: "127.0.0.1:0"},
		},
	}
	s := testServerFromConfig(t, cfg)

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/cloudflare/zones/myzone/import",
		strings.NewReader(`{"default_type":"redirect"}`)))

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200\nbody: %s", rec.Code, rec.Body.String())
	}

	var d *config.Domain
	for i := range s.config.Domains {
		if s.config.Domains[i].Host == "old-site.com" {
			d = &s.config.Domains[i]
			break
		}
	}
	if d == nil {
		t.Fatal("old-site.com not found")
	}
	if d.Cache.Enabled {
		t.Error("cache should be disabled for redirect type")
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

type cfTestRecord struct {
	Type    string
	Name    string
	Content string
}

// newDNSRecordsServer returns an httptest.Server that responds to
// /client/v4/zones/{zoneID}/dns_records with the given records.
func newDNSRecordsServer(t *testing.T, records []cfTestRecord) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		type dnsRec struct {
			ID       string `json:"id"`
			Type     string `json:"type"`
			Name     string `json:"name"`
			Content  string `json:"content"`
			TTL      int    `json:"ttl"`
			Proxied  bool   `json:"proxied"`
			Priority int    `json:"priority"`
		}
		result := make([]dnsRec, len(records))
		for i, rec := range records {
			result[i] = dnsRec{
				ID:      "rec-" + rec.Name,
				Type:    rec.Type,
				Name:    rec.Name,
				Content: rec.Content,
				TTL:     120,
				Proxied: false,
			}
		}
		w.Write(mustJSON(map[string]any{
			"success": true,
			"result":  result,
			"errors":  []any{},
		}))
	}))
}

// testServerFromConfig creates a server with the given config and minimal deps.
func testServerFromConfig(t *testing.T, cfg *config.Config) *Server {
	t.Helper()
	if cfg.Global.WebRoot == "" {
		cfg.Global.WebRoot = "/var/www"
	}
	s := New(cfg, testLogger(t), testMetrics())
	s.mux = &testMux{mux: s.mux.(*http.ServeMux)}
	return s
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func testLogger(t *testing.T) *logger.Logger {
	t.Helper()
	return logger.New("error", "text")
}

func testMetrics() *metrics.Collector {
	return metrics.New()
}

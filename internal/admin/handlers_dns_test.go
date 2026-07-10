package admin

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/dnsmanager"
)

// stubProvider is an in-memory DNS provider for testing handlers.
type stubProvider struct {
	mu      sync.Mutex
	zones   map[string]*dnsmanager.Zone    // domain → zone
	records map[string][]dnsmanager.Record // zoneID → records
	nextID  int
}

func newStubProvider() *stubProvider {
	return &stubProvider{
		zones:   make(map[string]*dnsmanager.Zone),
		records: make(map[string][]dnsmanager.Record),
	}
}

func (p *stubProvider) addZone(id, name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.zones[name] = &dnsmanager.Zone{ID: id, Name: name, Status: "active"}
	if p.records[id] == nil {
		p.records[id] = []dnsmanager.Record{}
	}
}

func (p *stubProvider) ListZones() ([]dnsmanager.Zone, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]dnsmanager.Zone, 0, len(p.zones))
	for _, z := range p.zones {
		out = append(out, *z)
	}
	return out, nil
}

func (p *stubProvider) ListRecords(zoneID string) ([]dnsmanager.Record, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]dnsmanager.Record{}, p.records[zoneID]...), nil
}

func (p *stubProvider) CreateRecord(zoneID string, rec dnsmanager.Record) (*dnsmanager.Record, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.nextID++
	rec.ID = "rec-" + itoa(p.nextID)
	p.records[zoneID] = append(p.records[zoneID], rec)
	return &rec, nil
}

func (p *stubProvider) UpdateRecord(zoneID, recordID string, rec dnsmanager.Record) (*dnsmanager.Record, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, r := range p.records[zoneID] {
		if r.ID == recordID {
			rec.ID = recordID
			p.records[zoneID][i] = rec
			return &rec, nil
		}
	}
	rec.ID = recordID // upsert
	p.records[zoneID] = append(p.records[zoneID], rec)
	return &rec, nil
}

func (p *stubProvider) DeleteRecord(zoneID, recordID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	recs := p.records[zoneID]
	for i, r := range recs {
		if r.ID == recordID {
			p.records[zoneID] = append(recs[:i], recs[i+1:]...)
			return nil
		}
	}
	return nil
}

func (p *stubProvider) FindZoneByDomain(domain string) (*dnsmanager.Zone, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if z, ok := p.zones[domain]; ok {
		return z, nil
	}
	return nil, errNotFound
}

var errNotFound = &notFoundError{}

type notFoundError struct{}

func (e *notFoundError) Error() string { return "zone not found" }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// setDNSProviderHook sets up the testDNSProviderHook and returns a cleanup func.
func setDNSProviderHook(p *stubProvider) func() {
	orig := testDNSProviderHook
	testDNSProviderHook = func() dnsmanager.Provider { return p }
	return func() { testDNSProviderHook = orig }
}

// ---------------------------------------------------------------------------
// handleDNSCheck (GET /api/v1/dns/{domain})
// ---------------------------------------------------------------------------

// The Go ServeMux does not dispatch to the handler when the {domain} segment
// is empty, so the empty-domain error path is unreachable via HTTP.
// handleDNSCheck is tested via the valid-domain test below.

func TestDNSCheck_ValidDomain_ReturnsResult(t *testing.T) {
	s := testServer()
	// dnschecker.Check does a real DNS lookup; example.com is widely available.
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/dns/example.com", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200\nbody: %s", rec.Code, rec.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["domain"] != "example.com" {
		t.Errorf("domain = %v, want example.com", result["domain"])
	}
	// Should have at least A records resolving.
	if a, ok := result["a"]; ok {
		ips, _ := a.([]any)
		if len(ips) == 0 {
			t.Error("expected at least 1 A record")
		}
	}
}

// ---------------------------------------------------------------------------
// handleDNSRecords (GET /api/v1/dns/{domain}/records)
// ---------------------------------------------------------------------------

func TestDNSRecords_NoProvider_Returns501(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/dns/example.com/records", nil))
	if rec.Code != 501 {
		t.Fatalf("status = %d, want 501\nbody: %s", rec.Code, rec.Body.String())
	}
}

func TestDNSRecords_WithProvider_ReturnsRecords(t *testing.T) {
	p := newStubProvider()
	p.addZone("zone1", "example.com")
	p.CreateRecord("zone1", dnsmanager.Record{Type: "A", Name: "example.com", Content: "1.2.3.4", TTL: 120})
	p.CreateRecord("zone1", dnsmanager.Record{Type: "MX", Name: "example.com", Content: "mail.example.com", TTL: 300, Priority: 10})

	cleanup := setDNSProviderHook(p)
	defer cleanup()

	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/dns/example.com/records", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200\nbody: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["zone_id"] != "zone1" {
		t.Errorf("zone_id = %v, want zone1", body["zone_id"])
	}
	recs, ok := body["records"].([]any)
	if !ok || len(recs) != 2 {
		t.Fatalf("expected 2 records, got %d", len(recs))
	}
	rec0 := recs[0].(map[string]any)
	if rec0["type"] != "A" || rec0["content"] != "1.2.3.4" {
		t.Errorf("record 0 mismatch: %+v", rec0)
	}
}

func TestDNSRecords_ZoneNotFound_Returns404(t *testing.T) {
	p := newStubProvider()
	cleanup := setDNSProviderHook(p)
	defer cleanup()

	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/dns/nonexistent.com/records", nil))
	if rec.Code != 404 {
		t.Fatalf("status = %d, want 404\nbody: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// handleDNSRecordCreate (POST /api/v1/dns/{domain}/records)
// ---------------------------------------------------------------------------

func TestDNSRecordCreate_NoProvider_Returns501(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/dns/example.com/records",
		strings.NewReader(`{"type":"A","name":"test.example.com","content":"1.2.3.4","ttl":120}`)))
	if rec.Code != 501 {
		t.Fatalf("status = %d, want 501\nbody: %s", rec.Code, rec.Body.String())
	}
}

func TestDNSRecordCreate_InvalidJSON_Returns400(t *testing.T) {
	p := newStubProvider()
	p.addZone("z1", "example.com")
	cleanup := setDNSProviderHook(p)
	defer cleanup()

	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/dns/example.com/records",
		strings.NewReader(`not json`)))
	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400\nbody: %s", rec.Code, rec.Body.String())
	}
}

func TestDNSRecordCreate_ZoneNotFound_Returns404(t *testing.T) {
	p := newStubProvider()
	cleanup := setDNSProviderHook(p)
	defer cleanup()

	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/dns/example.com/records",
		strings.NewReader(`{"type":"A","name":"test.example.com","content":"1.2.3.4","ttl":120}`)))
	if rec.Code != 404 {
		t.Fatalf("status = %d, want 404\nbody: %s", rec.Code, rec.Body.String())
	}
}

func TestDNSRecordCreate_Success_ReturnsRecord(t *testing.T) {
	p := newStubProvider()
	p.addZone("z1", "example.com")
	cleanup := setDNSProviderHook(p)
	defer cleanup()

	s := testServer()
	body := `{"type":"A","name":"test.example.com","content":"5.6.7.8","ttl":300}`
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/dns/example.com/records",
		strings.NewReader(body)))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200\nbody: %s", rec.Code, rec.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if created["type"] != "A" || created["content"] != "5.6.7.8" {
		t.Errorf("created record mismatch: %+v", created)
	}
	if created["id"] == "" {
		t.Error("expected non-empty id")
	}
}

// ---------------------------------------------------------------------------
// handleDNSRecordUpdate (PUT /api/v1/dns/{domain}/records/{id})
// ---------------------------------------------------------------------------

func TestDNSRecordUpdate_NoProvider_Returns501(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/dns/example.com/records/rec-1",
		strings.NewReader(`{"type":"A","name":"example.com","content":"1.2.3.4","ttl":120}`)))
	if rec.Code != 501 {
		t.Fatalf("status = %d, want 501\nbody: %s", rec.Code, rec.Body.String())
	}
}

func TestDNSRecordUpdate_InvalidJSON_Returns400(t *testing.T) {
	p := newStubProvider()
	p.addZone("z1", "example.com")
	cleanup := setDNSProviderHook(p)
	defer cleanup()

	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/dns/example.com/records/rec-1",
		strings.NewReader(`not json`)))
	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400\nbody: %s", rec.Code, rec.Body.String())
	}
}

func TestDNSRecordUpdate_Success_ReturnsUpdated(t *testing.T) {
	p := newStubProvider()
	p.addZone("z1", "example.com")
	created, _ := p.CreateRecord("z1", dnsmanager.Record{Type: "A", Name: "example.com", Content: "1.2.3.4", TTL: 120})
	cleanup := setDNSProviderHook(p)
	defer cleanup()

	s := testServer()
	body := `{"type":"A","name":"example.com","content":"9.9.9.9","ttl":300}`
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/dns/example.com/records/"+created.ID,
		strings.NewReader(body)))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200\nbody: %s", rec.Code, rec.Body.String())
	}
	var updated map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if updated["content"] != "9.9.9.9" {
		t.Errorf("content = %v, want 9.9.9.9", updated["content"])
	}
}

// ---------------------------------------------------------------------------
// handleDNSRecordDelete (DELETE /api/v1/dns/{domain}/records/{id})
// ---------------------------------------------------------------------------

func TestDNSRecordDelete_NoProvider_Returns501(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("DELETE", "/api/v1/dns/example.com/records/rec-1", nil))
	if rec.Code != 501 {
		t.Fatalf("status = %d, want 501\nbody: %s", rec.Code, rec.Body.String())
	}
}

func TestDNSRecordDelete_Success_ReturnsDeleted(t *testing.T) {
	p := newStubProvider()
	p.addZone("z1", "example.com")
	created, _ := p.CreateRecord("z1", dnsmanager.Record{Type: "A", Name: "example.com", Content: "1.2.3.4", TTL: 120})
	cleanup := setDNSProviderHook(p)
	defer cleanup()

	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("DELETE", "/api/v1/dns/example.com/records/"+created.ID, nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200\nbody: %s", rec.Code, rec.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["status"] != "deleted" {
		t.Errorf("status = %v, want deleted", result["status"])
	}
}

// ---------------------------------------------------------------------------
// handleDNSSync (POST /api/v1/dns/{domain}/sync)
// ---------------------------------------------------------------------------

func TestDNSSync_NoProvider_Returns501(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/dns/example.com/sync", nil))
	if rec.Code != 501 {
		t.Fatalf("status = %d, want 501\nbody: %s", rec.Code, rec.Body.String())
	}
}

func TestDNSSync_ZoneNotFound_Returns404(t *testing.T) {
	p := newStubProvider()
	cleanup := setDNSProviderHook(p)
	defer cleanup()

	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/dns/nonexistent.com/sync", nil))
	if rec.Code != 404 {
		t.Fatalf("status = %d, want 404\nbody: %s", rec.Code, rec.Body.String())
	}
}

func TestDNSSync_ExistingARecord_UpdatesIP(t *testing.T) {
	p := newStubProvider()
	p.addZone("z1", "example.com")
	p.CreateRecord("z1", dnsmanager.Record{Type: "A", Name: "example.com", Content: "1.2.3.4", TTL: 120})
	cleanup := setDNSProviderHook(p)
	defer cleanup()

	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/dns/example.com/sync", nil))

	// The handler calls serverip.PublicIP() which may return "" in CI.
	// If IP detection fails, the handler returns 500; that's acceptable
	// and tests the error path. If it succeeds, we check the response.
	if rec.Code == 200 {
		var result map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if result["status"] != "synced" {
			t.Errorf("status = %v, want synced", result["status"])
		}
		// The A record should have been updated to the server IP.
		recs, _ := p.ListRecords("z1")
		if len(recs) > 0 && recs[0].Content == "1.2.3.4" {
			t.Error("A record content was not updated")
		}
	} else if rec.Code == 500 {
		// Acceptable if PublicIP() returns "".
	} else {
		t.Fatalf("unexpected status = %d\nbody: %s", rec.Code, rec.Body.String())
	}
}

func TestDNSSync_NoExistingARecord_CreatesOne(t *testing.T) {
	p := newStubProvider()
	p.addZone("z1", "example.com")
	// Add a non-A record to ensure we don't confuse it with an A record.
	p.CreateRecord("z1", dnsmanager.Record{Type: "MX", Name: "example.com", Content: "mail.example.com", TTL: 300, Priority: 10})
	cleanup := setDNSProviderHook(p)
	defer cleanup()

	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/dns/example.com/sync", nil))

	if rec.Code == 200 {
		var result map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if result["status"] != "synced" {
			t.Errorf("status = %v, want synced", result["status"])
		}
		// An A record should now exist.
		recs, _ := p.ListRecords("z1")
		foundA := false
		for _, r := range recs {
			if r.Type == "A" {
				foundA = true
				break
			}
		}
		if !foundA {
			t.Error("A record was not created")
		}
	} else if rec.Code == 500 {
		// Acceptable if PublicIP() returns "".
	} else {
		t.Fatalf("unexpected status = %d\nbody: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// getDNSProvider edge cases (config-based)
// ---------------------------------------------------------------------------

func TestGetDNSProvider_NilCredentials_ReturnsNil(t *testing.T) {
	s := testServer()
	p := s.getDNSProvider()
	if p != nil {
		t.Error("expected nil provider when no credentials configured")
	}
}

func TestGetDNSProvider_UnknownProvider_ReturnsNil(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{Listen: "127.0.0.1:0"},
			ACME: config.ACMEConfig{
				DNSProvider:    "unknown",
				DNSCredentials: map[string]string{"token": "dummy"},
			},
		},
	}
	s := testServerFromConfig(t, cfg)
	p := s.getDNSProvider()
	if p != nil {
		t.Error("expected nil provider for unknown provider type")
	}
}

func TestGetDNSProvider_Cloudflare_MissingToken_ReturnsNil(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{Listen: "127.0.0.1:0"},
			ACME: config.ACMEConfig{
				DNSProvider:    "cloudflare",
				DNSCredentials: map[string]string{},
			},
		},
	}
	s := testServerFromConfig(t, cfg)
	p := s.getDNSProvider()
	if p != nil {
		t.Error("expected nil provider for cloudflare with no token")
	}
}

func TestGetDNSProvider_Route53_MissingKeys_ReturnsNil(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{Listen: "127.0.0.1:0"},
			ACME: config.ACMEConfig{
				DNSProvider:    "route53",
				DNSCredentials: map[string]string{"access_key": "ak"},
				// missing secret_key
			},
		},
	}
	s := testServerFromConfig(t, cfg)
	p := s.getDNSProvider()
	if p != nil {
		t.Error("expected nil provider for route53 with missing keys")
	}
}

func TestGetDNSProvider_Hetzner_MissingToken_ReturnsNil(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{Listen: "127.0.0.1:0"},
			ACME: config.ACMEConfig{
				DNSProvider:    "hetzner",
				DNSCredentials: map[string]string{},
			},
		},
	}
	s := testServerFromConfig(t, cfg)
	p := s.getDNSProvider()
	if p != nil {
		t.Error("expected nil provider for hetzner with no token")
	}
}

func TestGetDNSProvider_DigitalOcean_MissingToken_ReturnsNil(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{Listen: "127.0.0.1:0"},
			ACME: config.ACMEConfig{
				DNSProvider:    "digitalocean",
				DNSCredentials: map[string]string{},
			},
		},
	}
	s := testServerFromConfig(t, cfg)
	p := s.getDNSProvider()
	if p != nil {
		t.Error("expected nil provider for digitalocean with no token")
	}
}

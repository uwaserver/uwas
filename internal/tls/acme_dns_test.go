package uwastls

import (
	"errors"
	"testing"

	"github.com/uwaserver/uwas/internal/dnsmanager"
	"github.com/uwaserver/uwas/internal/logger"
)

// mockZoneRecords maps zoneID -> records in that zone.
type mockZoneRecords map[string][]dnsmanager.Record

// mockDNSProvider implements dnsmanager.Provider for testing.
type mockDNSProvider struct {
	zones      []dnsmanager.Zone
	records    mockZoneRecords
	createErr  error
	listErr    error
	deleteErr  error
	findZoneErr error
	nextRecID  int
}

func (m *mockDNSProvider) ListZones() ([]dnsmanager.Zone, error) {
	return m.zones, nil
}

func (m *mockDNSProvider) ListRecords(zoneID string) ([]dnsmanager.Record, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	if m.records == nil {
		return nil, nil
	}
	return m.records[zoneID], nil
}

func (m *mockDNSProvider) CreateRecord(zoneID string, rec dnsmanager.Record) (*dnsmanager.Record, error) {
	if m.createErr != nil {
		return nil, m.createErr
	}
	m.nextRecID++
	rec.ID = "rec-1"
	m.records[zoneID] = append(m.records[zoneID], rec)
	return &rec, nil
}

func (m *mockDNSProvider) UpdateRecord(zoneID, recordID string, rec dnsmanager.Record) (*dnsmanager.Record, error) {
	return &rec, nil
}

func (m *mockDNSProvider) DeleteRecord(zoneID, recordID string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	if m.records == nil {
		return errors.New("record not found")
	}
	recs := m.records[zoneID]
	for i, r := range recs {
		if r.ID == recordID {
			m.records[zoneID] = append(recs[:i], recs[i+1:]...)
			return nil
		}
	}
	return errors.New("record not found")
}

func (m *mockDNSProvider) FindZoneByDomain(domain string) (*dnsmanager.Zone, error) {
	if m.findZoneErr != nil {
		return nil, m.findZoneErr
	}
	for _, z := range m.zones {
		if len(domain) > len(z.Name) && domain[len(domain)-len(z.Name)-1:] == "."+z.Name {
			return &z, nil
		}
	}
	return nil, errors.New("zone not found")
}

func TestPresentDNSChallenge(t *testing.T) {
	log := logger.New("error", "text")
	mp := &mockDNSProvider{
		zones: []dnsmanager.Zone{
			{ID: "zone-1", Name: "example.com"},
		},
		records: make(mockZoneRecords),
	}
	p := &acmeDNSProvider{dp: mp, log: log}

	err := p.PresentDNSChallenge("_acme-challenge.example.com", "token", "keyauth123")
	if err != nil {
		t.Fatalf("PresentDNSChallenge failed: %v", err)
	}

	recs := mp.records["zone-1"]
	if len(recs) != 1 {
		t.Fatalf("expected 1 record, got %d", len(recs))
	}
	rec := recs[0]
	if rec.Type != "TXT" {
		t.Errorf("record type = %q, want TXT", rec.Type)
	}
	if rec.Name != "_acme-challenge" {
		t.Errorf("record name = %q, want _acme-challenge", rec.Name)
	}
	if rec.Content != "keyauth123" {
		t.Errorf("record content = %q, want keyauth123", rec.Content)
	}
}

func TestPresentDNSChallengeFindZoneError(t *testing.T) {
	log := logger.New("error", "text")
	mp := &mockDNSProvider{
		findZoneErr: errors.New("zone not found"),
	}
	p := &acmeDNSProvider{dp: mp, log: log}

	err := p.PresentDNSChallenge("_acme-challenge.example.com", "token", "keyauth")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestPresentDNSChallengeCreateError(t *testing.T) {
	log := logger.New("error", "text")
	mp := &mockDNSProvider{
		zones: []dnsmanager.Zone{
			{ID: "zone-1", Name: "example.com"},
		},
		createErr: errors.New("create failed"),
	}
	p := &acmeDNSProvider{dp: mp, log: log}

	err := p.PresentDNSChallenge("_acme-challenge.example.com", "token", "keyauth")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestCleanupDNSChallenge(t *testing.T) {
	log := logger.New("error", "text")
	mp := &mockDNSProvider{
		zones: []dnsmanager.Zone{
			{ID: "zone-1", Name: "example.com"},
		},
		records: mockZoneRecords{
			"zone-1": {
				{ID: "rec-1", Type: "TXT", Name: "_acme-challenge", Content: "keyauth123"},
			},
		},
	}
	p := &acmeDNSProvider{dp: mp, log: log, zoneID: "zone-1", zoneName: "example.com"}

	err := p.CleanupDNSChallenge("_acme-challenge.example.com", "token", "keyauth123")
	if err != nil {
		t.Fatalf("CleanupDNSChallenge failed: %v", err)
	}

	if len(mp.records["zone-1"]) != 0 {
		t.Errorf("expected 0 records after cleanup, got %d", len(mp.records["zone-1"]))
	}
}

func TestCleanupDNSChallengeFindZoneError(t *testing.T) {
	log := logger.New("error", "text")
	mp := &mockDNSProvider{
		findZoneErr: errors.New("zone not found"),
	}
	p := &acmeDNSProvider{dp: mp, log: log}

	// findZone error returns nil (nothing to clean up)
	err := p.CleanupDNSChallenge("_acme-challenge.example.com", "token", "keyauth")
	if err != nil {
		t.Fatalf("CleanupDNSChallenge returned error: %v", err)
	}
}

func TestCleanupDNSChallengeListError(t *testing.T) {
	log := logger.New("error", "text")
	mp := &mockDNSProvider{
		zones: []dnsmanager.Zone{
			{ID: "zone-1", Name: "example.com"},
		},
		listErr: errors.New("list failed"),
	}
	p := &acmeDNSProvider{dp: mp, log: log, zoneID: "zone-1", zoneName: "example.com"}

	err := p.CleanupDNSChallenge("_acme-challenge.example.com", "token", "keyauth")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestFindZoneCached(t *testing.T) {
	log := logger.New("error", "text")
	mp := &mockDNSProvider{
		zones: []dnsmanager.Zone{
			{ID: "zone-1", Name: "example.com"},
		},
	}
	p := &acmeDNSProvider{dp: mp, log: log, zoneID: "zone-1", zoneName: "example.com"}

	// Should use cached zone
	zone, err := p.findZone("_acme-challenge.example.com")
	if err != nil {
		t.Fatalf("findZone failed: %v", err)
	}
	if zone != "zone-1" {
		t.Errorf("zone = %q, want zone-1", zone)
	}
}

func TestFindZoneNotCached(t *testing.T) {
	log := logger.New("error", "text")
	mp := &mockDNSProvider{
		zones: []dnsmanager.Zone{
			{ID: "zone-1", Name: "example.com"},
		},
	}
	p := &acmeDNSProvider{dp: mp, log: log}

	zone, err := p.findZone("_acme-challenge.example.com")
	if err != nil {
		t.Fatalf("findZone failed: %v", err)
	}
	if zone != "zone-1" {
		t.Errorf("zone = %q, want zone-1", zone)
	}
	if p.zoneID != "zone-1" {
		t.Errorf("zoneID not cached: p.zoneID = %q", p.zoneID)
	}
	if p.zoneName != "example.com" {
		t.Errorf("zoneName not cached: p.zoneName = %q", p.zoneName)
	}
}

func TestNewACMEDNSProviderCloudflare(t *testing.T) {
	log := logger.New("error", "text")
	prov, err := NewACMEDNSProvider("cloudflare", map[string]string{"api_token": "abc123"}, log)
	if err != nil {
		t.Fatalf("NewACMEDNSProvider failed: %v", err)
	}
	if prov == nil {
		t.Fatal("provider is nil")
	}
	_, ok := prov.(*acmeDNSProvider)
	if !ok {
		t.Fatal("provider is not *acmeDNSProvider")
	}
}

func TestNewACMEDNSProviderDigitalOcean(t *testing.T) {
	log := logger.New("error", "text")
	prov, err := NewACMEDNSProvider("digitalocean", map[string]string{"api_token": "abc123"}, log)
	if err != nil {
		t.Fatalf("NewACMEDNSProvider failed: %v", err)
	}
	if prov == nil {
		t.Fatal("provider is nil")
	}
}

func TestNewACMEDNSProviderHetzner(t *testing.T) {
	log := logger.New("error", "text")
	prov, err := NewACMEDNSProvider("hetzner", map[string]string{"api_token": "abc123"}, log)
	if err != nil {
		t.Fatalf("NewACMEDNSProvider failed: %v", err)
	}
	if prov == nil {
		t.Fatal("provider is nil")
	}
}

func TestNewACMEDNSProviderRoute53(t *testing.T) {
	log := logger.New("error", "text")
	prov, err := NewACMEDNSProvider("route53", map[string]string{
		"access_key": "AKID",
		"secret_key": "SECRET",
		"region":     "us-west-2",
	}, log)
	if err != nil {
		t.Fatalf("NewACMEDNSProvider failed: %v", err)
	}
	if prov == nil {
		t.Fatal("provider is nil")
	}
}

func TestNewACMEDNSProviderRoute53DefaultRegion(t *testing.T) {
	log := logger.New("error", "text")
	prov, err := NewACMEDNSProvider("route53", map[string]string{
		"access_key": "AKID",
		"secret_key": "SECRET",
	}, log)
	if err != nil {
		t.Fatalf("NewACMEDNSProvider failed: %v", err)
	}
	if prov == nil {
		t.Fatal("provider is nil")
	}
}

func TestNewACMEDNSProviderUnknown(t *testing.T) {
	log := logger.New("error", "text")
	_, err := NewACMEDNSProvider("unknown", map[string]string{}, log)
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestNewACMEDNSProviderCloudflareMissingToken(t *testing.T) {
	log := logger.New("error", "text")
	_, err := NewACMEDNSProvider("cloudflare", map[string]string{}, log)
	if err == nil {
		t.Fatal("expected error for missing api_token")
	}
}

func TestNewACMEDNSProviderRoute53MissingCreds(t *testing.T) {
	log := logger.New("error", "text")
	_, err := NewACMEDNSProvider("route53", map[string]string{"access_key": "AKID"}, log)
	if err == nil {
		t.Fatal("expected error for missing secret_key")
	}
}

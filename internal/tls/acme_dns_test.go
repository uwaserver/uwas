package uwastls

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/dnsmanager"
	"github.com/uwaserver/uwas/internal/logger"
)

// mockZoneRecords maps zoneID -> records in that zone.
type mockZoneRecords map[string][]dnsmanager.Record

// mockDNSProvider implements dnsmanager.Provider for testing.
type mockDNSProvider struct {
	zones       []dnsmanager.Zone
	records     mockZoneRecords
	createErr   error
	listErr     error
	deleteErr   error
	findZoneErr error
	nextRecID   int
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
	// RFC 8555 §8.4: TXT content is base64url(SHA-256(keyAuthorization)),
	// never the raw key authorization.
	want := dnsChallengeTXT("keyauth123")
	if rec.Content != want {
		t.Errorf("record content = %q, want %q", rec.Content, want)
	}
	if rec.Content == "keyauth123" {
		t.Error("record content must not be the raw key authorization")
	}
}

func TestDNSChallengeTXTContent(t *testing.T) {
	// base64url(sha256(keyAuth)) per RFC 8555 §8.4, computed independently.
	keyAuth := "token.thumbprint"
	h := sha256.Sum256([]byte(keyAuth))
	want := base64.RawURLEncoding.EncodeToString(h[:])
	if got := dnsChallengeTXT(keyAuth); got != want {
		t.Errorf("dnsChallengeTXT = %q, want %q", got, want)
	}
	if strings.ContainsAny(dnsChallengeTXT(keyAuth), "+/=") {
		t.Error("dnsChallengeTXT must be unpadded base64url")
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
				{ID: "rec-1", Type: "TXT", Name: "_acme-challenge", Content: dnsChallengeTXT("keyauth123")},
			},
		},
	}
	p := &acmeDNSProvider{dp: mp, log: log}

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
	p := &acmeDNSProvider{dp: mp, log: log}

	err := p.CleanupDNSChallenge("_acme-challenge.example.com", "token", "keyauth")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestFindZoneCached(t *testing.T) {
	log := logger.New("error", "text")
	mp := &mockDNSProvider{
		findZoneErr: errors.New("must not be called for cached domain"),
	}
	p := &acmeDNSProvider{dp: mp, log: log, zones: map[string]*dnsmanager.Zone{
		"_acme-challenge.example.com": {ID: "zone-1", Name: "example.com"},
	}}

	// Should use cached zone (provider lookup would error)
	zone, err := p.findZone("_acme-challenge.example.com")
	if err != nil {
		t.Fatalf("findZone failed: %v", err)
	}
	if zone.ID != "zone-1" {
		t.Errorf("zone ID = %q, want zone-1", zone.ID)
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
	if zone.ID != "zone-1" {
		t.Errorf("zone ID = %q, want zone-1", zone.ID)
	}
	cached := p.zones["_acme-challenge.example.com"]
	if cached == nil || cached.ID != "zone-1" || cached.Name != "example.com" {
		t.Errorf("zone not cached: %+v", cached)
	}
}

// TestFindZoneMultipleZones ensures the per-domain zone cache does not leak
// the first domain's zone into a later domain from a different zone.
func TestFindZoneMultipleZones(t *testing.T) {
	log := logger.New("error", "text")
	mp := &mockDNSProvider{
		zones: []dnsmanager.Zone{
			{ID: "zone-1", Name: "example.com"},
			{ID: "zone-2", Name: "example.org"},
		},
		records: make(mockZoneRecords),
	}
	p := &acmeDNSProvider{dp: mp, log: log}

	zone1, err := p.findZone("_acme-challenge.example.com")
	if err != nil {
		t.Fatalf("findZone example.com: %v", err)
	}
	zone2, err := p.findZone("_acme-challenge.example.org")
	if err != nil {
		t.Fatalf("findZone example.org: %v", err)
	}
	if zone1.ID != "zone-1" {
		t.Errorf("zone1 ID = %q, want zone-1", zone1.ID)
	}
	if zone2.ID != "zone-2" {
		t.Errorf("zone2 ID = %q, want zone-2 (stale first-zone cache)", zone2.ID)
	}

	// End-to-end: records land in their own zones with correct names.
	if err := p.PresentDNSChallenge("_acme-challenge.example.com", "t1", "ka1"); err != nil {
		t.Fatalf("PresentDNSChallenge example.com: %v", err)
	}
	if err := p.PresentDNSChallenge("_acme-challenge.example.org", "t2", "ka2"); err != nil {
		t.Fatalf("PresentDNSChallenge example.org: %v", err)
	}
	if n := len(mp.records["zone-1"]); n != 1 {
		t.Errorf("zone-1 records = %d, want 1", n)
	}
	if n := len(mp.records["zone-2"]); n != 1 {
		t.Errorf("zone-2 records = %d, want 1", n)
	}
	if len(mp.records["zone-2"]) == 1 && mp.records["zone-2"][0].Name != "_acme-challenge" {
		t.Errorf("zone-2 record name = %q, want _acme-challenge", mp.records["zone-2"][0].Name)
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

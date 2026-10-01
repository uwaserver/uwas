package dnsmanager

// Regression: DigitalOceanProvider.FindZoneByDomain and
// HetznerProvider.FindZoneByDomain returned the first zone that matched in raw
// API order, with no longest-first sort.
//
// Route53Provider.FindZoneByDomain already sorts longest-first and documents
// why: the API returns zones in creation order, so when an account holds both
// a zone and a nested zone, the parent is listed first and wins —
// "sub.foo.example.com" ends with ".example.com", so "example.com" matched
// before "foo.example.com" ever was considered.
//
// Impact: internal/tls/acme_dns.go picks the zone for the ACME DNS-01
// challenge TXT record with this function. The wrong zone means the record is
// created where the CA does not look, and certificate issuance fails for that
// domain. Cloudflare already probes suffixes longest-first.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func doZoneOrderServer(t *testing.T, names ...string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		domains := make([]map[string]string, 0, len(names))
		for _, n := range names {
			domains = append(domains, map[string]string{"name": n})
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"domains": domains})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func hetznerZoneOrderServer(t *testing.T, zones ...string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		zs := make([]map[string]string, 0, len(zones))
		for i, n := range zones {
			zs = append(zs, map[string]string{"id": string(rune('a' + i)), "name": n})
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"zones": zs,
			"meta":  map[string]interface{}{"pagination": map[string]int{"last_page": 1}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDigitalOceanFindZoneByDomainPrefersMostSpecificZone(t *testing.T) {
	// Parent first, as a real API returns zones in creation order.
	srv := doZoneOrderServer(t, "example.com", "foo.example.com")

	z, err := newTestDigitalOcean(srv.URL).FindZoneByDomain("sub.foo.example.com")
	if err != nil {
		t.Fatalf("FindZoneByDomain: %v", err)
	}
	if z.Name != "foo.example.com" {
		t.Errorf("FindZoneByDomain(\"sub.foo.example.com\") = %q, want \"foo.example.com\": "+
			"the most specific zone must win, otherwise the ACME DNS-01 TXT record "+
			"is written to the parent zone and the CA never sees it", z.Name)
	}
}

func TestHetznerFindZoneByDomainPrefersMostSpecificZone(t *testing.T) {
	srv := hetznerZoneOrderServer(t, "example.com", "foo.example.com")

	z, err := newTestHetzner(srv.URL).FindZoneByDomain("sub.foo.example.com")
	if err != nil {
		t.Fatalf("FindZoneByDomain: %v", err)
	}
	if z.Name != "foo.example.com" {
		t.Errorf("FindZoneByDomain(\"sub.foo.example.com\") = %q, want \"foo.example.com\": "+
			"the most specific zone must win, otherwise the ACME DNS-01 TXT record "+
			"is written to the parent zone and the CA never sees it", z.Name)
	}
}

func TestFindZoneByDomainIgnoresZoneListOrder(t *testing.T) {
	// The result must depend on the domain, not on the order the API listed
	// the zones in. Both orders must resolve to the nested zone.
	for _, order := range [][]string{
		{"example.com", "foo.example.com"},
		{"foo.example.com", "example.com"},
	} {
		srv := doZoneOrderServer(t, order...)
		z, err := newTestDigitalOcean(srv.URL).FindZoneByDomain("sub.foo.example.com")
		if err != nil {
			t.Fatalf("order %v: %v", order, err)
		}
		if z.Name != "foo.example.com" {
			t.Errorf("order %v: got %q, want \"foo.example.com\"", order, z.Name)
		}
	}
}

func TestFindZoneByDomainExactAndSingleZoneStillMatch(t *testing.T) {
	srv := doZoneOrderServer(t, "example.com")
	p := newTestDigitalOcean(srv.URL)

	if z, err := p.FindZoneByDomain("example.com"); err != nil || z.Name != "example.com" {
		t.Errorf("exact match: got %q (err=%v), want \"example.com\"", z.Name, err)
	}
	if z, err := p.FindZoneByDomain("sub.example.com"); err != nil || z.Name != "example.com" {
		t.Errorf("single-zone subdomain: got %q (err=%v), want \"example.com\"", z.Name, err)
	}
}

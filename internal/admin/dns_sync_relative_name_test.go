package admin

import (
	"testing"

	"github.com/uwaserver/uwas/internal/dnsmanager"
)

// F36600: Hetzner and DigitalOcean report record names relative to the zone
// ("blog", "@"). The sync handler only matched the FQDN spelling or "@", so an
// existing relative-named subdomain A record was never found: every sync
// created one more record instead of updating the existing one.
func TestDNSSyncMatchesRelativeSubdomainRecord(t *testing.T) {
	p := newStubProvider()
	p.addZone("z1", "example.com")
	p.CreateRecord("z1", dnsmanager.Record{Type: "A", Name: "@", Content: "198.51.100.1", TTL: 300})
	p.CreateRecord("z1", dnsmanager.Record{Type: "A", Name: "blog", Content: "198.51.100.2", TTL: 300})

	for i := 0; i < 2; i++ {
		if rec := syncDomain(t, p, "blog.example.com"); rec.Code != 200 {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
		}
	}
	recs, _ := p.ListRecords("z1")
	var blogLike []dnsmanager.Record
	for _, r := range recs {
		if r.Type == "A" && r.Name != "@" {
			blogLike = append(blogLike, r)
		}
	}
	t.Logf("EXPECTED: one subdomain A record updated to 203.0.113.10\nACTUAL: %+v", blogLike)
	if len(blogLike) != 1 || blogLike[0].Content != "203.0.113.10" {
		t.Fatalf("PROBLEM CONFIRMED: relative-named record not matched, got %+v", blogLike)
	}
	// Control: the apex record is untouched.
	if got := contentOf(t, p, "@"); len(got) != 1 || got[0] != "198.51.100.1" {
		t.Fatalf("apex changed: %v", got)
	}
}

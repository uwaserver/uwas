package dnsmanager

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Route53 FindZoneByDomain must canonicalise both sides like the Hetzner and
// DigitalOcean providers: an upper-case host or one with a trailing dot found
// no zone (F1750).
func TestRoute53FindZoneByDomainCanonicalNames(t *testing.T) {
	for _, child := range []string{"foo.example.com.", "Foo.Example.COM."} {
		body := `<?xml version="1.0" encoding="UTF-8"?>
<ListHostedZonesResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/"><HostedZones>
<HostedZone><Id>/hostedzone/ZPARENT</Id><Name>example.com.</Name></HostedZone>
<HostedZone><Id>/hostedzone/ZCHILD</Id><Name>` + child + `</Name></HostedZone>
</HostedZones></ListHostedZonesResponse>`
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/xml")
			w.Write([]byte(body))
		}))
		p := NewRoute53("AKIATEST", "secrettest", "us-east-1")
		p.baseURL = srv.URL
		cases := []struct{ in, want string }{
			{"www.foo.example.com", "ZCHILD"}, // control: already worked
			{"WWW.Foo.Example.COM", "ZCHILD"},
			{"www.foo.example.com.", "ZCHILD"},
			{"foo.example.com.", "ZCHILD"},
			{"EXAMPLE.com", "ZPARENT"},
			{"notfoo.example.com", "ZPARENT"}, // label boundary
		}
		for _, c := range cases {
			z, err := p.FindZoneByDomain(c.in)
			if err != nil || z.ID != c.want {
				t.Errorf("zone %q: FindZoneByDomain(%q) = %v, %v; want %s", child, c.in, z, err, c.want)
			}
		}
		if _, err := p.FindZoneByDomain("example.org"); err == nil || !strings.Contains(err.Error(), "zone not found") {
			t.Errorf("unrelated domain: err=%v, want zone not found", err)
		}
		srv.Close()
	}
}

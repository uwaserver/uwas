package dnsmanager

// Regression tests for F66700 (concurrent per-value writes to one Route53
// record set lost a value) and F66701 (split-horizon: the private hosted zone
// could be chosen over the public one of the same name).

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// gatedRRSet holds the first n record-set listings until all n have been
// taken, forcing that many writers to compute their change from one snapshot.
type gatedRRSet struct {
	*rrsetStore
	mu      sync.Mutex
	n       int
	release chan struct{}
}

func (g *gatedRRSet) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := g.rrsetStore.RoundTrip(r)
	if r.Method != http.MethodGet {
		return resp, err
	}
	g.mu.Lock()
	gated := g.n > 0
	if gated {
		g.n--
		if g.n == 0 {
			close(g.release)
		}
	}
	g.mu.Unlock()
	if gated {
		<-g.release
	}
	return resp, err
}

func concurrently(fns ...func() error) []error {
	errs := make([]error, len(fns))
	var wg sync.WaitGroup
	for i, fn := range fns {
		wg.Add(1)
		go func() { defer wg.Done(); errs[i] = fn() }()
	}
	wg.Wait()
	return errs
}

func TestRoute53ConcurrentValueChangesKeepEachOther(t *testing.T) {
	const name = "_acme-challenge.example.com."
	id := func(v string) string { return r53RecordID(name, "TXT", `"`+v+`"`) }
	create := func(p *Route53Provider, v string) func() error {
		return func() error {
			_, err := p.CreateRecord("Z", Record{Type: "TXT", Name: "_acme-challenge.example.com", Content: v, TTL: 120})
			return err
		}
	}
	for _, tc := range []struct {
		label string
		start []string
		ops   func(p *Route53Provider) []func() error
		want  string
	}{
		{"two creates", []string{`"old"`}, func(p *Route53Provider) []func() error {
			return []func() error{create(p, "new1"), create(p, "new2")}
		}, `"new1" | "new2" | "old"`},
		{"two deletes", []string{`"a"`, `"b"`, `"c"`}, func(p *Route53Provider) []func() error {
			return []func() error{
				func() error { return p.DeleteRecord("Z", id("a")) },
				func() error { return p.DeleteRecord("Z", id("b")) },
			}
		}, `"c"`},
		{"two updates", []string{`"a"`, `"b"`}, func(p *Route53Provider) []func() error {
			return []func() error{
				func() error {
					_, err := p.UpdateRecord("Z", id("a"), Record{Type: "TXT", Name: name, Content: "a2"})
					return err
				},
				func() error {
					_, err := p.UpdateRecord("Z", id("b"), Record{Type: "TXT", Name: name, Content: "b2"})
					return err
				},
			}
		}, `"a2" | "b2"`},
		{"create and delete", []string{`"old"`, `"keep"`}, func(p *Route53Provider) []func() error {
			return []func() error{create(p, "new"), func() error { return p.DeleteRecord("Z", id("old")) }}
		}, `"keep" | "new"`},
	} {
		t.Run(tc.label, func(t *testing.T) {
			store := newRRSetStore(rrsetFixture{Name: name, Type: "TXT", TTL: 120, Values: tc.start})
			g := &gatedRRSet{rrsetStore: store, n: 2, release: make(chan struct{})}
			p := NewRoute53("fixture-key", "fixture-secret", "")
			p.client = &http.Client{Transport: g}
			for i, err := range concurrently(tc.ops(p)...) {
				if err != nil {
					t.Fatalf("op %d: %v", i, err)
				}
			}
			if got := rrsetVals(store, name, "TXT"); got != tc.want {
				t.Fatalf("record set = %s, want %s", got, tc.want)
			}
		})
	}
}

type splitHorizonZones struct{ private, public string }

func (z splitHorizonZones) RoundTrip(r *http.Request) (*http.Response, error) {
	var b strings.Builder
	b.WriteString(`<ListHostedZonesResponse><HostedZones>`)
	for _, hz := range []struct {
		id      string
		private bool
	}{{z.private, true}, {z.public, false}} {
		fmt.Fprintf(&b, `<HostedZone><Id>/hostedzone/%s</Id><Name>example.com.</Name><Config><PrivateZone>%t</PrivateZone></Config></HostedZone>`, hz.id, hz.private)
	}
	b.WriteString(`</HostedZones><IsTruncated>false</IsTruncated></ListHostedZonesResponse>`)
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(b.String())), Request: r}, nil
}

func TestRoute53FindZonePrefersPublicOverSameNamePrivateZone(t *testing.T) {
	p := NewRoute53("fixture-key", "fixture-secret", "")
	p.client = &http.Client{Transport: splitHorizonZones{private: "ZPRIV", public: "ZPUB"}}
	for _, domain := range []string{"_acme-challenge.example.com", "example.com"} {
		z, err := p.FindZoneByDomain(domain)
		if err != nil {
			t.Fatal(err)
		}
		if z.ID != "ZPUB" {
			t.Fatalf("FindZoneByDomain(%q) = %s, want the public zone ZPUB", domain, z.ID)
		}
	}
}

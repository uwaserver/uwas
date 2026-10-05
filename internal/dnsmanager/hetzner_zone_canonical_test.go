package dnsmanager

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

type regressionAudit49Transport struct {
	body string
	code int
}

func (x regressionAudit49Transport) RoundTrip(r *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: x.code, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(x.body)), Request: r}, nil
}
func TestHetznerZoneCanonicalNames(t *testing.T) {
	base := `{"zones":[{"id":"parent","name":"example.test"},{"id":"child","name":"foo.example.test"}]}`
	for _, zoneName := range []string{"foo.example.test", "Foo.Example.TEST."} {
		p := NewHetzner("fixture-token")
		p.client = &http.Client{Transport: regressionAudit49Transport{body: strings.ReplaceAll(base, "foo.example.test", zoneName), code: 200}}
		for _, domain := range []string{"www.foo.example.test", "WWW.Foo.Example.TEST.", "foo.example.test.", "Foo.Example.TEST"} {
			z, err := p.FindZoneByDomain(domain)
			if err != nil || z == nil || z.Name != zoneName {
				t.Fatalf("domain=%s got=%v err=%v", domain, z, err)
			}
		}
		z, err := p.FindZoneByDomain("notfoo.example.test")
		if err != nil || z == nil || z.Name != "example.test" {
			t.Fatal("label boundary", z, err)
		}
		if _, err := p.FindZoneByDomain("outside.test"); err == nil {
			t.Fatal("unrelated zone")
		}
	}
	p := NewHetzner("fixture-token")
	p.client = &http.Client{Transport: regressionAudit49Transport{body: "unavailable", code: 503}}
	if _, err := p.FindZoneByDomain("example.test"); err == nil {
		t.Fatal("provider error swallowed")
	}
	fmt.Println("FIX VERIFIED")
}

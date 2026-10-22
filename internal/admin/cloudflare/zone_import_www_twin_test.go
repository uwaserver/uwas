package cloudflare

// Regression test for F24600: zone import added an apex and its www twin as
// two domains (and re-added a host whose www/apex twin already existed).
// config.Validate treats the pair as one host ("duplicate host"), so the next
// config save/reload would have been rejected.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

type wtDeps struct {
	*tsDeps
	recs    []DNSRecord
	domains []config.Domain
}

func (d *wtDeps) FetchDNSRecords(string, string) ([]DNSRecord, error) { return d.recs, nil }
func (d *wtDeps) ExistingDomains() map[string]bool {
	m := map[string]bool{}
	for _, x := range d.domains {
		m[strings.ToLower(x.Host)] = true
	}
	return m
}
func (d *wtDeps) AddDomain(dom config.Domain) { d.domains = append(d.domains, dom) }

func wtImport(t *testing.T, body string, recs []DNSRecord, existing ...string) (hosts []string, resp map[string]any) {
	t.Helper()
	d := &wtDeps{tsDeps: newTSDeps(&State{Connected: true, Token: "t", AccountID: "a"}), recs: recs}
	for _, h := range existing {
		d.domains = append(d.domains, config.Domain{Host: h})
	}
	orig := MkdirAllFn
	MkdirAllFn = func(string, os.FileMode) error { return nil }
	t.Cleanup(func() { MkdirAllFn = orig })
	rec := httptest.NewRecorder()
	New(d).ZoneImport(rec, tsReq(http.MethodPost, body, "z1"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	for _, x := range d.domains[len(existing):] {
		hosts = append(hosts, x.Host)
	}
	return hosts, resp
}

func TestZoneImportMergesApexAndWWWTwin(t *testing.T) {
	a := func(n string) DNSRecord { return DNSRecord{Type: "A", Name: n} }
	cases := []struct {
		name     string
		body     string
		recs     []DNSRecord
		existing []string
		want     []string
	}{
		{"control unrelated hosts", `{}`, []DNSRecord{a("a.example.org"), a("b.example.org")}, nil, []string{"a.example.org", "b.example.org"}},
		{"apex then www", `{}`, []DNSRecord{a("example.com"), a("www.example.com")}, nil, []string{"example.com"}},
		{"www then apex keeps apex", `{}`, []DNSRecord{a("www.example.com"), a("example.com")}, nil, []string{"example.com"}},
		{"www only stays www", `{}`, []DNSRecord{a("www.example.com")}, nil, []string{"www.example.com"}},
		{"existing apex skips www twin", `{}`, []DNSRecord{a("www.example.net")}, []string{"example.net"}, nil},
		{"existing www skips apex twin", `{}`, []DNSRecord{a("example.net")}, []string{"WWW.Example.NET"}, nil},
		{"whitelist www only", `{"hostnames":["www.example.com"]}`, []DNSRecord{a("example.com"), a("www.example.com")}, nil, []string{"www.example.com"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _ := wtImport(t, c.body, c.recs, c.existing...)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("imported %v, want %v", got, c.want)
			}
		})
	}
}

func TestZoneImportDryRunCountsTwinOnce(t *testing.T) {
	_, resp := wtImport(t, `{"dry_run":true}`, []DNSRecord{{Type: "A", Name: "example.com"}, {Type: "CNAME", Name: "www.example.com"}})
	if total, _ := resp["total"].(float64); total != 1 {
		t.Fatalf("total = %v, want 1 (resp %v)", resp["total"], resp)
	}
}

package dnsmanager

// Regression tests for Tur 11 F231/F232 (Route53). rrsetStore models the
// documented ChangeResourceRecordSets contract: the record SET is the unit of
// change, CREATE fails on an existing set, UPSERT replaces the set, DELETE
// must echo the current TTL and values, TXT/SPF values must be quoted.

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
)

type rrsetFixture struct {
	Name   string
	Type   string
	TTL    int
	Values []string
}

type rrsetStore struct {
	mu   sync.Mutex
	sets map[string]*rrsetFixture // key: name|type
}

func newRRSetStore(sets ...rrsetFixture) *rrsetStore {
	f := &rrsetStore{sets: map[string]*rrsetFixture{}}
	for i := range sets {
		s := sets[i]
		s.Values = append([]string(nil), s.Values...)
		f.sets[s.Name+"|"+s.Type] = &s
	}
	return f
}

func (f *rrsetStore) provider() *Route53Provider {
	p := NewRoute53("fixture-key", "fixture-secret", "")
	p.client = &http.Client{Transport: f}
	return p
}

func (f *rrsetStore) get(name, typ string) (rrsetFixture, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sets[name+"|"+typ]
	if !ok {
		return rrsetFixture{}, false
	}
	c := *s
	c.Values = append([]string(nil), s.Values...)
	return c, true
}

func rrsetResp(r *http.Request, code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}
}

func rrsetErr(r *http.Request, msg string) *http.Response {
	return rrsetResp(r, 400, `<?xml version="1.0"?><InvalidChangeBatch xmlns="https://route53.amazonaws.com/doc/2013-04-01/"><Messages><Message>`+xmlEscape(msg)+`</Message></Messages></InvalidChangeBatch>`)
}

func (f *rrsetStore) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method == http.MethodGet {
		f.mu.Lock()
		keys := make([]string, 0, len(f.sets))
		for k := range f.sets {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		b.WriteString(`<ListResourceRecordSetsResponse><ResourceRecordSets>`)
		for _, k := range keys {
			s := f.sets[k]
			fmt.Fprintf(&b, `<ResourceRecordSet><Name>%s</Name><Type>%s</Type><TTL>%d</TTL><ResourceRecords>`, xmlEscape(s.Name), s.Type, s.TTL)
			for _, v := range s.Values {
				b.WriteString(`<ResourceRecord><Value>` + xmlEscape(v) + `</Value></ResourceRecord>`)
			}
			b.WriteString(`</ResourceRecords></ResourceRecordSet>`)
		}
		b.WriteString(`</ResourceRecordSets><IsTruncated>false</IsTruncated></ListResourceRecordSetsResponse>`)
		f.mu.Unlock()
		return rrsetResp(r, 200, b.String()), nil
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	var q struct {
		Changes []struct {
			Action string   `xml:"Action"`
			Name   string   `xml:"ResourceRecordSet>Name"`
			Type   string   `xml:"ResourceRecordSet>Type"`
			TTL    int      `xml:"ResourceRecordSet>TTL"`
			Values []string `xml:"ResourceRecordSet>ResourceRecords>ResourceRecord>Value"`
		} `xml:"ChangeBatch>Changes>Change"`
	}
	if err := xml.Unmarshal(data, &q); err != nil {
		return rrsetErr(r, "malformed XML: "+err.Error()), nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	// Validate the whole batch first (Route53 batches are atomic).
	for _, c := range q.Changes {
		if c.Type == "TXT" || c.Type == "SPF" {
			for _, v := range c.Values {
				if len(v) < 2 || v[0] != '"' || v[len(v)-1] != '"' {
					return rrsetErr(r, fmt.Sprintf("Invalid Resource Record: FATAL problem: InvalidCharacterString (Value should be enclosed in quotation marks) encountered with '%s'", v)), nil
				}
			}
		}
		key := c.Name + "|" + c.Type
		cur, exists := f.sets[key]
		switch c.Action {
		case "CREATE":
			if exists {
				return rrsetErr(r, fmt.Sprintf("Tried to create resource record set [name='%s', type='%s'] but it already exists", c.Name, c.Type)), nil
			}
		case "DELETE":
			if !exists {
				return rrsetErr(r, fmt.Sprintf("Tried to delete resource record set [name='%s', type='%s'] but it was not found", c.Name, c.Type)), nil
			}
			a := append([]string(nil), cur.Values...)
			b := append([]string(nil), c.Values...)
			sort.Strings(a)
			sort.Strings(b)
			if cur.TTL != c.TTL || strings.Join(a, "\x00") != strings.Join(b, "\x00") {
				return rrsetErr(r, fmt.Sprintf("Tried to delete resource record set [name='%s', type='%s'] but the values provided do not match the current values", c.Name, c.Type)), nil
			}
		case "UPSERT":
		default:
			return rrsetErr(r, "unknown action "+c.Action), nil
		}
	}
	for _, c := range q.Changes {
		key := c.Name + "|" + c.Type
		switch c.Action {
		case "CREATE", "UPSERT":
			f.sets[key] = &rrsetFixture{Name: c.Name, Type: c.Type, TTL: c.TTL, Values: append([]string(nil), c.Values...)}
		case "DELETE":
			delete(f.sets, key)
		}
	}
	return rrsetResp(r, 200, `<ChangeResourceRecordSetsResponse/>`), nil
}

func rrsetVals(f *rrsetStore, name, typ string) string {
	s, ok := f.get(name, typ)
	if !ok {
		return "<absent>"
	}
	v := append([]string(nil), s.Values...)
	sort.Strings(v)
	return strings.Join(v, " | ")
}

func rrsetRow(t *testing.T, p *Route53Provider, name, typ, content string) Record {
	t.Helper()
	recs, err := p.ListRecords("Z1")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		if r.Name == name && r.Type == typ && r.Content == content {
			return r
		}
	}
	t.Fatalf("row %s %s %q not listed: %+v", name, typ, content, recs)
	return Record{}
}

// F231: the DNS-01 challenge TXT record must be sent quoted (AWS rejects an
// unquoted value) and listed back as plain text, which CleanupDNSChallenge
// compares against.
func TestRoute53TXTValuesQuotedOnWriteAndPlainOnList(t *testing.T) {
	f := newRRSetStore()
	p := f.provider()
	if _, err := p.CreateRecord("Z1", Record{Type: "TXT", Name: "_acme-challenge.example.com", Content: "tok", TTL: 120}); err != nil {
		t.Fatalf("TXT create rejected: %v", err)
	}
	if got := rrsetVals(f, "_acme-challenge.example.com.", "TXT"); got != `"tok"` {
		t.Fatalf("stored %s, want \"tok\"", got)
	}
	row := rrsetRow(t, p, "_acme-challenge.example.com", "TXT", "tok")
	if err := p.DeleteRecord("Z1", row.ID); err != nil {
		t.Fatalf("cleanup delete: %v", err)
	}
	if got := rrsetVals(f, "_acme-challenge.example.com.", "TXT"); got != "<absent>" {
		t.Fatalf("challenge record left behind: %s", got)
	}
}

// F232: one listed value is one row — updating, deleting or adding it must
// not drop the other values of the same record set.
func TestRoute53PerValueChangesKeepSiblingValues(t *testing.T) {
	seed := func() *rrsetStore {
		return newRRSetStore(
			rrsetFixture{Name: "example.com.", Type: "MX", TTL: 300, Values: []string{"10 mx1.example.com.", "20 mx2.example.com."}},
			rrsetFixture{Name: "www.example.com.", Type: "A", TTL: 300, Values: []string{"192.0.2.1"}},
		)
	}

	f := seed()
	p := f.provider()
	row := rrsetRow(t, p, "example.com", "MX", "20 mx2.example.com.")
	row.Content = "20 mx3.example.com."
	if _, err := p.UpdateRecord("Z1", row.ID, row); err != nil {
		t.Fatal(err)
	}
	if got := rrsetVals(f, "example.com.", "MX"); got != "10 mx1.example.com. | 20 mx3.example.com." {
		t.Fatalf("update one MX: set = %s", got)
	}

	f = seed()
	p = f.provider()
	if err := p.DeleteRecord("Z1", rrsetRow(t, p, "example.com", "MX", "20 mx2.example.com.").ID); err != nil {
		t.Fatal(err)
	}
	if got := rrsetVals(f, "example.com.", "MX"); got != "10 mx1.example.com." {
		t.Fatalf("delete one MX: set = %s", got)
	}

	f = seed()
	if _, err := f.provider().CreateRecord("Z1", Record{Type: "A", Name: "www.example.com", Content: "192.0.2.2", TTL: 300}); err != nil {
		t.Fatalf("create second A: %v", err)
	}
	if got := rrsetVals(f, "www.example.com.", "A"); got != "192.0.2.1 | 192.0.2.2" {
		t.Fatalf("create second A: set = %s", got)
	}
}

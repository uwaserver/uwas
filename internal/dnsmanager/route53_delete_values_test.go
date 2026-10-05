package dnsmanager

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

type regressionAudit51Transport struct {
	ttl                                  int
	values                               []string
	getCode, postCode, gets, posts       int
	postedTTL                            int
	postedValues                         []string
	postedName, postedType, postedAction string
}

func (x *regressionAudit51Transport) RoundTrip(r *http.Request) (*http.Response, error) {
	code := 200
	body := `<ChangeResourceRecordSetsResponse/>`
	if r.Method == http.MethodGet {
		x.gets++
		if x.getCode != 0 {
			code = x.getCode
		}
		var records strings.Builder
		for _, v := range x.values {
			records.WriteString("<ResourceRecord><Value>" + xmlEscape(v) + "</Value></ResourceRecord>")
		}
		body = fmt.Sprintf(`<ListResourceRecordSetsResponse><ResourceRecordSets><ResourceRecordSet><Name>example.test.</Name><Type>A</Type><TTL>%d</TTL><ResourceRecords>%s</ResourceRecords></ResourceRecordSet></ResourceRecordSets></ListResourceRecordSetsResponse>`, x.ttl, records.String())
	} else {
		x.posts++
		if x.postCode != 0 {
			code = x.postCode
		}
		data, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		var q struct {
			Action string   `xml:"ChangeBatch>Changes>Change>Action"`
			Name   string   `xml:"ChangeBatch>Changes>Change>ResourceRecordSet>Name"`
			Type   string   `xml:"ChangeBatch>Changes>Change>ResourceRecordSet>Type"`
			TTL    int      `xml:"ChangeBatch>Changes>Change>ResourceRecordSet>TTL"`
			Values []string `xml:"ChangeBatch>Changes>Change>ResourceRecordSet>ResourceRecords>ResourceRecord>Value"`
		}
		if err := xml.Unmarshal(data, &q); err != nil {
			return nil, err
		}
		x.postedTTL = q.TTL
		x.postedValues = q.Values
		x.postedName = q.Name
		x.postedType = q.Type
		x.postedAction = q.Action
	}
	return &http.Response{StatusCode: code, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
}
func TestRoute53DeleteExistingValues(t *testing.T) {
	for _, ttl := range []int{0, 120, 300} {
		for _, values := range [][]string{{"192.0.2.1"}, {"192.0.2.1", "192.0.2.2"}, {"fixture&value", "fixture<value>"}} {
			for _, id := range []string{"example.test:A", "example.test.:A"} {
				tr := &regressionAudit51Transport{ttl: ttl, values: values}
				p := NewRoute53("fixture-key", "fixture-secret", "")
				p.client = &http.Client{Transport: tr}
				if err := p.DeleteRecord("ZONE", id); err != nil {
					t.Fatal(err)
				}
				if tr.gets != 1 || tr.posts != 1 || tr.postedTTL != ttl || !reflect.DeepEqual(tr.postedValues, values) || tr.postedAction != "DELETE" || tr.postedName != "example.test." || tr.postedType != "A" {
					t.Fatalf("delete request mismatch: %+v", tr)
				}
			}
		}
	}
	for _, tc := range []struct {
		id                             string
		get, post, wantGets, wantPosts int
	}{{"invalid", 0, 0, 0, 0}, {"other.test:A", 0, 0, 1, 0}, {"example.test:TXT", 0, 0, 1, 0}, {"example.test:A", 503, 0, 1, 0}, {"example.test:A", 0, 400, 1, 1}} {
		tr := &regressionAudit51Transport{ttl: 120, values: []string{"192.0.2.1"}, getCode: tc.get, postCode: tc.post}
		p := NewRoute53("fixture-key", "fixture-secret", "")
		p.client = &http.Client{Transport: tr}
		if err := p.DeleteRecord("ZONE", tc.id); err == nil {
			t.Fatalf("expected error for %+v", tc)
		}
		if tr.gets != tc.wantGets || tr.posts != tc.wantPosts {
			t.Fatalf("calls=%+v expected=%+v", tr, tc)
		}
	}
	fmt.Println("FIX VERIFIED")
}

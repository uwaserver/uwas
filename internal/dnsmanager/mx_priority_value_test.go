package dnsmanager

// F30100: Hetzner DNS and Route53 have no separate priority field; an MX value
// is "<priority> <host>". The dashboard sends the priority in Record.Priority,
// and both providers dropped it, so the MX was created without a preference
// (rejected by Route53, malformed at Hetzner).

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHetznerMXValueCarriesPriority(t *testing.T) {
	cases := []struct {
		name string
		rec  Record
		want string
	}{
		{"priority prefixed", Record{Type: "MX", Name: "@", Content: "mail.example.com.", TTL: 300, Priority: 10}, "10 mail.example.com."},
		{"already prefixed (control)", Record{Type: "MX", Name: "@", Content: "20 mail.example.com.", TTL: 300, Priority: 10}, "20 mail.example.com."},
		{"no priority (control)", Record{Type: "MX", Name: "@", Content: "mail.example.com.", TTL: 300}, "mail.example.com."},
		{"non-MX priority ignored (control)", Record{Type: "A", Name: "@", Content: "1.2.3.4", TTL: 300, Priority: 10}, "1.2.3.4"},
	}
	for _, c := range cases {
		for _, op := range []string{"create", "update"} {
			t.Run(c.name+"/"+op, func(t *testing.T) {
				var got string
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					b, _ := io.ReadAll(r.Body)
					var m map[string]interface{}
					_ = json.Unmarshal(b, &m)
					got, _ = m["value"].(string)
					w.Write([]byte(`{"record":{"id":"r1"}}`))
				}))
				defer srv.Close()
				p := newTestHetzner(srv.URL)
				var err error
				if op == "create" {
					_, err = p.CreateRecord("z1", c.rec)
				} else {
					_, err = p.UpdateRecord("z1", "r1", c.rec)
				}
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("EXPECTED: %q\nACTUAL: %q", c.want, got)
				if got != c.want {
					t.Fatalf("PROBLEM CONFIRMED: value %q, want %q", got, c.want)
				}
			})
		}
	}
}

func TestRoute53MXValueCarriesPriority(t *testing.T) {
	cases := []struct {
		name string
		rec  Record
		want string
	}{
		{"priority prefixed", Record{Type: "MX", Name: "example.com", Content: "mail.example.com.", TTL: 300, Priority: 10}, "10 mail.example.com."},
		{"already prefixed (control)", Record{Type: "MX", Name: "example.com", Content: "20 mail.example.com.", TTL: 300, Priority: 10}, "20 mail.example.com."},
		{"no priority (control)", Record{Type: "MX", Name: "example.com", Content: "mail.example.com.", TTL: 300}, "mail.example.com."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var body string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				body = string(b)
				w.Header().Set("Content-Type", "text/xml")
				w.Write([]byte(`<ChangeResourceRecordSetsResponse><ChangeInfo><Id>c</Id></ChangeInfo></ChangeResourceRecordSetsResponse>`))
			}))
			defer srv.Close()
			p := newTestRoute53(srv.URL)
			if _, err := p.CreateRecord("Z1", c.rec); err != nil {
				t.Fatal(err)
			}
			want := "<Value>" + c.want + "</Value>"
			t.Logf("EXPECTED: %s\nACTUAL: %s", want, body)
			if !strings.Contains(body, want) {
				t.Fatalf("PROBLEM CONFIRMED: request lacks %s", want)
			}
		})
	}
}

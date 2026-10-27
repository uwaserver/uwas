package dnsmanager

// F33100: Cloudflare requires "priority" on MX/SRV/URI records and a preference
// of 0 is valid, but Record.Priority is `omitempty`, so priority 0 was dropped
// from the request body and Cloudflare rejected the record. Types that take no
// priority (A, TXT) must still not carry one.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCloudflareSendsZeroPriorityForPriorityTypes(t *testing.T) {
	cases := []struct {
		name         string
		rec          Record
		wantPriority bool
	}{
		{"MX priority 0", Record{Type: "MX", Name: "@", Content: "mail.example.com", TTL: 300}, true},
		{"SRV priority 0", Record{Type: "SRV", Name: "_sip._tcp", Content: "sip.example.com", TTL: 300}, true},
		{"MX priority 10 (control)", Record{Type: "MX", Name: "@", Content: "mail.example.com", TTL: 300, Priority: 10}, true},
		{"A no priority (control)", Record{Type: "A", Name: "@", Content: "1.2.3.4", TTL: 300}, false},
		{"TXT no priority (control)", Record{Type: "TXT", Name: "@", Content: "v=spf1 -all", TTL: 300}, false},
	}
	for _, c := range cases {
		for _, op := range []string{"create", "update"} {
			t.Run(c.name+"/"+op, func(t *testing.T) {
				var body map[string]interface{}
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					b, _ := io.ReadAll(r.Body)
					_ = json.Unmarshal(b, &body)
					w.Write([]byte(`{"success":true,"result":{"id":"r1"}}`))
				}))
				defer srv.Close()
				p := newTestCloudflare(srv.URL)
				var err error
				if op == "create" {
					_, err = p.CreateRecord("z1", c.rec)
				} else {
					_, err = p.UpdateRecord("z1", "r1", c.rec)
				}
				if err != nil {
					t.Fatal(err)
				}
				pr, has := body["priority"]
				t.Logf("EXPECTED: priority present=%v\nACTUAL: present=%v value=%v", c.wantPriority, has, pr)
				if has != c.wantPriority {
					t.Fatalf("PROBLEM CONFIRMED: priority present=%v, want %v", has, c.wantPriority)
				}
				if c.rec.Priority != 0 && has && int(pr.(float64)) != c.rec.Priority {
					t.Fatalf("priority = %v, want %d", pr, c.rec.Priority)
				}
			})
		}
	}
}

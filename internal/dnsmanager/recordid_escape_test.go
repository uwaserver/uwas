package dnsmanager

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// Regression: zoneID and recordID are interpolated into the provider API path
// by string concatenation. Both reach these methods straight from the admin
// HTTP route — r.PathValue("id") on PUT/DELETE /api/v1/dns/{domain}/records/{id}
// — and a {id} wildcard in net/http ServeMux still decodes %2F, so the value
// really can contain "/" and ".." segments.
//
// Unescaped, a recordID of "../../ZONE_VICTIM/dns_records/R" made the request
// address a different zone than the one the operator had authenticated: an
// admin able to edit a record in one zone could delete or overwrite a record
// in any other zone of the same account.
//
// url.PathEscape percent-encodes "/" and "." is left intact but cannot combine
// into a traversal, because no segment can contribute a separator. The
// assertion below is deliberately model-free: it does not re-implement any
// server's normalization, it only states the property that must hold —
// an escaped segment cannot add a path separator, so the request path must
// still have exactly the segments the provider's URL template defines.

// capturePath serves a stub that records the request path it received and
// returns a syntactically valid body for the provider's decode step.
func capturePath(t *testing.T, wantSegments int, body string) (*httptest.Server, *string) {
	t.Helper()
	got := new(string)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// RequestURI is the raw request-target exactly as it arrived on the
		// wire. r.URL.Path is the DECODED form, which turns a correctly-escaped
		// %2F back into "/" and would fail a fixed build; EscapedPath() drops to
		// the decoded form when RawPath is empty (an unescaped build), which
		// would pass a broken one. Only the raw bytes discriminate both ways.
		*got = r.RequestURI
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

func assertSegments(t *testing.T, label, got string, want int) {
	t.Helper()
	// Count non-empty segments: an escaped segment can never introduce a "/",
	// so a segment count above the template's proves an escape.
	n := 0
	for _, seg := range strings.Split(got, "/") {
		if seg != "" {
			n++
		}
	}
	if n != want {
		t.Fatalf("FAIL: %s: path %q has %d segments, want %d — an ID contributed a path separator and reached a different resource", label, got, n, want)
	}
}

// Escaping payloads: each must not add a path segment to the request.
var escapePayloads = []struct{ name, id string }{
	{"dot_dot_traversal", "../../ZONE_VICTIM/dns_records/R"},
	{"leading_slash", "/ZONE_VICTIM/dns_records/R"},
	{"embedded_double_slash", "//ZONE_VICTIM/dns_records/R"},
	{"space", "a b"},
	{"question_mark", "R?x=1"},
	{"hash", "R#frag"},
}

func TestCloudflareRecordPathCannotEscapeZone(t *testing.T) {
	for _, op := range []struct {
		name string
		call func(p *CloudflareProvider, zone, id string) error
	}{
		{"delete", func(p *CloudflareProvider, z, id string) error { return p.DeleteRecord(z, id) }},
		{"update", func(p *CloudflareProvider, z, id string) error { _, err := p.UpdateRecord(z, id, Record{}); return err }},
	} {
		for _, tc := range escapePayloads {
			t.Run(op.name+"/"+tc.name, func(t *testing.T) {
				srv, got := capturePath(t, 4, `{"success":true}`)
				p := NewCloudflare("token")
				p.baseURL = srv.URL

				_ = op.call(p, "ZONE_OPERATOR", tc.id)
				if *got == "" {
					t.Fatalf("FAIL: %s: no request reached the API (harness problem, not a defect)", op.name)
				}
				assertSegments(t, op.name+"/"+tc.name, *got, 4)
			})
		}
	}
}

func TestHetznerRecordPathCannotEscapeZone(t *testing.T) {
	for _, tc := range escapePayloads {
		t.Run(tc.name, func(t *testing.T) {
			srv, got := capturePath(t, 2, `{"record":{"id":"R"}}`)
			p := NewHetzner("token")
			p.baseURL = srv.URL

			if err := p.DeleteRecord("ZONE_OPERATOR", tc.id); err != nil {
				t.Fatalf("FAIL: DeleteRecord: %v", err)
			}
			assertSegments(t, "hetzner/"+tc.name, *got, 2)
		})
	}
}

func TestDigitalOceanRecordPathCannotEscapeZone(t *testing.T) {
	for _, tc := range escapePayloads {
		t.Run(tc.name, func(t *testing.T) {
			srv, got := capturePath(t, 4, `{"domain_record":{"id":1}}`)
			p := NewDigitalOcean("token")
			p.baseURL = srv.URL

			if err := p.DeleteRecord("ZONE_OPERATOR", tc.id); err != nil {
				t.Fatalf("FAIL: DeleteRecord: %v", err)
			}
			assertSegments(t, "digitalocean/"+tc.name, *got, 4)
		})
	}
}

// Control: an ordinary opaque Cloudflare record ID must still address exactly
// one record in the requested zone — the fix must not mangle legitimate IDs.
func TestOrdinaryRecordIDStillAddressesItsZone(t *testing.T) {
	srv, got := capturePath(t, 4, `{"success":true}`)
	p := NewCloudflare("token")
	p.baseURL = srv.URL

	if err := p.DeleteRecord("ZONE_OPERATOR", "372e67954025e0ba6aaa6d586b9e0b59"); err != nil {
		t.Fatalf("DeleteRecord: %v", err)
	}
	want := "/zones/ZONE_OPERATOR/dns_records/372e67954025e0ba6aaa6d586b9e0b59"
	if *got != want {
		t.Fatalf("FAIL: path = %q, want %q", *got, want)
	}
}

// Control: the real DNS handler must still be able to receive an ID that
// contains a percent-encoded slash, and it must arrive decoded — this is what
// makes the fix necessary rather than theoretical.
func TestServeMuxFeedsPathSegmentsToRecordID(t *testing.T) {
	const id = "../../ZONE_VICTIM/dns_records/R"
	mux := http.NewServeMux()
	var got string
	mux.HandleFunc("DELETE /api/v1/dns/{domain}/records/{id}", func(w http.ResponseWriter, r *http.Request) {
		got = r.PathValue("id")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	req, err := http.NewRequest("DELETE", srv.URL+"/api/v1/dns/example.com/records/"+url.PathEscape(id), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got == "" {
		t.Skipf("this Go release does not decode %%2F inside a {id} wildcard; sink is unreachable here")
	}
	if got != id {
		t.Fatalf("got id %q, want %q", got, id)
	}
}

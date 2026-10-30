package dnsmanager

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// writeCapture answers a provider's API and records the "name" of every write
// body, so a test can see exactly what the provider was told (F55300).
type writeCapture struct {
	mu    sync.Mutex
	names []string
}

func (c *writeCapture) RoundTrip(r *http.Request) (*http.Response, error) {
	reply := func(body string) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	}
	if r.Method == http.MethodGet {
		return reply(`{"zones":[{"id":"z1","name":"example.test"}]}`)
	}
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	c.mu.Lock()
	c.names = append(c.names, body["name"].(string))
	c.mu.Unlock()
	return reply(`{"record":{"id":"7"},"domain_record":{"id":7}}`)
}

// Hetzner DNS and DigitalOcean take record names relative to the zone: a
// fully-qualified "blog.example.test" is stored as "blog.example.test.example.test".
// Their List calls already report relative names, so a write must send one too.
func TestRelativeProvidersGetZoneRelativeNames(t *testing.T) {
	cases := []struct{ in, want string }{
		{"blog.example.test", "blog"},
		{"Blog.Example.TEST.", "blog"},
		{"example.test", "@"},
		{"a.b.example.test", "a.b"},
		{"blog", "blog"},                       // control: already relative
		{"@", "@"},                             // control
		{"notexample.test", "notexample.test"}, // label boundary: not inside the zone
	}
	for _, name := range []string{"hetzner", "digitalocean"} {
		for _, c := range cases {
			cap := &writeCapture{}
			var create func() error
			var update func() error
			rec := Record{Type: "A", Name: c.in, Content: "192.0.2.1", TTL: 300}
			if name == "hetzner" {
				p := NewHetzner("t")
				p.client = &http.Client{Transport: cap}
				if _, err := p.FindZoneByDomain("example.test"); err != nil { // the handlers resolve the zone first
					t.Fatal(err)
				}
				create = func() error { _, err := p.CreateRecord("z1", rec); return err }
				update = func() error { _, err := p.UpdateRecord("z1", "r1", rec); return err }
			} else {
				p := NewDigitalOcean("t")
				p.client = &http.Client{Transport: cap}
				create = func() error { _, err := p.CreateRecord("example.test", rec); return err }
				update = func() error { _, err := p.UpdateRecord("example.test", "7", rec); return err }
			}
			if err := create(); err != nil {
				t.Fatalf("%s create: %v", name, err)
			}
			if err := update(); err != nil {
				t.Fatalf("%s update: %v", name, err)
			}
			for i, got := range cap.names {
				if got != c.want {
					t.Errorf("%s write %d of %q sent name %q, want %q", name, i, c.in, got, c.want)
				}
			}
			if len(cap.names) != 2 {
				t.Fatalf("%s: %d writes captured, want 2", name, len(cap.names))
			}
		}
	}
}

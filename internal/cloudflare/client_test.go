package cloudflare

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeCFServer returns an httptest server that mimics the Cloudflare API surface
// we need. Routes only respond to the methods/paths we exercise.
func fakeCFServer(t *testing.T) (*httptest.Server, *cfState) {
	t.Helper()
	state := &cfState{
		zones: []Zone{
			{ID: "zone-1", Name: "example.com"},
			{ID: "zone-2", Name: "app.example.com"},
		},
		tunnels: map[string]Tunnel{},
	}
	mux := http.NewServeMux()

	// GET /zones
	mux.HandleFunc("GET /zones", func(w http.ResponseWriter, _ *http.Request) {
		writeEnvelope(w, true, state.zones, nil)
	})

	// POST /accounts/{acc}/cfd_tunnel
	mux.HandleFunc("POST /accounts/{acc}/cfd_tunnel", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Name, ConfigSrc, TunnelSecret string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		t := Tunnel{ID: "t-" + body.Name, Name: body.Name}
		state.tunnels[t.ID] = t
		writeEnvelope(w, true, t, nil)
	})

	// DELETE /accounts/{acc}/cfd_tunnel/{tid}
	mux.HandleFunc("DELETE /accounts/{acc}/cfd_tunnel/{tid}", func(w http.ResponseWriter, r *http.Request) {
		delete(state.tunnels, r.PathValue("tid"))
		writeEnvelope(w, true, struct{}{}, nil)
	})

	// GET /accounts/{acc}/cfd_tunnel
	mux.HandleFunc("GET /accounts/{acc}/cfd_tunnel", func(w http.ResponseWriter, _ *http.Request) {
		out := make([]Tunnel, 0, len(state.tunnels))
		for _, t := range state.tunnels {
			out = append(out, t)
		}
		writeEnvelope(w, true, out, nil)
	})

	// GET /accounts/{acc}/cfd_tunnel/{tid}/token
	mux.HandleFunc("GET /accounts/{acc}/cfd_tunnel/{tid}/token", func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, true, "TOKEN-FOR-"+r.PathValue("tid"), nil)
	})

	// PUT /accounts/{acc}/cfd_tunnel/{tid}/configurations
	mux.HandleFunc("PUT /accounts/{acc}/cfd_tunnel/{tid}/configurations", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		writeEnvelope(w, true, struct{}{}, nil)
	})

	// POST /zones/{zid}/dns_records
	mux.HandleFunc("POST /zones/{zid}/dns_records", func(w http.ResponseWriter, r *http.Request) {
		var rec DNSRecord
		_ = json.NewDecoder(r.Body).Decode(&rec)
		rec.ID = "rec-1"
		writeEnvelope(w, true, rec, nil)
	})

	// DELETE /zones/{zid}/dns_records/{rid}
	mux.HandleFunc("DELETE /zones/{zid}/dns_records/{rid}", func(w http.ResponseWriter, _ *http.Request) {
		writeEnvelope(w, true, struct{}{}, nil)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, state
}

type cfState struct {
	zones   []Zone
	tunnels map[string]Tunnel
}

func writeEnvelope(w http.ResponseWriter, success bool, result any, errors []map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	body := map[string]any{"success": success, "result": result, "errors": errors}
	_ = json.NewEncoder(w).Encode(body)
}

func newTestClient(t *testing.T) (*Client, *cfState) {
	t.Helper()
	srv, state := fakeCFServer(t)
	c := New("test-token", "acc-1")
	c.SetBaseURL(srv.URL)
	return c, state
}

func TestCreateAndDeleteTunnel(t *testing.T) {
	c, state := newTestClient(t)
	tn, err := c.CreateTunnel("my-tunnel")
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}
	if tn.ID != "t-my-tunnel" || tn.Name != "my-tunnel" {
		t.Fatalf("unexpected tunnel: %+v", tn)
	}
	if _, ok := state.tunnels[tn.ID]; !ok {
		t.Fatalf("tunnel not stored on server")
	}
	if err := c.DeleteTunnel(tn.ID); err != nil {
		t.Fatalf("DeleteTunnel: %v", err)
	}
	if _, ok := state.tunnels[tn.ID]; ok {
		t.Fatalf("tunnel still on server after delete")
	}
}

func TestGetTunnelToken(t *testing.T) {
	c, _ := newTestClient(t)
	tok, err := c.GetTunnelToken("abc")
	if err != nil {
		t.Fatalf("GetTunnelToken: %v", err)
	}
	if tok != "TOKEN-FOR-abc" {
		t.Fatalf("got %q", tok)
	}
}

func TestPutTunnelConfig(t *testing.T) {
	c, _ := newTestClient(t)
	err := c.PutTunnelConfig("tid", []IngressRule{
		{Hostname: "app.example.com", Service: "http://localhost:8080"},
		{Service: "http_status:404"},
	})
	if err != nil {
		t.Fatalf("PutTunnelConfig: %v", err)
	}
}

func TestFindZoneByHostname_LongestSuffix(t *testing.T) {
	c, _ := newTestClient(t)
	// "api.app.example.com" matches both example.com and app.example.com;
	// we want the more specific match (app.example.com).
	z, err := c.FindZoneByHostname("api.app.example.com")
	if err != nil {
		t.Fatalf("FindZoneByHostname: %v", err)
	}
	if z.ID != "zone-2" {
		t.Fatalf("expected zone-2 (app.example.com), got %s (%s)", z.ID, z.Name)
	}
}

func TestFindZoneByHostname_NotFound(t *testing.T) {
	c, _ := newTestClient(t)
	_, err := c.FindZoneByHostname("foo.bar.io")
	if err == nil || !strings.Contains(err.Error(), "no zone covers") {
		t.Fatalf("expected no-zone error, got %v", err)
	}
}

func TestCreateTunnelCNAME(t *testing.T) {
	c, _ := newTestClient(t)
	id, err := c.CreateTunnelCNAME("zone-1", "tunnel.example.com", "my-tunnel-uuid")
	if err != nil {
		t.Fatalf("CreateTunnelCNAME: %v", err)
	}
	if id != "rec-1" {
		t.Fatalf("got record id %q", id)
	}
}

func TestErrorEnvelopeReturnsMessage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /accounts/acc-1/cfd_tunnel", func(w http.ResponseWriter, _ *http.Request) {
		writeEnvelope(w, false, nil, []map[string]any{{"code": 1234, "message": "duplicate name"}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := New("t", "acc-1")
	c.SetBaseURL(srv.URL)
	_, err := c.CreateTunnel("dup")
	if err == nil || !strings.Contains(err.Error(), "duplicate name") {
		t.Fatalf("expected duplicate name error, got %v", err)
	}
}

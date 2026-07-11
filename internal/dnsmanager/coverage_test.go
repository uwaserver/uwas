package dnsmanager

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// truncatedBodyServer returns a server that promises a large Content-Length in
// the response header but hijacks the connection and closes it after writing
// only a few bytes. This forces io.ReadAll on the client to fail with an
// unexpected EOF, exercising the "read response" error branches in each
// provider's request helper. Deterministic, local-only.
func truncatedBodyServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("ResponseWriter does not support hijacking")
		}
		conn, buf, err := hj.Hijack()
		if err != nil {
			t.Fatalf("hijack: %v", err)
		}
		// Declare 1024 bytes but send only a handful, then close.
		buf.WriteString("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 1024\r\n\r\n")
		buf.WriteString("{partial")
		buf.Flush()
		conn.Close()
	}))
}

// These tests cover the inner result-parsing error paths that remain after
// a successful HTTP transaction (and, for Cloudflare, a success=true envelope)
// but where the embedded result payload has the wrong JSON shape for the
// destination type. They are TEST-ONLY and use httptest servers exclusively.

// cfEnvelope wraps a raw result fragment in a success=true Cloudflare envelope.
func cfEnvelope(rawResult string) string {
	return `{"success":true,"result":` + rawResult + `,"errors":[]}`
}

// --- Cloudflare: inner result unmarshal errors (result is wrong shape) ---

func TestCloudflare_ListZones_ResultUnmarshalError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// result is a string, but ListZones expects []Zone
		w.Write([]byte(cfEnvelope(`"not-an-array"`)))
	}))
	t.Cleanup(srv.Close)

	p := newTestCloudflare(srv.URL)
	_, err := p.ListZones()
	if err == nil {
		t.Fatal("expected error")
	}
	// The paginated list path parses result as an array in the envelope, so a
	// non-array result fails at the response-parse stage.
	if !strings.Contains(err.Error(), "parse response") {
		t.Errorf("error = %v, want 'parse response'", err)
	}
}

func TestCloudflare_FindZoneByDomain_ResultUnmarshalError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(cfEnvelope(`123`)))
	}))
	t.Cleanup(srv.Close)

	p := newTestCloudflare(srv.URL)
	_, err := p.FindZoneByDomain("example.com")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "parse zone lookup") {
		t.Errorf("error = %v, want 'parse zone lookup'", err)
	}
}

func TestCloudflare_ListRecords_ResultUnmarshalError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(cfEnvelope(`"oops"`)))
	}))
	t.Cleanup(srv.Close)

	p := newTestCloudflare(srv.URL)
	_, err := p.ListRecords("z1")
	if err == nil {
		t.Fatal("expected error")
	}
	// The paginated list path parses result as an array in the envelope, so a
	// non-array result fails at the response-parse stage.
	if !strings.Contains(err.Error(), "parse response") {
		t.Errorf("error = %v, want 'parse response'", err)
	}
}

func TestCloudflare_CreateRecord_ResultUnmarshalError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// result is an array, but CreateRecord expects a single Record object
		w.Write([]byte(cfEnvelope(`["x"]`)))
	}))
	t.Cleanup(srv.Close)

	p := newTestCloudflare(srv.URL)
	_, err := p.CreateRecord("z1", Record{Type: "A", Name: "x.example.com", Content: "1.1.1.1"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "parse created record") {
		t.Errorf("error = %v, want 'parse created record'", err)
	}
}

func TestCloudflare_UpdateRecord_ResultUnmarshalError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(cfEnvelope(`["x"]`)))
	}))
	t.Cleanup(srv.Close)

	p := newTestCloudflare(srv.URL)
	_, err := p.UpdateRecord("z1", "r1", Record{Type: "A", Name: "x.example.com", Content: "1.1.1.1", TTL: 300})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "parse updated record") {
		t.Errorf("error = %v, want 'parse updated record'", err)
	}
}

// --- DigitalOcean: CreateRecord response parse error ---

func TestDigitalOcean_CreateRecord_ParseResponseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 2xx so the status check passes, but body is not valid JSON
		w.Write([]byte("not json"))
	}))
	t.Cleanup(srv.Close)

	p := newTestDigitalOcean(srv.URL)
	_, err := p.CreateRecord("example.com", Record{Type: "A", Name: "test", Content: "1.1.1.1", TTL: 300})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "parse create record response") {
		t.Errorf("error = %v, want 'parse create record response'", err)
	}
}

// --- Hetzner: CreateRecord response parse error ---

func TestHetzner_CreateRecord_ParseResponseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	t.Cleanup(srv.Close)

	p := newTestHetzner(srv.URL)
	_, err := p.CreateRecord("hz1", Record{Type: "A", Name: "test", Content: "1.1.1.1", TTL: 300})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "parse create record response") {
		t.Errorf("error = %v, want 'parse create record response'", err)
	}
}

// --- Cloudflare: marshal body path with non-nil body (already covered via
// CreateRecord, but assert the auth header is present on a body request too). ---

func TestCloudflare_CreateRecord_AuthHeaderPresent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization = %q, want 'Bearer test-token'", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(cfEnvelope(`{"id":"r1","type":"A","name":"x","content":"1.1.1.1","ttl":1}`)))
	}))
	t.Cleanup(srv.Close)

	p := newTestCloudflare(srv.URL)
	if _, err := p.CreateRecord("z1", Record{Type: "A", Name: "x", Content: "1.1.1.1"}); err != nil {
		t.Fatalf("error: %v", err)
	}
}

// --- Response body read failure (io.ReadAll error) for each provider ---

func TestCloudflare_Do_ReadBodyError(t *testing.T) {
	srv := truncatedBodyServer(t)
	t.Cleanup(srv.Close)

	p := newTestCloudflare(srv.URL)
	_, err := p.ListZones()
	if err == nil {
		t.Fatal("expected read error")
	}
	if !strings.Contains(err.Error(), "read response") {
		t.Errorf("error = %v, want 'read response'", err)
	}
}

func TestDigitalOcean_DoRequest_ReadBodyError(t *testing.T) {
	srv := truncatedBodyServer(t)
	t.Cleanup(srv.Close)

	p := newTestDigitalOcean(srv.URL)
	_, err := p.ListZones()
	if err == nil {
		t.Fatal("expected read error")
	}
	if !strings.Contains(err.Error(), "read response") {
		t.Errorf("error = %v, want 'read response'", err)
	}
}

func TestHetzner_HetznerRequest_ReadBodyError(t *testing.T) {
	srv := truncatedBodyServer(t)
	t.Cleanup(srv.Close)

	p := newTestHetzner(srv.URL)
	_, err := p.ListZones()
	if err == nil {
		t.Fatal("expected read error")
	}
	if !strings.Contains(err.Error(), "read response") {
		t.Errorf("error = %v, want 'read response'", err)
	}
}

func TestRoute53_R53Request_ReadBodyError(t *testing.T) {
	srv := truncatedBodyServer(t)
	t.Cleanup(srv.Close)

	p := newTestRoute53(srv.URL)
	_, err := p.ListZones()
	if err == nil {
		t.Fatal("expected read error")
	}
	if !strings.Contains(err.Error(), "read response") {
		t.Errorf("error = %v, want 'read response'", err)
	}
}

// --- Cloudflare do() read body error (covers line 82-84) ---
func TestCloudflare_Do_ReadBodyError_CreateRecord(t *testing.T) {
	srv := truncatedBodyServer(t)
	t.Cleanup(srv.Close)

	p := newTestCloudflare(srv.URL)
	_, err := p.CreateRecord("z1", Record{Type: "A", Name: "x", Content: "1.2.3.4"})
	if err == nil {
		t.Fatal("expected read error via do()")
	}
	if !strings.Contains(err.Error(), "read response") {
		t.Errorf("error = %v, want 'read response'", err)
	}
}

// --- Cloudflare do(): json.Unmarshal envelope error (covers line 93-95) ---
func TestCloudflare_Do_EnvelopeUnmarshalError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("{{{not json"))
	}))
	t.Cleanup(srv.Close)

	p := newTestCloudflare(srv.URL)
	_, err := p.CreateRecord("z1", Record{Type: "A", Name: "x", Content: "1.2.3.4"})
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "parse response") {
		t.Errorf("error = %v, want 'parse response'", err)
	}
}

// --- Cloudflare do(): success=false with no errors (covers line 100) ---
func TestCloudflare_Do_APIFailureNoErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"success":false,"result":null,"errors":[]}`))
	}))
	t.Cleanup(srv.Close)

	p := newTestCloudflare(srv.URL)
	_, err := p.CreateRecord("z1", Record{Type: "A", Name: "x", Content: "1.2.3.4"})
	if err == nil {
		t.Fatal("expected API failure error")
	}
	if !strings.Contains(err.Error(), "request failed") {
		t.Errorf("error = %v, want 'request failed'", err)
	}
}

// --- Cloudflare doList(): success=false with no errors (covers doList non-success) ---
func TestCloudflare_DoList_APIFailureNoErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"success":false,"result":null,"errors":[],"result_info":{"total_pages":0}}`))
	}))
	t.Cleanup(srv.Close)

	p := newTestCloudflare(srv.URL)
	_, err := p.ListZones()
	if err == nil {
		t.Fatal("expected API failure error")
	}
	if !strings.Contains(err.Error(), "request failed") {
		t.Errorf("error = %v, want 'request failed'", err)
	}
}

// --- Cloudflare ListZones: result unmarshal error (covers line 168-170) ---
func TestCloudflare_ListZones_ResultItemUnmarshalError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(cfEnvelope(`[{"id":"z1","name":"ex.com"},{"id":123}]`)))
	}))
	t.Cleanup(srv.Close)

	p := newTestCloudflare(srv.URL)
	_, err := p.ListZones()
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "parse zones") {
		t.Errorf("error = %v, want 'parse zones'", err)
	}
}

// --- Cloudflare ListRecords: result unmarshal error (covers line 238-240) ---
func TestCloudflare_ListRecords_ResultItemUnmarshalError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(cfEnvelope(`[{"type":"A"},{"type":123}]`)))
	}))
	t.Cleanup(srv.Close)

	p := newTestCloudflare(srv.URL)
	_, err := p.ListRecords("z1")
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "parse records") {
		t.Errorf("error = %v, want 'parse records'", err)
	}
}

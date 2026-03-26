package dnsmanager

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestHetzner(url string) *HetznerProvider {
	p := NewHetzner("hetzner-test-token")
	p.baseURL = url
	return p
}

// --- Constructor ---

func TestNewHetzner(t *testing.T) {
	p := NewHetzner("tok-h")
	if p == nil {
		t.Fatal("NewHetzner returned nil")
	}
	if p.apiToken != "tok-h" {
		t.Errorf("apiToken = %q", p.apiToken)
	}
	if p.client == nil {
		t.Error("client is nil")
	}
	if p.baseURL != hetznerBaseURL {
		t.Errorf("baseURL = %q, want %q", p.baseURL, hetznerBaseURL)
	}
}

// --- ListZones ---

func TestHetzner_ListZones_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("method = %s", r.Method)
		}
		if r.URL.Path != "/zones" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Auth-API-Token"); got != "hetzner-test-token" {
			t.Errorf("Auth-API-Token = %q", got)
		}
		resp := map[string]interface{}{
			"zones": []map[string]string{
				{"id": "hz1", "name": "example.com"},
				{"id": "hz2", "name": "test.org"},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)

	p := newTestHetzner(srv.URL)
	zones, err := p.ListZones()
	if err != nil {
		t.Fatalf("ListZones error: %v", err)
	}
	if len(zones) != 2 {
		t.Fatalf("len(zones) = %d, want 2", len(zones))
	}
	if zones[0].ID != "hz1" || zones[0].Name != "example.com" || zones[0].Status != "active" {
		t.Errorf("zones[0] = %+v", zones[0])
	}
}

func TestHetzner_ListZones_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("internal server error"))
	}))
	t.Cleanup(srv.Close)

	p := newTestHetzner(srv.URL)
	_, err := p.ListZones()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error = %v, want to contain '500'", err)
	}
}

func TestHetzner_ListZones_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("{bad json"))
	}))
	t.Cleanup(srv.Close)

	p := newTestHetzner(srv.URL)
	_, err := p.ListZones()
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- FindZoneByDomain ---

func TestHetzner_FindZoneByDomain_ExactMatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"zones": []map[string]string{
				{"id": "hz1", "name": "example.com"},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)

	p := newTestHetzner(srv.URL)
	z, err := p.FindZoneByDomain("example.com")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if z.ID != "hz1" {
		t.Errorf("zone ID = %q", z.ID)
	}
}

func TestHetzner_FindZoneByDomain_Subdomain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"zones": []map[string]string{
				{"id": "hz1", "name": "example.com"},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)

	p := newTestHetzner(srv.URL)
	z, err := p.FindZoneByDomain("www.example.com")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if z.ID != "hz1" {
		t.Errorf("zone ID = %q", z.ID)
	}
}

func TestHetzner_FindZoneByDomain_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"zones": []map[string]string{
				{"id": "hz1", "name": "other.com"},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)

	p := newTestHetzner(srv.URL)
	_, err := p.FindZoneByDomain("example.com")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "zone not found") {
		t.Errorf("error = %v", err)
	}
}

func TestHetzner_FindZoneByDomain_ListError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte("forbidden"))
	}))
	t.Cleanup(srv.Close)

	p := newTestHetzner(srv.URL)
	_, err := p.FindZoneByDomain("example.com")
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- ListRecords ---

func TestHetzner_ListRecords_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("method = %s", r.Method)
		}
		if !strings.Contains(r.URL.RawQuery, "zone_id=hz1") {
			t.Errorf("query = %s, want zone_id=hz1", r.URL.RawQuery)
		}
		resp := map[string]interface{}{
			"records": []map[string]interface{}{
				{"id": "hr1", "type": "A", "name": "@", "value": "1.2.3.4", "ttl": 300},
				{"id": "hr2", "type": "MX", "name": "@", "value": "mail.example.com", "ttl": 3600},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)

	p := newTestHetzner(srv.URL)
	recs, err := p.ListRecords("hz1")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("len(recs) = %d", len(recs))
	}
	if recs[0].ID != "hr1" || recs[0].Content != "1.2.3.4" {
		t.Errorf("recs[0] = %+v", recs[0])
	}
}

func TestHetzner_ListRecords_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte("not found"))
	}))
	t.Cleanup(srv.Close)

	p := newTestHetzner(srv.URL)
	_, err := p.ListRecords("bad-zone")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestHetzner_ListRecords_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	t.Cleanup(srv.Close)

	p := newTestHetzner(srv.URL)
	_, err := p.ListRecords("hz1")
	if err == nil {
		t.Fatal("expected error from invalid JSON")
	}
}

// --- CreateRecord ---

func TestHetzner_CreateRecord_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("method = %s", r.Method)
		}
		if r.URL.Path != "/records" {
			t.Errorf("path = %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		var data map[string]interface{}
		json.Unmarshal(body, &data)
		if data["zone_id"] != "hz1" {
			t.Errorf("zone_id = %v", data["zone_id"])
		}
		if data["type"] != "A" {
			t.Errorf("type = %v", data["type"])
		}
		resp := map[string]interface{}{
			"record": map[string]string{"id": "new-hr1"},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)

	p := newTestHetzner(srv.URL)
	rec, err := p.CreateRecord("hz1", Record{Type: "A", Name: "test", Content: "1.2.3.4", TTL: 300})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if rec.ID != "new-hr1" {
		t.Errorf("ID = %q", rec.ID)
	}
	if rec.Type != "A" {
		t.Errorf("Type = %q", rec.Type)
	}
}

func TestHetzner_CreateRecord_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(422)
		w.Write([]byte("unprocessable entity"))
	}))
	t.Cleanup(srv.Close)

	p := newTestHetzner(srv.URL)
	_, err := p.CreateRecord("hz1", Record{Type: "A", Name: "test", Content: "1.2.3.4"})
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- UpdateRecord ---

func TestHetzner_UpdateRecord_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" {
			t.Errorf("method = %s", r.Method)
		}
		if r.URL.Path != "/records/hr1" {
			t.Errorf("path = %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		var data map[string]interface{}
		json.Unmarshal(body, &data)
		if data["value"] != "5.6.7.8" {
			t.Errorf("value = %v", data["value"])
		}
		w.Write([]byte("{}"))
	}))
	t.Cleanup(srv.Close)

	p := newTestHetzner(srv.URL)
	rec, err := p.UpdateRecord("hz1", "hr1", Record{Type: "A", Name: "test", Content: "5.6.7.8", TTL: 300})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if rec.ID != "hr1" {
		t.Errorf("ID = %q", rec.ID)
	}
	if rec.Content != "5.6.7.8" {
		t.Errorf("Content = %q", rec.Content)
	}
}

func TestHetzner_UpdateRecord_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("error"))
	}))
	t.Cleanup(srv.Close)

	p := newTestHetzner(srv.URL)
	_, err := p.UpdateRecord("hz1", "hr1", Record{Type: "A", Name: "test", Content: "1.1.1.1"})
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- DeleteRecord ---

func TestHetzner_DeleteRecord_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" {
			t.Errorf("method = %s", r.Method)
		}
		if r.URL.Path != "/records/hr1" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.WriteHeader(200)
	}))
	t.Cleanup(srv.Close)

	p := newTestHetzner(srv.URL)
	err := p.DeleteRecord("hz1", "hr1")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
}

func TestHetzner_DeleteRecord_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte("not found"))
	}))
	t.Cleanup(srv.Close)

	p := newTestHetzner(srv.URL)
	err := p.DeleteRecord("hz1", "bad-id")
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- Network error ---

func TestHetzner_NetworkError(t *testing.T) {
	p := newTestHetzner("http://127.0.0.1:1")
	_, err := p.ListZones()
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- Content-Type header set for POST/PUT ---

func TestHetzner_ContentTypeOnBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			if ct := r.Header.Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", ct)
			}
		}
		resp := map[string]interface{}{
			"record": map[string]string{"id": "hr1"},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)

	p := newTestHetzner(srv.URL)
	_, err := p.CreateRecord("hz1", Record{Type: "A", Name: "test", Content: "1.2.3.4", TTL: 300})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
}

// --- No Content-Type on GET ---

func TestHetzner_NoContentTypeOnGET(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			if ct := r.Header.Get("Content-Type"); ct != "" {
				t.Errorf("Content-Type on GET = %q, want empty", ct)
			}
		}
		resp := map[string]interface{}{"zones": []interface{}{}}
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)

	p := newTestHetzner(srv.URL)
	_, _ = p.ListZones()
}

// --- json.Marshal error ---

func TestHetzner_MarshalError(t *testing.T) {
	p := newTestHetzner("http://127.0.0.1:1")
	// A channel cannot be marshalled to JSON
	_, err := p.hetznerRequest("POST", "/records", make(chan int))
	if err == nil {
		t.Fatal("expected marshal error")
	}
}

// --- http.NewRequest invalid URL ---

func TestHetzner_InvalidURL(t *testing.T) {
	p := NewHetzner("tok")
	p.baseURL = "://invalid"
	_, err := p.ListZones()
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
}

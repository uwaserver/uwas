package dnsmanager

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestDigitalOcean(url string) *DigitalOceanProvider {
	p := NewDigitalOcean("do-test-token")
	p.baseURL = url
	return p
}

// --- Constructor ---

func TestNewDigitalOcean(t *testing.T) {
	p := NewDigitalOcean("tok-do")
	if p == nil {
		t.Fatal("NewDigitalOcean returned nil")
	}
	if p.apiToken != "tok-do" {
		t.Errorf("apiToken = %q", p.apiToken)
	}
	if p.client == nil {
		t.Error("client is nil")
	}
	if p.baseURL != doBaseURL {
		t.Errorf("baseURL = %q, want %q", p.baseURL, doBaseURL)
	}
}

// --- ListZones ---

func TestDigitalOcean_ListZones_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("method = %s", r.Method)
		}
		if r.URL.Path != "/domains" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer do-test-token" {
			t.Errorf("Authorization = %q", got)
		}
		resp := map[string]interface{}{
			"domains": []map[string]string{
				{"name": "example.com"},
				{"name": "test.org"},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)

	p := newTestDigitalOcean(srv.URL)
	zones, err := p.ListZones()
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(zones) != 2 {
		t.Fatalf("len(zones) = %d", len(zones))
	}
	// DigitalOcean uses domain name as ID
	if zones[0].ID != "example.com" || zones[0].Name != "example.com" {
		t.Errorf("zones[0] = %+v", zones[0])
	}
	if zones[0].Status != "active" {
		t.Errorf("Status = %q, want 'active'", zones[0].Status)
	}
}

func TestDigitalOcean_ListZones_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("internal error"))
	}))
	t.Cleanup(srv.Close)

	p := newTestDigitalOcean(srv.URL)
	_, err := p.ListZones()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error = %v", err)
	}
}

func TestDigitalOcean_ListZones_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	t.Cleanup(srv.Close)

	p := newTestDigitalOcean(srv.URL)
	_, err := p.ListZones()
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- FindZoneByDomain ---

func TestDigitalOcean_FindZoneByDomain_ExactMatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"domains": []map[string]string{
				{"name": "example.com"},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)

	p := newTestDigitalOcean(srv.URL)
	z, err := p.FindZoneByDomain("example.com")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if z.Name != "example.com" {
		t.Errorf("Name = %q", z.Name)
	}
}

func TestDigitalOcean_FindZoneByDomain_Subdomain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"domains": []map[string]string{
				{"name": "example.com"},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)

	p := newTestDigitalOcean(srv.URL)
	z, err := p.FindZoneByDomain("sub.example.com")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if z.Name != "example.com" {
		t.Errorf("Name = %q", z.Name)
	}
}

func TestDigitalOcean_FindZoneByDomain_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"domains": []map[string]string{
				{"name": "other.com"},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)

	p := newTestDigitalOcean(srv.URL)
	_, err := p.FindZoneByDomain("example.com")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "zone not found") {
		t.Errorf("error = %v", err)
	}
}

func TestDigitalOcean_FindZoneByDomain_ListError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte("unauthorized"))
	}))
	t.Cleanup(srv.Close)

	p := newTestDigitalOcean(srv.URL)
	_, err := p.FindZoneByDomain("example.com")
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- ListRecords ---

func TestDigitalOcean_ListRecords_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/domains/example.com/records" {
			t.Errorf("path = %s", r.URL.Path)
		}
		resp := map[string]interface{}{
			"domain_records": []map[string]interface{}{
				{"id": 123, "type": "A", "name": "@", "data": "1.2.3.4", "ttl": 1800, "priority": 0},
				{"id": 456, "type": "MX", "name": "@", "data": "mail.example.com", "ttl": 3600, "priority": 10},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)

	p := newTestDigitalOcean(srv.URL)
	recs, err := p.ListRecords("example.com")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("len(recs) = %d", len(recs))
	}
	if recs[0].ID != "123" || recs[0].Content != "1.2.3.4" {
		t.Errorf("recs[0] = %+v", recs[0])
	}
	if recs[1].Priority != 10 {
		t.Errorf("recs[1].Priority = %d", recs[1].Priority)
	}
}

func TestDigitalOcean_ListRecords_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte("domain not found"))
	}))
	t.Cleanup(srv.Close)

	p := newTestDigitalOcean(srv.URL)
	_, err := p.ListRecords("nonexistent.com")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDigitalOcean_ListRecords_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("bad"))
	}))
	t.Cleanup(srv.Close)

	p := newTestDigitalOcean(srv.URL)
	_, err := p.ListRecords("example.com")
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- CreateRecord ---

func TestDigitalOcean_CreateRecord_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("method = %s", r.Method)
		}
		if r.URL.Path != "/domains/example.com/records" {
			t.Errorf("path = %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		var data map[string]interface{}
		json.Unmarshal(body, &data)
		if data["type"] != "A" {
			t.Errorf("type = %v", data["type"])
		}
		if data["data"] != "5.6.7.8" {
			t.Errorf("data = %v", data["data"])
		}
		resp := map[string]interface{}{
			"domain_record": map[string]interface{}{"id": 789},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)

	p := newTestDigitalOcean(srv.URL)
	rec, err := p.CreateRecord("example.com", Record{Type: "A", Name: "test", Content: "5.6.7.8", TTL: 1800})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if rec.ID != "789" {
		t.Errorf("ID = %q", rec.ID)
	}
}

func TestDigitalOcean_CreateRecord_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(422)
		w.Write([]byte("unprocessable"))
	}))
	t.Cleanup(srv.Close)

	p := newTestDigitalOcean(srv.URL)
	_, err := p.CreateRecord("example.com", Record{Type: "A", Name: "test", Content: "1.1.1.1"})
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- UpdateRecord ---

func TestDigitalOcean_UpdateRecord_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" {
			t.Errorf("method = %s", r.Method)
		}
		if r.URL.Path != "/domains/example.com/records/123" {
			t.Errorf("path = %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		var data map[string]interface{}
		json.Unmarshal(body, &data)
		if data["data"] != "9.8.7.6" {
			t.Errorf("data = %v", data["data"])
		}
		w.Write([]byte("{}"))
	}))
	t.Cleanup(srv.Close)

	p := newTestDigitalOcean(srv.URL)
	rec, err := p.UpdateRecord("example.com", "123", Record{Type: "A", Name: "test", Content: "9.8.7.6", TTL: 300})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if rec.ID != "123" {
		t.Errorf("ID = %q", rec.ID)
	}
	if rec.Content != "9.8.7.6" {
		t.Errorf("Content = %q", rec.Content)
	}
}

func TestDigitalOcean_UpdateRecord_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("error"))
	}))
	t.Cleanup(srv.Close)

	p := newTestDigitalOcean(srv.URL)
	_, err := p.UpdateRecord("example.com", "123", Record{Type: "A", Name: "test", Content: "1.1.1.1"})
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- DeleteRecord ---

func TestDigitalOcean_DeleteRecord_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" {
			t.Errorf("method = %s", r.Method)
		}
		if r.URL.Path != "/domains/example.com/records/123" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.WriteHeader(204)
	}))
	t.Cleanup(srv.Close)

	p := newTestDigitalOcean(srv.URL)
	err := p.DeleteRecord("example.com", "123")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
}

func TestDigitalOcean_DeleteRecord_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte("not found"))
	}))
	t.Cleanup(srv.Close)

	p := newTestDigitalOcean(srv.URL)
	err := p.DeleteRecord("example.com", "bad-id")
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- Network error ---

func TestDigitalOcean_NetworkError(t *testing.T) {
	p := newTestDigitalOcean("http://127.0.0.1:1")
	_, err := p.ListZones()
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- Content-Type / Authorization ---

func TestDigitalOcean_ContentTypeOnBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			if ct := r.Header.Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q", ct)
			}
		}
		resp := map[string]interface{}{
			"domain_record": map[string]interface{}{"id": 1},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)

	p := newTestDigitalOcean(srv.URL)
	_, _ = p.CreateRecord("example.com", Record{Type: "A", Name: "@", Content: "1.1.1.1", TTL: 300})
}

func TestDigitalOcean_NoContentTypeOnGET(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			if ct := r.Header.Get("Content-Type"); ct != "" {
				t.Errorf("Content-Type on GET = %q", ct)
			}
		}
		resp := map[string]interface{}{"domains": []interface{}{}}
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)

	p := newTestDigitalOcean(srv.URL)
	_, _ = p.ListZones()
}

// --- json.Marshal error ---

func TestDigitalOcean_MarshalError(t *testing.T) {
	p := newTestDigitalOcean("http://127.0.0.1:1")
	_, err := p.doRequest("POST", "/domains/x/records", make(chan int))
	if err == nil {
		t.Fatal("expected marshal error")
	}
}

// --- http.NewRequest invalid URL ---

func TestDigitalOcean_InvalidURL(t *testing.T) {
	p := NewDigitalOcean("tok")
	p.baseURL = "://invalid"
	_, err := p.ListZones()
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
}

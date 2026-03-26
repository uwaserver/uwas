package dnsmanager

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- Cloudflare helpers ---

// cfResponse builds a Cloudflare-style JSON envelope.
func cfResponse(success bool, result interface{}, errMsg string) string {
	r, _ := json.Marshal(result)
	if !success && errMsg != "" {
		return `{"success":false,"result":null,"errors":[{"message":"` + errMsg + `"}]}`
	}
	if !success {
		return `{"success":false,"result":null,"errors":[]}`
	}
	return `{"success":true,"result":` + string(r) + `,"errors":[]}`
}

func newTestCloudflare(url string) *CloudflareProvider {
	p := NewCloudflare("test-token")
	p.baseURL = url
	return p
}

// --- Constructor ---

func TestNewCloudflare(t *testing.T) {
	p := NewCloudflare("tok-abc")
	if p == nil {
		t.Fatal("NewCloudflare returned nil")
	}
	if p.apiToken != "tok-abc" {
		t.Errorf("apiToken = %q, want %q", p.apiToken, "tok-abc")
	}
	if p.client == nil {
		t.Error("client is nil")
	}
	if p.baseURL != cfBaseURL {
		t.Errorf("baseURL = %q, want %q", p.baseURL, cfBaseURL)
	}
}

// --- ListZones ---

func TestCloudflare_ListZones_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if !strings.HasPrefix(r.URL.Path, "/zones") {
			t.Errorf("path = %s, want /zones*", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization = %q", got)
		}
		zones := []Zone{
			{ID: "z1", Name: "example.com", Status: "active"},
			{ID: "z2", Name: "test.org", Status: "active"},
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(cfResponse(true, zones, "")))
	}))
	t.Cleanup(srv.Close)

	p := newTestCloudflare(srv.URL)
	zones, err := p.ListZones()
	if err != nil {
		t.Fatalf("ListZones error: %v", err)
	}
	if len(zones) != 2 {
		t.Fatalf("len(zones) = %d, want 2", len(zones))
	}
	if zones[0].ID != "z1" || zones[0].Name != "example.com" {
		t.Errorf("zones[0] = %+v", zones[0])
	}
	if zones[1].ID != "z2" || zones[1].Name != "test.org" {
		t.Errorf("zones[1] = %+v", zones[1])
	}
}

func TestCloudflare_ListZones_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(cfResponse(false, nil, "authentication error")))
	}))
	t.Cleanup(srv.Close)

	p := newTestCloudflare(srv.URL)
	_, err := p.ListZones()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "authentication error") {
		t.Errorf("error = %v, want to contain 'authentication error'", err)
	}
}

func TestCloudflare_ListZones_NoErrors(t *testing.T) {
	// success=false with empty errors array
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(cfResponse(false, nil, "")))
	}))
	t.Cleanup(srv.Close)

	p := newTestCloudflare(srv.URL)
	_, err := p.ListZones()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "request failed") {
		t.Errorf("error = %v, want to contain 'request failed'", err)
	}
}

func TestCloudflare_ListZones_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	t.Cleanup(srv.Close)

	p := newTestCloudflare(srv.URL)
	_, err := p.ListZones()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "parse response") {
		t.Errorf("error = %v, want to contain 'parse response'", err)
	}
}

// --- FindZoneByDomain ---

func TestCloudflare_FindZoneByDomain_Found(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		zones := []Zone{{ID: "z1", Name: "example.com", Status: "active"}}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(cfResponse(true, zones, "")))
	}))
	t.Cleanup(srv.Close)

	p := newTestCloudflare(srv.URL)
	z, err := p.FindZoneByDomain("example.com")
	if err != nil {
		t.Fatalf("FindZoneByDomain error: %v", err)
	}
	if z.ID != "z1" || z.Name != "example.com" {
		t.Errorf("zone = %+v", z)
	}
}

func TestCloudflare_FindZoneByDomain_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(cfResponse(true, []Zone{}, "")))
	}))
	t.Cleanup(srv.Close)

	p := newTestCloudflare(srv.URL)
	_, err := p.FindZoneByDomain("nonexistent.com")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "zone not found") {
		t.Errorf("error = %v", err)
	}
}

func TestCloudflare_FindZoneByDomain_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(cfResponse(false, nil, "forbidden")))
	}))
	t.Cleanup(srv.Close)

	p := newTestCloudflare(srv.URL)
	_, err := p.FindZoneByDomain("example.com")
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- ListRecords ---

func TestCloudflare_ListRecords_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/zones/z1/dns_records") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		recs := []Record{
			{ID: "r1", Type: "A", Name: "example.com", Content: "1.2.3.4", TTL: 300},
			{ID: "r2", Type: "CNAME", Name: "www.example.com", Content: "example.com", TTL: 300},
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(cfResponse(true, recs, "")))
	}))
	t.Cleanup(srv.Close)

	p := newTestCloudflare(srv.URL)
	recs, err := p.ListRecords("z1")
	if err != nil {
		t.Fatalf("ListRecords error: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("len(recs) = %d, want 2", len(recs))
	}
	if recs[0].Type != "A" || recs[0].Content != "1.2.3.4" {
		t.Errorf("recs[0] = %+v", recs[0])
	}
}

func TestCloudflare_ListRecords_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(cfResponse(false, nil, "zone not found")))
	}))
	t.Cleanup(srv.Close)

	p := newTestCloudflare(srv.URL)
	_, err := p.ListRecords("bad-zone")
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- CreateRecord ---

func TestCloudflare_CreateRecord_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("method = %s, want POST", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		var rec Record
		json.Unmarshal(body, &rec)
		if rec.TTL != 1 {
			t.Errorf("TTL = %d, want 1 (auto)", rec.TTL)
		}
		result := Record{ID: "new-r1", Type: rec.Type, Name: rec.Name, Content: rec.Content, TTL: rec.TTL}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(cfResponse(true, result, "")))
	}))
	t.Cleanup(srv.Close)

	p := newTestCloudflare(srv.URL)
	rec, err := p.CreateRecord("z1", Record{Type: "A", Name: "test.example.com", Content: "5.6.7.8"})
	if err != nil {
		t.Fatalf("CreateRecord error: %v", err)
	}
	if rec.ID != "new-r1" {
		t.Errorf("ID = %q, want 'new-r1'", rec.ID)
	}
}

func TestCloudflare_CreateRecord_WithTTL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var rec Record
		json.Unmarshal(body, &rec)
		if rec.TTL != 600 {
			t.Errorf("TTL = %d, want 600", rec.TTL)
		}
		result := Record{ID: "r-ttl", Type: rec.Type, Name: rec.Name, Content: rec.Content, TTL: rec.TTL}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(cfResponse(true, result, "")))
	}))
	t.Cleanup(srv.Close)

	p := newTestCloudflare(srv.URL)
	rec, err := p.CreateRecord("z1", Record{Type: "A", Name: "test.example.com", Content: "5.6.7.8", TTL: 600})
	if err != nil {
		t.Fatalf("CreateRecord error: %v", err)
	}
	if rec.TTL != 600 {
		t.Errorf("TTL = %d, want 600", rec.TTL)
	}
}

func TestCloudflare_CreateRecord_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(cfResponse(false, nil, "record already exists")))
	}))
	t.Cleanup(srv.Close)

	p := newTestCloudflare(srv.URL)
	_, err := p.CreateRecord("z1", Record{Type: "A", Name: "dup.example.com", Content: "1.1.1.1"})
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- UpdateRecord ---

func TestCloudflare_UpdateRecord_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" {
			t.Errorf("method = %s, want PUT", r.Method)
		}
		if !strings.Contains(r.URL.Path, "/dns_records/r1") {
			t.Errorf("path = %s, expected to contain /dns_records/r1", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		var rec Record
		json.Unmarshal(body, &rec)
		result := Record{ID: "r1", Type: rec.Type, Name: rec.Name, Content: rec.Content, TTL: rec.TTL}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(cfResponse(true, result, "")))
	}))
	t.Cleanup(srv.Close)

	p := newTestCloudflare(srv.URL)
	rec, err := p.UpdateRecord("z1", "r1", Record{Type: "A", Name: "example.com", Content: "9.8.7.6"})
	if err != nil {
		t.Fatalf("UpdateRecord error: %v", err)
	}
	if rec.Content != "9.8.7.6" {
		t.Errorf("Content = %q, want '9.8.7.6'", rec.Content)
	}
}

func TestCloudflare_UpdateRecord_AutoTTL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var rec Record
		json.Unmarshal(body, &rec)
		if rec.TTL != 1 {
			t.Errorf("TTL = %d, want 1 (auto)", rec.TTL)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(cfResponse(true, rec, "")))
	}))
	t.Cleanup(srv.Close)

	p := newTestCloudflare(srv.URL)
	_, err := p.UpdateRecord("z1", "r1", Record{Type: "A", Name: "x.com", Content: "1.1.1.1", TTL: 0})
	if err != nil {
		t.Fatalf("UpdateRecord error: %v", err)
	}
}

// --- DeleteRecord ---

func TestCloudflare_DeleteRecord_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" {
			t.Errorf("method = %s, want DELETE", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(cfResponse(true, map[string]string{"id": "r1"}, "")))
	}))
	t.Cleanup(srv.Close)

	p := newTestCloudflare(srv.URL)
	err := p.DeleteRecord("z1", "r1")
	if err != nil {
		t.Fatalf("DeleteRecord error: %v", err)
	}
}

func TestCloudflare_DeleteRecord_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(cfResponse(false, nil, "record not found")))
	}))
	t.Cleanup(srv.Close)

	p := newTestCloudflare(srv.URL)
	err := p.DeleteRecord("z1", "bad-id")
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- SyncDomainToIP ---

func TestCloudflare_SyncDomainToIP_AlreadyCorrect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.Path
		if strings.Contains(path, "/dns_records") {
			recs := []Record{
				{ID: "r1", Type: "A", Name: "www.example.com", Content: "1.2.3.4", TTL: 300},
			}
			w.Write([]byte(cfResponse(true, recs, "")))
		} else if strings.HasPrefix(path, "/zones") {
			zones := []Zone{{ID: "z1", Name: "example.com", Status: "active"}}
			w.Write([]byte(cfResponse(true, zones, "")))
		}
	}))
	t.Cleanup(srv.Close)

	p := newTestCloudflare(srv.URL)
	err := p.SyncDomainToIP("www.example.com", "1.2.3.4")
	if err != nil {
		t.Fatalf("SyncDomainToIP error: %v", err)
	}
}

func TestCloudflare_SyncDomainToIP_Update(t *testing.T) {
	updated := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.Path
		if r.Method == "PUT" && strings.Contains(path, "/dns_records/r1") {
			updated = true
			body, _ := io.ReadAll(r.Body)
			var rec Record
			json.Unmarshal(body, &rec)
			if rec.Content != "5.6.7.8" {
				t.Errorf("update Content = %q, want '5.6.7.8'", rec.Content)
			}
			w.Write([]byte(cfResponse(true, rec, "")))
		} else if strings.Contains(path, "/dns_records") {
			recs := []Record{
				{ID: "r1", Type: "A", Name: "www.example.com", Content: "1.2.3.4", TTL: 300},
			}
			w.Write([]byte(cfResponse(true, recs, "")))
		} else if strings.HasPrefix(path, "/zones") {
			zones := []Zone{{ID: "z1", Name: "example.com", Status: "active"}}
			w.Write([]byte(cfResponse(true, zones, "")))
		}
	}))
	t.Cleanup(srv.Close)

	p := newTestCloudflare(srv.URL)
	err := p.SyncDomainToIP("www.example.com", "5.6.7.8")
	if err != nil {
		t.Fatalf("SyncDomainToIP error: %v", err)
	}
	if !updated {
		t.Error("expected record to be updated")
	}
}

func TestCloudflare_SyncDomainToIP_Create(t *testing.T) {
	created := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.Path
		if r.Method == "POST" && strings.Contains(path, "/dns_records") {
			created = true
			result := Record{ID: "new-r", Type: "A", Name: "new.example.com", Content: "10.0.0.1", TTL: 1}
			w.Write([]byte(cfResponse(true, result, "")))
		} else if r.Method == "GET" && strings.Contains(path, "/dns_records") {
			// return no matching records
			w.Write([]byte(cfResponse(true, []Record{}, "")))
		} else if strings.HasPrefix(path, "/zones") {
			zones := []Zone{{ID: "z1", Name: "example.com", Status: "active"}}
			w.Write([]byte(cfResponse(true, zones, "")))
		}
	}))
	t.Cleanup(srv.Close)

	p := newTestCloudflare(srv.URL)
	err := p.SyncDomainToIP("new.example.com", "10.0.0.1")
	if err != nil {
		t.Fatalf("SyncDomainToIP error: %v", err)
	}
	if !created {
		t.Error("expected record to be created")
	}
}

func TestCloudflare_SyncDomainToIP_ZoneError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(cfResponse(true, []Zone{}, "")))
	}))
	t.Cleanup(srv.Close)

	p := newTestCloudflare(srv.URL)
	err := p.SyncDomainToIP("foo.nonexistent.com", "1.1.1.1")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCloudflare_SyncDomainToIP_ListRecordsError(t *testing.T) {
	call := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		call++
		if call == 1 {
			// FindZoneByDomain
			zones := []Zone{{ID: "z1", Name: "example.com", Status: "active"}}
			w.Write([]byte(cfResponse(true, zones, "")))
		} else {
			// ListRecords fails
			w.Write([]byte(cfResponse(false, nil, "internal error")))
		}
	}))
	t.Cleanup(srv.Close)

	p := newTestCloudflare(srv.URL)
	err := p.SyncDomainToIP("www.example.com", "1.1.1.1")
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- extractBaseDomain / splitDomain ---

func TestExtractBaseDomain(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"example.com", "example.com"},
		{"www.example.com", "example.com"},
		{"sub.domain.example.com", "example.com"},
		{"a.b.c.d.example.com", "example.com"},
		{"singleword", "singleword"},
		{"", ""},
	}
	for _, tt := range tests {
		got := extractBaseDomain(tt.input)
		if got != tt.want {
			t.Errorf("extractBaseDomain(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSplitDomain(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"example.com", []string{"example", "com"}},
		{"www.example.com", []string{"www", "example", "com"}},
		{"a.b.c", []string{"a", "b", "c"}},
		{"singleword", []string{"singleword"}},
		{"", nil},
		{"trailing.", []string{"trailing"}},
		{".leading", []string{"leading"}},
		{"a..b", []string{"a", "b"}},
	}
	for _, tt := range tests {
		got := splitDomain(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("splitDomain(%q) = %v (len %d), want %v (len %d)",
				tt.input, got, len(got), tt.want, len(tt.want))
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("splitDomain(%q)[%d] = %q, want %q",
					tt.input, i, got[i], tt.want[i])
			}
		}
	}
}

// --- Struct tests ---

func TestRecordStruct(t *testing.T) {
	rec := Record{
		ID: "abc", Type: "A", Name: "example.com", Content: "1.2.3.4",
		TTL: 300, Proxied: true, Priority: 10,
	}
	if rec.ID != "abc" {
		t.Errorf("ID = %q", rec.ID)
	}
	if rec.Type != "A" {
		t.Errorf("Type = %q", rec.Type)
	}
	if rec.TTL != 300 {
		t.Errorf("TTL = %d", rec.TTL)
	}
	if !rec.Proxied {
		t.Error("Proxied should be true")
	}
	if rec.Priority != 10 {
		t.Errorf("Priority = %d", rec.Priority)
	}
}

func TestZoneStruct(t *testing.T) {
	z := Zone{ID: "z1", Name: "example.com", Status: "active"}
	if z.ID != "z1" || z.Name != "example.com" || z.Status != "active" {
		t.Errorf("Zone = %+v", z)
	}
}

// --- do method edge cases ---

func TestCloudflare_Do_NetworkError(t *testing.T) {
	p := newTestCloudflare("http://127.0.0.1:1") // nothing listening
	_, err := p.ListZones()
	if err == nil {
		t.Fatal("expected network error")
	}
}

func TestCloudflare_Do_BodyMarshal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if len(body) == 0 {
			t.Error("expected non-empty body for POST")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(cfResponse(true, Record{ID: "x"}, "")))
	}))
	t.Cleanup(srv.Close)

	p := newTestCloudflare(srv.URL)
	_, err := p.CreateRecord("z1", Record{Type: "TXT", Name: "txt.example.com", Content: "v=spf1"})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
}

func TestCloudflare_Do_InvalidURL(t *testing.T) {
	p := NewCloudflare("tok")
	p.baseURL = "://invalid"
	_, err := p.ListZones()
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
}

func TestCloudflare_UpdateRecord_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(cfResponse(false, nil, "update failed")))
	}))
	t.Cleanup(srv.Close)

	p := newTestCloudflare(srv.URL)
	_, err := p.UpdateRecord("z1", "r1", Record{Type: "A", Name: "x.com", Content: "1.1.1.1", TTL: 300})
	if err == nil {
		t.Fatal("expected error")
	}
}

package dnsmanager

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestRoute53(url string) *Route53Provider {
	p := NewRoute53("AKIATEST", "secrettest", "us-east-1")
	p.baseURL = url
	return p
}

// --- Constructor ---

func TestNewRoute53(t *testing.T) {
	p := NewRoute53("ak", "sk", "eu-west-1")
	if p == nil {
		t.Fatal("returned nil")
	}
	if p.accessKey != "ak" {
		t.Errorf("accessKey = %q", p.accessKey)
	}
	if p.secretKey != "sk" {
		t.Errorf("secretKey = %q", p.secretKey)
	}
	if p.region != "eu-west-1" {
		t.Errorf("region = %q", p.region)
	}
	if p.client == nil {
		t.Error("client is nil")
	}
	if p.baseURL != r53BaseURL {
		t.Errorf("baseURL = %q", p.baseURL)
	}
}

func TestNewRoute53_DefaultRegion(t *testing.T) {
	p := NewRoute53("ak", "sk", "")
	if p.region != "us-east-1" {
		t.Errorf("region = %q, want us-east-1", p.region)
	}
}

// --- ListZones ---

func TestRoute53_ListZones_Success(t *testing.T) {
	xmlResp := `<?xml version="1.0" encoding="UTF-8"?>
<ListHostedZonesResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/">
  <HostedZones>
    <HostedZone>
      <Id>/hostedzone/Z1234</Id>
      <Name>example.com.</Name>
    </HostedZone>
    <HostedZone>
      <Id>/hostedzone/Z5678</Id>
      <Name>test.org.</Name>
    </HostedZone>
  </HostedZones>
</ListHostedZonesResponse>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("method = %s", r.Method)
		}
		if r.URL.Path != "/hostedzone" {
			t.Errorf("path = %s", r.URL.Path)
		}
		// Verify Authorization header exists (signed request)
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256") {
			t.Errorf("Authorization = %q, want AWS4-HMAC-SHA256 prefix", auth)
		}
		if !strings.Contains(auth, "Credential=AKIATEST/") {
			t.Errorf("Authorization missing Credential")
		}
		w.Header().Set("Content-Type", "text/xml")
		w.Write([]byte(xmlResp))
	}))
	t.Cleanup(srv.Close)

	p := newTestRoute53(srv.URL)
	zones, err := p.ListZones()
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(zones) != 2 {
		t.Fatalf("len(zones) = %d", len(zones))
	}
	// Verify /hostedzone/ prefix stripped and trailing dot removed
	if zones[0].ID != "Z1234" {
		t.Errorf("zones[0].ID = %q, want Z1234", zones[0].ID)
	}
	if zones[0].Name != "example.com" {
		t.Errorf("zones[0].Name = %q, want example.com", zones[0].Name)
	}
	if zones[0].Status != "active" {
		t.Errorf("zones[0].Status = %q", zones[0].Status)
	}
	if zones[1].ID != "Z5678" || zones[1].Name != "test.org" {
		t.Errorf("zones[1] = %+v", zones[1])
	}
}

func TestRoute53_ListZones_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte("<ErrorResponse><Error><Code>AccessDenied</Code></Error></ErrorResponse>"))
	}))
	t.Cleanup(srv.Close)

	p := newTestRoute53(srv.URL)
	_, err := p.ListZones()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("error = %v", err)
	}
}

func TestRoute53_ListZones_InvalidXML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not xml"))
	}))
	t.Cleanup(srv.Close)

	p := newTestRoute53(srv.URL)
	_, err := p.ListZones()
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- FindZoneByDomain ---

func TestRoute53_FindZoneByDomain_Found(t *testing.T) {
	xmlResp := `<?xml version="1.0"?>
<ListHostedZonesResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/">
  <HostedZones>
    <HostedZone><Id>/hostedzone/Z1</Id><Name>example.com.</Name></HostedZone>
  </HostedZones>
</ListHostedZonesResponse>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(xmlResp))
	}))
	t.Cleanup(srv.Close)

	p := newTestRoute53(srv.URL)
	z, err := p.FindZoneByDomain("example.com")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if z.ID != "Z1" || z.Name != "example.com" {
		t.Errorf("zone = %+v", z)
	}
}

func TestRoute53_FindZoneByDomain_Subdomain(t *testing.T) {
	xmlResp := `<?xml version="1.0"?>
<ListHostedZonesResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/">
  <HostedZones>
    <HostedZone><Id>/hostedzone/Z1</Id><Name>example.com.</Name></HostedZone>
  </HostedZones>
</ListHostedZonesResponse>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(xmlResp))
	}))
	t.Cleanup(srv.Close)

	p := newTestRoute53(srv.URL)
	z, err := p.FindZoneByDomain("www.example.com")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if z.Name != "example.com" {
		t.Errorf("Name = %q", z.Name)
	}
}

func TestRoute53_FindZoneByDomain_NotFound(t *testing.T) {
	xmlResp := `<?xml version="1.0"?>
<ListHostedZonesResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/">
  <HostedZones>
    <HostedZone><Id>/hostedzone/Z1</Id><Name>other.com.</Name></HostedZone>
  </HostedZones>
</ListHostedZonesResponse>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(xmlResp))
	}))
	t.Cleanup(srv.Close)

	p := newTestRoute53(srv.URL)
	_, err := p.FindZoneByDomain("example.com")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "zone not found") {
		t.Errorf("error = %v", err)
	}
}

func TestRoute53_FindZoneByDomain_ListError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("error"))
	}))
	t.Cleanup(srv.Close)

	p := newTestRoute53(srv.URL)
	_, err := p.FindZoneByDomain("example.com")
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- ListRecords ---

func TestRoute53_ListRecords_Success(t *testing.T) {
	xmlResp := `<?xml version="1.0"?>
<ListResourceRecordSetsResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/">
  <ResourceRecordSets>
    <ResourceRecordSet>
      <Name>example.com.</Name>
      <Type>A</Type>
      <TTL>300</TTL>
      <ResourceRecords>
        <ResourceRecord><Value>1.2.3.4</Value></ResourceRecord>
        <ResourceRecord><Value>5.6.7.8</Value></ResourceRecord>
      </ResourceRecords>
    </ResourceRecordSet>
    <ResourceRecordSet>
      <Name>example.com.</Name>
      <Type>NS</Type>
      <TTL>86400</TTL>
      <ResourceRecords>
        <ResourceRecord><Value>ns1.aws.example.com</Value></ResourceRecord>
      </ResourceRecords>
    </ResourceRecordSet>
  </ResourceRecordSets>
</ListResourceRecordSetsResponse>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/hostedzone/Z1/rrset" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Write([]byte(xmlResp))
	}))
	t.Cleanup(srv.Close)

	p := newTestRoute53(srv.URL)
	recs, err := p.ListRecords("Z1")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	// 2 A records + 1 NS record = 3 total
	if len(recs) != 3 {
		t.Fatalf("len(recs) = %d, want 3", len(recs))
	}
	// First A record
	if recs[0].Type != "A" || recs[0].Content != "1.2.3.4" {
		t.Errorf("recs[0] = %+v", recs[0])
	}
	// Name should have trailing dot stripped
	if recs[0].Name != "example.com" {
		t.Errorf("recs[0].Name = %q", recs[0].Name)
	}
	// ID format: Name:Type
	if recs[0].ID != "example.com.:A" {
		t.Errorf("recs[0].ID = %q", recs[0].ID)
	}
}

func TestRoute53_ListRecords_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte("not found"))
	}))
	t.Cleanup(srv.Close)

	p := newTestRoute53(srv.URL)
	_, err := p.ListRecords("bad-zone")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRoute53_ListRecords_InvalidXML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not xml"))
	}))
	t.Cleanup(srv.Close)

	p := newTestRoute53(srv.URL)
	_, err := p.ListRecords("Z1")
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- CreateRecord ---

func TestRoute53_CreateRecord_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("method = %s", r.Method)
		}
		if r.URL.Path != "/hostedzone/Z1/rrset" {
			t.Errorf("path = %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		s := string(body)
		if !strings.Contains(s, "<Action>CREATE</Action>") {
			t.Errorf("body missing CREATE action: %s", s)
		}
		if !strings.Contains(s, "<Name>test.example.com.</Name>") {
			t.Errorf("body missing Name: %s", s)
		}
		if !strings.Contains(s, "<Value>1.2.3.4</Value>") {
			t.Errorf("body missing Value: %s", s)
		}
		if !strings.Contains(s, "<TTL>300</TTL>") {
			t.Errorf("body missing TTL: %s", s)
		}
		w.Write([]byte(`<?xml version="1.0"?><ChangeResourceRecordSetsResponse><ChangeInfo><Status>PENDING</Status></ChangeInfo></ChangeResourceRecordSetsResponse>`))
	}))
	t.Cleanup(srv.Close)

	p := newTestRoute53(srv.URL)
	rec, err := p.CreateRecord("Z1", Record{Type: "A", Name: "test.example.com", Content: "1.2.3.4", TTL: 300})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if rec.Name != "test.example.com" || rec.Content != "1.2.3.4" {
		t.Errorf("rec = %+v", rec)
	}
}

func TestRoute53_CreateRecord_DefaultTTL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "<TTL>300</TTL>") {
			t.Errorf("default TTL not 300: %s", string(body))
		}
		w.Write([]byte(`<?xml version="1.0"?><ChangeResourceRecordSetsResponse><ChangeInfo><Status>PENDING</Status></ChangeInfo></ChangeResourceRecordSetsResponse>`))
	}))
	t.Cleanup(srv.Close)

	p := newTestRoute53(srv.URL)
	_, err := p.CreateRecord("Z1", Record{Type: "A", Name: "x.example.com", Content: "1.1.1.1", TTL: 0})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
}

func TestRoute53_CreateRecord_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte("bad request"))
	}))
	t.Cleanup(srv.Close)

	p := newTestRoute53(srv.URL)
	_, err := p.CreateRecord("Z1", Record{Type: "A", Name: "x.com", Content: "1.1.1.1"})
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- UpdateRecord ---

func TestRoute53_UpdateRecord_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "<Action>UPSERT</Action>") {
			t.Errorf("body missing UPSERT: %s", string(body))
		}
		w.Write([]byte(`<?xml version="1.0"?><ChangeResourceRecordSetsResponse><ChangeInfo><Status>PENDING</Status></ChangeInfo></ChangeResourceRecordSetsResponse>`))
	}))
	t.Cleanup(srv.Close)

	p := newTestRoute53(srv.URL)
	rec, err := p.UpdateRecord("Z1", "example.com.:A", Record{Type: "A", Name: "example.com", Content: "9.8.7.6", TTL: 300})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if rec.Content != "9.8.7.6" {
		t.Errorf("Content = %q", rec.Content)
	}
}

func TestRoute53_UpdateRecord_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("error"))
	}))
	t.Cleanup(srv.Close)

	p := newTestRoute53(srv.URL)
	_, err := p.UpdateRecord("Z1", "x.com.:A", Record{Type: "A", Name: "x.com", Content: "1.1.1.1"})
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- DeleteRecord ---

func TestRoute53_DeleteRecord_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s := string(body)
		if !strings.Contains(s, "<Action>DELETE</Action>") {
			t.Errorf("body missing DELETE: %s", s)
		}
		// Name should come from the recordID "example.com:A" -> Name="example.com"
		if !strings.Contains(s, "<Name>example.com.</Name>") {
			t.Errorf("body missing Name: %s", s)
		}
		if !strings.Contains(s, "<Type>A</Type>") {
			t.Errorf("body missing Type: %s", s)
		}
		w.Write([]byte(`<?xml version="1.0"?><ChangeResourceRecordSetsResponse><ChangeInfo><Status>PENDING</Status></ChangeInfo></ChangeResourceRecordSetsResponse>`))
	}))
	t.Cleanup(srv.Close)

	p := newTestRoute53(srv.URL)
	err := p.DeleteRecord("Z1", "example.com:A")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
}

func TestRoute53_DeleteRecord_InvalidID(t *testing.T) {
	p := NewRoute53("ak", "sk", "us-east-1")
	err := p.DeleteRecord("Z1", "no-colon-here")
	if err == nil {
		t.Fatal("expected error for invalid record ID")
	}
	if !strings.Contains(err.Error(), "invalid record ID") {
		t.Errorf("error = %v", err)
	}
}

func TestRoute53_DeleteRecord_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte("bad request"))
	}))
	t.Cleanup(srv.Close)

	p := newTestRoute53(srv.URL)
	err := p.DeleteRecord("Z1", "x.com:A")
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- changeRecord trailing dot ---

func TestRoute53_ChangeRecord_TrailingDot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s := string(body)
		// Name "example.com" should become "example.com." in XML
		if !strings.Contains(s, "<Name>example.com.</Name>") {
			t.Errorf("Name missing trailing dot: %s", s)
		}
		w.Write([]byte(`<?xml version="1.0"?><ChangeResourceRecordSetsResponse><ChangeInfo><Status>PENDING</Status></ChangeInfo></ChangeResourceRecordSetsResponse>`))
	}))
	t.Cleanup(srv.Close)

	p := newTestRoute53(srv.URL)
	_, err := p.CreateRecord("Z1", Record{Type: "A", Name: "example.com", Content: "1.1.1.1", TTL: 300})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
}

func TestRoute53_ChangeRecord_AlreadyTrailingDot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s := string(body)
		// Name "example.com." should stay as "example.com." (not "example.com..")
		if strings.Contains(s, "example.com..") {
			t.Errorf("double trailing dot: %s", s)
		}
		w.Write([]byte(`<?xml version="1.0"?><ChangeResourceRecordSetsResponse><ChangeInfo><Status>PENDING</Status></ChangeInfo></ChangeResourceRecordSetsResponse>`))
	}))
	t.Cleanup(srv.Close)

	p := newTestRoute53(srv.URL)
	_, err := p.CreateRecord("Z1", Record{Type: "A", Name: "example.com.", Content: "1.1.1.1", TTL: 300})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
}

// --- signRequest / AWS Sig V4 ---

func TestRoute53_SignRequest_HasAuthHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth == "" {
			t.Fatal("missing Authorization header")
		}
		if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256") {
			t.Errorf("Authorization = %q, want AWS4-HMAC-SHA256 prefix", auth)
		}
		if !strings.Contains(auth, "Credential=AKIATEST/") {
			t.Errorf("missing Credential in Authorization")
		}
		if !strings.Contains(auth, "SignedHeaders=host;x-amz-date") {
			t.Errorf("missing SignedHeaders in Authorization")
		}
		if !strings.Contains(auth, "Signature=") {
			t.Errorf("missing Signature in Authorization")
		}

		amzDate := r.Header.Get("X-Amz-Date")
		if amzDate == "" {
			t.Error("missing X-Amz-Date")
		}
		// Format: 20060102T150405Z
		if len(amzDate) != 16 || amzDate[8] != 'T' || amzDate[15] != 'Z' {
			t.Errorf("X-Amz-Date format invalid: %q", amzDate)
		}

		xmlResp := `<?xml version="1.0"?><ListHostedZonesResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/"><HostedZones></HostedZones></ListHostedZonesResponse>`
		w.Write([]byte(xmlResp))
	}))
	t.Cleanup(srv.Close)

	p := newTestRoute53(srv.URL)
	_, err := p.ListZones()
	if err != nil {
		t.Fatalf("error: %v", err)
	}
}

func TestRoute53_SignRequest_ContentTypeOnPOST(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			if ct := r.Header.Get("Content-Type"); ct != "text/xml" {
				t.Errorf("Content-Type = %q, want text/xml", ct)
			}
		}
		w.Write([]byte(`<?xml version="1.0"?><ChangeResourceRecordSetsResponse><ChangeInfo><Status>PENDING</Status></ChangeInfo></ChangeResourceRecordSetsResponse>`))
	}))
	t.Cleanup(srv.Close)

	p := newTestRoute53(srv.URL)
	_, err := p.CreateRecord("Z1", Record{Type: "A", Name: "x.com", Content: "1.1.1.1", TTL: 300})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
}

// --- sha256hex ---

func TestSha256hex(t *testing.T) {
	tests := []struct {
		input []byte
		want  string
	}{
		{nil, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		{[]byte(""), "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		{[]byte("hello"), "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"},
	}
	for _, tt := range tests {
		got := sha256hex(tt.input)
		if got != tt.want {
			t.Errorf("sha256hex(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// --- hmacSHA256 ---

func TestHmacSHA256(t *testing.T) {
	key := []byte("secret")
	data := []byte("message")
	result := hmacSHA256(key, data)

	// Verify independently
	h := hmac.New(sha256.New, key)
	h.Write(data)
	expected := h.Sum(nil)

	if hex.EncodeToString(result) != hex.EncodeToString(expected) {
		t.Errorf("hmacSHA256 mismatch")
	}
}

func TestHmacSHA256_Empty(t *testing.T) {
	result := hmacSHA256([]byte("key"), []byte(""))
	if len(result) != sha256.Size {
		t.Errorf("len = %d, want %d", len(result), sha256.Size)
	}
}

// --- Network error ---

func TestRoute53_NetworkError(t *testing.T) {
	p := newTestRoute53("http://127.0.0.1:1")
	_, err := p.ListZones()
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- http.NewRequest invalid URL ---

func TestRoute53_InvalidURL(t *testing.T) {
	p := NewRoute53("ak", "sk", "us-east-1")
	p.baseURL = "://invalid"
	_, err := p.ListZones()
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
}

package dnsmanager

import (
	"testing"
)

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

func TestRecordStruct(t *testing.T) {
	rec := Record{
		ID:       "abc123",
		Type:     "A",
		Name:     "example.com",
		Content:  "93.184.216.34",
		TTL:      300,
		Proxied:  true,
		Priority: 0,
	}

	if rec.Type != "A" {
		t.Errorf("expected Type 'A', got %q", rec.Type)
	}
	if rec.TTL != 300 {
		t.Errorf("expected TTL 300, got %d", rec.TTL)
	}
	if !rec.Proxied {
		t.Error("expected Proxied=true")
	}
}

func TestZoneStruct(t *testing.T) {
	z := Zone{
		ID:     "zone-id-1",
		Name:   "example.com",
		Status: "active",
	}

	if z.Name != "example.com" {
		t.Errorf("expected Name 'example.com', got %q", z.Name)
	}
	if z.Status != "active" {
		t.Errorf("expected Status 'active', got %q", z.Status)
	}
}

func TestNewCloudflare(t *testing.T) {
	p := NewCloudflare("test-token")
	if p == nil {
		t.Fatal("NewCloudflare returned nil")
	}
	if p.apiToken != "test-token" {
		t.Errorf("expected apiToken 'test-token', got %q", p.apiToken)
	}
	if p.client == nil {
		t.Error("expected non-nil HTTP client")
	}
}

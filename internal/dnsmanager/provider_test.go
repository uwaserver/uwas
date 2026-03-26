package dnsmanager

import (
	"testing"
)

func TestNewProvider_Cloudflare(t *testing.T) {
	p, err := NewProvider("cloudflare", map[string]string{"api_token": "cf-tok"})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	cf, ok := p.(*CloudflareProvider)
	if !ok {
		t.Fatalf("type = %T, want *CloudflareProvider", p)
	}
	if cf.apiToken != "cf-tok" {
		t.Errorf("apiToken = %q", cf.apiToken)
	}
}

func TestNewProvider_Cloudflare_MissingToken(t *testing.T) {
	_, err := NewProvider("cloudflare", map[string]string{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestNewProvider_Cloudflare_EmptyToken(t *testing.T) {
	_, err := NewProvider("cloudflare", map[string]string{"api_token": ""})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestNewProvider_Route53(t *testing.T) {
	p, err := NewProvider("route53", map[string]string{
		"access_key": "AK", "secret_key": "SK", "region": "eu-west-1",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	r53, ok := p.(*Route53Provider)
	if !ok {
		t.Fatalf("type = %T, want *Route53Provider", p)
	}
	if r53.accessKey != "AK" || r53.secretKey != "SK" || r53.region != "eu-west-1" {
		t.Errorf("fields: ak=%q sk=%q region=%q", r53.accessKey, r53.secretKey, r53.region)
	}
}

func TestNewProvider_Route53_DefaultRegion(t *testing.T) {
	p, err := NewProvider("route53", map[string]string{
		"access_key": "AK", "secret_key": "SK",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	r53 := p.(*Route53Provider)
	if r53.region != "us-east-1" {
		t.Errorf("region = %q, want us-east-1", r53.region)
	}
}

func TestNewProvider_Route53_MissingKeys(t *testing.T) {
	tests := []map[string]string{
		{},
		{"access_key": "AK"},
		{"secret_key": "SK"},
		{"access_key": "", "secret_key": "SK"},
		{"access_key": "AK", "secret_key": ""},
	}
	for _, creds := range tests {
		_, err := NewProvider("route53", creds)
		if err == nil {
			t.Errorf("expected error for creds %v", creds)
		}
	}
}

func TestNewProvider_Hetzner(t *testing.T) {
	p, err := NewProvider("hetzner", map[string]string{"api_token": "hz-tok"})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	hz, ok := p.(*HetznerProvider)
	if !ok {
		t.Fatalf("type = %T, want *HetznerProvider", p)
	}
	if hz.apiToken != "hz-tok" {
		t.Errorf("apiToken = %q", hz.apiToken)
	}
}

func TestNewProvider_Hetzner_MissingToken(t *testing.T) {
	_, err := NewProvider("hetzner", map[string]string{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestNewProvider_DigitalOcean(t *testing.T) {
	p, err := NewProvider("digitalocean", map[string]string{"api_token": "do-tok"})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	do, ok := p.(*DigitalOceanProvider)
	if !ok {
		t.Fatalf("type = %T, want *DigitalOceanProvider", p)
	}
	if do.apiToken != "do-tok" {
		t.Errorf("apiToken = %q", do.apiToken)
	}
}

func TestNewProvider_DigitalOcean_MissingToken(t *testing.T) {
	_, err := NewProvider("digitalocean", map[string]string{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestNewProvider_Unknown(t *testing.T) {
	_, err := NewProvider("unknown", map[string]string{})
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); got != "unknown DNS provider: unknown" {
		t.Errorf("error = %q", got)
	}
}

func TestNewProvider_EmptyType(t *testing.T) {
	_, err := NewProvider("", map[string]string{})
	if err == nil {
		t.Fatal("expected error")
	}
}

// Verify all providers implement the Provider interface
func TestProviderInterface(t *testing.T) {
	var _ Provider = (*CloudflareProvider)(nil)
	var _ Provider = (*HetznerProvider)(nil)
	var _ Provider = (*DigitalOceanProvider)(nil)
	var _ Provider = (*Route53Provider)(nil)
}

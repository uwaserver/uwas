package uwastls

import (
	"fmt"

	"github.com/uwaserver/uwas/internal/dnsmanager"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/tls/acme"
)

// acmeDNSProvider wraps a dnsmanager.Provider and implements acme.DNSProvider
// for ACME DNS-01 challenges.
type acmeDNSProvider struct {
	dp       dnsmanager.Provider
	zoneID   string // cached zone ID for record operations
	zoneName string // cached zone name for domain prefix stripping
	log      *logger.Logger
}

// PresentDNSChallenge creates a TXT record for DNS-01 challenge validation.
func (p *acmeDNSProvider) PresentDNSChallenge(domain, token, keyAuth string) error {
	// domain is like "_acme-challenge.example.com"
	// We need to find the zone (example.com) and create a TXT record there.
	zoneID, err := p.findZone(domain)
	if err != nil {
		return fmt.Errorf("find zone for %s: %w", domain, err)
	}

	// Strip zone prefix to get the record name
	name := domain
	if len(name) > len(p.zoneName)+1 {
		name = name[:len(name)-len(p.zoneName)-1]
	}

	_, err = p.dp.CreateRecord(zoneID, dnsmanager.Record{
		Type:    "TXT",
		Name:    name,
		Content: keyAuth,
		TTL:     120, // 2 minutes - short TTL for challenge records
	})
	return err
}

// CleanupDNSChallenge removes the TXT record after challenge validation.
func (p *acmeDNSProvider) CleanupDNSChallenge(domain, token, keyAuth string) error {
	zoneID, err := p.findZone(domain)
	if err != nil {
		return nil // nothing to clean up
	}

	name := domain
	if len(name) > len(p.zoneName)+1 {
		name = name[:len(name)-len(p.zoneName)-1]
	}

	// Find and delete the record.
	records, err := p.dp.ListRecords(zoneID)
	if err != nil {
		return err
	}
	for _, rec := range records {
		if rec.Type == "TXT" && rec.Name == name && rec.Content == keyAuth {
			return p.dp.DeleteRecord(zoneID, rec.ID)
		}
	}
	return nil
}

// findZone finds the zone ID for a domain, caching it for subsequent calls.
// Returns the zone ID for use in record operations.
func (p *acmeDNSProvider) findZone(domain string) (string, error) {
	if p.zoneID != "" {
		return p.zoneID, nil
	}

	zone, err := p.dp.FindZoneByDomain(domain)
	if err != nil {
		return "", err
	}
	p.zoneID = zone.ID
	p.zoneName = zone.Name
	return p.zoneID, nil
}

// NewACMEDNSProvider creates an acme.DNSProvider from config.
func NewACMEDNSProvider(providerName string, credentials map[string]string, log *logger.Logger) (acme.DNSProvider, error) {
	var dp dnsmanager.Provider

	switch providerName {
	case "cloudflare":
		apiToken := credentials["api_token"]
		if apiToken == "" {
			return nil, fmt.Errorf("cloudflare: api_token required")
		}
		dp = dnsmanager.NewCloudflare(apiToken)
	case "digitalocean":
		apiToken := credentials["api_token"]
		if apiToken == "" {
			return nil, fmt.Errorf("digitalocean: api_token required")
		}
		dp = dnsmanager.NewDigitalOcean(apiToken)
	case "hetzner":
		apiToken := credentials["api_token"]
		if apiToken == "" {
			return nil, fmt.Errorf("hetzner: api_token required")
		}
		dp = dnsmanager.NewHetzner(apiToken)
	case "route53":
		accessKey := credentials["access_key"]
		secretKey := credentials["secret_key"]
		region := credentials["region"]
		if accessKey == "" || secretKey == "" {
			return nil, fmt.Errorf("route53: access_key and secret_key required")
		}
		if region == "" {
			region = "us-east-1"
		}
		dp = dnsmanager.NewRoute53(accessKey, secretKey, region)
	default:
		return nil, fmt.Errorf("unknown DNS provider: %s", providerName)
	}

	return &acmeDNSProvider{dp: dp, log: log}, nil
}

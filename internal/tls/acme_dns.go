package uwastls

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/uwaserver/uwas/internal/dnsmanager"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/tls/acme"
)

const (
	// dnsPropagationTimeout bounds how long we wait for the challenge TXT
	// record to become visible before notifying the CA.
	dnsPropagationTimeout = 2 * time.Minute
	dnsPollInterval       = 5 * time.Second
)

// lookupTXT is a testable hook for DNS TXT lookups.
var lookupTXT = net.LookupTXT

// acmeDNSProvider wraps a dnsmanager.Provider and implements acme.DNSProvider
// for ACME DNS-01 challenges.
type acmeDNSProvider struct {
	dp                 dnsmanager.Provider
	mu                 sync.Mutex
	zones              map[string]*dnsmanager.Zone // challenge domain → zone
	log                *logger.Logger
	propagationTimeout time.Duration // 0 disables the propagation wait (tests)
}

// dnsChallengeTXT returns the TXT record content for a DNS-01 challenge:
// base64url(SHA-256(keyAuthorization)) per RFC 8555 §8.4.
func dnsChallengeTXT(keyAuth string) string {
	h := sha256.Sum256([]byte(keyAuth))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

// PresentDNSChallenge creates a TXT record for DNS-01 challenge validation.
func (p *acmeDNSProvider) PresentDNSChallenge(domain, token, keyAuth string) error {
	// domain is like "_acme-challenge.example.com"
	// We need to find the zone (example.com) and create a TXT record there.
	zone, err := p.findZone(domain)
	if err != nil {
		return fmt.Errorf("find zone for %s: %w", domain, err)
	}

	// Strip zone prefix to get the record name
	name := domain
	if len(name) > len(zone.Name)+1 {
		name = name[:len(name)-len(zone.Name)-1]
	}

	content := dnsChallengeTXT(keyAuth)
	_, err = p.dp.CreateRecord(zone.ID, dnsmanager.Record{
		Type:    "TXT",
		Name:    name,
		Content: content,
		TTL:     120, // 2 minutes - short TTL for challenge records
	})
	if err != nil {
		return err
	}

	p.waitForPropagation(domain, content)
	return nil
}

// waitForPropagation polls DNS for the challenge TXT record so the CA's
// validation query doesn't race record propagation. Best-effort: on timeout
// we proceed anyway and let the CA's own retries handle any remaining lag.
func (p *acmeDNSProvider) waitForPropagation(domain, content string) {
	if p.propagationTimeout <= 0 {
		return
	}
	deadline := time.Now().Add(p.propagationTimeout)
	for time.Now().Before(deadline) {
		txts, err := lookupTXT(domain)
		if err == nil {
			for _, txt := range txts {
				if txt == content {
					return
				}
			}
		}
		time.Sleep(dnsPollInterval)
	}
	if p.log != nil {
		p.log.Warn("dns-01 TXT record not visible before timeout, proceeding", "domain", domain)
	}
}

// CleanupDNSChallenge removes the TXT record after challenge validation.
func (p *acmeDNSProvider) CleanupDNSChallenge(domain, token, keyAuth string) error {
	zone, err := p.findZone(domain)
	if err != nil {
		return nil // nothing to clean up
	}

	name := domain
	if len(name) > len(zone.Name)+1 {
		name = name[:len(name)-len(zone.Name)-1]
	}

	// Find and delete the record.
	content := dnsChallengeTXT(keyAuth)
	records, err := p.dp.ListRecords(zone.ID)
	if err != nil {
		return err
	}
	for _, rec := range records {
		if rec.Type == "TXT" && rec.Name == name && rec.Content == content {
			return p.dp.DeleteRecord(zone.ID, rec.ID)
		}
	}
	return nil
}

// findZone finds the zone for a domain, caching it per challenge domain so
// domains in different zones don't reuse each other's zone.
func (p *acmeDNSProvider) findZone(domain string) (*dnsmanager.Zone, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if zone, ok := p.zones[domain]; ok {
		return zone, nil
	}

	zone, err := p.dp.FindZoneByDomain(domain)
	if err != nil {
		return nil, err
	}
	if p.zones == nil {
		p.zones = make(map[string]*dnsmanager.Zone)
	}
	p.zones[domain] = zone
	return zone, nil
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

	return &acmeDNSProvider{dp: dp, log: log, propagationTimeout: dnsPropagationTimeout}, nil
}

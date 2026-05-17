package dnsmanager

import "testing"

// Verify all providers implement the Provider interface
func TestProviderInterface(t *testing.T) {
	var _ Provider = (*CloudflareProvider)(nil)
	var _ Provider = (*HetznerProvider)(nil)
	var _ Provider = (*DigitalOceanProvider)(nil)
	var _ Provider = (*Route53Provider)(nil)
}

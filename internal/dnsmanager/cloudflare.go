// Package dnsmanager manages DNS records via provider APIs (Cloudflare, etc.)
package dnsmanager

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Record represents a DNS record.
type Record struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`    // A, AAAA, CNAME, MX, TXT, NS
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
	Proxied bool   `json:"proxied,omitempty"`
	Priority int   `json:"priority,omitempty"`
}

// Zone represents a DNS zone.
type Zone struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

// Provider interface for DNS management.
type Provider interface {
	ListZones() ([]Zone, error)
	ListRecords(zoneID string) ([]Record, error)
	CreateRecord(zoneID string, rec Record) (*Record, error)
	UpdateRecord(zoneID, recordID string, rec Record) (*Record, error)
	DeleteRecord(zoneID, recordID string) error
	FindZoneByDomain(domain string) (*Zone, error)
}

// CloudflareProvider implements DNS management via Cloudflare API.
type CloudflareProvider struct {
	apiToken string
	client   *http.Client
}

const cfBaseURL = "https://api.cloudflare.com/client/v4"

// NewCloudflare creates a Cloudflare DNS provider.
func NewCloudflare(apiToken string) *CloudflareProvider {
	return &CloudflareProvider{
		apiToken: apiToken,
		client:   &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *CloudflareProvider) do(method, path string, body any) (json.RawMessage, error) {
	var bodyReader io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, cfBaseURL+path, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	var cfResp struct {
		Success bool            `json:"success"`
		Result  json.RawMessage `json:"result"`
		Errors  []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(respBody, &cfResp); err != nil {
		return nil, fmt.Errorf("cloudflare: parse response: %w", err)
	}
	if !cfResp.Success {
		if len(cfResp.Errors) > 0 {
			return nil, fmt.Errorf("cloudflare: %s", cfResp.Errors[0].Message)
		}
		return nil, fmt.Errorf("cloudflare: request failed")
	}
	return cfResp.Result, nil
}

func (c *CloudflareProvider) ListZones() ([]Zone, error) {
	data, err := c.do("GET", "/zones?per_page=50", nil)
	if err != nil {
		return nil, err
	}
	var zones []Zone
	json.Unmarshal(data, &zones)
	return zones, nil
}

func (c *CloudflareProvider) FindZoneByDomain(domain string) (*Zone, error) {
	data, err := c.do("GET", "/zones?name="+domain+"&per_page=1", nil)
	if err != nil {
		return nil, err
	}
	var zones []Zone
	json.Unmarshal(data, &zones)
	if len(zones) == 0 {
		return nil, fmt.Errorf("zone not found for %s", domain)
	}
	return &zones[0], nil
}

func (c *CloudflareProvider) ListRecords(zoneID string) ([]Record, error) {
	data, err := c.do("GET", fmt.Sprintf("/zones/%s/dns_records?per_page=100", zoneID), nil)
	if err != nil {
		return nil, err
	}
	var records []Record
	json.Unmarshal(data, &records)
	return records, nil
}

func (c *CloudflareProvider) CreateRecord(zoneID string, rec Record) (*Record, error) {
	if rec.TTL == 0 {
		rec.TTL = 1 // auto
	}
	data, err := c.do("POST", fmt.Sprintf("/zones/%s/dns_records", zoneID), rec)
	if err != nil {
		return nil, err
	}
	var result Record
	json.Unmarshal(data, &result)
	return &result, nil
}

func (c *CloudflareProvider) UpdateRecord(zoneID, recordID string, rec Record) (*Record, error) {
	if rec.TTL == 0 {
		rec.TTL = 1
	}
	data, err := c.do("PUT", fmt.Sprintf("/zones/%s/dns_records/%s", zoneID, recordID), rec)
	if err != nil {
		return nil, err
	}
	var result Record
	json.Unmarshal(data, &result)
	return &result, nil
}

func (c *CloudflareProvider) DeleteRecord(zoneID, recordID string) error {
	_, err := c.do("DELETE", fmt.Sprintf("/zones/%s/dns_records/%s", zoneID, recordID), nil)
	return err
}

// SyncDomainToIP ensures a domain's A record points to the given IP.
func (c *CloudflareProvider) SyncDomainToIP(domain, ip string) error {
	zone, err := c.FindZoneByDomain(extractBaseDomain(domain))
	if err != nil {
		return err
	}

	records, err := c.ListRecords(zone.ID)
	if err != nil {
		return err
	}

	// Find existing A record
	for _, r := range records {
		if r.Type == "A" && r.Name == domain {
			if r.Content == ip {
				return nil // already correct
			}
			// Update
			r.Content = ip
			_, err := c.UpdateRecord(zone.ID, r.ID, r)
			return err
		}
	}

	// Create new
	_, err = c.CreateRecord(zone.ID, Record{
		Type:    "A",
		Name:    domain,
		Content: ip,
		TTL:     1,
	})
	return err
}

func extractBaseDomain(domain string) string {
	parts := splitDomain(domain)
	if len(parts) >= 2 {
		return parts[len(parts)-2] + "." + parts[len(parts)-1]
	}
	return domain
}

func splitDomain(domain string) []string {
	var parts []string
	current := ""
	for _, ch := range domain {
		if ch == '.' {
			if current != "" {
				parts = append(parts, current)
			}
			current = ""
		} else {
			current += string(ch)
		}
	}
	if current != "" {
		parts = append(parts, current)
	}
	return parts
}

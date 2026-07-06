// Package dnsmanager manages DNS records via provider APIs (Cloudflare, etc.)
package dnsmanager

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Record represents a DNS record.
type Record struct {
	ID       string `json:"id,omitempty"`
	Type     string `json:"type"` // A, AAAA, CNAME, MX, TXT, NS
	Name     string `json:"name"`
	Content  string `json:"content"`
	TTL      int    `json:"ttl"`
	Proxied  bool   `json:"proxied,omitempty"`
	Priority int    `json:"priority,omitempty"`
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
	baseURL  string
}

const cfBaseURL = "https://api.cloudflare.com/client/v4"

// NewCloudflare creates a Cloudflare DNS provider.
func NewCloudflare(apiToken string) *CloudflareProvider {
	return &CloudflareProvider{
		apiToken: apiToken,
		client:   &http.Client{Timeout: 15 * time.Second},
		baseURL:  cfBaseURL,
	}
}

func (c *CloudflareProvider) do(method, path string, body any) (json.RawMessage, error) {
	var bodyReader io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, c.baseURL+path, bodyReader)
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

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, fmt.Errorf("cloudflare: read response: %w", err)
	}

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

// doList issues paginated GETs against a list endpoint, following Cloudflare's
// result_info.total_pages until every page is collected. pathBase must not
// already contain page/per_page. Without this, callers saw only the first page
// (50 zones / 100 records): zone/record lookups past page 1 silently failed and
// SyncDomainToIP created a duplicate A record when the existing one was beyond
// the first page.
func (c *CloudflareProvider) doList(pathBase string) ([]json.RawMessage, error) {
	sep := "?"
	if strings.Contains(pathBase, "?") {
		sep = "&"
	}
	var out []json.RawMessage
	for page := 1; page <= 1000; page++ { // hard cap: runaway guard
		path := fmt.Sprintf("%s%sper_page=100&page=%d", pathBase, sep, page)
		req, err := http.NewRequest("GET", c.baseURL+path, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.apiToken)
		resp, err := c.client.Do(req)
		if err != nil {
			return nil, err
		}
		respBody, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("cloudflare: read response: %w", err)
		}
		var cfResp struct {
			Success bool              `json:"success"`
			Result  []json.RawMessage `json:"result"`
			Errors  []struct {
				Message string `json:"message"`
			} `json:"errors"`
			ResultInfo struct {
				TotalPages int `json:"total_pages"`
			} `json:"result_info"`
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
		out = append(out, cfResp.Result...)
		if cfResp.ResultInfo.TotalPages <= page {
			break
		}
	}
	return out, nil
}

func (c *CloudflareProvider) ListZones() ([]Zone, error) {
	raws, err := c.doList("/zones")
	if err != nil {
		return nil, err
	}
	zones := make([]Zone, 0, len(raws))
	for _, r := range raws {
		var z Zone
		if err := json.Unmarshal(r, &z); err != nil {
			return nil, fmt.Errorf("cloudflare: parse zones: %w", err)
		}
		zones = append(zones, z)
	}
	return zones, nil
}

func (c *CloudflareProvider) FindZoneByDomain(domain string) (*Zone, error) {
	data, err := c.do("GET", "/zones?name="+url.QueryEscape(domain)+"&per_page=1", nil)
	if err != nil {
		return nil, err
	}
	var zones []Zone
	if err := json.Unmarshal(data, &zones); err != nil {
		return nil, fmt.Errorf("cloudflare: parse zone lookup: %w", err)
	}
	if len(zones) == 0 {
		return nil, fmt.Errorf("zone not found for %s", domain)
	}
	return &zones[0], nil
}

func (c *CloudflareProvider) ListRecords(zoneID string) ([]Record, error) {
	raws, err := c.doList(fmt.Sprintf("/zones/%s/dns_records", zoneID))
	if err != nil {
		return nil, err
	}
	records := make([]Record, 0, len(raws))
	for _, r := range raws {
		var rec Record
		if err := json.Unmarshal(r, &rec); err != nil {
			return nil, fmt.Errorf("cloudflare: parse records: %w", err)
		}
		records = append(records, rec)
	}
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
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("cloudflare: parse created record: %w", err)
	}
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
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("cloudflare: parse updated record: %w", err)
	}
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

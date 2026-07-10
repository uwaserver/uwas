package dnsmanager

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DigitalOceanProvider implements DNS management via DigitalOcean API.
type DigitalOceanProvider struct {
	apiToken string
	client   *http.Client
	baseURL  string
}

const doBaseURL = "https://api.digitalocean.com/v2"

func NewDigitalOcean(apiToken string) *DigitalOceanProvider {
	return &DigitalOceanProvider{
		apiToken: apiToken,
		client:   &http.Client{Timeout: 30 * time.Second},
		baseURL:  doBaseURL,
	}
}

func (p *DigitalOceanProvider) doRequest(method, path string, body interface{}) ([]byte, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, p.baseURL+path, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.apiToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, fmt.Errorf("digitalocean: read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("DigitalOcean %s %s: %d — %s", method, path, resp.StatusCode, string(data))
	}
	return data, nil
}

func (p *DigitalOceanProvider) ListZones() ([]Zone, error) {
	var zones []Zone
	// DigitalOcean paginates (20 per page by default); follow links.pages.next
	// until it is empty, otherwise only the first page of domains was returned.
	for page := 1; page <= 1000; page++ { // hard cap: runaway guard
		data, err := p.doRequest("GET", fmt.Sprintf("/domains?per_page=200&page=%d", page), nil)
		if err != nil {
			return nil, err
		}
		var resp struct {
			Domains []struct {
				Name string `json:"name"`
			} `json:"domains"`
			Links struct {
				Pages struct {
					Next string `json:"next"`
				} `json:"pages"`
			} `json:"links"`
		}
		if err := json.Unmarshal(data, &resp); err != nil {
			return nil, err
		}
		for _, d := range resp.Domains {
			zones = append(zones, Zone{ID: d.Name, Name: d.Name, Status: "active"})
		}
		if resp.Links.Pages.Next == "" || len(resp.Domains) == 0 {
			break
		}
	}
	return zones, nil
}

func (p *DigitalOceanProvider) FindZoneByDomain(domain string) (*Zone, error) {
	zones, err := p.ListZones()
	if err != nil {
		return nil, err
	}
	for _, z := range zones {
		if z.Name == domain || strings.HasSuffix(domain, "."+z.Name) {
			return &z, nil
		}
	}
	return nil, fmt.Errorf("zone not found for domain %s", domain)
}

func (p *DigitalOceanProvider) ListRecords(zoneID string) ([]Record, error) {
	var records []Record
	// Follow links.pages.next so domains with more than one page of records
	// (20 per page by default) are fully enumerated.
	for page := 1; page <= 1000; page++ { // hard cap: runaway guard
		data, err := p.doRequest("GET", fmt.Sprintf("/domains/%s/records?per_page=200&page=%d", zoneID, page), nil)
		if err != nil {
			return nil, err
		}
		var resp struct {
			Records []struct {
				ID       int    `json:"id"`
				Type     string `json:"type"`
				Name     string `json:"name"`
				Data     string `json:"data"`
				TTL      int    `json:"ttl"`
				Priority int    `json:"priority"`
			} `json:"domain_records"`
			Links struct {
				Pages struct {
					Next string `json:"next"`
				} `json:"pages"`
			} `json:"links"`
		}
		if err := json.Unmarshal(data, &resp); err != nil {
			return nil, err
		}
		for _, r := range resp.Records {
			records = append(records, Record{
				ID: fmt.Sprintf("%d", r.ID), Type: r.Type,
				Name: r.Name, Content: r.Data, TTL: r.TTL, Priority: r.Priority,
			})
		}
		if resp.Links.Pages.Next == "" || len(resp.Records) == 0 {
			break
		}
	}
	return records, nil
}

func (p *DigitalOceanProvider) CreateRecord(zoneID string, rec Record) (*Record, error) {
	body := map[string]interface{}{
		"type": rec.Type, "name": rec.Name, "data": rec.Content,
		"ttl": rec.TTL, "priority": rec.Priority,
	}
	data, err := p.doRequest("POST", "/domains/"+zoneID+"/records", body)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Record struct {
			ID int `json:"id"`
		} `json:"domain_record"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("digitalocean: parse create record response: %w", err)
	}
	rec.ID = fmt.Sprintf("%d", resp.Record.ID)
	return &rec, nil
}

func (p *DigitalOceanProvider) UpdateRecord(zoneID, recordID string, rec Record) (*Record, error) {
	body := map[string]interface{}{
		"type": rec.Type, "name": rec.Name, "data": rec.Content,
		"ttl": rec.TTL, "priority": rec.Priority,
	}
	_, err := p.doRequest("PUT", "/domains/"+zoneID+"/records/"+recordID, body)
	if err != nil {
		return nil, err
	}
	rec.ID = recordID
	return &rec, nil
}

func (p *DigitalOceanProvider) DeleteRecord(zoneID, recordID string) error {
	_, err := p.doRequest("DELETE", "/domains/"+zoneID+"/records/"+recordID, nil)
	return err
}

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
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("DigitalOcean %s %s: %d — %s", method, path, resp.StatusCode, string(data))
	}
	return data, nil
}

func (p *DigitalOceanProvider) ListZones() ([]Zone, error) {
	data, err := p.doRequest("GET", "/domains", nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Domains []struct {
			Name string `json:"name"`
		} `json:"domains"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	zones := make([]Zone, len(resp.Domains))
	for i, d := range resp.Domains {
		zones[i] = Zone{ID: d.Name, Name: d.Name, Status: "active"}
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
	data, err := p.doRequest("GET", "/domains/"+zoneID+"/records", nil)
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
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	records := make([]Record, len(resp.Records))
	for i, r := range resp.Records {
		records[i] = Record{
			ID: fmt.Sprintf("%d", r.ID), Type: r.Type,
			Name: r.Name, Content: r.Data, TTL: r.TTL, Priority: r.Priority,
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
		Record struct{ ID int `json:"id"` } `json:"domain_record"`
	}
	json.Unmarshal(data, &resp)
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

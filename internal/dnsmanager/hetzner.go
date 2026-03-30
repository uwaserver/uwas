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

// HetznerProvider implements DNS management via Hetzner DNS API.
type HetznerProvider struct {
	apiToken string
	client   *http.Client
	baseURL  string
}

const hetznerBaseURL = "https://dns.hetzner.com/api/v1"

func NewHetzner(apiToken string) *HetznerProvider {
	return &HetznerProvider{
		apiToken: apiToken,
		client:   &http.Client{Timeout: 30 * time.Second},
		baseURL:  hetznerBaseURL,
	}
}

func (p *HetznerProvider) hetznerRequest(method, path string, body interface{}) ([]byte, error) {
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
	req.Header.Set("Auth-API-Token", p.apiToken)
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
		return nil, fmt.Errorf("hetzner %s %s: %d - %s", method, path, resp.StatusCode, string(data))
	}
	return data, nil
}

func (p *HetznerProvider) ListZones() ([]Zone, error) {
	data, err := p.hetznerRequest("GET", "/zones", nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Zones []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"zones"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	zones := make([]Zone, len(resp.Zones))
	for i, z := range resp.Zones {
		zones[i] = Zone{ID: z.ID, Name: z.Name, Status: "active"}
	}
	return zones, nil
}

func (p *HetznerProvider) FindZoneByDomain(domain string) (*Zone, error) {
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

func (p *HetznerProvider) ListRecords(zoneID string) ([]Record, error) {
	data, err := p.hetznerRequest("GET", "/records?zone_id="+zoneID, nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Records []struct {
			ID    string `json:"id"`
			Type  string `json:"type"`
			Name  string `json:"name"`
			Value string `json:"value"`
			TTL   int    `json:"ttl"`
		} `json:"records"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	records := make([]Record, len(resp.Records))
	for i, r := range resp.Records {
		records[i] = Record{ID: r.ID, Type: r.Type, Name: r.Name, Content: r.Value, TTL: r.TTL}
	}
	return records, nil
}

func (p *HetznerProvider) CreateRecord(zoneID string, rec Record) (*Record, error) {
	body := map[string]interface{}{
		"zone_id": zoneID, "type": rec.Type, "name": rec.Name,
		"value": rec.Content, "ttl": rec.TTL,
	}
	data, err := p.hetznerRequest("POST", "/records", body)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Record struct {
			ID string `json:"id"`
		} `json:"record"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("hetzner: parse create record response: %w", err)
	}
	rec.ID = resp.Record.ID
	return &rec, nil
}

func (p *HetznerProvider) UpdateRecord(zoneID, recordID string, rec Record) (*Record, error) {
	body := map[string]interface{}{
		"zone_id": zoneID, "type": rec.Type, "name": rec.Name,
		"value": rec.Content, "ttl": rec.TTL,
	}
	_, err := p.hetznerRequest("PUT", "/records/"+recordID, body)
	if err != nil {
		return nil, err
	}
	rec.ID = recordID
	return &rec, nil
}

func (p *HetznerProvider) DeleteRecord(zoneID, recordID string) error {
	_, err := p.hetznerRequest("DELETE", "/records/"+recordID, nil)
	return err
}

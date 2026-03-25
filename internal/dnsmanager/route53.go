package dnsmanager

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Route53Provider implements DNS management via AWS Route53 API.
type Route53Provider struct {
	accessKey string
	secretKey string
	region    string
	client    *http.Client
}

// NewRoute53 creates a Route53 DNS provider.
func NewRoute53(accessKey, secretKey, region string) *Route53Provider {
	if region == "" {
		region = "us-east-1"
	}
	return &Route53Provider{
		accessKey: accessKey,
		secretKey: secretKey,
		region:    region,
		client:    &http.Client{Timeout: 30 * time.Second},
	}
}

const r53BaseURL = "https://route53.amazonaws.com/2013-04-01"

func (p *Route53Provider) ListZones() ([]Zone, error) {
	body, err := p.r53Request("GET", "/hostedzone", nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		HostedZones []struct {
			Id   string `xml:"Id"`
			Name string `xml:"Name"`
		} `xml:"HostedZones>HostedZone"`
	}
	if err := xml.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	zones := make([]Zone, len(resp.HostedZones))
	for i, hz := range resp.HostedZones {
		id := strings.TrimPrefix(hz.Id, "/hostedzone/")
		zones[i] = Zone{ID: id, Name: strings.TrimSuffix(hz.Name, "."), Status: "active"}
	}
	return zones, nil
}

func (p *Route53Provider) FindZoneByDomain(domain string) (*Zone, error) {
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

func (p *Route53Provider) ListRecords(zoneID string) ([]Record, error) {
	body, err := p.r53Request("GET", "/hostedzone/"+zoneID+"/rrset", nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		ResourceRecordSets []struct {
			Name string `xml:"Name"`
			Type string `xml:"Type"`
			TTL  int    `xml:"TTL"`
			Recs []struct {
				Value string `xml:"Value"`
			} `xml:"ResourceRecords>ResourceRecord"`
		} `xml:"ResourceRecordSets>ResourceRecordSet"`
	}
	if err := xml.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	var records []Record
	for _, rrs := range resp.ResourceRecordSets {
		for _, rr := range rrs.Recs {
			records = append(records, Record{
				ID:      rrs.Name + ":" + rrs.Type,
				Type:    rrs.Type,
				Name:    strings.TrimSuffix(rrs.Name, "."),
				Content: rr.Value,
				TTL:     rrs.TTL,
			})
		}
	}
	return records, nil
}

func (p *Route53Provider) CreateRecord(zoneID string, rec Record) (*Record, error) {
	return p.changeRecord(zoneID, "CREATE", rec)
}

func (p *Route53Provider) UpdateRecord(zoneID, recordID string, rec Record) (*Record, error) {
	return p.changeRecord(zoneID, "UPSERT", rec)
}

func (p *Route53Provider) DeleteRecord(zoneID, recordID string) error {
	parts := strings.SplitN(recordID, ":", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid record ID: %s", recordID)
	}
	rec := Record{Name: parts[0], Type: parts[1]}
	_, err := p.changeRecord(zoneID, "DELETE", rec)
	return err
}

func (p *Route53Provider) changeRecord(zoneID, action string, rec Record) (*Record, error) {
	name := rec.Name
	if !strings.HasSuffix(name, ".") {
		name += "."
	}
	ttl := rec.TTL
	if ttl == 0 {
		ttl = 300
	}
	xmlBody := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<ChangeResourceRecordSetsRequest xmlns="https://route53.amazonaws.com/doc/2013-04-01/">
  <ChangeBatch>
    <Changes>
      <Change>
        <Action>%s</Action>
        <ResourceRecordSet>
          <Name>%s</Name>
          <Type>%s</Type>
          <TTL>%d</TTL>
          <ResourceRecords>
            <ResourceRecord><Value>%s</Value></ResourceRecord>
          </ResourceRecords>
        </ResourceRecordSet>
      </Change>
    </Changes>
  </ChangeBatch>
</ChangeResourceRecordSetsRequest>`, action, name, rec.Type, ttl, rec.Content)

	_, err := p.r53Request("POST", "/hostedzone/"+zoneID+"/rrset", []byte(xmlBody))
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

// r53Request makes a signed AWS request to Route53.
func (p *Route53Provider) r53Request(method, path string, body []byte) ([]byte, error) {
	url := r53BaseURL + path
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, err
	}

	// AWS Signature Version 4
	now := time.Now().UTC()
	req.Header.Set("Host", "route53.amazonaws.com")
	req.Header.Set("X-Amz-Date", now.Format("20060102T150405Z"))
	if body != nil {
		req.Header.Set("Content-Type", "text/xml")
	}
	p.signRequest(req, body, now)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("Route53 %s %s: %d — %s", method, path, resp.StatusCode, string(data))
	}
	return data, nil
}

func (p *Route53Provider) signRequest(req *http.Request, body []byte, t time.Time) {
	// Simplified AWS Sig V4 for Route53
	dateStamp := t.Format("20060102")
	amzDate := t.Format("20060102T150405Z")
	service := "route53"

	// Canonical request
	bodyHash := sha256hex(body)
	headers := []string{"host", "x-amz-date"}
	sort.Strings(headers)
	canonicalHeaders := fmt.Sprintf("host:%s\nx-amz-date:%s\n", "route53.amazonaws.com", amzDate)
	signedHeaders := strings.Join(headers, ";")
	canonicalReq := strings.Join([]string{
		req.Method, req.URL.Path, req.URL.RawQuery,
		canonicalHeaders, signedHeaders, bodyHash,
	}, "\n")

	// String to sign
	credScope := dateStamp + "/" + p.region + "/" + service + "/aws4_request"
	stringToSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + credScope + "\n" + sha256hex([]byte(canonicalReq))

	// Signing key
	kDate := hmacSHA256([]byte("AWS4"+p.secretKey), []byte(dateStamp))
	kRegion := hmacSHA256(kDate, []byte(p.region))
	kService := hmacSHA256(kRegion, []byte(service))
	kSigning := hmacSHA256(kService, []byte("aws4_request"))

	sig := hex.EncodeToString(hmacSHA256(kSigning, []byte(stringToSign)))

	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		p.accessKey, credScope, signedHeaders, sig))
}

func sha256hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

// --- Helper for provider detection ---

// NewProvider creates a DNS provider based on type and credentials.
func NewProvider(providerType string, credentials map[string]string) (Provider, error) {
	switch providerType {
	case "cloudflare":
		token := credentials["api_token"]
		if token == "" {
			return nil, fmt.Errorf("cloudflare requires api_token")
		}
		return NewCloudflare(token), nil
	case "route53":
		ak := credentials["access_key"]
		sk := credentials["secret_key"]
		if ak == "" || sk == "" {
			return nil, fmt.Errorf("route53 requires access_key and secret_key")
		}
		return NewRoute53(ak, sk, credentials["region"]), nil
	case "hetzner":
		token := credentials["api_token"]
		if token == "" {
			return nil, fmt.Errorf("hetzner requires api_token")
		}
		return NewHetzner(token), nil
	case "digitalocean":
		token := credentials["api_token"]
		if token == "" {
			return nil, fmt.Errorf("digitalocean requires api_token")
		}
		return NewDigitalOcean(token), nil
	default:
		return nil, fmt.Errorf("unknown DNS provider: %s", providerType)
	}
}

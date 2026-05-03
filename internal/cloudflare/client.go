// Package cloudflare wraps the Cloudflare Tunnel API and the local cloudflared binary.
// Used by the admin server to create/run real tunnels via the dashboard.
package cloudflare

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const apiBase = "https://api.cloudflare.com/client/v4"

// Client is a thin Cloudflare API wrapper scoped to one account/token.
type Client struct {
	token     string
	accountID string
	http      *http.Client
	baseURL   string
}

// New returns a Client. accountID is the Cloudflare account UUID.
func New(token, accountID string) *Client {
	return &Client{
		token:     token,
		accountID: accountID,
		http:      &http.Client{Timeout: 20 * time.Second},
		baseURL:   apiBase,
	}
}

// envelope is the standard {success,result,errors} CF API envelope.
type envelope struct {
	Success bool            `json:"success"`
	Result  json.RawMessage `json:"result"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
}

// do sends a JSON request and returns the unwrapped result. Caller may pass nil body.
func (c *Client) do(method, path string, body any) (json.RawMessage, error) {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, c.baseURL+path, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("parse response (status %d): %w", resp.StatusCode, err)
	}
	if !env.Success {
		if len(env.Errors) > 0 {
			return nil, fmt.Errorf("cloudflare: %s", env.Errors[0].Message)
		}
		return nil, fmt.Errorf("cloudflare: request failed (status %d)", resp.StatusCode)
	}
	return env.Result, nil
}

// --- Tunnel operations ---

// Tunnel mirrors the Cloudflare Tunnel API resource (cfd_tunnel).
type Tunnel struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
	Status    string    `json:"status,omitempty"`
}

// CreateTunnel creates a locally-managed tunnel and returns its UUID + name.
// configSrc must be "cloudflare" (we manage ingress via API).
func (c *Client) CreateTunnel(name string) (*Tunnel, error) {
	body := map[string]any{
		"name":          name,
		"config_src":    "cloudflare",
		"tunnel_secret": randomTunnelSecret(),
	}
	raw, err := c.do("POST", "/accounts/"+c.accountID+"/cfd_tunnel", body)
	if err != nil {
		return nil, err
	}
	var t Tunnel
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, fmt.Errorf("parse tunnel: %w", err)
	}
	return &t, nil
}

// DeleteTunnel removes a tunnel. Will fail if the tunnel still has active connections.
func (c *Client) DeleteTunnel(tunnelID string) error {
	_, err := c.do("DELETE", "/accounts/"+c.accountID+"/cfd_tunnel/"+tunnelID, nil)
	return err
}

// ListTunnels returns all (non-deleted) tunnels in the account.
func (c *Client) ListTunnels() ([]Tunnel, error) {
	raw, err := c.do("GET", "/accounts/"+c.accountID+"/cfd_tunnel?is_deleted=false", nil)
	if err != nil {
		return nil, err
	}
	var ts []Tunnel
	if err := json.Unmarshal(raw, &ts); err != nil {
		return nil, fmt.Errorf("parse tunnels: %w", err)
	}
	return ts, nil
}

// GetTunnelToken returns the connector token used by `cloudflared tunnel run --token <T>`.
// Returned as a base64-encoded JWT-like string.
func (c *Client) GetTunnelToken(tunnelID string) (string, error) {
	raw, err := c.do("GET", "/accounts/"+c.accountID+"/cfd_tunnel/"+tunnelID+"/token", nil)
	if err != nil {
		return "", err
	}
	// API returns the token as a bare JSON string.
	var token string
	if err := json.Unmarshal(raw, &token); err != nil {
		return "", fmt.Errorf("parse token: %w", err)
	}
	return token, nil
}

// IngressRule maps a public hostname to a local target service.
type IngressRule struct {
	Hostname string `json:"hostname,omitempty"`
	Path     string `json:"path,omitempty"`
	Service  string `json:"service"` // e.g. "http://localhost:8080" or "http_status:404"
}

// PutTunnelConfig writes the ingress configuration for a tunnel. The last rule
// must have no hostname and a fallback service like "http_status:404".
func (c *Client) PutTunnelConfig(tunnelID string, rules []IngressRule) error {
	body := map[string]any{
		"config": map[string]any{
			"ingress": rules,
		},
	}
	_, err := c.do("PUT", "/accounts/"+c.accountID+"/cfd_tunnel/"+tunnelID+"/configurations", body)
	return err
}

// --- DNS operations (for tunnel hostname CNAMEs) ---

// Zone is the minimal zone shape we need.
type Zone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// FindZoneByHostname returns the zone whose name is the longest suffix of host.
// E.g. host="api.app.example.com" with zones {example.com, app.example.com}
// returns app.example.com.
func (c *Client) FindZoneByHostname(host string) (*Zone, error) {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	raw, err := c.do("GET", "/zones?per_page=50", nil)
	if err != nil {
		return nil, err
	}
	var zones []Zone
	if err := json.Unmarshal(raw, &zones); err != nil {
		return nil, fmt.Errorf("parse zones: %w", err)
	}
	var best *Zone
	for i, z := range zones {
		zn := strings.ToLower(z.Name)
		if host == zn || strings.HasSuffix(host, "."+zn) {
			if best == nil || len(zn) > len(best.Name) {
				best = &zones[i]
			}
		}
	}
	if best == nil {
		return nil, fmt.Errorf("no zone covers hostname %q", host)
	}
	return best, nil
}

// DNSRecord is the minimal record shape we read back.
type DNSRecord struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
}

// CreateTunnelCNAME creates a proxied CNAME hostname → {tunnelID}.cfargotunnel.com.
// Returns the new record ID.
func (c *Client) CreateTunnelCNAME(zoneID, hostname, tunnelID string) (string, error) {
	body := map[string]any{
		"type":    "CNAME",
		"name":    hostname,
		"content": tunnelID + ".cfargotunnel.com",
		"ttl":     1, // automatic
		"proxied": true,
		"comment": "Managed by UWAS — cloudflare tunnel",
	}
	raw, err := c.do("POST", "/zones/"+zoneID+"/dns_records", body)
	if err != nil {
		return "", err
	}
	var rec DNSRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return "", fmt.Errorf("parse record: %w", err)
	}
	return rec.ID, nil
}

// DeleteDNSRecord removes a DNS record from a zone.
func (c *Client) DeleteDNSRecord(zoneID, recordID string) error {
	_, err := c.do("DELETE", "/zones/"+zoneID+"/dns_records/"+recordID, nil)
	return err
}

// SetBaseURL is for tests.
func (c *Client) SetBaseURL(u string) { c.baseURL = u }

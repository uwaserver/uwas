package acme

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/uwaserver/uwas/internal/logger"
)

// Testable hooks for crypto operations.
var (
	generateKey = func() (*ecdsa.PrivateKey, error) {
		return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	}
	createCSR = func(key *ecdsa.PrivateKey, domains []string) ([]byte, error) {
		return x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
			DNSNames: domains,
		}, key)
	}
	thumbprintFunc = jwkThumbprint
)

// DNSProvider interface for DNS-01 challenge solving.
type DNSProvider interface {
	// PresentDNSChallenge creates a TXT record for the given domain with the given value.
	PresentDNSChallenge(domain, token, keyAuth string) error
	// CleanupDNSChallenge removes the TXT record after challenge validation.
	CleanupDNSChallenge(domain, token, keyAuth string) error
}

// Client implements the ACME protocol (RFC 8555) for automated certificate issuance.
type Client struct {
	directoryURL string
	accountKey   *ecdsa.PrivateKey
	accountURL   string
	directory    *Directory
	nonces       *noncePool
	httpClient   *http.Client
	storageDir   string
	logger       *logger.Logger
	dnsProvider  DNSProvider

	// HTTP-01 challenge tokens: token → keyAuthorization
	httpTokens sync.Map
}

// Directory contains ACME server endpoint URLs.
type Directory struct {
	NewNonce   string `json:"newNonce"`
	NewAccount string `json:"newAccount"`
	NewOrder   string `json:"newOrder"`
	RevokeCert string `json:"revokeCert"`
	KeyChange  string `json:"keyChange"`
}

type Order struct {
	URL            string
	Status         string       `json:"status"`
	Identifiers    []Identifier `json:"identifiers"`
	Authorizations []string     `json:"authorizations"`
	Finalize       string       `json:"finalize"`
	Certificate    string       `json:"certificate"`
}

type Identifier struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

type Authorization struct {
	Status     string      `json:"status"`
	Identifier Identifier  `json:"identifier"`
	Challenges []Challenge `json:"challenges"`
}

type Challenge struct {
	Type   string `json:"type"`
	URL    string `json:"url"`
	Token  string `json:"token"`
	Status string `json:"status"`
}

func NewClient(directoryURL, storageDir string, log *logger.Logger) *Client {
	return &Client{
		directoryURL: directoryURL,
		storageDir:   storageDir,
		nonces:       &noncePool{},
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		logger: log,
	}
}

// SetDNSProvider sets the DNS provider for DNS-01 challenges.
func (c *Client) SetDNSProvider(provider DNSProvider) {
	c.dnsProvider = provider
}

// ObtainCertificate performs the full ACME flow for the given domains.
// Returns: tls.Certificate, certPEM, keyPEM, error.
func (c *Client) ObtainCertificate(ctx context.Context, domains []string) (*tls.Certificate, []byte, []byte, error) {
	// 1. Ensure directory
	if err := c.ensureDirectory(ctx); err != nil {
		return nil, nil, nil, fmt.Errorf("directory: %w", err)
	}

	// 2. Ensure account
	if err := c.ensureAccount(ctx); err != nil {
		return nil, nil, nil, fmt.Errorf("account: %w", err)
	}

	// 3. Create order
	order, err := c.newOrder(ctx, domains)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("new order: %w", err)
	}

	// 4. Solve authorizations
	for _, authzURL := range order.Authorizations {
		authz, err := c.getAuthorization(ctx, authzURL)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("get authorization: %w", err)
		}
		if authz.Status == "valid" {
			continue
		}
		if err := c.solveChallenge(ctx, authz); err != nil {
			return nil, nil, nil, fmt.Errorf("challenge %s: %w", authz.Identifier.Value, err)
		}
	}

	// 5. Wait for order ready
	order, err = c.waitForStatus(ctx, order.URL, "ready", 30)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("order ready: %w", err)
	}

	// 6. Generate cert key + CSR
	certKey, err := generateKey()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("generate key: %w", err)
	}
	csr, err := createCSR(certKey, domains)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create CSR: %w", err)
	}

	// 7. Finalize order
	orderURL := order.URL
	order, err = c.finalizeOrder(ctx, order.Finalize, csr)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("finalize: %w", err)
	}

	// 8. Wait for certificate
	if order.Status != "valid" {
		order, err = c.waitForStatus(ctx, orderURL, "valid", 30)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("order valid: %w", err)
		}
	}

	// 9. Download certificate
	certPEM, err := c.downloadCert(ctx, order.Certificate)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("download cert: %w", err)
	}

	// 10. Build tls.Certificate
	keyPEM := encodeECKey(certKey)
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("keypair: %w", err)
	}
	if len(cert.Certificate) > 0 {
		cert.Leaf, _ = x509.ParseCertificate(cert.Certificate[0])
	}

	return &cert, certPEM, keyPEM, nil
}

// HandleHTTPChallenge responds to ACME HTTP-01 challenge requests.
// Returns true if the request was an ACME challenge.
func (c *Client) HandleHTTPChallenge(w http.ResponseWriter, r *http.Request) bool {
	const prefix = "/.well-known/acme-challenge/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		return false
	}

	token := r.URL.Path[len(prefix):]
	if keyAuth, ok := c.httpTokens.Load(token); ok {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write([]byte(keyAuth.(string)))
		return true
	}

	http.Error(w, "challenge not found", http.StatusNotFound)
	return true
}

func (c *Client) ensureDirectory(ctx context.Context) error {
	if c.directory != nil {
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, "GET", c.directoryURL, nil)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("directory returned %d", resp.StatusCode)
	}

	c.directory = &Directory{}
	return json.NewDecoder(resp.Body).Decode(c.directory)
}

func (c *Client) ensureAccount(ctx context.Context) error {
	if c.accountURL != "" {
		return nil
	}

	// Load or generate account key
	if err := c.loadOrCreateAccountKey(); err != nil {
		return err
	}

	// Register account (or get existing)
	payload := map[string]any{
		"termsOfServiceAgreed": true,
	}

	resp, err := c.signedRequest(ctx, c.directory.NewAccount, payload)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("account creation returned %d: %s", resp.StatusCode, body)
	}

	c.accountURL = resp.Header.Get("Location")
	return nil
}

func (c *Client) loadOrCreateAccountKey() error {
	keyPath := filepath.Join(c.storageDir, "account.key")

	// Try to load existing key
	data, err := os.ReadFile(keyPath)
	if err == nil {
		block, _ := pem.Decode(data)
		if block != nil {
			key, err := x509.ParseECPrivateKey(block.Bytes)
			if err == nil {
				c.accountKey = key
				return nil
			}
		}
	}

	// Generate new key
	key, err := generateKey()
	if err != nil {
		return err
	}
	c.accountKey = key

	// Persist
	if err := os.MkdirAll(c.storageDir, 0700); err != nil {
		return err
	}
	keyPEM := encodeECKey(key)
	return os.WriteFile(keyPath, keyPEM, 0600)
}

func (c *Client) newOrder(ctx context.Context, domains []string) (*Order, error) {
	ids := make([]Identifier, len(domains))
	for i, d := range domains {
		ids[i] = Identifier{Type: "dns", Value: d}
	}

	payload := map[string]any{
		"identifiers": ids,
	}

	resp, err := c.signedRequest(ctx, c.directory.NewOrder, payload)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("new order returned %d: %s", resp.StatusCode, body)
	}

	order := &Order{}
	if err := json.NewDecoder(resp.Body).Decode(order); err != nil {
		return nil, err
	}
	order.URL = resp.Header.Get("Location")
	return order, nil
}

func (c *Client) getAuthorization(ctx context.Context, url string) (*Authorization, error) {
	resp, err := c.signedRequest(ctx, url, nil) // POST-as-GET
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	authz := &Authorization{}
	return authz, json.NewDecoder(resp.Body).Decode(authz)
}

func (c *Client) solveChallenge(ctx context.Context, authz *Authorization) error {
	thumbprint, err := thumbprintFunc(&c.accountKey.PublicKey)
	if err != nil {
		return err
	}

	// Try HTTP-01 first, then DNS-01 if no HTTP-01 available.
	var challenge *Challenge
	for i := range authz.Challenges {
		if authz.Challenges[i].Type == "http-01" {
			challenge = &authz.Challenges[i]
			break
		}
	}

	// If no HTTP-01, try DNS-01 if we have a DNS provider configured.
	if challenge == nil && c.dnsProvider != nil {
		for i := range authz.Challenges {
			if authz.Challenges[i].Type == "dns-01" {
				challenge = &authz.Challenges[i]
				break
			}
		}
	}

	if challenge == nil {
		return fmt.Errorf("no supported challenge available (tried http-01 and dns-01)")
	}

	keyAuth := challenge.Token + "." + thumbprint

	// Handle DNS-01 challenge.
	if challenge.Type == "dns-01" && c.dnsProvider != nil {
		return c.solveDNS01(ctx, authz.Identifier.Value, challenge, keyAuth)
	}

	// Handle HTTP-01 challenge.
	c.httpTokens.Store(challenge.Token, keyAuth)
	defer c.httpTokens.Delete(challenge.Token)

	// Tell ACME server we're ready
	resp, err := c.signedRequest(ctx, challenge.URL, map[string]any{})
	if err != nil {
		return err
	}
	resp.Body.Close()

	// Wait for challenge validation
	_, err = c.waitForStatus(ctx, challenge.URL, "valid", 30)
	return err
}

// solveDNS01 performs the DNS-01 challenge flow.
func (c *Client) solveDNS01(ctx context.Context, domain string, challenge *Challenge, keyAuth string) error {
	// Create TXT record for _acme-challenge.<domain>
	dnsName := "_acme-challenge." + domain
	if err := c.dnsProvider.PresentDNSChallenge(dnsName, challenge.Token, keyAuth); err != nil {
		return fmt.Errorf("dns-01 present: %w", err)
	}

	// Tell ACME server we're ready
	resp, err := c.signedRequest(ctx, challenge.URL, map[string]any{})
	if err != nil {
		c.dnsProvider.CleanupDNSChallenge(dnsName, challenge.Token, keyAuth)
		return err
	}
	resp.Body.Close()

	// Wait for challenge validation
	_, err = c.waitForStatus(ctx, challenge.URL, "valid", 30)
	if err != nil {
		c.dnsProvider.CleanupDNSChallenge(dnsName, challenge.Token, keyAuth)
		return err
	}

	// Clean up the TXT record after successful validation.
	if err := c.dnsProvider.CleanupDNSChallenge(dnsName, challenge.Token, keyAuth); err != nil {
		c.logger.Warn("failed to cleanup dns-01 challenge", "domain", domain, "error", err)
	}

	return nil
}

func (c *Client) waitForStatus(ctx context.Context, url, target string, maxAttempts int) (*Order, error) {
	for i := 0; i < maxAttempts; i++ {
		wait := time.NewTimer(time.Duration(i+1) * time.Second)
		select {
		case <-ctx.Done():
			wait.Stop()
			return nil, ctx.Err()
		case <-wait.C:
		}

		resp, err := c.signedRequest(ctx, url, nil) // POST-as-GET
		if err != nil {
			return nil, err
		}

		var obj Order
		json.NewDecoder(resp.Body).Decode(&obj)
		resp.Body.Close()
		obj.URL = url

		if obj.Status == target {
			return &obj, nil
		}
		if obj.Status == "invalid" {
			return nil, fmt.Errorf("status became invalid")
		}
	}
	return nil, fmt.Errorf("timeout waiting for status %q", target)
}

func (c *Client) finalizeOrder(ctx context.Context, url string, csr []byte) (*Order, error) {
	payload := map[string]any{
		"csr": base64url(csr),
	}

	resp, err := c.signedRequest(ctx, url, payload)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	order := &Order{}
	return order, json.NewDecoder(resp.Body).Decode(order)
}

func (c *Client) downloadCert(ctx context.Context, url string) ([]byte, error) {
	resp, err := c.signedRequest(ctx, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
}

func encodeECKey(key *ecdsa.PrivateKey) []byte {
	der, _ := x509.MarshalECPrivateKey(key)
	return pem.EncodeToMemory(&pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: der,
	})
}

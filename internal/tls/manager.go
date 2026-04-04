package uwastls

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"math/big"
	"net/http"
	neturl "net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ocsp"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/tls/acme"
)

// Manager handles TLS certificates: loading, ACME issuance, renewal, SNI routing.
type Manager struct {
	certs     sync.Map // host → *tls.Certificate
	acme      *acme.Client
	config    config.ACMEConfig
	storage   *CertStorage
	logger    *logger.Logger
	domainsMu sync.RWMutex
	domains   []config.Domain

	// On-demand rate limiting: max 10 certs per minute.
	onDemandCount atomic.Int64
	onDemandReset atomic.Int64 // unix timestamp of current window start

	// onCertRenewed is called after a certificate is successfully renewed.
	onCertRenewed func(host string)
	// onCertExpiry is called when a certificate is near expiry and renewal fails.
	onCertExpiry func(host string, daysLeft int)

	// acmeObtainFunc overrides ACME cert obtainment for testing.
	// When nil, uses m.acme.ObtainCertificate.
	acmeObtainFunc func(ctx context.Context, domains []string) (*tls.Certificate, []byte, []byte, error)

	// renewalInitialDelay overrides the 1-minute initial delay in StartRenewal for testing.
	renewalInitialDelay time.Duration
	// renewalInterval overrides the 12-hour ticker interval in StartRenewal for testing.
	renewalInterval time.Duration

	// mTLS fields
	clientCA       *x509.CertPool
	clientAuthMode tls.ClientAuthType

	// AllowSelfSigned enables auto-generated self-signed certs as fallback.
	// Set to true in production server; false in tests.
	AllowSelfSigned bool
}

const onDemandMaxPerMinute = 10

const (
	onDemandAskTimeout      = 5 * time.Second
	onDemandObtainTimeout   = 2 * time.Minute
	maxOnDemandAskBodyBytes = 8 << 10 // 8KB
	ocspFetchTimeout        = 10 * time.Second
)

func NewManager(cfg config.ACMEConfig, domains []config.Domain, log *logger.Logger) *Manager {
	m := &Manager{
		config:  cfg,
		storage: NewCertStorage(cfg.Storage),
		logger:  log,
		domains: append([]config.Domain(nil), domains...),
	}

	// Initialize ACME client if email is configured
	if cfg.Email != "" {
		m.acme = acme.NewClient(cfg.CAURL, cfg.Storage, log)

		// Wire up DNS provider for DNS-01 challenges if configured.
		if cfg.DNSProvider != "" {
			if dp, err := NewACMEDNSProvider(cfg.DNSProvider, cfg.DNSCredentials, log); err == nil {
				m.acme.SetDNSProvider(dp)
				log.Info("ACME DNS-01 provider configured", "provider", cfg.DNSProvider)
			} else {
				log.Warn("failed to initialize ACME DNS provider", "provider", cfg.DNSProvider, "error", err)
			}
		}
	}

	return m
}

// LoadExistingCerts loads all certificates from disk storage.
func (m *Manager) LoadExistingCerts() {
	certs, err := m.storage.LoadAll()
	if err != nil {
		m.logger.Warn("failed to load existing certs", "error", err)
		return
	}

	for host, cert := range certs {
		m.certs.Store(host, cert)
		m.logger.Debug("loaded certificate", "host", host)
	}

	if len(certs) > 0 {
		m.logger.Info("loaded certificates from disk", "count", len(certs))
	}
}

// LoadManualCerts loads manually configured certificates.
func (m *Manager) LoadManualCerts() {
	for _, d := range m.snapshotDomains() {
		if d.SSL.Mode != "manual" {
			continue
		}
		cert, err := tls.LoadX509KeyPair(d.SSL.Cert, d.SSL.Key)
		if err != nil {
			m.logger.Error("failed to load manual cert",
				"host", d.Host, "error", err)
			continue
		}
		m.certs.Store(strings.ToLower(d.Host), &cert)
		m.logger.Info("loaded manual certificate", "host", d.Host)
	}
}

// isDomainConfigured checks if a domain is in the configured domains list.
// It supports exact match, wildcard match, and alias match.
// If no domains are configured, returns true to allow all (backward compatibility).
func (m *Manager) isDomainConfigured(host string) bool {
	m.domainsMu.RLock()
	defer m.domainsMu.RUnlock()

	// If no domains configured, allow all (for backward compatibility in tests/dev)
	if len(m.domains) == 0 {
		return true
	}

	for _, d := range m.domains {
		domainHost := strings.ToLower(d.Host)
		// Exact match
		if domainHost == host {
			return true
		}
		// Wildcard match: *.example.com matches sub.example.com
		if strings.HasPrefix(domainHost, "*.") {
			suffix := domainHost[2:] // Remove "*."
			if strings.HasSuffix(host, "."+suffix) || host == suffix {
				return true
			}
		}
		// Check aliases
		for _, alias := range d.Aliases {
			if strings.ToLower(alias) == host {
				return true
			}
		}
	}
	return false
}

// GetCertificate is the tls.Config.GetCertificate callback for SNI routing.
func (m *Manager) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	name := strings.ToLower(hello.ServerName)
	handshakeCtx := context.Background()
	if helloCtx := hello.Context(); helloCtx != nil {
		handshakeCtx = helloCtx
	}

	// 0. Check if domain is in whitelist (configured domains)
	// This prevents generating certificates for unknown subdomains like imap2, postmaster, etc.
	if !m.isDomainConfigured(name) {
		// Domain not configured - return error to reject the connection
		// This prevents self-signed cert generation for unknown domains
		return nil, fmt.Errorf("domain %s not configured", name)
	}

	// 1. Exact match
	if cert, ok := m.certs.Load(name); ok {
		return cert.(*tls.Certificate), nil
	}

	// 2. Wildcard match: "sub.example.com" → "*.example.com"
	if parts := strings.SplitN(name, ".", 2); len(parts) == 2 {
		wildcard := "*." + parts[1]
		if cert, ok := m.certs.Load(wildcard); ok {
			return cert.(*tls.Certificate), nil
		}
	}

	// 3. On-demand TLS
	if m.config.OnDemand && m.acme != nil {
		// Check OnDemandAsk URL if configured.
		if m.config.OnDemandAsk != "" {
			askURL, err := neturl.Parse(m.config.OnDemandAsk)
			if err != nil {
				return nil, fmt.Errorf("invalid on-demand ask URL: %w", err)
			}
			query := askURL.Query()
			query.Set("domain", name)
			askURL.RawQuery = query.Encode()

			askCtx, cancel := context.WithTimeout(handshakeCtx, onDemandAskTimeout)
			defer cancel()
			req, err := http.NewRequestWithContext(askCtx, http.MethodGet, askURL.String(), nil)
			if err != nil {
				return nil, fmt.Errorf("on-demand ask request failed for %s: %w", name, err)
			}

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				m.logger.Error("on-demand ask failed", "host", name, "error", err)
				return nil, fmt.Errorf("on-demand ask error for %s: %w", name, err)
			}
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxOnDemandAskBodyBytes))
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return nil, fmt.Errorf("on-demand ask rejected %s (status %d)", name, resp.StatusCode)
			}
		}

		// Rate limit: max 10 on-demand certs per minute.
		if !m.onDemandAllow() {
			return nil, fmt.Errorf("on-demand rate limit exceeded for %s", name)
		}

		obtainCtx, cancel := context.WithTimeout(handshakeCtx, onDemandObtainTimeout)
		defer cancel()

		cert, err := m.obtainCert(obtainCtx, name)
		if err != nil {
			m.logger.Error("on-demand cert failed", "host", name, "error", err)
			return nil, err
		}
		return cert, nil
	}

	// 4. Default certificate
	if cert, ok := m.certs.Load("_default"); ok {
		return cert.(*tls.Certificate), nil
	}

	// 5. Fallback: if AllowSelfSigned is set, generate a temp cert so TLS works.
	if m.AllowSelfSigned {
		m.logger.Warn("no certificate, generating self-signed", "host", name)
		cert, err := m.generateSelfSigned(name)
		if err != nil {
			return nil, fmt.Errorf("no certificate for %s: %w", name, err)
		}
		m.certs.Store(name, cert)
		return cert, nil
	}

	return nil, fmt.Errorf("no certificate for %s", name)
}

// generateSelfSigned creates a temporary self-signed certificate for the given host.
func (m *Manager) generateSelfSigned(host string) (*tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	validity := m.config.SelfSignedValidity.Duration
	if validity <= 0 {
		validity = 24 * time.Hour
	}

	serial := big.NewInt(1)
	if serialNumber, err := rand.Int(rand.Reader, big.NewInt(1<<62)); err == nil {
		serial = serialNumber
	}

	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(validity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	cert := &tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  key,
	}
	return cert, nil
}

// ObtainCerts requests ACME certificates for all auto-SSL domains.
// Failed domains are retried up to 3 times with exponential backoff.
func (m *Manager) ObtainCerts(ctx context.Context) {
	if m.acme == nil {
		return
	}

	// Collect domains that need certificates.
	var pending []string
	for _, d := range m.snapshotDomains() {
		if d.SSL.Mode != "auto" {
			continue
		}
		host := strings.ToLower(d.Host)
		if _, ok := m.certs.Load(host); ok {
			continue
		}
		pending = append(pending, host)
	}

	backoff := 30 * time.Second
	for attempt := 0; attempt < 3 && len(pending) > 0; attempt++ {
		if attempt > 0 {
			m.logger.Info("retrying failed certificates", "count", len(pending), "attempt", attempt+1)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff *= 2
		}

		var failed []string
		for _, host := range pending {
			m.logger.Info("obtaining certificate", "host", host)
			_, err := m.obtainCert(ctx, host)
			if err != nil {
				m.logger.Error("failed to obtain cert", "host", host, "error", err)
				failed = append(failed, host)
				continue
			}
		}
		pending = failed
	}

	if len(pending) > 0 {
		m.logger.Warn("some certificates could not be obtained after retries", "hosts", pending)
	}
}

// SetOnCertRenewed sets a callback that fires after a certificate is renewed.
func (m *Manager) SetOnCertRenewed(fn func(host string)) {
	m.onCertRenewed = fn
}

// SetOnCertExpiry sets a callback that fires when a certificate is near expiry and renewal fails.
func (m *Manager) SetOnCertExpiry(fn func(host string, daysLeft int)) {
	m.onCertExpiry = fn
}

// RenewCert forces renewal of a certificate for the given host.
func (m *Manager) RenewCert(ctx context.Context, host string) error {
	if m.acme == nil {
		return fmt.Errorf("ACME client not configured (set acme.email in config)")
	}
	host = strings.ToLower(host)
	_, err := m.obtainCert(ctx, host)
	return err
}

// CertStatus returns metadata for a loaded certificate, or nil if not loaded.
func (m *Manager) CertStatus(host string) *CertStatusInfo {
	host = strings.ToLower(host)
	val, ok := m.certs.Load(host)
	if !ok {
		return nil
	}
	cert := val.(*tls.Certificate)
	leaf, err := leafCert(cert)
	if err != nil {
		return nil
	}
	remaining := time.Until(leaf.NotAfter)
	return &CertStatusInfo{
		Issuer:   leaf.Issuer.CommonName,
		Expiry:   leaf.NotAfter,
		DaysLeft: int(remaining.Hours() / 24),
	}
}

// CertStatusInfo holds certificate status details.
type CertStatusInfo struct {
	Issuer   string
	Expiry   time.Time
	DaysLeft int
}

func (m *Manager) obtainCert(ctx context.Context, host string) (*tls.Certificate, error) {
	if m.acme == nil && m.acmeObtainFunc == nil {
		return nil, fmt.Errorf("ACME client not configured")
	}

	var cert *tls.Certificate
	var certPEM, keyPEM []byte
	var err error
	if m.acmeObtainFunc != nil {
		cert, certPEM, keyPEM, err = m.acmeObtainFunc(ctx, []string{host})
	} else {
		cert, certPEM, keyPEM, err = m.acme.ObtainCertificate(ctx, []string{host})
	}
	if err != nil {
		return nil, err
	}

	// OCSP staple (best-effort)
	m.stapleOCSP(cert, host)

	// Store in memory
	m.certs.Store(host, cert)

	// Persist to disk
	if err := m.storage.Save(host, cert, keyPEM, certPEM); err != nil {
		m.logger.Warn("failed to persist cert", "host", host, "error", err)
	}

	m.logger.Info("certificate obtained", "host", host)
	return cert, nil
}

// onDemandAllow checks the on-demand rate limiter. Returns true if the
// request is allowed (fewer than onDemandMaxPerMinute in the current window).
func (m *Manager) onDemandAllow() bool {
	now := time.Now().Unix()
	windowStart := m.onDemandReset.Load()
	if now-windowStart >= 60 {
		// New window: reset counter.
		m.onDemandReset.Store(now)
		m.onDemandCount.Store(1)
		return true
	}
	count := m.onDemandCount.Add(1)
	return count <= onDemandMaxPerMinute
}

// HandleHTTPChallenge checks if the request is an ACME HTTP-01 challenge
// and responds if so. Returns true if handled.
func (m *Manager) HandleHTTPChallenge(w http.ResponseWriter, r *http.Request) bool {
	if m.acme == nil {
		return false
	}
	return m.acme.HandleHTTPChallenge(w, r)
}

// StartRenewal launches a background goroutine that checks cert expiry
// and renews certificates that are within 30 days of expiry.
func (m *Manager) StartRenewal(ctx context.Context) {
	if m.acme == nil {
		return
	}

	m.logger.SafeGo("tls.renewal", func() {
		// Initial delay to let server start
		initDelay := m.renewalInitialDelay
		if initDelay == 0 {
			initDelay = 1 * time.Minute
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(initDelay):
		}

		interval := m.renewalInterval
		if interval == 0 {
			interval = 12 * time.Hour
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			m.checkRenewals(ctx)

			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	})
}

func (m *Manager) checkRenewals(ctx context.Context) {
	m.certs.Range(func(key, value any) bool {
		host := key.(string)
		if host == "_default" {
			return true
		}

		cert := value.(*tls.Certificate)
		leaf, err := leafCert(cert)
		if err != nil {
			return true
		}

		remaining := time.Until(leaf.NotAfter)
		if remaining < 30*24*time.Hour {
			m.logger.Info("renewing certificate",
				"host", host,
				"expires_in", remaining.Round(time.Hour),
			)

			var newCert *tls.Certificate
			var certPEM, keyPEM []byte
			var err error
			if m.acmeObtainFunc != nil {
				newCert, certPEM, keyPEM, err = m.acmeObtainFunc(ctx, leaf.DNSNames)
			} else {
				newCert, certPEM, keyPEM, err = m.acme.ObtainCertificate(ctx, leaf.DNSNames)
			}
			if err != nil {
				m.logger.Error("renewal failed", "host", host, "error", err)
				if m.onCertExpiry != nil {
					daysLeft := int(remaining.Hours() / 24)
					m.onCertExpiry(host, daysLeft)
				}
				return true
			}

			m.certs.Store(host, newCert)
			if err := m.storage.Save(host, newCert, keyPEM, certPEM); err != nil {
				m.logger.Warn("failed to persist renewed cert", "host", host, "error", err)
			}

			m.logger.Info("certificate renewed", "host", host)
			if m.onCertRenewed != nil {
				m.onCertRenewed(host)
			}
		}

		return true
	})
}

// UpdateDomains updates the domain list (for hot reload).
func (m *Manager) UpdateDomains(domains []config.Domain) {
	m.domainsMu.Lock()
	m.domains = append([]config.Domain(nil), domains...)
	m.domainsMu.Unlock()
}

func (m *Manager) snapshotDomains() []config.Domain {
	m.domainsMu.RLock()
	defer m.domainsMu.RUnlock()

	out := make([]config.Domain, len(m.domains))
	copy(out, m.domains)
	return out
}

// stapleOCSP fetches and attaches an OCSP response to the certificate.
// This is best-effort: if OCSP fails, the cert works without stapling.
func (m *Manager) stapleOCSP(cert *tls.Certificate, host string) {
	if cert == nil || len(cert.Certificate) < 2 {
		return // need at least leaf + issuer
	}

	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil || len(leaf.OCSPServer) == 0 {
		return // no OCSP server or unparseable cert
	}

	issuer, err := x509.ParseCertificate(cert.Certificate[1])
	if err != nil {
		return
	}

	ocspReq, err := ocsp.CreateRequest(leaf, issuer, nil)
	if err != nil {
		return
	}

	ocspCtx, cancel := context.WithTimeout(context.Background(), ocspFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ocspCtx, http.MethodPost, leaf.OCSPServer[0], bytes.NewReader(ocspReq))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/ocsp-request")

	httpResp, err := http.DefaultClient.Do(req)
	if err != nil {
		m.logger.Debug("OCSP fetch failed", "host", host, "error", err)
		return
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode != http.StatusOK {
		return
	}

	respBytes, err := io.ReadAll(io.LimitReader(httpResp.Body, 1<<20))
	if err != nil {
		return
	}

	ocspResp, err := ocsp.ParseResponse(respBytes, issuer)
	if err != nil {
		return
	}

	if ocspResp.Status == ocsp.Good {
		cert.OCSPStaple = respBytes
		m.logger.Info("OCSP stapled", "host", host)
	}
}

// TLSConfig returns a tls.Config with best-practice settings.
func (m *Manager) TLSConfig() *tls.Config {
	cfg := &tls.Config{
		GetCertificate: m.GetCertificate,
		MinVersion:     tls.VersionTLS12,
		NextProtos:     []string{"h2", "http/1.1"},
		CipherSuites: []uint16{
			// TLS 1.3 ciphers are automatically included
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
		},
		CurvePreferences: []tls.CurveID{
			tls.X25519,
			tls.CurveP256,
		},
	}

	// mTLS: load client CA and set client auth mode if configured
	if m.clientCA != nil {
		cfg.ClientCAs = m.clientCA
		cfg.ClientAuth = m.clientAuthMode
	}

	return cfg
}

// SetClientAuth configures mutual TLS (mTLS) with client certificate verification.
func (m *Manager) SetClientAuth(caPath, mode string) error {
	if caPath == "" {
		return nil
	}
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		return fmt.Errorf("read client CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return fmt.Errorf("no valid certs in client CA file: %s", caPath)
	}
	m.clientCA = pool
	switch mode {
	case "require":
		m.clientAuthMode = tls.RequireAndVerifyClientCert
	case "request":
		m.clientAuthMode = tls.VerifyClientCertIfGiven
	default:
		m.clientAuthMode = tls.NoClientCert
	}
	m.logger.Info("mTLS configured", "ca", caPath, "mode", mode)
	return nil
}

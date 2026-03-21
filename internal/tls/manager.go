package uwastls

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/tls/acme"
)

// Manager handles TLS certificates: loading, ACME issuance, renewal, SNI routing.
type Manager struct {
	certs   sync.Map // host → *tls.Certificate
	acme    *acme.Client
	config  config.ACMEConfig
	storage *CertStorage
	logger  *logger.Logger
	domains []config.Domain
}

func NewManager(cfg config.ACMEConfig, domains []config.Domain, log *logger.Logger) *Manager {
	m := &Manager{
		config:  cfg,
		storage: NewCertStorage(cfg.Storage),
		logger:  log,
		domains: domains,
	}

	// Initialize ACME client if email is configured
	if cfg.Email != "" {
		m.acme = acme.NewClient(cfg.CAURL, cfg.Storage, log)
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
	for _, d := range m.domains {
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

// GetCertificate is the tls.Config.GetCertificate callback for SNI routing.
func (m *Manager) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	name := strings.ToLower(hello.ServerName)

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
		cert, err := m.obtainCert(context.Background(), name)
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

	return nil, fmt.Errorf("no certificate for %s", name)
}

// ObtainCerts requests ACME certificates for all auto-SSL domains.
func (m *Manager) ObtainCerts(ctx context.Context) {
	if m.acme == nil {
		return
	}

	for _, d := range m.domains {
		if d.SSL.Mode != "auto" {
			continue
		}

		host := strings.ToLower(d.Host)

		// Skip if already loaded
		if _, ok := m.certs.Load(host); ok {
			continue
		}

		m.logger.Info("obtaining certificate", "host", host)
		cert, err := m.obtainCert(ctx, host)
		if err != nil {
			m.logger.Error("failed to obtain cert", "host", host, "error", err)
			continue
		}
		_ = cert // already stored in obtainCert
	}
}

func (m *Manager) obtainCert(ctx context.Context, host string) (*tls.Certificate, error) {
	if m.acme == nil {
		return nil, fmt.Errorf("ACME client not configured")
	}

	cert, certPEM, keyPEM, err := m.acme.ObtainCertificate(ctx, []string{host})
	if err != nil {
		return nil, err
	}

	// Store in memory
	m.certs.Store(host, cert)

	// Persist to disk
	if err := m.storage.Save(host, cert, keyPEM, certPEM); err != nil {
		m.logger.Warn("failed to persist cert", "host", host, "error", err)
	}

	m.logger.Info("certificate obtained", "host", host)
	return cert, nil
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

	go func() {
		// Initial delay to let server start
		select {
		case <-ctx.Done():
			return
		case <-time.After(1 * time.Minute):
		}

		ticker := time.NewTicker(12 * time.Hour)
		defer ticker.Stop()

		for {
			m.checkRenewals(ctx)

			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
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

			newCert, certPEM, keyPEM, err := m.acme.ObtainCertificate(ctx, leaf.DNSNames)
			if err != nil {
				m.logger.Error("renewal failed", "host", host, "error", err)
				return true
			}

			m.certs.Store(host, newCert)
			if err := m.storage.Save(host, newCert, keyPEM, certPEM); err != nil {
				m.logger.Warn("failed to persist renewed cert", "host", host, "error", err)
			}

			m.logger.Info("certificate renewed", "host", host)
		}

		return true
	})
}

// UpdateDomains updates the domain list (for hot reload).
func (m *Manager) UpdateDomains(domains []config.Domain) {
	m.domains = domains
}

// TLSConfig returns a tls.Config with best-practice settings.
func (m *Manager) TLSConfig() *tls.Config {
	return &tls.Config{
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
}

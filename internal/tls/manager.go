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
	"errors"
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
	"golang.org/x/sync/singleflight"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/router"
	"github.com/uwaserver/uwas/internal/tls/acme"
)

// Manager handles TLS certificates: loading, ACME issuance, renewal, SNI routing.
type Manager struct {
	certs sync.Map // host → *tls.Certificate
	// manualCerts holds the certificate LoadManualCerts put into certs for each
	// host. A later pass uses it to drop what the config no longer provides
	// without touching a certificate that arrived another way (ACME,
	// RegisterCert), which is a different pointer in certs (F16300).
	manualCerts sync.Map // host → *tls.Certificate
	manualMu    sync.Mutex
	acme        *acme.Client
	config      config.ACMEConfig
	storage     *CertStorage
	logger      *logger.Logger
	domainsMu   sync.RWMutex
	domains     []config.Domain
	// policyRouter resolves an SNI name to the domain whose TLS policy
	// applies, built over domains with the request router's precedence so
	// the TLS layer and the router never disagree about who owns a name.
	policyRouter *router.VHostRouter

	// allowlist is consulted lock-free on every TLS handshake by
	// GetCertificate → isDomainConfigured. Rebuilt under domainsMu in
	// UpdateDomains; readers load the *current* snapshot atomically.
	// Refs: refactor.md P1.
	allowlist atomic.Pointer[domainAllowlist]

	// singleflight per host to prevent parallel ACME issuance for same host.
	obtainFlight sync.Map // host → singleflight.Group

	// On-demand rate limiting: max 10 certs per minute.
	// onDemandFail remembers hosts whose on-demand order failed, so repeated
	// handshakes do not each become a CA order (F7901). host → *onDemandFailure.
	onDemandFail      sync.Map
	onDemandFailCount atomic.Int64
	// onDemandBackoffBase overrides the first retry delay for testing.
	onDemandBackoffBase time.Duration

	onDemandMu    sync.Mutex
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

	// retryBackoff overrides the 30-second retry delay in ObtainCerts for testing.
	// When zero, defaults to 30 * time.Second.
	retryBackoff time.Duration
	// renewalInterval overrides the 12-hour ticker interval in StartRenewal for testing.
	renewalInterval time.Duration

	// mTLS fields
	clientCA       *x509.CertPool
	clientAuthMode tls.ClientAuthType
	// Per-domain client CA pools, keyed by domain host. A single shared
	// pool would let one mTLS domain force client certificates onto every
	// other domain on the same listener.
	clientCAs map[string]*x509.CertPool

	// AllowSelfSigned enables auto-generated self-signed certs as fallback.
	// Set to true in production server; false in tests.
	AllowSelfSigned bool

	// selfSigned caches fallback certs apart from certs, so HasCert,
	// ObtainCerts and checkRenewals never mistake them for issued ones.
	// Bounded: under a wildcard domain the SNI names are client-chosen.
	selfSigned      sync.Map // host → *tls.Certificate
	selfSignedCount atomic.Int64
}

const (
	onDemandAskTimeout      = 5 * time.Second
	onDemandObtainTimeout   = 2 * time.Minute
	maxOnDemandAskBodyBytes = 8 << 10 // 8KB
	ocspFetchTimeout        = 10 * time.Second
	maxSelfSignedCached     = 1024
	// issueFlightTimeout bounds one ACME order. It is detached from the
	// callers' contexts (see issueCert), so it is the order's own limit.
	issueFlightTimeout = 10 * time.Minute
	// An on-demand order that failed is not retried for onDemandBackoffMin,
	// doubling per consecutive failure up to onDemandBackoffMax. The CA limits
	// failed validations per hostname, so retrying on every handshake would
	// lock the host out and spend the shared on-demand budget.
	onDemandBackoffMin     = time.Minute
	onDemandBackoffMax     = time.Hour
	maxOnDemandFailTracked = 4096
)

type onDemandFailure struct {
	until    time.Time
	failures int
}

func NewManager(cfg config.ACMEConfig, domains []config.Domain, log *logger.Logger) *Manager {
	m := &Manager{
		config:  cfg,
		storage: NewCertStorage(cfg.Storage),
		logger:  log,
		domains: cloneTLSManagedDomains(domains),
	}
	m.policyRouter = router.NewVHostRouter(m.domains)
	m.allowlist.Store(buildDomainAllowlist(domains))

	// Initialize ACME client if email is configured
	if cfg.Email != "" {
		m.acme = acme.NewClient(cfg.CAURL, cfg.Storage, log)
		m.acme.SetContactEmail(cfg.Email)

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
	certs, skipped, err := m.storage.LoadAllDetailed()
	if err != nil {
		m.logger.Warn("failed to load existing certs", "error", err)
		return
	}
	for dir, serr := range skipped {
		m.logger.Warn("skipping unreadable certificate on disk",
			"directory", dir, "error", serr)
	}

	// A directory an earlier version named after a Unicode host is found under
	// the A-label every lookup now uses (F13000). When that directory and the
	// A-label one both exist (a renewal wrote the second), they collapse onto
	// one key, and ranging over the map picked between them at random, so a
	// restart could serve the older, possibly expired, certificate (F15400).
	type pick struct {
		dir  string
		cert *tls.Certificate
	}
	best := make(map[string]pick, len(certs))
	for dir, cert := range certs {
		key := config.CanonicalHost(dir)
		cur, ok := best[key]
		switch {
		case !ok:
			best[key] = pick{dir, cert}
		case betterStoredCert(dir, cert, cur.dir, cur.cert, key):
			m.logger.Warn("two certificate directories name the same host; using the newer certificate",
				"host", key, "using", dir, "ignoring", cur.dir)
			best[key] = pick{dir, cert}
		default:
			m.logger.Warn("two certificate directories name the same host; using the newer certificate",
				"host", key, "using", cur.dir, "ignoring", dir)
		}
	}
	for key, p := range best {
		m.certs.Store(key, p.cert)
		m.logger.Debug("loaded certificate", "domain", p.dir)
	}

	if len(best) > 0 {
		m.logger.Info("loaded certificates from disk", "count", len(best))
	}
}

// betterStoredCert reports whether the certificate in directory a should
// replace the one in directory b for the host key. The later expiry wins; on a
// tie the directory already named after the key wins, then the name order, so
// the choice never depends on map iteration.
func betterStoredCert(a string, ac *tls.Certificate, b string, bc *tls.Certificate, key string) bool {
	an, bn := certNotAfter(ac), certNotAfter(bc)
	if !an.Equal(bn) {
		return an.After(bn)
	}
	if (a == key) != (b == key) {
		return a == key
	}
	return a < b
}

func certNotAfter(c *tls.Certificate) time.Time {
	if c == nil {
		return time.Time{}
	}
	if c.Leaf != nil {
		return c.Leaf.NotAfter
	}
	if leaf, err := leafCert(c); err == nil {
		return leaf.NotAfter
	}
	return time.Time{}
}

// LoadManualCerts loads manually configured certificates.
//
// It also removes the ones the config no longer provides. A certificate lives
// only in memory once loaded, and nothing took it out again: a domain switched
// to ssl.mode off kept answering TLS with its old certificate, and a host
// removed and later given to another domain handed that domain the previous
// owner's certificate, whose key the previous owner still holds (F16300). A
// host that is now auto-SSL keeps its certificate until ACME issues a new one,
// so switching a working site to auto never leaves a gap.
func (m *Manager) LoadManualCerts() {
	m.manualMu.Lock()
	defer m.manualMu.Unlock()

	domains := m.snapshotDomains()
	m.domainsMu.RLock()
	pr := m.policyRouter
	m.domainsMu.RUnlock()

	// claimed is every host a manual domain owns, whether or not its files
	// loaded this time: a pair that does not parse keeps the previous
	// certificate (F2770), so its hosts must not look unconfigured.
	claimed := make(map[string]struct{})
	autoHosts := make(map[string]struct{})
	for _, d := range domains {
		if d.SSL.Mode == "auto" {
			for _, host := range tlsHostsForDomain(d) {
				autoHosts[host] = struct{}{}
			}
		}
		if d.SSL.Mode != "manual" {
			continue
		}
		var owned []string
		for _, host := range tlsHostsForDomain(d) {
			// A name another domain owns (e.g. the apex of an explicit
			// www domain) keeps that owner's certificate; storing it here
			// let config order decide whose cert SNI was served.
			if owner := tlsOwner(pr, domains, host); owner != nil && !sameTLSDomain(owner, &d) {
				continue
			}
			owned = append(owned, host)
			claimed[host] = struct{}{}
		}
		cert, err := tls.LoadX509KeyPair(d.SSL.Cert, d.SSL.Key)
		if err != nil {
			m.logger.Error("failed to load manual cert",
				"domain", d.Host, "error", err)
			continue
		}
		for _, host := range owned {
			m.certs.Store(host, &cert)
			m.manualCerts.Store(host, &cert)
		}
		m.logger.Info("loaded manual certificate", "domain", d.Host, "aliases", len(d.Aliases))
	}

	m.manualCerts.Range(func(k, v any) bool {
		host := k.(string)
		if _, ok := claimed[host]; ok {
			return true
		}
		if _, ok := autoHosts[host]; ok {
			return true // ACME replaces it when the order completes
		}
		// Only the entry this pass would have written is removed; a
		// certificate stored since (ACME, RegisterCert) is a different
		// pointer and stays.
		if m.certs.CompareAndDelete(host, v) {
			m.logger.Info("manual certificate no longer configured; stopped serving it", "host", host)
		}
		m.manualCerts.Delete(host)
		return true
	})
}

// domainAllowlist is the read-mostly snapshot consulted on every TLS
// handshake. Built once per UpdateDomains, swapped via atomic.Pointer so
// readers never take a lock. allowAll=true reproduces the
// "no-configured-domains → allow everything" legacy fallback used by
// tests / first-boot dev.
type domainAllowlist struct {
	allowAll  bool
	exact     map[string]struct{} // hosts + aliases, lowercased
	apex      map[string]struct{} // apex hosts implied by "*.x" entries
	wildcards []string            // dot-prefixed suffixes, e.g. ".example.com"
}

func (a *domainAllowlist) allow(host string) bool {
	if a == nil || a.allowAll {
		return true
	}
	if _, ok := a.exact[host]; ok {
		return true
	}
	if _, ok := a.apex[host]; ok {
		return true
	}
	for _, suf := range a.wildcards {
		if strings.HasSuffix(host, suf) {
			return true
		}
	}
	return false
}

func buildDomainAllowlist(domains []config.Domain) *domainAllowlist {
	if len(domains) == 0 {
		return &domainAllowlist{allowAll: true}
	}
	a := &domainAllowlist{
		exact: make(map[string]struct{}, len(domains)*2),
		apex:  make(map[string]struct{}),
	}
	for _, d := range domains {
		h := config.CanonicalHost(d.Host)
		if strings.HasPrefix(h, "*.") {
			suffix := h[2:]
			a.wildcards = append(a.wildcards, "."+suffix)
			a.apex[suffix] = struct{}{}
		} else {
			for _, host := range []string{canonicalTLSHostname(h), implicitTLSWWWHostname(h)} {
				if host != "" {
					a.exact[host] = struct{}{}
				}
			}
		}
		for _, alias := range d.Aliases {
			alias = canonicalTLSHostname(alias)
			if alias == "" {
				continue
			}
			if strings.HasPrefix(alias, "*.") {
				// The router serves every name under a wildcard alias, so
				// the handshake must accept them, not just the literal "*.x".
				a.wildcards = append(a.wildcards, "."+alias[2:])
				a.apex[alias[2:]] = struct{}{}
				continue
			}
			a.exact[alias] = struct{}{}
			if www := implicitTLSWWWHostname(alias); www != "" {
				a.exact[www] = struct{}{}
			}
		}
	}
	return a
}

// isDomainConfigured checks if a domain is in the configured domains list.
// It supports exact match, wildcard match, and alias match.
// If no domains are configured, returns true to allow all (backward compatibility).
//
// Hot path: every TLS handshake. Reads through atomic.Pointer; no mutex.
func (m *Manager) isDomainConfigured(host string) bool {
	return m.allowlist.Load().allow(host)
}

// askURLCause drops the URL a *url.Error carries. The on-demand ask URL is
// operator config and usually holds a shared secret in its query, which the
// error text would otherwise copy into logs and handshake errors (F2112).
func askURLCause(err error) error {
	var ue *neturl.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// GetCertificate is the tls.Config.GetCertificate callback for SNI routing.
func (m *Manager) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	// Browsers send the A-label form of an IDN host and the config may spell it
	// in Unicode; every key in this package is the A-label (F13000).
	name := config.CanonicalHost(hello.ServerName)
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
		if m.onDemandBackedOff(name) {
			return nil, fmt.Errorf("on-demand certificate for %s failed recently; not retrying yet", name)
		}
		// Check OnDemandAsk URL if configured.
		if m.config.OnDemandAsk != "" {
			askURL, err := neturl.Parse(m.config.OnDemandAsk)
			if err != nil {
				return nil, fmt.Errorf("invalid on-demand ask URL: %w", askURLCause(err))
			}
			query := askURL.Query()
			query.Set("domain", name)
			askURL.RawQuery = query.Encode()

			askCtx, cancel := context.WithTimeout(handshakeCtx, onDemandAskTimeout)
			defer cancel()
			req, err := http.NewRequestWithContext(askCtx, http.MethodGet, askURL.String(), nil)
			if err != nil {
				return nil, fmt.Errorf("on-demand ask request failed for %s: %w", name, askURLCause(err))
			}

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				err = askURLCause(err)
				m.logger.Error("on-demand ask failed", "domain", name, "error", err)
				return nil, fmt.Errorf("on-demand ask error for %s: %w", name, err)
			}
			defer resp.Body.Close()
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxOnDemandAskBodyBytes))
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

		cert, err := m.obtainCert(obtainCtx, name, false)
		if err != nil {
			m.logger.Error("on-demand cert failed", "domain", name, "error", err)
			if !errors.Is(err, context.Canceled) { // a dropped handshake is not the CA's verdict
				m.recordOnDemandFailure(name)
			}
			return nil, err
		}
		m.clearOnDemandFailure(name)
		return cert, nil
	}

	// 4. Default certificate
	if cert, ok := m.certs.Load("_default"); ok {
		return cert.(*tls.Certificate), nil
	}

	// 5. Fallback: if AllowSelfSigned is set, generate a temp cert so TLS works.
	if m.AllowSelfSigned {
		cached, hit := m.selfSigned.Load(name)
		if hit && selfSignedUsable(cached.(*tls.Certificate)) {
			return cached.(*tls.Certificate), nil
		}
		m.logger.Warn("no certificate, generating self-signed", "domain", name)
		cert, err := m.generateSelfSigned(name)
		if err != nil {
			return nil, fmt.Errorf("no certificate for %s: %w", name, err)
		}
		if hit {
			// Replace the expired entry in place; its slot is already counted.
			if !m.selfSigned.CompareAndSwap(name, cached, cert) {
				if cur, ok := m.selfSigned.Load(name); ok && selfSignedUsable(cur.(*tls.Certificate)) {
					return cur.(*tls.Certificate), nil
				}
			}
		} else if m.selfSignedCount.Load() < maxSelfSignedCached {
			if prev, loaded := m.selfSigned.LoadOrStore(name, cert); loaded {
				return prev.(*tls.Certificate), nil
			}
			m.selfSignedCount.Add(1)
		}
		return cert, nil
	}

	return nil, fmt.Errorf("no certificate for %s", name)
}

// selfSignedUsable reports whether a cached fallback cert is still inside
// its validity window. Without this check the first fallback generated for a
// host was served forever, long past self_signed_validity.
func selfSignedUsable(cert *tls.Certificate) bool {
	leaf, err := leafCert(cert)
	if err != nil {
		return false
	}
	return time.Now().Before(leaf.NotAfter)
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
	// Parsed once here so the per-handshake expiry check stays cheap.
	if leaf, err := x509.ParseCertificate(certDER); err == nil {
		cert.Leaf = leaf
	}
	return cert, nil
}

// ObtainCerts requests ACME certificates for all auto-SSL domains.
// Failed domains are retried up to 3 times with exponential backoff.
func (m *Manager) ObtainCerts(ctx context.Context) {
	if m.acme == nil {
		return
	}

	// Collect every auto-SSL hostname that needs a certificate. Aliases get
	// their own certs so SNI works cleanly for both example.com and www.example.com.
	var pending []string
	for _, d := range m.snapshotDomains() {
		if d.SSL.Mode != "auto" {
			continue
		}
		for _, host := range tlsHostsForDomain(d) {
			if _, ok := m.certs.Load(host); ok {
				continue
			}
			pending = append(pending, host)
		}
	}

	backoff := 30 * time.Second
	if m.retryBackoff > 0 {
		backoff = m.retryBackoff
	}
	for attempt := 0; attempt < 3 && len(pending) > 0; attempt++ {
		if attempt > 0 {
			m.logger.Info("retrying failed certificates", "count", len(pending), "attempt", attempt+1)
			timer := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			backoff *= 2
		}

		var failed []string
		for _, host := range pending {
			m.logger.Info("obtaining certificate", "domain", host)
			_, err := m.obtainCert(ctx, host, false)
			if err != nil {
				m.logger.Error("failed to obtain cert", "domain", host, "error", err)
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

func tlsHostsForDomain(d config.Domain) []string {
	seen := make(map[string]struct{}, 2+len(d.Aliases)*2)
	hosts := make([]string, 0, 2+len(d.Aliases)*2)
	for _, host := range append([]string{d.Host}, d.Aliases...) {
		host = canonicalTLSHostname(host)
		if host == "" {
			continue
		}
		candidates := []string{host, implicitTLSWWWHostname(host)}
		if normalizeTLSCanonicalHost(d.CanonicalHost) == "www" && implicitTLSWWWHostname(host) != "" {
			candidates = []string{implicitTLSWWWHostname(host), host}
		}
		for _, candidate := range candidates {
			if candidate == "" {
				continue
			}
			if _, ok := seen[candidate]; ok {
				continue
			}
			seen[candidate] = struct{}{}
			hosts = append(hosts, candidate)
		}
	}
	return hosts
}

func normalizeTLSCanonicalHost(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "www":
		return "www"
	default:
		return "apex"
	}
}

func canonicalTLSHostname(host string) string {
	host = config.CanonicalHost(host)
	if strings.HasPrefix(host, "www.") && !strings.Contains(host, ":") && !strings.HasPrefix(host, "*.") {
		apex := strings.TrimPrefix(host, "www.")
		if apex != "" && strings.Contains(apex, ".") {
			return apex
		}
	}
	return host
}

func implicitTLSWWWHostname(host string) string {
	host = canonicalTLSHostname(host)
	if host == "" || strings.Contains(host, ":") || strings.HasPrefix(host, "*.") || !strings.Contains(host, ".") {
		return ""
	}
	return "www." + host
}

// SetOnCertRenewed sets a callback that fires after a certificate is renewed.
func (m *Manager) SetOnCertRenewed(fn func(host string)) {
	m.onCertRenewed = fn
}

// SetOnCertExpiry sets a callback that fires when a certificate is near expiry and renewal fails.
func (m *Manager) SetOnCertExpiry(fn func(host string, daysLeft int)) {
	m.onCertExpiry = fn
}

// RenewCert forces renewal of a certificate for the given host. Unlike
// obtainCert called from the on-demand TLS path or the pending-issuance
// retry loop, this MUST hit ACME even when a (still-valid) cert is
// already cached — operators click "Force Renew" precisely because they
// want a fresh certificate, and silently returning the cached one would
// look indistinguishable from a no-op button.
func (m *Manager) RenewCert(ctx context.Context, host string) error {
	if m.acme == nil && m.acmeObtainFunc == nil {
		return fmt.Errorf("ACME client not configured (set acme.email in config)")
	}
	host = config.CanonicalHost(host)
	_, err := m.obtainCert(ctx, host, true)
	return err
}

// HasCert reports whether a usable TLS certificate is loaded for the given
// host, considering exact and wildcard matches. Callers on port 80 use this
// to avoid redirecting to HTTPS for auto-SSL domains whose certificate has
// not been issued yet (ACME in flight, DNS not pointed, etc.) — without this
// guard the browser hits a TLS handshake failure and the site is unreachable.
func (m *Manager) HasCert(host string) bool {
	host = config.CanonicalHost(host)
	if _, ok := m.certs.Load(host); ok {
		return true
	}
	if parts := strings.SplitN(host, ".", 2); len(parts) == 2 {
		if _, ok := m.certs.Load("*." + parts[1]); ok {
			return true
		}
	}
	return false
}

// RegisterCert stores a pre-loaded certificate for the given host. Used when
// a certificate is supplied out-of-band (uploaded via the API or constructed
// in tests) rather than read from disk through LoadManualCerts.
func (m *Manager) RegisterCert(host string, cert *tls.Certificate) {
	if cert == nil {
		return
	}
	m.certs.Store(config.CanonicalHost(host), cert)
}

// CertStatus returns metadata for a loaded certificate, or nil if not loaded.
func (m *Manager) CertStatus(host string) *CertStatusInfo {
	host = config.CanonicalHost(host)
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

// obtainCert issues (or returns the cached) certificate for host. When
// force is true the cache short-circuit is bypassed so an explicit
// RenewCert always hits ACME — handshake-driven on-demand callers and
// startup retry loops pass force=false to coalesce concurrent first-
// issuance requests onto a single ACME call.
func (m *Manager) obtainCert(ctx context.Context, host string, force bool) (*tls.Certificate, error) {
	return m.issueCert(ctx, host, []string{host}, force)
}

// issueCert runs one ACME issuance for host through the per-host
// singleflight, requesting names when this caller leads the flight.
// Scheduled renewal goes through here too, so it coalesces with a
// concurrent Force Renew instead of issuing a second certificate.
func (m *Manager) issueCert(ctx context.Context, host string, names []string, force bool) (*tls.Certificate, error) {
	if m.acme == nil && m.acmeObtainFunc == nil {
		return nil, fmt.Errorf("ACME client not configured")
	}
	var prev any
	if force {
		prev, _ = m.certs.Load(host)
	}

	// Get or create singleflight group for this host. The entry is dropped by
	// the flight itself when the ORDER ends (doIssue), not when a caller
	// returns: the order runs detached from its callers (F7600), so a caller
	// that leaves early (a handshake that drops) must not remove the entry
	// while the order is still in flight, or the next caller starts a second
	// concurrent order for the same host and burns the CA's duplicate limit
	// (F15100). Removing it when the order ends keeps the map from growing
	// with attacker-influenced SNI names.
	gVal, _ := m.obtainFlight.LoadOrStore(host, &singleflight.Group{})

	// Use singleflight so only one caller does the actual ACME issuance.
	// Subsequent callers for the same host block until the first completes.
	v, err, shared := m.doIssue(ctx, gVal.(*singleflight.Group), host, names, force)
	// A forced caller that joined a non-forced flight answered from the
	// cache got no new certificate; lead a forced flight of its own.
	if force && err == nil && shared && prev != nil && v == prev {
		gVal, _ = m.obtainFlight.LoadOrStore(host, &singleflight.Group{})
		v, err, _ = m.doIssue(ctx, gVal.(*singleflight.Group), host, names, force)
	}

	if err != nil {
		return nil, err
	}
	return v.(*tls.Certificate), nil
}

// doIssue joins or starts the flight for host. The order runs detached from
// ctx and each caller waits under its own: the flight belongs to every caller
// sharing it, so the first caller's cancellation (a visitor dropping its
// handshake, an admin request that ends) must not abort the order for the
// others (F7600).
func (m *Manager) doIssue(ctx context.Context, g *singleflight.Group, host string, names []string, force bool) (any, error, bool) {
	flight := m.issueFlight(ctx, host, names, force)
	ch := g.DoChan("", func() (any, error) {
		// The order ends here, whoever is still waiting for it: drop this
		// group's map entry (and only this group's) now (F15100).
		defer m.obtainFlight.CompareAndDelete(host, g)
		return flight()
	})
	select {
	case r := <-ch:
		return r.Val, r.Err, r.Shared
	case <-ctx.Done():
		return nil, ctx.Err(), false
	}
}

func (m *Manager) issueFlight(ctx context.Context, host string, names []string, force bool) func() (any, error) {
	return func() (res any, err error) {
		// DoChan runs this on its own goroutine, where a panic would end the
		// process instead of the one handshake or request that led the flight.
		defer func() {
			if rec := recover(); rec != nil {
				res, err = nil, fmt.Errorf("certificate issuance for %s panicked: %v", host, rec)
			}
		}()
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), issueFlightTimeout)
		defer cancel()

		// Double-check the cache only when force=false. Forced renewal
		// (the "Force Renew" dashboard button) must skip this so a fresh
		// cert is actually obtained from ACME — otherwise the call returns
		// the still-cached cert and the operator sees no change.
		if !force {
			if cert, ok := m.certs.Load(host); ok {
				return cert.(*tls.Certificate), nil
			}
		}

		var cert *tls.Certificate
		var certPEM, keyPEM []byte
		if m.acmeObtainFunc != nil {
			cert, certPEM, keyPEM, err = m.acmeObtainFunc(ctx, names)
		} else {
			cert, certPEM, keyPEM, err = m.acme.ObtainCertificate(ctx, names)
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
			m.logger.Warn("failed to persist cert", "domain", host, "error", err)
		}

		m.logger.Info("certificate obtained", "domain", host)
		return cert, nil
	}
}

func (m *Manager) onDemandBackedOff(host string) bool {
	v, ok := m.onDemandFail.Load(host)
	return ok && time.Now().Before(v.(*onDemandFailure).until)
}

func (m *Manager) recordOnDemandFailure(host string) {
	base := m.onDemandBackoffBase
	if base <= 0 {
		base = onDemandBackoffMin
	}
	failures := 1
	if v, ok := m.onDemandFail.Load(host); ok {
		failures = v.(*onDemandFailure).failures + 1
	} else if m.onDemandFailCount.Load() >= maxOnDemandFailTracked {
		return // bounded: SNI names under a wildcard domain are client-chosen
	}
	delay := base
	for i := 1; i < failures && delay < onDemandBackoffMax; i++ {
		delay *= 2
	}
	if delay > onDemandBackoffMax {
		delay = onDemandBackoffMax
	}
	if _, loaded := m.onDemandFail.Swap(host, &onDemandFailure{until: time.Now().Add(delay), failures: failures}); !loaded {
		m.onDemandFailCount.Add(1)
	}
}

func (m *Manager) clearOnDemandFailure(host string) {
	if _, loaded := m.onDemandFail.LoadAndDelete(host); loaded {
		m.onDemandFailCount.Add(-1)
	}
}

// onDemandAllow checks the on-demand rate limiter. Returns true if the
// request is allowed (fewer than onDemandMaxPerMinute in the current window).
func (m *Manager) onDemandAllow() bool {
	m.onDemandMu.Lock()
	defer m.onDemandMu.Unlock()
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
// and renews certificates that are within 30 days of expiry. Without ACME
// configured it still runs, as an expiry alert and OCSP refresh pass over the
// operator's own certificates (F7900).
func (m *Manager) StartRenewal(ctx context.Context) {
	m.logger.SafeGo("tls.renewal", func() {
		// Initial delay to let server start
		initDelay := m.renewalInitialDelay
		if initDelay == 0 {
			initDelay = 1 * time.Minute
		}
		initTimer := time.NewTimer(initDelay)
		select {
		case <-ctx.Done():
			initTimer.Stop()
			return
		case <-initTimer.C:
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

// renewalCandidate is a snapshot taken in pass 1 of checkRenewals so the
// network-bound ACME call in pass 2 does not run while iterating m.certs.
// sync.Map.Range is non-blocking for unrelated reads, but holding
// minutes-long ACME work inside the callback delays any concurrent
// Range / Store on the same map and serializes against renewals for
// other hosts.
type renewalCandidate struct {
	host      string
	dnsNames  []string
	remaining time.Duration
	// alertOnly marks a cert ACME does not own (e.g. ssl.mode manual): it is
	// reported as expiring but never re-issued over the operator's cert.
	alertOnly bool
}

// renewalModeFor returns the ssl.mode of the configured domain that owns
// host, or "" when no domain does (e.g. a cert left on disk for a domain
// since removed). An exact or alias match wins over a wildcard match.
func renewalModeFor(domains []config.Domain, host string) string {
	return renewalModeWith(router.NewVHostRouter(domains), domains, host)
}

// renewalModeWith resolves the owner the way the router does (see
// configForHost); config order decided it before, so an apex domain listed
// first could mark an explicit www domain's ACME cert as manual and never
// renew it. The match loop below is kept for names no owner resolves.
func renewalModeWith(pr *router.VHostRouter, domains []config.Domain, host string) string {
	if d := tlsOwner(pr, domains, host); d != nil {
		return d.SSL.Mode
	}
	wildcardMode := ""
	for i := range domains {
		d := &domains[i]
		if !domainMatchesHost(d, host) {
			continue
		}
		if !strings.HasPrefix(d.Host, "*.") {
			return d.SSL.Mode
		}
		if wildcardMode == "" {
			wildcardMode = d.SSL.Mode
		}
	}
	return wildcardMode
}

func (m *Manager) checkRenewals(ctx context.Context) {
	m.refreshOCSP(ctx)

	// No configured domains keeps the legacy renew-everything behaviour,
	// matching the allow-all fallback of the SNI allowlist.
	domains := m.snapshotDomains()
	pr := router.NewVHostRouter(domains)
	// Nothing can re-issue a cert without ACME, so every expiring one is only
	// reported, like a manual cert.
	canIssue := m.acme != nil || m.acmeObtainFunc != nil

	// Pass 1: walk the cert map once and copy out the hosts that need
	// renewing. The Range callback returns quickly, freeing the map.
	var candidates []renewalCandidate
	m.certs.Range(func(key, value any) bool {
		host := key.(string)
		if host == "_default" {
			return true
		}
		alertOnly := false
		if len(domains) > 0 {
			switch renewalModeWith(pr, domains, host) {
			case "auto":
			case "":
				return true
			default:
				alertOnly = true
			}
		}
		if !canIssue {
			alertOnly = true
		}
		cert := value.(*tls.Certificate)
		leaf, err := leafCert(cert)
		if err != nil {
			return true
		}
		remaining := time.Until(leaf.NotAfter)
		if remaining < 30*24*time.Hour {
			candidates = append(candidates, renewalCandidate{
				host:      host,
				dnsNames:  append([]string(nil), leaf.DNSNames...),
				remaining: remaining,
				alertOnly: alertOnly,
			})
		}
		return true
	})

	// Pass 2: renew sequentially outside the Range. ACME issuance is rate-
	// limited upstream (Let's Encrypt new-cert/new-order quotas) so adding
	// concurrency here mostly buys 429s; sequential is the right default.
	// Refs: refactor.md P7.
	for _, c := range candidates {
		if err := ctx.Err(); err != nil {
			return
		}
		m.renewOne(ctx, c)
	}
}

func (m *Manager) renewOne(ctx context.Context, c renewalCandidate) {
	if c.alertOnly {
		m.logger.Warn("certificate near expiry; not managed by ACME, replace it manually",
			"domain", c.host, "expires_in", c.remaining.Round(time.Hour))
		if m.onCertExpiry != nil {
			m.onCertExpiry(c.host, int(c.remaining.Hours()/24))
		}
		return
	}
	m.logger.Info("renewing certificate",
		"domain", c.host,
		"expires_in", c.remaining.Round(time.Hour),
	)

	// Same per-host singleflight as obtainCert (staples, stores and
	// persists), so a concurrent Force Renew shares this issuance.
	if _, err := m.issueCert(ctx, c.host, c.dnsNames, true); err != nil {
		m.logger.Error("renewal failed", "domain", c.host, "error", err)
		if m.onCertExpiry != nil {
			daysLeft := int(c.remaining.Hours() / 24)
			m.onCertExpiry(c.host, daysLeft)
		}
		return
	}

	m.logger.Info("certificate renewed", "domain", c.host)
	if m.onCertRenewed != nil {
		m.onCertRenewed(c.host)
	}
}

// UpdateDomains updates the domain list (for hot reload).
func (m *Manager) UpdateDomains(domains []config.Domain) {
	allowlist := buildDomainAllowlist(domains)
	m.domainsMu.Lock()
	m.domains = cloneTLSManagedDomains(domains)
	m.policyRouter = router.NewVHostRouter(m.domains)
	m.domainsMu.Unlock()
	// Publish *after* the slice swap so any handshake that races us either
	// sees the old allowlist+old domains or the new allowlist+new domains.
	m.allowlist.Store(allowlist)

	// Manual certificates and client CA bundles are read from disk, not from
	// the config, so swapping the domain list alone left a replaced
	// certificate, a newly added manual-SSL domain and a new ssl.client_ca
	// unapplied until restart (F2770). LoadManualCerts keeps the previous
	// certificate when the new files do not parse.
	m.LoadManualCerts()
	_ = m.LoadClientCAs() // logs per-domain failures itself
}

func (m *Manager) snapshotDomains() []config.Domain {
	m.domainsMu.RLock()
	defer m.domainsMu.RUnlock()

	return cloneTLSManagedDomains(m.domains)
}

func cloneTLSManagedDomains(domains []config.Domain) []config.Domain {
	out := append([]config.Domain(nil), domains...)
	for i := range out {
		out[i].Aliases = append([]string(nil), domains[i].Aliases...)
	}
	return out
}

// stapleOCSP fetches and attaches an OCSP response to the certificate.
// This is best-effort: if OCSP fails, the cert works without stapling.
// It mutates cert, so only call it before the certificate is published.
func (m *Manager) stapleOCSP(cert *tls.Certificate, host string) {
	if resp := m.fetchOCSP(context.Background(), cert, host); resp != nil {
		cert.OCSPStaple = resp
		m.logger.Info("OCSP stapled", "domain", host)
	}
}

// fetchOCSP asks the certificate's OCSP responder for a "good" response and
// returns it, or nil when the certificate has no responder, the fetch fails,
// or the response is not a valid good one.
func (m *Manager) fetchOCSP(ctx context.Context, cert *tls.Certificate, host string) []byte {
	if cert == nil || len(cert.Certificate) < 2 {
		return nil // need at least leaf + issuer
	}

	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil || len(leaf.OCSPServer) == 0 {
		return nil // no OCSP server or unparseable cert
	}

	issuer, err := x509.ParseCertificate(cert.Certificate[1])
	if err != nil {
		return nil
	}

	ocspReq, err := ocsp.CreateRequest(leaf, issuer, nil)
	if err != nil {
		return nil
	}

	ocspCtx, cancel := context.WithTimeout(ctx, ocspFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ocspCtx, http.MethodPost, leaf.OCSPServer[0], bytes.NewReader(ocspReq))
	if err != nil {
		return nil
	}
	req.Header.Set("Content-Type", "application/ocsp-request")

	httpResp, err := http.DefaultClient.Do(req)
	if err != nil {
		m.logger.Debug("OCSP fetch failed", "domain", host, "error", err)
		return nil
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode != http.StatusOK {
		return nil
	}

	respBytes, err := io.ReadAll(io.LimitReader(httpResp.Body, 1<<20))
	if err != nil {
		return nil
	}

	ocspResp, err := ocsp.ParseResponse(respBytes, issuer)
	if err != nil {
		return nil
	}

	if ocspResp.Status != ocsp.Good {
		return nil
	}
	// Signed by the issuer is not enough: the responder URL is plain HTTP, and
	// a response for another certificate or one past its NextUpdate must not be
	// stapled as this certificate's (F19300).
	if ocspResp.SerialNumber == nil || ocspResp.SerialNumber.Cmp(leaf.SerialNumber) != 0 {
		return nil
	}
	if !ocspResp.NextUpdate.IsZero() && !time.Now().Before(ocspResp.NextUpdate) {
		return nil
	}
	return respBytes
}

// ocspStapleStale reports whether the staple on cert is missing, unreadable,
// or past the middle of its validity window. OCSP responses last days while a
// certificate lasts months, so a staple fetched at issuance runs out long
// before the certificate does.
func ocspStapleStale(cert *tls.Certificate, now time.Time) bool {
	if len(cert.OCSPStaple) == 0 {
		return true
	}
	resp, err := ocsp.ParseResponse(cert.OCSPStaple, nil)
	if err != nil || resp.NextUpdate.IsZero() {
		return true
	}
	return resp.NextUpdate.Sub(now) < resp.NextUpdate.Sub(resp.ThisUpdate)/2
}

// refreshOCSP re-fetches the OCSP staple of every certificate that has a
// responder and a missing or stale staple. Certificates loaded from disk at
// start-up carry no staple, and the staple fetched at issuance expires within
// days, so without this pass stapling would silently stop (F7602). A certificate
// is shared with in-flight handshakes, so the refreshed one is a copy swapped
// into the map rather than a mutation.
func (m *Manager) refreshOCSP(ctx context.Context) {
	type target struct {
		host string
		cert *tls.Certificate
	}
	var targets []target
	now := time.Now()
	m.certs.Range(func(key, value any) bool {
		cert := value.(*tls.Certificate)
		if len(cert.Certificate) >= 2 && ocspStapleStale(cert, now) {
			targets = append(targets, target{key.(string), cert})
		}
		return true
	})
	for _, t := range targets {
		if ctx.Err() != nil {
			return
		}
		resp := m.fetchOCSP(ctx, t.cert, t.host)
		if resp == nil {
			continue
		}
		refreshed := *t.cert
		refreshed.OCSPStaple = resp
		// manualMu keeps the pair of swaps atomic against LoadManualCerts: it
		// removes a manual certificate by the pointer it stored, so a copy
		// left in certs alone would never be taken out again (F27100).
		m.manualMu.Lock()
		if m.certs.CompareAndSwap(t.host, t.cert, &refreshed) {
			m.manualCerts.CompareAndSwap(t.host, t.cert, &refreshed)
			m.logger.Info("OCSP staple refreshed", "domain", t.host)
		}
		m.manualMu.Unlock()
	}
}

// minTLSVersionFor maps a domain's ssl.min_version to a tls constant.
//
// Values below 1.2 are CLAMPED to 1.2 rather than honoured. The setting was
// dead until now and validation has always accepted "1.0" and "1.1", so a
// config carrying either has been served at 1.2 regardless. Honouring them
// literally would silently downgrade those deployments the moment this field
// started being read — a fix that weakens security is not a fix.
//
// Hardening upwards works: "1.3" now means 1.3.
func minTLSVersionFor(v string, log *logger.Logger, host string) uint16 {
	switch v {
	case "1.3":
		return tls.VersionTLS13
	case "1.2", "":
		return tls.VersionTLS12
	case "1.0", "1.1":
		if log != nil {
			log.Warn("ssl.min_version below 1.2 ignored; serving TLS 1.2",
				"domain", host, "requested", v)
		}
		return tls.VersionTLS12
	default:
		return tls.VersionTLS12
	}
}

// configForHost builds the per-domain TLS settings for an SNI name. Returns
// nil when the host matches no configured domain, so the caller keeps the
// base config.
func (m *Manager) configForHost(base *tls.Config, host string) *tls.Config {
	m.domainsMu.RLock()
	domains, pr := m.domains, m.policyRouter
	m.domainsMu.RUnlock()
	if pr == nil {
		pr = router.NewVHostRouter(domains)
	}

	// Resolve the owner exactly as the router does (explicit host or alias,
	// then implicit www/apex, then the longest wildcard host or alias), so
	// the policy applied at the handshake is that of the domain the request
	// will be served by. Config-order matching here let a public apex strip
	// an explicit www domain of its mTLS, or an mTLS www domain impose
	// client certificates on another domain's apex.
	d, ok := pr.LookupWithStatus(host)
	if !ok {
		// The router routes no domain here, but a wildcard host or alias
		// still claims its bare apex for TLS (allowlist, certificates), so
		// its policy keeps applying to that name.
		d = wildcardApexOwner(domains, host)
	}
	if d == nil {
		return nil
	}
	if d.SSL.MinVersion == "" && d.SSL.ClientCA == "" {
		return nil
	}

	out := base.Clone()
	out.MinVersion = minTLSVersionFor(d.SSL.MinVersion, m.logger, d.Host)

	// Per-domain mTLS, using the pool LoadClientCAs parsed for this
	// domain. Falling back to the listener-wide pool here would apply
	// one domain's CA to another's clients.
	if d.SSL.ClientCA != "" {
		pool := m.clientCAFor(d.Host)
		if pool == nil {
			// An unavailable configured CA must trust no clients rather
			// than silently disable the requested authentication policy.
			pool = x509.NewCertPool()
		}
		out.ClientCAs = pool
		out.ClientAuth = clientAuthModeFor(d.SSL.ClientAuth)
	}
	return out
}

// wildcardApexOwner returns the first domain whose wildcard host or alias
// "*.<host>" covers host as its bare apex, or nil.
func wildcardApexOwner(domains []config.Domain, host string) *config.Domain {
	host = config.CanonicalHost(host)
	if host == "" {
		return nil
	}
	for i := range domains {
		for _, name := range append([]string{domains[i].Host}, domains[i].Aliases...) {
			if config.CanonicalHost(name) == "*."+host {
				return &domains[i]
			}
		}
	}
	return nil
}

// tlsOwner returns the domain that owns an SNI name or certificate key, by
// the router's precedence with the wildcard-apex rule of configForHost, or
// nil when no configured domain does.
func tlsOwner(pr *router.VHostRouter, domains []config.Domain, host string) *config.Domain {
	if pr != nil {
		if d, ok := pr.LookupWithStatus(host); ok {
			return d
		}
	}
	return wildcardApexOwner(domains, host)
}

func sameTLSDomain(a, b *config.Domain) bool {
	return config.CanonicalHost(a.Host) == config.CanonicalHost(b.Host)
}

// domainMatchesHost reports whether an SNI name belongs to this domain.
//
// Mirrors buildDomainAllowlist: exact host, its implicit www form, wildcard
// hosts and aliases. Kept next to it so the two cannot drift into disagreeing
// about which names a domain owns.
func domainMatchesHost(d *config.Domain, host string) bool {
	host = canonicalTLSHostname(host)
	if host == "" {
		return false
	}

	h := config.CanonicalHost(d.Host)
	if strings.HasPrefix(h, "*.") {
		suffix := h[2:]
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}

	for _, name := range []string{canonicalTLSHostname(h), implicitTLSWWWHostname(h)} {
		if name != "" && name == host {
			return true
		}
	}
	for _, alias := range d.Aliases {
		a := canonicalTLSHostname(alias)
		if a == "" {
			continue
		}
		if strings.HasPrefix(a, "*.") && (host == a[2:] || strings.HasSuffix(host, a[1:])) {
			return true
		}
		if a == host || implicitTLSWWWHostname(a) == host {
			return true
		}
	}
	return false
}

// clientAuthModeFor maps ssl.client_auth to a tls.ClientAuthType.
func clientAuthModeFor(mode string) tls.ClientAuthType {
	switch mode {
	case "require":
		return tls.RequireAndVerifyClientCert
	case "request":
		return tls.VerifyClientCertIfGiven
	default:
		return tls.NoClientCert
	}
}

// TLSConfig returns a tls.Config with best-practice settings.
//
// MinVersion here is the floor for hosts that set nothing; GetConfigForClient
// raises it per domain. Before this, ssl.min_version was dead configuration:
// defaulted, validated and merged, but never read — a domain asking for 1.3
// was served 1.2.
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

	// Per-domain overrides are resolved from the ClientHello, where SNI is
	// available. Returning nil keeps this base config.
	cfg.GetConfigForClient = func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		return m.configForHost(cfg, hello.ServerName), nil
	}

	return cfg
}

// SetClientAuth configures mutual TLS (mTLS) with client certificate verification.
// LoadClientCAs parses the client CA file of every domain that configures one.
//
// ssl.client_ca and ssl.client_auth were dead configuration: SetClientAuth
// existed but nothing ever called it, so the pool stayed nil, the mTLS branch
// in TLSConfig was unreachable, and a domain asking for client certificates
// silently accepted anonymous clients.
//
// Pools are kept per domain rather than merged: a merged pool would make one
// domain's CA acceptable for clients of every other domain on the listener,
// and setting ClientAuth listener-wide would force certificates onto domains
// that never asked for them. The per-SNI path in configForHost applies them.
func (m *Manager) LoadClientCAs() error {
	m.domainsMu.RLock()
	domains := m.domains
	m.domainsMu.RUnlock()

	pools := make(map[string]*x509.CertPool)
	var firstErr error

	for i := range domains {
		d := &domains[i]
		if d.SSL.ClientCA == "" {
			continue
		}
		pool, err := clientCAPool(d.SSL.ClientCA)
		if err != nil {
			// One unreadable CA must not stop the others from loading;
			// its domain retains the configured authentication policy.
			m.logger.Error("client CA could not be loaded; configured CA will trust no clients",
				"domain", d.Host, "ca", d.SSL.ClientCA, "error", err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		pools[d.Host] = pool
		m.logger.Info("mTLS configured", "domain", d.Host, "ca", d.SSL.ClientCA, "mode", d.SSL.ClientAuth)
	}

	m.domainsMu.Lock()
	m.clientCAs = pools
	m.domainsMu.Unlock()
	return firstErr
}

// clientCAFor returns the CA pool loaded for a domain, or nil if it has none.
func (m *Manager) clientCAFor(host string) *x509.CertPool {
	m.domainsMu.RLock()
	defer m.domainsMu.RUnlock()
	return m.clientCAs[host]
}

// clientCAPool reads a PEM bundle into a pool.
func clientCAPool(caPath string) (*x509.CertPool, error) {
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("read client CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("no valid certs in client CA file: %s", caPath)
	}
	return pool, nil
}

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

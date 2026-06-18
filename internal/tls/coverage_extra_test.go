package uwastls

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/ocsp"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/dnsmanager"
	"github.com/uwaserver/uwas/internal/logger"
)

// --- generateSelfSigned ---

func TestGenerateSelfSignedDefaultValidity(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{Storage: t.TempDir()} // SelfSignedValidity zero → default 24h
	m := NewManager(cfg, nil, log)

	cert, err := m.generateSelfSigned("self.example")
	if err != nil {
		t.Fatalf("generateSelfSigned: %v", err)
	}
	leaf, err := leafCert(cert)
	if err != nil {
		t.Fatalf("leafCert: %v", err)
	}
	if leaf.Subject.CommonName != "self.example" {
		t.Errorf("CN = %q, want self.example", leaf.Subject.CommonName)
	}
	remaining := time.Until(leaf.NotAfter)
	// Default validity is 24h; allow generous slack for slow CI.
	if remaining <= 0 || remaining > 25*time.Hour {
		t.Errorf("remaining validity = %v, want ~24h", remaining)
	}
}

func TestGenerateSelfSignedCustomValidity(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{
		Storage:            t.TempDir(),
		SelfSignedValidity: config.Duration{Duration: 72 * time.Hour},
	}
	m := NewManager(cfg, nil, log)

	cert, err := m.generateSelfSigned("custom.example")
	if err != nil {
		t.Fatalf("generateSelfSigned: %v", err)
	}
	leaf, err := leafCert(cert)
	if err != nil {
		t.Fatalf("leafCert: %v", err)
	}
	remaining := time.Until(leaf.NotAfter)
	if remaining < 70*time.Hour || remaining > 73*time.Hour {
		t.Errorf("remaining validity = %v, want ~72h", remaining)
	}
}

// TestGetCertificateAllowSelfSignedFallback exercises GetCertificate branch 5
// (AllowSelfSigned), which delegates to generateSelfSigned and caches the cert.
func TestGetCertificateAllowSelfSignedFallback(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{Storage: t.TempDir()}
	m := NewManager(cfg, nil, log) // no domains → allowlist allowAll
	m.AllowSelfSigned = true

	hello := &tls.ClientHelloInfo{ServerName: "fallback.example"}
	cert, err := m.GetCertificate(hello)
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	if cert == nil {
		t.Fatal("expected self-signed cert, got nil")
	}
	// Cached: a second call must return the same stored cert (exact-match path).
	if _, ok := m.certs.Load("fallback.example"); !ok {
		t.Error("self-signed cert was not cached")
	}
	cert2, err := m.GetCertificate(hello)
	if err != nil {
		t.Fatalf("GetCertificate (cached): %v", err)
	}
	if cert2 != cert {
		t.Error("expected cached cert on second call")
	}
}

// --- SetClientAuth ---

func TestSetClientAuthEmptyPath(t *testing.T) {
	log := logger.New("error", "text")
	m := NewManager(config.ACMEConfig{Storage: t.TempDir()}, nil, log)
	if err := m.SetClientAuth("", "require"); err != nil {
		t.Errorf("empty path should be no-op, got %v", err)
	}
	if m.clientCA != nil {
		t.Error("clientCA should remain nil for empty path")
	}
}

func TestSetClientAuthReadError(t *testing.T) {
	log := logger.New("error", "text")
	m := NewManager(config.ACMEConfig{Storage: t.TempDir()}, nil, log)
	missing := filepath.Join(t.TempDir(), "nope.pem")
	if err := m.SetClientAuth(missing, "require"); err == nil {
		t.Error("expected read error for missing CA file")
	}
}

func TestSetClientAuthInvalidPEM(t *testing.T) {
	log := logger.New("error", "text")
	m := NewManager(config.ACMEConfig{Storage: t.TempDir()}, nil, log)
	path := filepath.Join(t.TempDir(), "bad.pem")
	if err := os.WriteFile(path, []byte("not a pem"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.SetClientAuth(path, "require"); err == nil {
		t.Error("expected error for invalid PEM data")
	}
}

func TestSetClientAuthModes(t *testing.T) {
	log := logger.New("error", "text")
	caPEM := generateCAPEM(t)

	cases := []struct {
		mode string
		want tls.ClientAuthType
	}{
		{"require", tls.RequireAndVerifyClientCert},
		{"request", tls.VerifyClientCertIfGiven},
		{"other", tls.NoClientCert},
	}
	for _, tc := range cases {
		m := NewManager(config.ACMEConfig{Storage: t.TempDir()}, nil, log)
		path := filepath.Join(t.TempDir(), "ca.pem")
		if err := os.WriteFile(path, caPEM, 0600); err != nil {
			t.Fatal(err)
		}
		if err := m.SetClientAuth(path, tc.mode); err != nil {
			t.Fatalf("SetClientAuth(%s): %v", tc.mode, err)
		}
		if m.clientCA == nil {
			t.Errorf("mode %s: clientCA not set", tc.mode)
		}
		if m.clientAuthMode != tc.want {
			t.Errorf("mode %s: clientAuthMode = %v, want %v", tc.mode, m.clientAuthMode, tc.want)
		}

		// TLSConfig must reflect mTLS settings once clientCA is set (covers the
		// previously-uncovered branch of TLSConfig).
		cfg := m.TLSConfig()
		if cfg.ClientCAs == nil {
			t.Errorf("mode %s: TLSConfig.ClientCAs nil", tc.mode)
		}
		if cfg.ClientAuth != tc.want {
			t.Errorf("mode %s: TLSConfig.ClientAuth = %v, want %v", tc.mode, cfg.ClientAuth, tc.want)
		}
	}
}

// --- allow (apex + wildcard branches) ---

func TestAllowlistWildcardAndApex(t *testing.T) {
	domains := []config.Domain{
		{Host: "*.wild.example"},
	}
	a := buildDomainAllowlist(domains)
	if a.allowAll {
		t.Fatal("allowlist should not be allowAll with a configured wildcard")
	}
	// apex implied by the wildcard entry
	if !a.allow("wild.example") {
		t.Error("apex wild.example should be allowed")
	}
	// suffix match against the wildcard
	if !a.allow("sub.wild.example") {
		t.Error("sub.wild.example should match wildcard")
	}
	if !a.allow("a.b.wild.example") {
		t.Error("deep subdomain should match wildcard suffix")
	}
	// unrelated host rejected
	if a.allow("other.example") {
		t.Error("other.example must not be allowed")
	}
}

func TestAllowlistNilReceiver(t *testing.T) {
	var a *domainAllowlist
	if !a.allow("anything.example") {
		t.Error("nil allowlist should allow all")
	}
}

func TestIsDomainConfiguredApexViaManager(t *testing.T) {
	log := logger.New("error", "text")
	domains := []config.Domain{{Host: "*.zone.example"}}
	m := NewManager(config.ACMEConfig{Storage: t.TempDir()}, domains, log)
	if !m.isDomainConfigured("zone.example") {
		t.Error("apex of wildcard should be configured")
	}
	if !m.isDomainConfigured("x.zone.example") {
		t.Error("subdomain should be configured")
	}
	if m.isDomainConfigured("evil.example") {
		t.Error("unrelated domain should be rejected")
	}
}

func TestAllowlistEmptyAliasSkipped(t *testing.T) {
	// An alias that canonicalizes to "" (e.g. a bare "www." or whitespace) hits
	// the `alias == ""` continue branch in buildDomainAllowlist.
	domains := []config.Domain{
		{Host: "keep.example", Aliases: []string{"   ", "alias.example"}},
	}
	a := buildDomainAllowlist(domains)
	if !a.allow("alias.example") {
		t.Error("real alias should be allowed")
	}
	if !a.allow("www.alias.example") {
		t.Error("implicit www of alias should be allowed")
	}
}

// --- GetCertificate: reject unconfigured + handshake context ---

func TestGetCertificateRejectsUnconfiguredDomain(t *testing.T) {
	log := logger.New("error", "text")
	domains := []config.Domain{{Host: "configured.example"}}
	m := NewManager(config.ACMEConfig{Storage: t.TempDir()}, domains, log)

	hello := &tls.ClientHelloInfo{ServerName: "evil.example"}
	if _, err := m.GetCertificate(hello); err == nil {
		t.Error("expected rejection for unconfigured domain")
	}
}

func TestGetCertificateUsesHelloContext(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{Storage: t.TempDir()}
	m := NewManager(cfg, nil, log)
	// Register a real keypair (with private key) so the handshake can complete.
	cert, _, _ := generateTestCert(t, "ctx.example")
	m.RegisterCert("ctx.example", cert)

	// Drive a real TLS handshake so hello.Context() is non-nil, exercising the
	// `helloCtx != nil` branch of GetCertificate. ClientHelloInfo.Context() is
	// only populated by the stdlib during an actual handshake.
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	srvCfg := m.TLSConfig()
	server := tls.Server(serverConn, srvCfg)
	client := tls.Client(clientConn, &tls.Config{
		InsecureSkipVerify: true,
		ServerName:         "ctx.example",
	})

	errc := make(chan error, 1)
	go func() { errc <- server.Handshake() }()
	if err := client.Handshake(); err != nil {
		t.Fatalf("client handshake: %v", err)
	}
	if err := <-errc; err != nil {
		t.Fatalf("server handshake: %v", err)
	}
}

// --- tlsHostsForDomain: empty host/alias skip branches ---

func TestTLSHostsForDomainSkipsEmpty(t *testing.T) {
	// An all-whitespace host and a whitespace alias canonicalize to "" and must
	// be skipped (covers the `host == ""` continue branches).
	d := config.Domain{Host: "   ", Aliases: []string{"   ", "real.example"}}
	hosts := tlsHostsForDomain(d)
	for _, h := range hosts {
		if h == "" {
			t.Error("empty host should not be present")
		}
	}
	found := false
	for _, h := range hosts {
		if h == "real.example" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected real.example in hosts, got %v", hosts)
	}
}

// --- checkRenewals: cancelled context short-circuit ---

func TestCheckRenewalsCancelledContext(t *testing.T) {
	log := logger.New("error", "text")
	m := NewManager(config.ACMEConfig{Storage: t.TempDir()}, nil, log)

	called := false
	m.acmeObtainFunc = func(ctx context.Context, domains []string) (*tls.Certificate, []byte, []byte, error) {
		called = true
		c, cp, kp := generateTestCert(t, domains[0])
		return c, cp, kp, nil
	}
	// A near-expiry cert makes it a renewal candidate.
	expiring := generateCertWithExpiry(t, "expiring.example", time.Hour)
	m.certs.Store("expiring.example", expiring)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled → pass 2 returns before renewOne

	m.checkRenewals(ctx)
	if called {
		t.Error("acmeObtainFunc must not run when context is already cancelled")
	}
}

// --- NewManager ACME / DNS provider wiring ---

func TestNewManagerWithEmailInitsACME(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{
		Email:   "ops@example.com",
		CAURL:   "https://acme.example/dir",
		Storage: t.TempDir(),
	}
	m := NewManager(cfg, nil, log)
	if m.acme == nil {
		t.Fatal("ACME client should be initialized when email is set")
	}
}

func TestNewManagerWithDNSProviderSuccess(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{
		Email:          "ops@example.com",
		Storage:        t.TempDir(),
		DNSProvider:    "cloudflare",
		DNSCredentials: map[string]string{"api_token": "tok"},
	}
	m := NewManager(cfg, nil, log)
	if m.acme == nil {
		t.Fatal("ACME client should be initialized")
	}
}

func TestNewManagerWithDNSProviderFailure(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{
		Email:       "ops@example.com",
		Storage:     t.TempDir(),
		DNSProvider: "cloudflare",
		// no api_token → NewACMEDNSProvider returns error, warn branch taken
		DNSCredentials: map[string]string{},
	}
	m := NewManager(cfg, nil, log)
	if m.acme == nil {
		t.Fatal("ACME client should still be initialized even if DNS provider fails")
	}
}

// --- normalizeTLSCanonicalHost / implicitTLSWWWHostname edge cases ---

func TestNormalizeTLSCanonicalHostWWW(t *testing.T) {
	if got := normalizeTLSCanonicalHost("  WWW  "); got != "www" {
		t.Errorf("normalizeTLSCanonicalHost(WWW) = %q, want www", got)
	}
	if got := normalizeTLSCanonicalHost("apex"); got != "apex" {
		t.Errorf("normalizeTLSCanonicalHost(apex) = %q, want apex", got)
	}
	if got := normalizeTLSCanonicalHost(""); got != "apex" {
		t.Errorf("normalizeTLSCanonicalHost('') = %q, want apex", got)
	}
}

func TestImplicitTLSWWWHostnameEdgeCases(t *testing.T) {
	// host with colon (port) → empty
	if got := implicitTLSWWWHostname("example.com:8443"); got != "" {
		t.Errorf("port host should yield empty, got %q", got)
	}
	// wildcard → empty
	if got := implicitTLSWWWHostname("*.example.com"); got != "" {
		t.Errorf("wildcard should yield empty, got %q", got)
	}
	// single-label host (no dot) → empty
	if got := implicitTLSWWWHostname("localhost"); got != "" {
		t.Errorf("single-label host should yield empty, got %q", got)
	}
	// normal apex → www. prefixed
	if got := implicitTLSWWWHostname("example.com"); got != "www.example.com" {
		t.Errorf("got %q, want www.example.com", got)
	}
}

// TestTLSHostsForDomainCanonicalWWW exercises the canonical-host=www reorder
// branch of tlsHostsForDomain.
func TestTLSHostsForDomainCanonicalWWW(t *testing.T) {
	d := config.Domain{Host: "example.com", CanonicalHost: "www"}
	hosts := tlsHostsForDomain(d)
	if len(hosts) < 2 {
		t.Fatalf("expected at least 2 hosts, got %v", hosts)
	}
	if hosts[0] != "www.example.com" {
		t.Errorf("canonical=www should put www first, got %v", hosts)
	}
}

// --- atomicWriteCertFile rename error branch ---

func TestAtomicWriteCertFileRenameError(t *testing.T) {
	// Target path is a directory: os.Rename(tmp, dir) fails, exercising the
	// rename-error branch (cleanup of the temp file).
	base := t.TempDir()
	target := filepath.Join(base, "iamadir")
	if err := os.MkdirAll(target, 0700); err != nil {
		t.Fatal(err)
	}
	err := atomicWriteCertFile(target, []byte("data"), 0644)
	if err == nil {
		t.Fatal("expected rename error when target is a directory")
	}
	// Temp file should have been cleaned up.
	entries, _ := os.ReadDir(base)
	for _, e := range entries {
		if e.Name() != "iamadir" {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}

func TestAtomicWriteCertFileCreateTempError(t *testing.T) {
	// Parent directory does not exist → os.CreateTemp fails (covers the
	// CreateTemp error branch).
	path := filepath.Join(t.TempDir(), "no-such-dir", "out.pem")
	if err := atomicWriteCertFile(path, []byte("x"), 0644); err == nil {
		t.Error("expected CreateTemp error for missing parent directory")
	}
}

func TestAtomicWriteCertFileSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.pem")
	if err := atomicWriteCertFile(path, []byte("hello"), 0600); err != nil {
		t.Fatalf("atomicWriteCertFile: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Errorf("content = %q, want hello", string(data))
	}
}

// --- stapleOCSP guard branches + full flow ---

func TestStapleOCSPNilCert(t *testing.T) {
	m := NewManager(config.ACMEConfig{Storage: t.TempDir()}, nil, logger.New("error", "text"))
	// Should not panic.
	m.stapleOCSP(nil, "x.example")
}

func TestStapleOCSPSingleCert(t *testing.T) {
	m := NewManager(config.ACMEConfig{Storage: t.TempDir()}, nil, logger.New("error", "text"))
	cert, _, _ := generateTestCert(t, "single.example") // single cert, len < 2
	m.stapleOCSP(cert, "single.example")
	if cert.OCSPStaple != nil {
		t.Error("single-cert chain must not be stapled")
	}
}

func TestStapleOCSPNoOCSPServer(t *testing.T) {
	m := NewManager(config.ACMEConfig{Storage: t.TempDir()}, nil, logger.New("error", "text"))
	// Build a 2-cert chain (leaf + issuer) but leaf has no OCSPServer set.
	cert := buildChain(t, "noocsp.example", "")
	m.stapleOCSP(cert, "noocsp.example")
	if cert.OCSPStaple != nil {
		t.Error("cert without OCSPServer must not be stapled")
	}
}

func TestStapleOCSPFullFlowGood(t *testing.T) {
	log := logger.New("error", "text")
	m := NewManager(config.ACMEConfig{Storage: t.TempDir()}, nil, log)

	// Build issuer + leaf with an OCSP responder pointing at our test server.
	issuerKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	issuerTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(100),
		Subject:               pkix.Name{CommonName: "Test Issuer"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	issuerDER, err := x509.CreateCertificate(rand.Reader, issuerTmpl, issuerTmpl, &issuerKey.PublicKey, issuerKey)
	if err != nil {
		t.Fatal(err)
	}
	issuer, err := x509.ParseCertificate(issuerDER)
	if err != nil {
		t.Fatal(err)
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	// OCSP responder server.
	ocspSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Drain the request body, then respond with a Good OCSP response.
		_, _ = io.Copy(io.Discard, r.Body)
		leaf := parseLeafFromManager(m)
		tmpl := ocsp.Response{
			Status:       ocsp.Good,
			SerialNumber: leaf.SerialNumber,
			ThisUpdate:   time.Now(),
			NextUpdate:   time.Now().Add(time.Hour),
		}
		respBytes, err := ocsp.CreateResponse(issuer, issuer, tmpl, issuerKey)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "application/ocsp-response")
		w.Write(respBytes)
	}))
	defer ocspSrv.Close()

	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(200),
		Subject:      pkix.Name{CommonName: "leaf.example"},
		DNSNames:     []string{"leaf.example"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		OCSPServer:   []string{ocspSrv.URL},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, issuer, &leafKey.PublicKey, issuerKey)
	if err != nil {
		t.Fatal(err)
	}

	cert := &tls.Certificate{
		Certificate: [][]byte{leafDER, issuerDER},
		PrivateKey:  leafKey,
	}
	// Stash the leaf so the responder can read its serial.
	stashLeaf, _ = x509.ParseCertificate(leafDER)

	m.stapleOCSP(cert, "leaf.example")
	if cert.OCSPStaple == nil {
		t.Error("expected OCSP staple to be attached for a Good response")
	}
}

func TestStapleOCSPServerError(t *testing.T) {
	log := logger.New("error", "text")
	m := NewManager(config.ACMEConfig{Storage: t.TempDir()}, nil, log)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	cert := buildChain(t, "err.example", srv.URL)
	m.stapleOCSP(cert, "err.example")
	if cert.OCSPStaple != nil {
		t.Error("non-200 OCSP response must not be stapled")
	}
}

func TestStapleOCSPConnectionError(t *testing.T) {
	log := logger.New("error", "text")
	m := NewManager(config.ACMEConfig{Storage: t.TempDir()}, nil, log)
	// Unreachable responder → http Do error branch.
	cert := buildChain(t, "down.example", "http://127.0.0.1:1/ocsp")
	m.stapleOCSP(cert, "down.example")
	if cert.OCSPStaple != nil {
		t.Error("unreachable OCSP responder must not staple")
	}
}

// TestObtainCertStaplesChain runs obtainCert with acmeObtainFunc returning a
// 2-cert chain whose leaf carries no OCSP responder, so stapleOCSP runs past
// the length guard and returns at the no-OCSP-server branch — exercising the
// obtainCert → stapleOCSP integration with a real chain.
func TestObtainCertStaplesChain(t *testing.T) {
	log := logger.New("error", "text")
	m := NewManager(config.ACMEConfig{Storage: t.TempDir()}, nil, log)

	chain := buildChain(t, "chain.example", "")
	certPEM, keyPEM := chainPEM(t, chain)
	m.acmeObtainFunc = func(ctx context.Context, domains []string) (*tls.Certificate, []byte, []byte, error) {
		return chain, certPEM, keyPEM, nil
	}

	got, err := m.obtainCert(context.Background(), "chain.example", false)
	if err != nil {
		t.Fatalf("obtainCert: %v", err)
	}
	if got == nil {
		t.Fatal("expected cert")
	}
	if _, ok := m.certs.Load("chain.example"); !ok {
		t.Error("cert not cached after obtainCert")
	}
}

// --- acme_dns extra branches ---

func TestNewACMEDNSProviderDigitalOceanMissingToken(t *testing.T) {
	log := logger.New("error", "text")
	if _, err := NewACMEDNSProvider("digitalocean", map[string]string{}, log); err == nil {
		t.Error("expected error for missing digitalocean api_token")
	}
}

func TestNewACMEDNSProviderHetznerMissingToken(t *testing.T) {
	log := logger.New("error", "text")
	if _, err := NewACMEDNSProvider("hetzner", map[string]string{}, log); err == nil {
		t.Error("expected error for missing hetzner api_token")
	}
}

// TestCleanupDNSChallengeNoMatch exercises the loop-falls-through (return nil)
// branch of CleanupDNSChallenge: the zone lists records, but none match.
func TestCleanupDNSChallengeNoMatch(t *testing.T) {
	log := logger.New("error", "text")
	mp := &mockDNSProvider{
		zones: []dnsmanager.Zone{{ID: "zone-1", Name: "example.com"}},
		records: mockZoneRecords{
			"zone-1": {
				{ID: "rec-1", Type: "TXT", Name: "_acme-challenge", Content: "different-value"},
			},
		},
	}
	p := &acmeDNSProvider{dp: mp, log: log, zoneID: "zone-1", zoneName: "example.com"}
	err := p.CleanupDNSChallenge("_acme-challenge.example.com", "token", "keyauth-no-match")
	if err != nil {
		t.Fatalf("CleanupDNSChallenge: %v", err)
	}
	// Record must remain untouched.
	if len(mp.records["zone-1"]) != 1 {
		t.Errorf("expected record to remain, got %d", len(mp.records["zone-1"]))
	}
}

// --- helpers ---

// chainPEM encodes a tls.Certificate chain and its EC key to PEM.
func chainPEM(t *testing.T, cert *tls.Certificate) (certPEM, keyPEM []byte) {
	t.Helper()
	var buf []byte
	for _, der := range cert.Certificate {
		buf = append(buf, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	key, ok := cert.PrivateKey.(*ecdsa.PrivateKey)
	if !ok {
		t.Fatal("expected ecdsa private key")
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return buf, keyPEM
}

// stashLeaf lets the OCSP test responder learn the leaf serial it must sign.
var stashLeaf *x509.Certificate

func parseLeafFromManager(_ *Manager) *x509.Certificate { return stashLeaf }

// generateCAPEM returns a self-signed CA certificate in PEM form.
func generateCAPEM(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// buildChain returns a 2-cert (leaf + issuer) tls.Certificate. ocspURL, when
// non-empty, is set as the leaf's OCSPServer.
func buildChain(t *testing.T, cn, ocspURL string) *tls.Certificate {
	t.Helper()
	issuerKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	issuerTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(10),
		Subject:               pkix.Name{CommonName: "Chain Issuer"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	issuerDER, err := x509.CreateCertificate(rand.Reader, issuerTmpl, issuerTmpl, &issuerKey.PublicKey, issuerKey)
	if err != nil {
		t.Fatal(err)
	}
	issuer, _ := x509.ParseCertificate(issuerDER)

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(20),
		Subject:      pkix.Name{CommonName: cn},
		DNSNames:     []string{cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ocspURL != "" {
		leafTmpl.OCSPServer = []string{ocspURL}
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, issuer, &leafKey.PublicKey, issuerKey)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Certificate{
		Certificate: [][]byte{leafDER, issuerDER},
		PrivateKey:  leafKey,
	}
}

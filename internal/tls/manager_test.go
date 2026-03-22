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
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

func TestManagerGetCertificateExact(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{Storage: t.TempDir()}
	m := NewManager(cfg, nil, log)

	cert, _, _ := generateTestCert(t, "example.com")
	m.certs.Store("example.com", cert)

	hello := &tls.ClientHelloInfo{ServerName: "example.com"}
	got, err := m.GetCertificate(hello)
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	if got != cert {
		t.Error("should return exact match cert")
	}
}

func TestManagerGetCertificateWildcard(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{Storage: t.TempDir()}
	m := NewManager(cfg, nil, log)

	cert, _, _ := generateTestCert(t, "*.example.com")
	m.certs.Store("*.example.com", cert)

	hello := &tls.ClientHelloInfo{ServerName: "sub.example.com"}
	got, err := m.GetCertificate(hello)
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	if got != cert {
		t.Error("should return wildcard cert")
	}
}

func TestManagerGetCertificateDefault(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{Storage: t.TempDir()}
	m := NewManager(cfg, nil, log)

	cert, _, _ := generateTestCert(t, "default")
	m.certs.Store("_default", cert)

	hello := &tls.ClientHelloInfo{ServerName: "unknown.com"}
	got, err := m.GetCertificate(hello)
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	if got != cert {
		t.Error("should return default cert")
	}
}

func TestManagerGetCertificateNone(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{Storage: t.TempDir()}
	m := NewManager(cfg, nil, log)

	hello := &tls.ClientHelloInfo{ServerName: "unknown.com"}
	_, err := m.GetCertificate(hello)
	if err == nil {
		t.Error("should return error when no cert available")
	}
}

func TestManagerLoadExistingCerts(t *testing.T) {
	dir := t.TempDir()
	storage := NewCertStorage(dir)

	cert, certPEM, keyPEM := generateTestCert(t, "loaded.com")
	storage.Save("loaded.com", cert, keyPEM, certPEM)

	log := logger.New("error", "text")
	cfg := config.ACMEConfig{Storage: dir}
	m := NewManager(cfg, nil, log)
	m.LoadExistingCerts()

	hello := &tls.ClientHelloInfo{ServerName: "loaded.com"}
	got, err := m.GetCertificate(hello)
	if err != nil {
		t.Fatalf("GetCertificate after load: %v", err)
	}
	if got == nil {
		t.Error("should find loaded cert")
	}
}

func TestTLSConfig(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{Storage: t.TempDir()}
	m := NewManager(cfg, nil, log)

	tlsCfg := m.TLSConfig()
	if tlsCfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion = %d, want TLS 1.2", tlsCfg.MinVersion)
	}
	if tlsCfg.GetCertificate == nil {
		t.Error("GetCertificate should be set")
	}
	if len(tlsCfg.NextProtos) != 2 {
		t.Error("NextProtos should have h2 and http/1.1")
	}
}

func TestLeafCertWithLeaf(t *testing.T) {
	cert, _, _ := generateTestCert(t, "leaf.com")
	// cert.Leaf is already set by generateTestCert
	leaf, err := leafCert(cert)
	if err != nil {
		t.Fatalf("leafCert: %v", err)
	}
	if leaf.Subject.CommonName != "leaf.com" {
		t.Errorf("CN = %q, want leaf.com", leaf.Subject.CommonName)
	}
}

func TestLeafCertWithoutLeaf(t *testing.T) {
	cert, _, _ := generateTestCert(t, "noleaf.com")
	// Clear the Leaf field so leafCert has to parse from DER
	cert.Leaf = nil
	leaf, err := leafCert(cert)
	if err != nil {
		t.Fatalf("leafCert: %v", err)
	}
	if leaf.Subject.CommonName != "noleaf.com" {
		t.Errorf("CN = %q, want noleaf.com", leaf.Subject.CommonName)
	}
}

func TestLeafCertNoCertData(t *testing.T) {
	cert := &tls.Certificate{}
	_, err := leafCert(cert)
	if err == nil {
		t.Error("expected error for empty cert")
	}
}

func TestLoadManualCerts(t *testing.T) {
	dir := t.TempDir()

	// Generate a test cert and write cert/key PEM files
	_, certPEM, keyPEM := generateTestCert(t, "manual.com")
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	os.WriteFile(certPath, certPEM, 0644)
	os.WriteFile(keyPath, keyPEM, 0600)

	log := logger.New("error", "text")
	cfg := config.ACMEConfig{Storage: t.TempDir()}
	domains := []config.Domain{
		{
			Host: "manual.com",
			SSL:  config.SSLConfig{Mode: "manual", Cert: certPath, Key: keyPath},
		},
	}
	m := NewManager(cfg, domains, log)
	m.LoadManualCerts()

	hello := &tls.ClientHelloInfo{ServerName: "manual.com"}
	got, err := m.GetCertificate(hello)
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	if got == nil {
		t.Error("should find manual cert")
	}
}

func TestLoadManualCertsSkipsNonManual(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{Storage: t.TempDir()}
	domains := []config.Domain{
		{
			Host: "auto.com",
			SSL:  config.SSLConfig{Mode: "auto"},
		},
	}
	m := NewManager(cfg, domains, log)
	m.LoadManualCerts()

	hello := &tls.ClientHelloInfo{ServerName: "auto.com"}
	_, err := m.GetCertificate(hello)
	if err == nil {
		t.Error("should not have loaded cert for non-manual domain")
	}
}

func TestLoadManualCertsInvalidPath(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{Storage: t.TempDir()}
	domains := []config.Domain{
		{
			Host: "bad.com",
			SSL:  config.SSLConfig{Mode: "manual", Cert: "/nonexistent/cert.pem", Key: "/nonexistent/key.pem"},
		},
	}
	m := NewManager(cfg, domains, log)
	// Should not panic, just log error
	m.LoadManualCerts()

	hello := &tls.ClientHelloInfo{ServerName: "bad.com"}
	_, err := m.GetCertificate(hello)
	if err == nil {
		t.Error("should not have loaded cert for invalid paths")
	}
}

func TestUpdateDomains(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{Storage: t.TempDir()}
	m := NewManager(cfg, nil, log)

	newDomains := []config.Domain{
		{Host: "new.com", Type: "static"},
	}
	m.UpdateDomains(newDomains)

	if len(m.domains) != 1 {
		t.Errorf("domains count = %d, want 1", len(m.domains))
	}
	if m.domains[0].Host != "new.com" {
		t.Errorf("domain host = %q, want new.com", m.domains[0].Host)
	}
}

func TestEncodeCertPEM(t *testing.T) {
	// Create some fake DER data
	_, certPEM, _ := generateTestCert(t, "encode.com")

	// Parse the PEM back to get the DER block
	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("failed to decode PEM")
	}

	// Encode a chain of one certificate
	result := EncodeCertPEM([][]byte{block.Bytes})
	if len(result) == 0 {
		t.Fatal("EncodeCertPEM returned empty")
	}

	// Parse back and verify
	decoded, _ := pem.Decode(result)
	if decoded == nil {
		t.Fatal("failed to decode result PEM")
	}
	if decoded.Type != "CERTIFICATE" {
		t.Errorf("type = %q, want CERTIFICATE", decoded.Type)
	}

	parsedCert, err := x509.ParseCertificate(decoded.Bytes)
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}
	if parsedCert.Subject.CommonName != "encode.com" {
		t.Errorf("CN = %q, want encode.com", parsedCert.Subject.CommonName)
	}
}

func TestEncodeCertPEMMultiple(t *testing.T) {
	_, certPEM1, _ := generateTestCert(t, "a.com")
	_, certPEM2, _ := generateTestCert(t, "b.com")

	block1, _ := pem.Decode(certPEM1)
	block2, _ := pem.Decode(certPEM2)

	result := EncodeCertPEM([][]byte{block1.Bytes, block2.Bytes})

	// Should contain two CERTIFICATE blocks
	rest := result
	count := 0
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		count++
	}
	if count != 2 {
		t.Errorf("PEM block count = %d, want 2", count)
	}
}

func TestHandleHTTPChallengeNoACME(t *testing.T) {
	log := logger.New("error", "text")
	// No email configured, so acme client will be nil
	cfg := config.ACMEConfig{Storage: t.TempDir()}
	m := NewManager(cfg, nil, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/.well-known/acme-challenge/test-token", nil)

	handled := m.HandleHTTPChallenge(rec, req)
	if handled {
		t.Error("HandleHTTPChallenge should return false when no ACME client")
	}
}

func TestNewManagerWithACME(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{
		Email:   "test@example.com",
		CAURL:   "https://acme.example.com/directory",
		Storage: t.TempDir(),
	}
	m := NewManager(cfg, nil, log)
	if m.acme == nil {
		t.Error("ACME client should be initialized when email is set")
	}
}

func TestHandleHTTPChallengeWithACMENonChallengePath(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{
		Email:   "test@example.com",
		CAURL:   "https://acme.example.com/directory",
		Storage: t.TempDir(),
	}
	m := NewManager(cfg, nil, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/some/page", nil)

	handled := m.HandleHTTPChallenge(rec, req)
	if handled {
		t.Error("should not handle non-challenge path")
	}
}

func TestHandleHTTPChallengeWithACMEUnknownToken(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{
		Email:   "test@example.com",
		CAURL:   "https://acme.example.com/directory",
		Storage: t.TempDir(),
	}
	m := NewManager(cfg, nil, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/.well-known/acme-challenge/unknown-token", nil)

	handled := m.HandleHTTPChallenge(rec, req)
	if !handled {
		t.Error("should handle ACME challenge path (even if token not found)")
	}
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestObtainCertsNoACME(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{Storage: t.TempDir()}
	domains := []config.Domain{
		{Host: "test.com", SSL: config.SSLConfig{Mode: "auto"}},
	}
	m := NewManager(cfg, domains, log)

	// acme is nil because no email is set; ObtainCerts should return immediately
	m.ObtainCerts(context.Background())

	// Verify no certs were stored (no panic, no error)
	hello := &tls.ClientHelloInfo{ServerName: "test.com"}
	_, err := m.GetCertificate(hello)
	if err == nil {
		t.Error("should not have a cert when acme is nil")
	}
}

func TestLoadExistingCertsEmptyDir(t *testing.T) {
	dir := t.TempDir()
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{Storage: dir}
	m := NewManager(cfg, nil, log)

	// Should not panic or error on an empty storage dir
	m.LoadExistingCerts()

	hello := &tls.ClientHelloInfo{ServerName: "anything.com"}
	_, err := m.GetCertificate(hello)
	if err == nil {
		t.Error("should have no certs loaded from empty dir")
	}
}

func TestLoadExistingCertsWithValidCerts(t *testing.T) {
	dir := t.TempDir()
	storage := NewCertStorage(dir)

	// Save two certs to disk
	cert1, certPEM1, keyPEM1 := generateTestCert(t, "alpha.com")
	storage.Save("alpha.com", cert1, keyPEM1, certPEM1)

	cert2, certPEM2, keyPEM2 := generateTestCert(t, "beta.com")
	storage.Save("beta.com", cert2, keyPEM2, certPEM2)

	log := logger.New("error", "text")
	cfg := config.ACMEConfig{Storage: dir}
	m := NewManager(cfg, nil, log)
	m.LoadExistingCerts()

	// Verify both certs are retrievable via GetCertificate
	for _, host := range []string{"alpha.com", "beta.com"} {
		hello := &tls.ClientHelloInfo{ServerName: host}
		got, err := m.GetCertificate(hello)
		if err != nil {
			t.Fatalf("GetCertificate(%q): %v", host, err)
		}
		if got == nil {
			t.Errorf("expected cert for %q, got nil", host)
		}
		if got.Leaf == nil {
			t.Errorf("expected parsed Leaf for %q", host)
		} else if got.Leaf.Subject.CommonName != host {
			t.Errorf("CN = %q, want %q", got.Leaf.Subject.CommonName, host)
		}
	}
}

func TestNewManagerWithoutEmail(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{
		CAURL:   "https://acme.example.com/directory",
		Storage: t.TempDir(),
		// Email is empty
	}
	m := NewManager(cfg, nil, log)
	if m.acme != nil {
		t.Error("ACME client should be nil when email is empty")
	}
}

// --- LoadExistingCerts with valid certs on disk ---

func TestLoadExistingCertsMultipleCerts(t *testing.T) {
	dir := t.TempDir()
	storage := NewCertStorage(dir)

	// Save three certs to disk
	hosts := []string{"one.com", "two.com", "three.com"}
	for _, h := range hosts {
		cert, certPEM, keyPEM := generateTestCert(t, h)
		if err := storage.Save(h, cert, keyPEM, certPEM); err != nil {
			t.Fatalf("Save %s: %v", h, err)
		}
	}

	log := logger.New("error", "text")
	cfg := config.ACMEConfig{Storage: dir}
	m := NewManager(cfg, nil, log)
	m.LoadExistingCerts()

	// Verify all three are loadable
	for _, h := range hosts {
		hello := &tls.ClientHelloInfo{ServerName: h}
		got, err := m.GetCertificate(hello)
		if err != nil {
			t.Errorf("GetCertificate(%q): %v", h, err)
			continue
		}
		if got == nil {
			t.Errorf("expected cert for %q, got nil", h)
			continue
		}
		if got.Leaf != nil && got.Leaf.Subject.CommonName != h {
			t.Errorf("CN = %q, want %q", got.Leaf.Subject.CommonName, h)
		}
	}
}

func TestLoadExistingCertsSkipsInvalidCert(t *testing.T) {
	dir := t.TempDir()

	// Create a valid cert
	storage := NewCertStorage(dir)
	cert, certPEM, keyPEM := generateTestCert(t, "valid.com")
	storage.Save("valid.com", cert, keyPEM, certPEM)

	// Create an invalid cert directory (corrupt files)
	badDir := filepath.Join(dir, "bad.com")
	os.MkdirAll(badDir, 0700)
	os.WriteFile(filepath.Join(badDir, "cert.pem"), []byte("not a cert"), 0644)
	os.WriteFile(filepath.Join(badDir, "key.pem"), []byte("not a key"), 0600)

	log := logger.New("error", "text")
	cfg := config.ACMEConfig{Storage: dir}
	m := NewManager(cfg, nil, log)
	m.LoadExistingCerts()

	// Valid cert should still be loaded
	hello := &tls.ClientHelloInfo{ServerName: "valid.com"}
	got, err := m.GetCertificate(hello)
	if err != nil {
		t.Fatalf("GetCertificate(valid.com): %v", err)
	}
	if got == nil {
		t.Error("valid.com cert should be loaded despite bad.com being invalid")
	}

	// Bad cert should not be loaded
	hello2 := &tls.ClientHelloInfo{ServerName: "bad.com"}
	_, err2 := m.GetCertificate(hello2)
	if err2 == nil {
		t.Error("bad.com should not have a valid cert")
	}
}

// --- onDemandAllow rate limiter ---

func TestOnDemandAllowWithinLimit(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{Storage: t.TempDir()}
	m := NewManager(cfg, nil, log)

	// First call in a new window should succeed
	for i := 0; i < onDemandMaxPerMinute; i++ {
		if !m.onDemandAllow() {
			t.Errorf("call %d should be allowed (within limit)", i+1)
		}
	}
}

func TestOnDemandAllowExceedsLimit(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{Storage: t.TempDir()}
	m := NewManager(cfg, nil, log)

	// Exhaust the limit
	for i := 0; i < onDemandMaxPerMinute; i++ {
		m.onDemandAllow()
	}

	// Next call should be rejected
	if m.onDemandAllow() {
		t.Error("should be rate limited after exceeding max per minute")
	}
}

func TestOnDemandAllowResetsAfterWindow(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{Storage: t.TempDir()}
	m := NewManager(cfg, nil, log)

	// Set the window start to 61 seconds ago so next call starts a new window
	m.onDemandReset.Store(time.Now().Unix() - 61)
	m.onDemandCount.Store(int64(onDemandMaxPerMinute))

	// Should succeed because the window has expired
	if !m.onDemandAllow() {
		t.Error("should allow after window reset")
	}

	// Counter should have been reset to 1
	if m.onDemandCount.Load() != 1 {
		t.Errorf("count = %d, want 1 after window reset", m.onDemandCount.Load())
	}
}

func TestOnDemandAllowConcurrent(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{Storage: t.TempDir()}
	m := NewManager(cfg, nil, log)

	// Run many concurrent calls
	var wg sync.WaitGroup
	allowed := atomic.Int64{}
	total := 50

	for i := 0; i < total; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if m.onDemandAllow() {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()

	// Should have allowed at most onDemandMaxPerMinute (10) + 1 (for window reset)
	if allowed.Load() > int64(onDemandMaxPerMinute)+1 {
		t.Errorf("allowed %d, should be <= %d", allowed.Load(), onDemandMaxPerMinute+1)
	}
}

// --- TLSConfig returns correct settings ---

func TestTLSConfigCipherSuites(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{Storage: t.TempDir()}
	m := NewManager(cfg, nil, log)

	tlsCfg := m.TLSConfig()

	if len(tlsCfg.CipherSuites) != 6 {
		t.Errorf("CipherSuites count = %d, want 6", len(tlsCfg.CipherSuites))
	}

	// Verify specific ciphers are present
	expectedCiphers := []uint16{
		tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
		tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
		tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
		tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
		tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
		tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
	}
	for i, expected := range expectedCiphers {
		if tlsCfg.CipherSuites[i] != expected {
			t.Errorf("CipherSuites[%d] = %d, want %d", i, tlsCfg.CipherSuites[i], expected)
		}
	}
}

func TestTLSConfigCurvePreferences(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{Storage: t.TempDir()}
	m := NewManager(cfg, nil, log)

	tlsCfg := m.TLSConfig()

	if len(tlsCfg.CurvePreferences) != 2 {
		t.Errorf("CurvePreferences count = %d, want 2", len(tlsCfg.CurvePreferences))
	}
	if tlsCfg.CurvePreferences[0] != tls.X25519 {
		t.Errorf("first curve = %v, want X25519", tlsCfg.CurvePreferences[0])
	}
	if tlsCfg.CurvePreferences[1] != tls.CurveP256 {
		t.Errorf("second curve = %v, want CurveP256", tlsCfg.CurvePreferences[1])
	}
}

func TestTLSConfigNextProtos(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{Storage: t.TempDir()}
	m := NewManager(cfg, nil, log)

	tlsCfg := m.TLSConfig()

	if len(tlsCfg.NextProtos) != 2 {
		t.Fatalf("NextProtos count = %d, want 2", len(tlsCfg.NextProtos))
	}
	if tlsCfg.NextProtos[0] != "h2" {
		t.Errorf("NextProtos[0] = %q, want h2", tlsCfg.NextProtos[0])
	}
	if tlsCfg.NextProtos[1] != "http/1.1" {
		t.Errorf("NextProtos[1] = %q, want http/1.1", tlsCfg.NextProtos[1])
	}
}

// --- GetCertificate case insensitivity ---

func TestGetCertificateCaseInsensitive(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{Storage: t.TempDir()}
	m := NewManager(cfg, nil, log)

	cert, _, _ := generateTestCert(t, "example.com")
	m.certs.Store("example.com", cert)

	// ServerName in upper case should still match
	hello := &tls.ClientHelloInfo{ServerName: "EXAMPLE.COM"}
	got, err := m.GetCertificate(hello)
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	if got != cert {
		t.Error("should match case-insensitively")
	}
}

// --- StartRenewal with nil ACME ---

func TestStartRenewalNoACME(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{Storage: t.TempDir()}
	m := NewManager(cfg, nil, log)

	// Should return immediately without panic when acme is nil
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	m.StartRenewal(ctx)
}

// --- ObtainCerts skips already loaded certs ---

func TestObtainCertsSkipsLoaded(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{
		Email:   "test@example.com",
		CAURL:   "https://acme-staging.example.com/directory",
		Storage: t.TempDir(),
	}
	domains := []config.Domain{
		{Host: "already.com", SSL: config.SSLConfig{Mode: "auto"}},
	}
	m := NewManager(cfg, domains, log)

	// Pre-load a cert
	cert, _, _ := generateTestCert(t, "already.com")
	m.certs.Store("already.com", cert)

	// ObtainCerts should skip already.com since it's already loaded
	// (we can't actually test ACME issuance here, but verify no panic/error)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	m.ObtainCerts(ctx)

	// Verify the original cert is still there (not replaced)
	hello := &tls.ClientHelloInfo{ServerName: "already.com"}
	got, err := m.GetCertificate(hello)
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	if got != cert {
		t.Error("original cert should not have been replaced")
	}
}

// --- ObtainCerts skips non-auto domains ---

func TestObtainCertsSkipsNonAuto(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{
		Email:   "test@example.com",
		CAURL:   "https://acme-staging.example.com/directory",
		Storage: t.TempDir(),
	}
	domains := []config.Domain{
		{Host: "manual.com", SSL: config.SSLConfig{Mode: "manual"}},
		{Host: "off.com", SSL: config.SSLConfig{Mode: "off"}},
	}
	m := NewManager(cfg, domains, log)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	m.ObtainCerts(ctx)

	// Neither domain should have a cert
	for _, host := range []string{"manual.com", "off.com"} {
		hello := &tls.ClientHelloInfo{ServerName: host}
		_, err := m.GetCertificate(hello)
		if err == nil {
			t.Errorf("should not have cert for %s (non-auto mode)", host)
		}
	}
}

// --- Save with cert that has no Leaf (fallback to DER parsing) ---

func TestSaveWithoutLeaf(t *testing.T) {
	dir := t.TempDir()
	storage := NewCertStorage(dir)

	cert, certPEM, keyPEM := generateTestCert(t, "noleaf-save.com")
	// Clear the Leaf so Save must parse from DER
	cert.Leaf = nil

	err := storage.Save("noleaf-save.com", cert, keyPEM, certPEM)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify meta was still written correctly
	meta, err := storage.LoadMeta("noleaf-save.com")
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if meta.Domain != "noleaf-save.com" {
		t.Errorf("Domain = %q, want noleaf-save.com", meta.Domain)
	}
}

// --- Save with empty cert chain ---

func TestSaveWithEmptyCertChain(t *testing.T) {
	dir := t.TempDir()
	storage := NewCertStorage(dir)

	cert := &tls.Certificate{
		// No Leaf, no Certificate chain
	}

	_, certPEM, keyPEM := generateTestCert(t, "empty-chain.com")
	err := storage.Save("empty-chain.com", cert, keyPEM, certPEM)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Meta should still be written, but with empty issuer/SANs
	meta, err := storage.LoadMeta("empty-chain.com")
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if meta.Domain != "empty-chain.com" {
		t.Errorf("Domain = %q, want empty-chain.com", meta.Domain)
	}
}

// --- LoadAll with non-dir entries ---

func TestLoadAllSkipsFiles(t *testing.T) {
	dir := t.TempDir()
	storage := NewCertStorage(dir)

	// Create a valid cert
	cert, certPEM, keyPEM := generateTestCert(t, "real.com")
	storage.Save("real.com", cert, keyPEM, certPEM)

	// Create a non-directory file in the storage dir
	os.WriteFile(filepath.Join(dir, "not-a-dir.txt"), []byte("junk"), 0644)

	all, err := storage.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	if len(all) != 1 {
		t.Errorf("LoadAll count = %d, want 1 (should skip non-dir files)", len(all))
	}
	if _, ok := all["real.com"]; !ok {
		t.Error("should have loaded real.com cert")
	}
}

// --- LoadAll with nonexistent storage dir ---

func TestLoadAllNonexistentDir(t *testing.T) {
	storage := NewCertStorage("/nonexistent/path/that/does/not/exist")

	all, err := storage.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll should return empty map, not error for nonexistent dir: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("should be empty for nonexistent dir, got %d", len(all))
	}
}

// --- LoadMeta error path ---

func TestLoadMetaNonexistent(t *testing.T) {
	dir := t.TempDir()
	storage := NewCertStorage(dir)

	_, err := storage.LoadMeta("nonexistent.com")
	if err == nil {
		t.Error("LoadMeta should return error for nonexistent domain")
	}
}

func TestLoadMetaInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	storage := NewCertStorage(dir)

	// Create domain dir with invalid meta.json
	domainDir := filepath.Join(dir, "badjson.com")
	os.MkdirAll(domainDir, 0700)
	os.WriteFile(filepath.Join(domainDir, "meta.json"), []byte("not json"), 0644)

	_, err := storage.LoadMeta("badjson.com")
	if err == nil {
		t.Error("LoadMeta should return error for invalid JSON")
	}
}

// --- Load error paths ---

func TestLoadMissingKey(t *testing.T) {
	dir := t.TempDir()
	storage := NewCertStorage(dir)

	// Create domain dir with cert but no key
	_, certPEM, _ := generateTestCert(t, "nokey.com")
	domainDir := filepath.Join(dir, "nokey.com")
	os.MkdirAll(domainDir, 0700)
	os.WriteFile(filepath.Join(domainDir, "cert.pem"), certPEM, 0644)

	_, err := storage.Load("nokey.com")
	if err == nil {
		t.Error("Load should fail when key.pem is missing")
	}
}

func TestLoadMissingCert(t *testing.T) {
	dir := t.TempDir()
	storage := NewCertStorage(dir)

	_, err := storage.Load("missing.com")
	if err == nil {
		t.Error("Load should fail when domain dir doesn't exist")
	}
}

func TestLoadInvalidKeyPair(t *testing.T) {
	dir := t.TempDir()
	storage := NewCertStorage(dir)

	domainDir := filepath.Join(dir, "badpair.com")
	os.MkdirAll(domainDir, 0700)
	os.WriteFile(filepath.Join(domainDir, "cert.pem"), []byte("invalid cert"), 0644)
	os.WriteFile(filepath.Join(domainDir, "key.pem"), []byte("invalid key"), 0600)

	_, err := storage.Load("badpair.com")
	if err == nil {
		t.Error("Load should fail for invalid key pair")
	}
}

// --- checkRenewals with near-expiry cert ---

func TestCheckRenewalsLogsExpiring(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{
		Email:   "test@example.com",
		CAURL:   "https://acme-staging.example.com/directory",
		Storage: t.TempDir(),
	}
	m := NewManager(cfg, nil, log)

	// Generate a cert that expires in 10 days (< 30 day threshold)
	cert, _, _ := generateTestCertWithExpiry(t, "expiring.com", 10*24*time.Hour)
	m.certs.Store("expiring.com", cert)

	// checkRenewals will try to renew via ACME, which will fail since
	// no real ACME server, but the important thing is it runs without panic
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	m.checkRenewals(ctx)

	// The cert should still be there (renewal failed but didn't remove it)
	hello := &tls.ClientHelloInfo{ServerName: "expiring.com"}
	got, err := m.GetCertificate(hello)
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	if got == nil {
		t.Error("cert should still be present after failed renewal")
	}
}

func TestCheckRenewalsSkipsDefault(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{
		Email:   "test@example.com",
		CAURL:   "https://acme.example.com/directory",
		Storage: t.TempDir(),
	}
	m := NewManager(cfg, nil, log)

	// Store a _default cert
	cert, _, _ := generateTestCert(t, "default")
	m.certs.Store("_default", cert)

	// checkRenewals should skip _default and not panic
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	m.checkRenewals(ctx)
}

func TestCheckRenewalsSkipsNonExpiring(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{
		Email:   "test@example.com",
		CAURL:   "https://acme.example.com/directory",
		Storage: t.TempDir(),
	}
	m := NewManager(cfg, nil, log)

	// Cert with 90 days left -- should NOT trigger renewal
	cert, _, _ := generateTestCert(t, "fresh.com")
	m.certs.Store("fresh.com", cert)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	m.checkRenewals(ctx)

	// Cert should be unchanged
	hello := &tls.ClientHelloInfo{ServerName: "fresh.com"}
	got, err := m.GetCertificate(hello)
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	if got != cert {
		t.Error("fresh cert should not be replaced")
	}
}

// --- obtainCert with nil ACME ---

func TestObtainCertNilACME(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{Storage: t.TempDir()}
	m := NewManager(cfg, nil, log)

	_, err := m.obtainCert(context.Background(), "test.com")
	if err == nil {
		t.Error("obtainCert should fail when ACME client is nil")
	}
}

// --- GetCertificate on-demand rate limit rejection ---

func TestGetCertificateOnDemandRateLimited(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{
		Email:    "test@example.com",
		CAURL:    "https://acme.example.com/directory",
		Storage:  t.TempDir(),
		OnDemand: true,
	}
	m := NewManager(cfg, nil, log)

	// Exhaust the rate limiter
	for i := 0; i < onDemandMaxPerMinute+1; i++ {
		m.onDemandAllow()
	}

	hello := &tls.ClientHelloInfo{ServerName: "ratelimited.com"}
	_, err := m.GetCertificate(hello)
	if err == nil {
		t.Error("should fail with rate limit exceeded")
	}
}

// --- GetCertificate on-demand with ask URL rejection ---

func TestGetCertificateOnDemandAskRejection(t *testing.T) {
	// Start a server that rejects all on-demand asks
	askServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer askServer.Close()

	log := logger.New("error", "text")
	cfg := config.ACMEConfig{
		Email:       "test@example.com",
		CAURL:       "https://acme.example.com/directory",
		Storage:     t.TempDir(),
		OnDemand:    true,
		OnDemandAsk: askServer.URL,
	}
	m := NewManager(cfg, nil, log)

	hello := &tls.ClientHelloInfo{ServerName: "rejected.com"}
	_, err := m.GetCertificate(hello)
	if err == nil {
		t.Error("should fail when on-demand ask returns non-200")
	}
}

// --- GetCertificate on-demand ask URL error (bad URL) ---

func TestGetCertificateOnDemandAskError(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{
		Email:       "test@example.com",
		CAURL:       "https://acme.example.com/directory",
		Storage:     t.TempDir(),
		OnDemand:    true,
		OnDemandAsk: "http://127.0.0.1:1", // port 1 should fail to connect
	}
	m := NewManager(cfg, nil, log)

	hello := &tls.ClientHelloInfo{ServerName: "error.com"}
	_, err := m.GetCertificate(hello)
	if err == nil {
		t.Error("should fail when on-demand ask URL is unreachable")
	}
}

// Helper to generate a test cert with a specific duration until expiry
func generateTestCertWithExpiry(t *testing.T, cn string, validFor time.Duration) (*tls.Certificate, []byte, []byte) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: cn},
		DNSNames:     []string{cn},
		NotBefore:    time.Now().Add(-1 * time.Hour),
		NotAfter:     time.Now().Add(validFor),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, _ := x509.MarshalECPrivateKey(key)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	cert.Leaf, _ = x509.ParseCertificate(cert.Certificate[0])

	return &cert, certPEM, keyPEM
}

package uwastls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// --- GetCertificate for unknown domain (fallback to _default) ---

func TestGetCertificateFallbackDefault(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{Storage: t.TempDir()}
	m := NewManager(cfg, nil, log)

	// Store a default cert.
	defaultCert, _, _ := generateTestCert(t, "default")
	m.certs.Store("_default", defaultCert)

	// Store a specific cert.
	specificCert, _, _ := generateTestCert(t, "specific.com")
	m.certs.Store("specific.com", specificCert)

	// Request for unknown domain should fall back to default.
	hello := &tls.ClientHelloInfo{ServerName: "totally-unknown.com"}
	got, err := m.GetCertificate(hello)
	if err != nil {
		t.Fatalf("GetCertificate fallback: %v", err)
	}
	if got != defaultCert {
		t.Error("should return default cert for unknown domain")
	}

	// Request for known domain should return specific cert.
	hello2 := &tls.ClientHelloInfo{ServerName: "specific.com"}
	got2, err := m.GetCertificate(hello2)
	if err != nil {
		t.Fatalf("GetCertificate specific: %v", err)
	}
	if got2 != specificCert {
		t.Error("should return specific cert for known domain")
	}
}

// --- AddManualCert with invalid cert/key data ---

func TestLoadManualCertsInvalidCertData(t *testing.T) {
	dir := t.TempDir()

	// Write invalid cert/key data.
	certPath := filepath.Join(dir, "bad-cert.pem")
	keyPath := filepath.Join(dir, "bad-key.pem")
	os.WriteFile(certPath, []byte("not a valid certificate"), 0644)
	os.WriteFile(keyPath, []byte("not a valid key"), 0600)

	log := logger.New("error", "text")
	cfg := config.ACMEConfig{Storage: t.TempDir()}
	domains := []config.Domain{
		{
			Host: "invalid.com",
			SSL:  config.SSLConfig{Mode: "manual", Cert: certPath, Key: keyPath},
		},
	}
	m := NewManager(cfg, domains, log)
	m.LoadManualCerts()

	// Should not have loaded the cert.
	hello := &tls.ClientHelloInfo{ServerName: "invalid.com"}
	_, err := m.GetCertificate(hello)
	if err == nil {
		t.Error("should not have loaded cert from invalid data")
	}
}

// --- AddManualCert with mismatched cert/key ---

func TestLoadManualCertsMismatchedCertKey(t *testing.T) {
	dir := t.TempDir()

	// Generate two different key pairs.
	_, certPEM, _ := generateTestCert(t, "mismatch.com")

	key2, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	keyDER2, _ := x509.MarshalECPrivateKey(key2)
	keyPEM2 := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER2})

	// Write cert from one key and private key from another.
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	os.WriteFile(certPath, certPEM, 0644)
	os.WriteFile(keyPath, keyPEM2, 0600)

	log := logger.New("error", "text")
	cfg := config.ACMEConfig{Storage: t.TempDir()}
	domains := []config.Domain{
		{
			Host: "mismatch.com",
			SSL:  config.SSLConfig{Mode: "manual", Cert: certPath, Key: keyPath},
		},
	}
	m := NewManager(cfg, domains, log)
	m.LoadManualCerts()

	// Should not have loaded the mismatched cert.
	hello := &tls.ClientHelloInfo{ServerName: "mismatch.com"}
	_, err := m.GetCertificate(hello)
	if err == nil {
		t.Error("should not have loaded cert with mismatched key")
	}
}

// --- ListCerts returns correct info ---

func TestListCertsInfo(t *testing.T) {
	dir := t.TempDir()
	storage := NewCertStorage(dir)

	// Save multiple certs.
	hosts := []string{"alpha.com", "beta.com", "gamma.com"}
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

	// Verify all 3 certs are loaded and have correct SNI selection.
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

	// Unknown domain should fail (no _default).
	hello := &tls.ClientHelloInfo{ServerName: "unknown.com"}
	_, err := m.GetCertificate(hello)
	if err == nil {
		t.Error("should not have cert for unknown domain")
	}
}

// --- SNI-based cert selection ---

func TestSNICertSelection(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{Storage: t.TempDir()}
	m := NewManager(cfg, nil, log)

	// Store multiple certs.
	cert1, _, _ := generateTestCert(t, "domain1.com")
	cert2, _, _ := generateTestCert(t, "domain2.com")
	cert3, _, _ := generateTestCert(t, "*.wildcard.com")
	defaultCert, _, _ := generateTestCert(t, "default")

	m.certs.Store("domain1.com", cert1)
	m.certs.Store("domain2.com", cert2)
	m.certs.Store("*.wildcard.com", cert3)
	m.certs.Store("_default", defaultCert)

	// Exact match for domain1.
	hello1 := &tls.ClientHelloInfo{ServerName: "domain1.com"}
	got1, err := m.GetCertificate(hello1)
	if err != nil || got1 != cert1 {
		t.Error("should return cert1 for domain1.com")
	}

	// Exact match for domain2.
	hello2 := &tls.ClientHelloInfo{ServerName: "domain2.com"}
	got2, err := m.GetCertificate(hello2)
	if err != nil || got2 != cert2 {
		t.Error("should return cert2 for domain2.com")
	}

	// Wildcard match.
	hello3 := &tls.ClientHelloInfo{ServerName: "sub.wildcard.com"}
	got3, err := m.GetCertificate(hello3)
	if err != nil || got3 != cert3 {
		t.Error("should return wildcard cert for sub.wildcard.com")
	}

	// Default fallback.
	hello4 := &tls.ClientHelloInfo{ServerName: "other.com"}
	got4, err := m.GetCertificate(hello4)
	if err != nil || got4 != defaultCert {
		t.Error("should return default cert for unknown domain")
	}
}

// --- GetCertificate with empty ServerName ---

func TestGetCertificateEmptyServerName(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{Storage: t.TempDir()}
	m := NewManager(cfg, nil, log)

	defaultCert, _, _ := generateTestCert(t, "default")
	m.certs.Store("_default", defaultCert)

	// Empty server name should fall back to default.
	hello := &tls.ClientHelloInfo{ServerName: ""}
	got, err := m.GetCertificate(hello)
	if err != nil {
		t.Fatalf("GetCertificate empty name: %v", err)
	}
	if got != defaultCert {
		t.Error("should return default cert for empty ServerName")
	}
}

// --- GetCertificate with no default and no match returns error ---

func TestGetCertificateNoDefaultNoMatch(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{Storage: t.TempDir()}
	m := NewManager(cfg, nil, log)

	// Store only one specific cert.
	cert, _, _ := generateTestCert(t, "only.com")
	m.certs.Store("only.com", cert)

	hello := &tls.ClientHelloInfo{ServerName: "notfound.com"}
	_, err := m.GetCertificate(hello)
	if err == nil {
		t.Error("should return error when no cert matches and no default")
	}
	if !strings.Contains(err.Error(), "no certificate") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- LoadManualCerts with multiple domains (manual + non-manual) ---

func TestLoadManualCertsMixed(t *testing.T) {
	dir := t.TempDir()

	// Generate a valid cert and write it.
	_, certPEM, keyPEM := generateTestCert(t, "manual1.com")
	certPath := filepath.Join(dir, "cert1.pem")
	keyPath := filepath.Join(dir, "key1.pem")
	os.WriteFile(certPath, certPEM, 0644)
	os.WriteFile(keyPath, keyPEM, 0600)

	log := logger.New("error", "text")
	cfg := config.ACMEConfig{Storage: t.TempDir()}
	domains := []config.Domain{
		{Host: "manual1.com", SSL: config.SSLConfig{Mode: "manual", Cert: certPath, Key: keyPath}},
		{Host: "auto1.com", SSL: config.SSLConfig{Mode: "auto"}},
		{Host: "off1.com", SSL: config.SSLConfig{Mode: "off"}},
	}
	m := NewManager(cfg, domains, log)
	m.LoadManualCerts()

	// manual1.com should be loaded.
	hello1 := &tls.ClientHelloInfo{ServerName: "manual1.com"}
	_, err := m.GetCertificate(hello1)
	if err != nil {
		t.Errorf("should have loaded manual1.com cert: %v", err)
	}

	// auto1.com and off1.com should NOT be loaded.
	for _, host := range []string{"auto1.com", "off1.com"} {
		hello := &tls.ClientHelloInfo{ServerName: host}
		_, err := m.GetCertificate(hello)
		if err == nil {
			t.Errorf("should not have loaded %s cert", host)
		}
	}
}

// --- Cert storage: Save and Load round trip ---

func TestCertStorageSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	storage := NewCertStorage(dir)

	cert, certPEM, keyPEM := generateTestCert(t, "roundtrip.com")
	if err := storage.Save("roundtrip.com", cert, keyPEM, certPEM); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := storage.Load("roundtrip.com")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded == nil {
		t.Fatal("loaded cert should not be nil")
	}
	if loaded.Leaf == nil {
		t.Fatal("Leaf should be parsed during Load")
	}
	if loaded.Leaf.Subject.CommonName != "roundtrip.com" {
		t.Errorf("CN = %q, want roundtrip.com", loaded.Leaf.Subject.CommonName)
	}
}

// --- Near-expiry cert for renewal check ---

func TestCertExpiryCheck(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.ACMEConfig{Storage: t.TempDir()}
	m := NewManager(cfg, nil, log)

	// Create a cert that expires in 20 days (within the 30-day renewal window).
	nearExpiryCert := generateCertWithExpiry(t, "expiring.com", 20*24*time.Hour)
	m.certs.Store("expiring.com", nearExpiryCert)

	// Create a cert that expires in 60 days (not within renewal window).
	healthyCert := generateCertWithExpiry(t, "healthy.com", 60*24*time.Hour)
	m.certs.Store("healthy.com", healthyCert)

	// Verify both certs are retrievable.
	for _, host := range []string{"expiring.com", "healthy.com"} {
		hello := &tls.ClientHelloInfo{ServerName: host}
		got, err := m.GetCertificate(hello)
		if err != nil {
			t.Errorf("GetCertificate(%q): %v", host, err)
		}
		if got == nil {
			t.Errorf("expected cert for %q", host)
		}
	}

	// Verify the leaf cert has correct expiry.
	leaf, err := leafCert(nearExpiryCert)
	if err != nil {
		t.Fatalf("leafCert: %v", err)
	}
	remaining := time.Until(leaf.NotAfter)
	if remaining > 30*24*time.Hour {
		t.Errorf("near-expiry cert should expire within 30 days, remaining: %v", remaining)
	}
}

// generateCertWithExpiry creates a self-signed cert that expires in the given duration.
func generateCertWithExpiry(t *testing.T, cn string, validFor time.Duration) *tls.Certificate {
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
	if len(cert.Certificate) > 0 {
		cert.Leaf, _ = x509.ParseCertificate(cert.Certificate[0])
	}
	return &cert
}

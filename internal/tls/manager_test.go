package uwastls

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

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

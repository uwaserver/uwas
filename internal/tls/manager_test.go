package uwastls

import (
	"crypto/tls"
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

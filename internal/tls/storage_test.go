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
	"testing"
	"time"
)

func TestCertStorageSaveLoad(t *testing.T) {
	dir := t.TempDir()
	storage := NewCertStorage(dir)

	cert, certPEM, keyPEM := generateTestCert(t, "example.com")

	// Save
	if err := storage.Save("example.com", cert, keyPEM, certPEM); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify files exist
	if _, err := os.Stat(filepath.Join(dir, "example.com", "cert.pem")); err != nil {
		t.Error("cert.pem not found")
	}
	if _, err := os.Stat(filepath.Join(dir, "example.com", "key.pem")); err != nil {
		t.Error("key.pem not found")
	}
	if _, err := os.Stat(filepath.Join(dir, "example.com", "meta.json")); err != nil {
		t.Error("meta.json not found")
	}

	// Load
	loaded, err := storage.Load("example.com")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Leaf == nil {
		t.Error("Leaf should be parsed")
	}
	if loaded.Leaf.Subject.CommonName != "example.com" {
		t.Errorf("CN = %q, want example.com", loaded.Leaf.Subject.CommonName)
	}
}

func TestCertStorageLoadAll(t *testing.T) {
	dir := t.TempDir()
	storage := NewCertStorage(dir)

	domains := []string{"a.com", "b.com", "c.com"}
	for _, d := range domains {
		cert, certPEM, keyPEM := generateTestCert(t, d)
		if err := storage.Save(d, cert, keyPEM, certPEM); err != nil {
			t.Fatalf("Save %s: %v", d, err)
		}
	}

	all, err := storage.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("LoadAll count = %d, want 3", len(all))
	}
}

func TestCertStorageExists(t *testing.T) {
	dir := t.TempDir()
	storage := NewCertStorage(dir)

	if storage.Exists("nonexistent.com") {
		t.Error("should not exist")
	}

	cert, certPEM, keyPEM := generateTestCert(t, "test.com")
	storage.Save("test.com", cert, keyPEM, certPEM)

	if !storage.Exists("test.com") {
		t.Error("should exist after save")
	}
}

func TestCertStorageDelete(t *testing.T) {
	dir := t.TempDir()
	storage := NewCertStorage(dir)

	cert, certPEM, keyPEM := generateTestCert(t, "delete.com")
	storage.Save("delete.com", cert, keyPEM, certPEM)

	if err := storage.Delete("delete.com"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if storage.Exists("delete.com") {
		t.Error("should not exist after delete")
	}
}

func TestCertStorageMeta(t *testing.T) {
	dir := t.TempDir()
	storage := NewCertStorage(dir)

	cert, certPEM, keyPEM := generateTestCert(t, "meta.com")
	storage.Save("meta.com", cert, keyPEM, certPEM)

	meta, err := storage.LoadMeta("meta.com")
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if meta.Domain != "meta.com" {
		t.Errorf("Domain = %q, want meta.com", meta.Domain)
	}
}

// generateTestCert creates a self-signed certificate for testing.
func generateTestCert(t *testing.T, cn string) (*tls.Certificate, []byte, []byte) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: cn},
		DNSNames:     []string{cn},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(90 * 24 * time.Hour),
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

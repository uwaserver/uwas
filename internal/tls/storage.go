package uwastls

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// CertStorage handles reading/writing certificates to disk.
type CertStorage struct {
	baseDir string
	// readDirFunc overrides os.ReadDir for testing error paths.
	readDirFunc func(name string) ([]os.DirEntry, error)
}

// CertMeta stores metadata about a certificate on disk.
type CertMeta struct {
	Domain  string    `json:"domain"`
	Issuer  string    `json:"issuer"`
	Expiry  time.Time `json:"expiry"`
	Created time.Time `json:"created"`
	SANs    []string  `json:"sans"`
}

func NewCertStorage(baseDir string) *CertStorage {
	return &CertStorage{baseDir: baseDir}
}

func (s *CertStorage) domainDir(domain string) string {
	return filepath.Join(s.baseDir, domain)
}

// Save persists a certificate and its key to disk.
func (s *CertStorage) Save(domain string, cert *tls.Certificate, keyPEM, certPEM []byte) error {
	dir := s.domainDir(domain)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	// Write cert
	certPath := filepath.Join(dir, "cert.pem")
	if err := os.WriteFile(certPath, certPEM, 0644); err != nil {
		return fmt.Errorf("write cert: %w", err)
	}

	// Write key (restricted permissions)
	keyPath := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		return fmt.Errorf("write key: %w", err)
	}

	// Write metadata
	meta := CertMeta{
		Domain:  domain,
		Created: time.Now().UTC(),
	}
	if cert.Leaf != nil {
		meta.Issuer = cert.Leaf.Issuer.CommonName
		meta.Expiry = cert.Leaf.NotAfter
		meta.SANs = cert.Leaf.DNSNames
	} else if len(cert.Certificate) > 0 {
		if leaf, err := x509.ParseCertificate(cert.Certificate[0]); err == nil {
			meta.Issuer = leaf.Issuer.CommonName
			meta.Expiry = leaf.NotAfter
			meta.SANs = leaf.DNSNames
		}
	}

	metaJSON, _ := json.MarshalIndent(meta, "", "  ")
	metaPath := filepath.Join(dir, "meta.json")
	if err := os.WriteFile(metaPath, metaJSON, 0644); err != nil {
		return fmt.Errorf("write meta: %w", err)
	}

	return nil
}

// Load reads a certificate and key from disk.
func (s *CertStorage) Load(domain string) (*tls.Certificate, error) {
	dir := s.domainDir(domain)

	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")

	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, fmt.Errorf("read cert: %w", err)
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("read key: %w", err)
	}

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("parse keypair: %w", err)
	}

	// Parse leaf for metadata access
	if len(cert.Certificate) > 0 {
		cert.Leaf, _ = x509.ParseCertificate(cert.Certificate[0])
	}

	return &cert, nil
}

// LoadAll loads all certificates from the storage directory.
func (s *CertStorage) LoadAll() (map[string]*tls.Certificate, error) {
	certs := make(map[string]*tls.Certificate)

	readDir := os.ReadDir
	if s.readDirFunc != nil {
		readDir = s.readDirFunc
	}
	entries, err := readDir(s.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return certs, nil
		}
		return nil, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		domain := entry.Name()
		cert, err := s.Load(domain)
		if err != nil {
			continue // skip invalid certs
		}
		certs[domain] = cert
	}

	return certs, nil
}

// Exists checks if a certificate exists on disk for the domain.
func (s *CertStorage) Exists(domain string) bool {
	certPath := filepath.Join(s.domainDir(domain), "cert.pem")
	_, err := os.Stat(certPath)
	return err == nil
}

// LoadMeta reads just the metadata for a domain certificate.
func (s *CertStorage) LoadMeta(domain string) (*CertMeta, error) {
	metaPath := filepath.Join(s.domainDir(domain), "meta.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, err
	}
	var meta CertMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

// Delete removes a certificate and all its files from disk.
func (s *CertStorage) Delete(domain string) error {
	return os.RemoveAll(s.domainDir(domain))
}

// EncodeCertPEM encodes a raw DER certificate chain as PEM.
func EncodeCertPEM(derChain [][]byte) []byte {
	var out []byte
	for _, der := range derChain {
		block := &pem.Block{Type: "CERTIFICATE", Bytes: der}
		out = append(out, pem.EncodeToMemory(block)...)
	}
	return out
}

package uwastls

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/uwaserver/uwas/internal/pathsafe"
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

func (s *CertStorage) domainDir(domain string) (string, error) {
	if domain == "" || domain == "." || domain == ".." || filepath.Base(domain) != domain {
		return "", fmt.Errorf("invalid certificate domain %q", domain)
	}
	dir := filepath.Join(s.baseDir, domain)
	if !pathsafe.IsWithinBaseResolved(s.baseDir, dir) {
		return "", fmt.Errorf("certificate domain %q escapes storage", domain)
	}
	return dir, nil
}

// Save persists a certificate and its key to disk.
func (s *CertStorage) Save(domain string, cert *tls.Certificate, keyPEM, certPEM []byte) error {
	dir, err := s.domainDir(domain)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	// Stage both the key and the cert (temp + fsync) before renaming either,
	// so a write failure (e.g. disk full) leaves the previous pair intact
	// instead of a new key next to the old cert, which Load rejects.
	keyPath := filepath.Join(dir, "key.pem")
	keyTmp, err := stageCertFile(keyPath, keyPEM, 0600)
	if err != nil {
		return fmt.Errorf("write key: %w", err)
	}
	defer os.Remove(keyTmp)

	certPath := filepath.Join(dir, "cert.pem")
	certTmp, err := stageCertFile(certPath, certPEM, 0644)
	if err != nil {
		return fmt.Errorf("write cert: %w", err)
	}
	defer os.Remove(certTmp)

	if err := os.Rename(keyTmp, keyPath); err != nil {
		return fmt.Errorf("write key: %w", err)
	}
	if err := os.Rename(certTmp, certPath); err != nil {
		return fmt.Errorf("write cert: %w", err)
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
	if err := atomicWriteCertFile(metaPath, metaJSON, 0644); err != nil {
		return fmt.Errorf("write meta: %w", err)
	}

	return nil
}

// atomicWriteCertFile writes data crash-safely: temp file in the same directory,
// fsync, then atomic rename over the target. Prevents readers (SNI lookup,
// renewal) from observing a partially-written cert/key file.
func atomicWriteCertFile(path string, data []byte, perm os.FileMode) error {
	tmpName, err := stageCertFile(path, data, perm)
	if err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// stageCertFile writes data to a fsynced temp file next to path and returns
// its name; the caller renames it into place (or removes it).
func stageCertFile(path string, data []byte, perm os.FileMode) (string, error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	fail := func(err error) (string, error) {
		tmp.Close()
		os.Remove(tmpName)
		return "", err
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Chmod(perm); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return "", err
	}
	return tmpName, nil
}

// Load reads a certificate and key from disk.
func (s *CertStorage) Load(domain string) (*tls.Certificate, error) {
	dir, err := s.domainDir(domain)
	if err != nil {
		return nil, err
	}

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
	dir, err := s.domainDir(domain)
	if err != nil {
		return false
	}
	certPath := filepath.Join(dir, "cert.pem")
	_, err = os.Stat(certPath)
	return err == nil
}

// LoadMeta reads just the metadata for a domain certificate.
func (s *CertStorage) LoadMeta(domain string) (*CertMeta, error) {
	dir, err := s.domainDir(domain)
	if err != nil {
		return nil, err
	}
	metaPath := filepath.Join(dir, "meta.json")
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
	dir, err := s.domainDir(domain)
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

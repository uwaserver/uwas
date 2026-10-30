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

// afterKeyRename, when set (tests only), runs between the key and cert renames
// of Save; a non-nil error aborts Save there, as a crash would.
var afterKeyRename func() error

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

	// Keep the pair being replaced under .prev names for the two renames
	// below; a crash between them leaves a new key beside the old cert, and
	// Load falls back to this pair instead of losing the certificate (F8500).
	prevKey, prevCert := keyPath+prevSuffix, certPath+prevSuffix
	_ = linkPrev(keyPath, prevKey) && linkPrev(certPath, prevCert)
	if err := os.Rename(keyTmp, keyPath); err != nil {
		return fmt.Errorf("write key: %w", err)
	}
	if afterKeyRename != nil {
		if err := afterKeyRename(); err != nil {
			return err
		}
	}
	if err := os.Rename(certTmp, certPath); err != nil {
		return fmt.Errorf("write cert: %w", err)
	}

	// Both names go once the pair is in place, also when only one of them was
	// linked (a key.pem with no cert.pem): the half left behind was an extra
	// copy of a private key nothing ever removed (F47303).
	os.Remove(prevKey)
	os.Remove(prevCert)

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

// prevSuffix names the previous key/cert pair kept while Save replaces them.
const prevSuffix = ".prev"

// linkPrev hard-links an existing file to its .prev name (replacing a stale
// one) and reports whether the previous copy is in place.
func linkPrev(path, prev string) bool {
	if _, err := os.Lstat(path); err != nil {
		return false
	}
	os.Remove(prev)
	return os.Link(path, prev) == nil
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
		// An interrupted Save can leave a new key beside the old cert; the
		// previous pair it kept is still a valid certificate (F8500).
		prevCertPEM, cerr := os.ReadFile(certPath + prevSuffix)
		prevKeyPEM, kerr := os.ReadFile(keyPath + prevSuffix)
		if cerr != nil || kerr != nil {
			return nil, fmt.Errorf("parse keypair: %w", err)
		}
		prev, perr := tls.X509KeyPair(prevCertPEM, prevKeyPEM)
		if perr != nil {
			return nil, fmt.Errorf("parse keypair: %w", err)
		}
		cert = prev
	}

	// Parse leaf for metadata access
	if len(cert.Certificate) > 0 {
		cert.Leaf, _ = x509.ParseCertificate(cert.Certificate[0])
	}

	return &cert, nil
}

// LoadAll loads all certificates from the storage directory.
func (s *CertStorage) LoadAll() (map[string]*tls.Certificate, error) {
	certs, _, err := s.LoadAllDetailed()
	return certs, err
}

// LoadAllDetailed is LoadAll that also reports the directories it could not
// load, with the reason. A directory holding no cert.pem at all (an earlier
// issuance that never got as far as writing one) is not a certificate and is
// not reported; one whose files are present but unreadable, truncated or
// mismatched is, so a lost certificate is not dropped without a word (F15401).
func (s *CertStorage) LoadAllDetailed() (certs map[string]*tls.Certificate, skipped map[string]error, err error) {
	certs = make(map[string]*tls.Certificate)
	skipped = make(map[string]error)

	readDir := os.ReadDir
	if s.readDirFunc != nil {
		readDir = s.readDirFunc
	}
	entries, err := readDir(s.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return certs, skipped, nil
		}
		return nil, nil, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		domain := entry.Name()
		cert, err := s.Load(domain)
		if err != nil {
			if s.Exists(domain) {
				skipped[domain] = err
			}
			continue
		}
		certs[domain] = cert
	}

	return certs, skipped, nil
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

// HasMeta reports whether the directory for domain holds the meta.json the ACME
// store writes with every pair it saves. An uploaded pair has none.
func (s *CertStorage) HasMeta(domain string) bool {
	dir, err := s.domainDir(domain)
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(dir, "meta.json"))
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

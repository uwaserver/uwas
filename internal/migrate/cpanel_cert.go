package migrate

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The places a pkgacct backup keeps TLS material. ssl/ holds <domain>.crt and
// <domain>.key; sslcerts/ and sslkeys/ hold files whose names carry a hash
// (alice_com_<hash>.crt); apache_tls/<domain> is one PEM with the key, the
// certificate and the CA bundle together. The file names differ between
// cPanel versions, so certificates are matched to a domain by the names inside
// them and keys by pairing, not by file name.
var cpanelCertDirs = []string{"sslcerts", "ssl", "apache_tls", "sslkeys"}

const (
	cpanelCertMaxFiles    = 500
	cpanelCertMaxFileSize = 256 << 10
)

type cpanelCert struct {
	cert  *x509.Certificate
	pem   []byte   // the certificate block
	chain [][]byte // CA certificates that sat in the same file
	file  string
}

// cpanelTLSMaterial is every certificate and private key found in a backup.
type cpanelTLSMaterial struct {
	certs []cpanelCert
	keys  [][]byte
	// named lists the base names (without extension) of certificate files, to
	// report a <domain>.crt that holds nothing usable for that domain.
	named map[string]bool
}

func loadCPanelTLSMaterial(cpRoot string) *cpanelTLSMaterial {
	m := &cpanelTLSMaterial{named: map[string]bool{}}
	files := 0
	for _, sub := range cpanelCertDirs {
		entries, err := os.ReadDir(filepath.Join(cpRoot, sub))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if files >= cpanelCertMaxFiles {
				return m
			}
			info, err := e.Info()
			if err != nil || !info.Mode().IsRegular() || info.Size() > cpanelCertMaxFileSize {
				continue
			}
			files++
			path := filepath.Join(cpRoot, sub, e.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			if sub == "ssl" || sub == "sslcerts" {
				if ext := filepath.Ext(e.Name()); ext == ".crt" || ext == ".pem" || ext == ".cert" {
					m.named[strings.ToLower(strings.TrimSuffix(e.Name(), ext))] = true
				}
			}
			var certs []cpanelCert
			var cas [][]byte
			rest := data
			for {
				var blk *pem.Block
				blk, rest = pem.Decode(rest)
				if blk == nil {
					break
				}
				switch {
				case blk.Type == "CERTIFICATE":
					c, err := x509.ParseCertificate(blk.Bytes)
					if err != nil {
						continue
					}
					enc := pem.EncodeToMemory(blk)
					if c.IsCA {
						cas = append(cas, enc)
						continue
					}
					certs = append(certs, cpanelCert{cert: c, pem: enc, file: path})
				case strings.HasSuffix(blk.Type, "PRIVATE KEY"):
					m.keys = append(m.keys, pem.EncodeToMemory(blk))
				}
			}
			for i := range certs {
				certs[i].chain = cas
			}
			m.certs = append(m.certs, certs...)
		}
	}
	return m
}

// find returns the certificate and key to serve domain with. It returns
// (nil, nil, nil) when the backup holds no certificate for the domain, and an
// error when it holds one that cannot be used (expired, no matching key).
func (m *cpanelTLSMaterial) find(domain string, now time.Time) (certPEM, keyPEM []byte, err error) {
	var matches []cpanelCert
	for _, c := range m.certs {
		if c.cert.VerifyHostname(domain) == nil {
			matches = append(matches, c)
		}
	}
	if len(matches) == 0 {
		if m.named[strings.ToLower(domain)] {
			return nil, nil, fmt.Errorf("certificate file for %s does not contain a valid certificate for that host", domain)
		}
		return nil, nil, nil
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].cert.NotAfter.After(matches[j].cert.NotAfter) })
	best := matches[0]
	if now.After(best.cert.NotAfter) {
		return nil, nil, fmt.Errorf("certificate for %s expired on %s; not imported", domain, best.cert.NotAfter.UTC().Format("2006-01-02"))
	}
	if now.Before(best.cert.NotBefore) {
		return nil, nil, fmt.Errorf("certificate for %s is not valid before %s; not imported", domain, best.cert.NotBefore.UTC().Format("2006-01-02"))
	}
	var chain []byte
	for _, ca := range best.chain {
		chain = append(chain, ca...)
	}
	full := append(append([]byte(nil), best.pem...), chain...)
	for _, k := range m.keys {
		if _, perr := tls.X509KeyPair(full, k); perr == nil {
			return full, k, nil
		}
	}
	return nil, nil, fmt.Errorf("no private key in the backup matches the certificate for %s; not imported", domain)
}

// writeCPanelCertPair stores the pair under dir with the key owner-only, and
// returns the two paths.
func writeCPanelCertPair(dir string, certPEM, keyPEM []byte) (certPath, keyPath string, err error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", err
	}
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, append(bytes.TrimSpace(certPEM), '\n'), 0o644); err != nil {
		return "", "", err
	}
	out, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return "", "", err
	}
	defer out.Close()
	if err := out.Chmod(0o600); err != nil {
		return "", "", err
	}
	if _, err := out.Write(append(bytes.TrimSpace(keyPEM), '\n')); err != nil {
		return "", "", err
	}
	return certPath, keyPath, nil
}

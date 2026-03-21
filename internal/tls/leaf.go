package uwastls

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
)

// leafCert returns the parsed leaf certificate from a tls.Certificate.
func leafCert(cert *tls.Certificate) (*x509.Certificate, error) {
	if cert.Leaf != nil {
		return cert.Leaf, nil
	}
	if len(cert.Certificate) == 0 {
		return nil, fmt.Errorf("no certificate data")
	}
	return x509.ParseCertificate(cert.Certificate[0])
}

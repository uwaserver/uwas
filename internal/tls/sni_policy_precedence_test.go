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

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

func writeTestClientCA(t *testing.T) string {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test client ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true,
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// A wildcard domain listed before an exact one must not strip the exact
// domain of its client-certificate requirement and min_version.
func TestConfigForHostExactBeatsEarlierWildcard(t *testing.T) {
	ca := writeTestClientCA(t)
	secure := config.Domain{Host: "secure.example.com",
		SSL: config.SSLConfig{Mode: "auto", ClientCA: ca, ClientAuth: "require", MinVersion: "1.3"}}
	wildcard := config.Domain{Host: "*.example.com", SSL: config.SSLConfig{Mode: "auto"}}

	for _, order := range [][]config.Domain{{secure, wildcard}, {wildcard, secure}} {
		m := NewManager(config.ACMEConfig{Storage: t.TempDir()}, order, logger.New("error", "text"))
		if err := m.LoadClientCAs(); err != nil {
			t.Fatal(err)
		}
		out, _ := m.TLSConfig().GetConfigForClient(&tls.ClientHelloInfo{ServerName: "secure.example.com"})
		if out == nil || out.ClientAuth != tls.RequireAndVerifyClientCert || out.MinVersion != tls.VersionTLS13 {
			t.Fatalf("order %s first: secure.example.com lost its policy: %+v", order[0].Host, out)
		}
		if other, _ := m.TLSConfig().GetConfigForClient(&tls.ClientHelloInfo{ServerName: "other.example.com"}); other != nil {
			t.Fatalf("order %s first: wildcard-only host got a policy", order[0].Host)
		}
	}
}

// Nested wildcards resolve to the longest suffix, as the router does.
func TestConfigForHostLongestWildcardWins(t *testing.T) {
	shallow := config.Domain{Host: "*.example.com", SSL: config.SSLConfig{Mode: "auto"}}
	deep := config.Domain{Host: "*.dev.example.com", SSL: config.SSLConfig{Mode: "auto", MinVersion: "1.3"}}
	for _, order := range [][]config.Domain{{shallow, deep}, {deep, shallow}} {
		m := NewManager(config.ACMEConfig{Storage: t.TempDir()}, order, logger.New("error", "text"))
		out, _ := m.TLSConfig().GetConfigForClient(&tls.ClientHelloInfo{ServerName: "api.dev.example.com"})
		if out == nil || out.MinVersion != tls.VersionTLS13 {
			t.Fatalf("order %s first: api.dev.example.com did not get *.dev.example.com policy", order[0].Host)
		}
	}
}

// A cached self-signed fallback past its NotAfter is regenerated, not
// served for the rest of the process lifetime.
func TestSelfSignedFallbackRegeneratedAfterExpiry(t *testing.T) {
	m := NewManager(config.ACMEConfig{Storage: t.TempDir()},
		[]config.Domain{{Host: "pending.example.com", SSL: config.SSLConfig{Mode: "auto"}}}, logger.New("error", "text"))
	m.AllowSelfSigned = true
	hello := &tls.ClientHelloInfo{ServerName: "pending.example.com"}

	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "pending.example.com"},
		DNSNames: []string{"pending.example.com"}, NotBefore: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
		NotAfter: time.Date(2000, 1, 2, 0, 0, 0, 0, time.UTC)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	m.selfSigned.Store("pending.example.com", &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key})
	m.selfSignedCount.Store(1)

	got, err := m.GetCertificate(hello)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := leafCert(got)
	if err != nil || !time.Now().Before(leaf.NotAfter) {
		t.Fatalf("served expired fallback: NotAfter=%v err=%v", leaf.NotAfter, err)
	}
	if again, _ := m.GetCertificate(hello); again != got {
		t.Fatal("regenerated fallback not cached")
	}
	if n := m.selfSignedCount.Load(); n != 1 {
		t.Fatalf("selfSignedCount = %d, want 1", n)
	}
}

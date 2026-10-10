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

func writeOwnerTaggedCert(t *testing.T, cn string, names ...string) (certPath, keyPath string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		DNSNames:     names,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(90 * 24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPath, keyPath = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}

func servedCN(t *testing.T, m *Manager, sni string) string {
	t.Helper()
	c, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: sni})
	if err != nil {
		return "error: " + err.Error()
	}
	leaf, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	return leaf.Subject.CommonName
}

// Names under a wildcard alias are routed to the domain, so the handshake
// must accept them and serve the domain's certificate.
func TestWildcardAliasAcceptedAtHandshake(t *testing.T) {
	c, k := writeOwnerTaggedCert(t, "WILD-ALIAS", "*.secure.test", "secure.test")
	d := []config.Domain{{Host: "secure.test", Aliases: []string{"*.secure.test"}, SSL: config.SSLConfig{Mode: "manual", Cert: c, Key: k}}}
	m := NewManager(config.ACMEConfig{Storage: t.TempDir()}, d, logger.New("error", "text"))
	m.LoadManualCerts()
	if got := servedCN(t, m, "api.secure.test"); got != "WILD-ALIAS" {
		t.Fatalf("api.secure.test served %q, want WILD-ALIAS", got)
	}
	if m.isDomainConfigured("evilsecure.test") {
		t.Fatal("lookalike evilsecure.test must stay refused")
	}
}

// Each name's certificate and renewal mode come from the domain the router
// serves for it, whatever the config order.
func TestCertOwnerFollowsRouterPrecedence(t *testing.T) {
	ac, ak := writeOwnerTaggedCert(t, "APEX", "example.com")
	wc, wk := writeOwnerTaggedCert(t, "WWW", "www.example.com")
	apex := config.Domain{Host: "example.com", SSL: config.SSLConfig{Mode: "manual", Cert: ac, Key: ak}}
	www := config.Domain{Host: "www.example.com", SSL: config.SSLConfig{Mode: "manual", Cert: wc, Key: wk}}
	for _, order := range [][]config.Domain{{apex, www}, {www, apex}} {
		m := NewManager(config.ACMEConfig{Storage: t.TempDir()}, order, logger.New("error", "text"))
		m.LoadManualCerts()
		if got := servedCN(t, m, "example.com"); got != "APEX" {
			t.Errorf("%s first: example.com served %q, want APEX", order[0].Host, got)
		}
		if got := servedCN(t, m, "www.example.com"); got != "WWW" {
			t.Errorf("%s first: www.example.com served %q, want WWW", order[0].Host, got)
		}
	}

	apexManual := config.Domain{Host: "example.com", SSL: config.SSLConfig{Mode: "manual"}}
	wwwAuto := config.Domain{Host: "www.example.com", SSL: config.SSLConfig{Mode: "auto"}}
	if got := renewalModeFor([]config.Domain{apexManual, wwwAuto}, "www.example.com"); got != "auto" {
		t.Errorf("www.example.com renewal mode = %q, want auto", got)
	}
	if got := renewalModeFor([]config.Domain{wwwAuto, apexManual}, "example.com"); got != "manual" {
		t.Errorf("example.com renewal mode = %q, want manual", got)
	}
}

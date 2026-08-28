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
)

// writeCAFile writes a self-signed CA to disk and returns its path.
func writeCAFile(t *testing.T, cn string) string {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("anahtar: %v", err)
	}
	tpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Unix(1700000000, 0),
		NotAfter:              time.Unix(2000000000, 0),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("sertifika: %v", err)
	}

	yol := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(yol, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		t.Fatalf("yaz: %v", err)
	}
	return yol
}

func TestLoadClientCAsAppliesPerDomain(t *testing.T) {
	ca := writeCAFile(t, "dgn-mtls-ca")

	m := testManager(t, []config.Domain{
		{Host: "mtls.test", SSL: config.SSLConfig{Mode: "auto", ClientCA: ca, ClientAuth: "require"}},
		{Host: "acik.test", SSL: config.SSLConfig{Mode: "auto"}},
	})
	if err := m.LoadClientCAs(); err != nil {
		t.Fatalf("LoadClientCAs: %v", err)
	}

	base := m.TLSConfig()
	// The listener itself must stay anonymous, otherwise one mTLS domain
	// would demand client certificates from every other domain's visitors.
	if base.ClientAuth != tls.NoClientCert {
		t.Errorf("taban ClientAuth = %v, want NoClientCert", base.ClientAuth)
	}
	if base.ClientCAs != nil {
		t.Error("a CA pool leaked into the base config")
	}

	got, err := base.GetConfigForClient(&tls.ClientHelloInfo{ServerName: "mtls.test"})
	if err != nil {
		t.Fatalf("GetConfigForClient: %v", err)
	}
	if got == nil {
		t.Fatal("nil for a domain with client_ca — mTLS never applies")
	}
	if got.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Errorf("ClientAuth = %v, want RequireAndVerifyClientCert", got.ClientAuth)
	}
	if got.ClientCAs == nil {
		t.Error("ClientCAs is empty — no client certificate can be verified")
	}

	// The domain next door must be untouched.
	digeri, err := base.GetConfigForClient(&tls.ClientHelloInfo{ServerName: "acik.test"})
	if err != nil {
		t.Fatalf("GetConfigForClient: %v", err)
	}
	if digeri != nil {
		t.Errorf("mTLS istemeyen domain etkilendi: ClientAuth=%v", digeri.ClientAuth)
	}
}

// Two domains with different CAs must not share a pool.
func TestLoadClientCAsKeepsPoolsSeparate(t *testing.T) {
	m := testManager(t, []config.Domain{
		{Host: "a.test", SSL: config.SSLConfig{Mode: "auto", ClientCA: writeCAFile(t, "ca-a"), ClientAuth: "require"}},
		{Host: "b.test", SSL: config.SSLConfig{Mode: "auto", ClientCA: writeCAFile(t, "ca-b"), ClientAuth: "request"}},
	})
	if err := m.LoadClientCAs(); err != nil {
		t.Fatalf("LoadClientCAs: %v", err)
	}

	a, b := m.clientCAFor("a.test"), m.clientCAFor("b.test")
	if a == nil || b == nil {
		t.Fatal("one of the pools did not load")
	}
	if a.Equal(b) {
		t.Error("two domains share one pool — one domain's CA would verify the other's clients")
	}

	base := m.TLSConfig()
	got, _ := base.GetConfigForClient(&tls.ClientHelloInfo{ServerName: "b.test"})
	if got == nil || got.ClientAuth != tls.VerifyClientCertIfGiven {
		t.Errorf("b.test ClientAuth is wrong: %v", got)
	}
}

// An unreadable CA must not take the other domains down with it.
func TestLoadClientCAsSurvivesBadFile(t *testing.T) {
	iyi := writeCAFile(t, "iyi-ca")
	bozuk := filepath.Join(t.TempDir(), "bozuk.pem")
	if err := os.WriteFile(bozuk, []byte("this is not a certificate"), 0o644); err != nil {
		t.Fatalf("yaz: %v", err)
	}

	m := testManager(t, []config.Domain{
		{Host: "bozuk.test", SSL: config.SSLConfig{Mode: "auto", ClientCA: bozuk, ClientAuth: "require"}},
		{Host: "iyi.test", SSL: config.SSLConfig{Mode: "auto", ClientCA: iyi, ClientAuth: "require"}},
	})
	if err := m.LoadClientCAs(); err == nil {
		t.Error("a broken CA returned no error — it was swallowed")
	}

	if m.clientCAFor("iyi.test") == nil {
		t.Error("the broken file stopped the other domain from loading")
	}
	// A domain whose CA failed to parse must not silently accept anonymous
	// clients under a config that claims to require certificates.
	got, _ := m.TLSConfig().GetConfigForClient(&tls.ClientHelloInfo{ServerName: "bozuk.test"})
	if got != nil && got.ClientAuth != tls.NoClientCert && got.ClientCAs == nil {
		t.Errorf("ClientAuth=%v set with no CA — the handshake would reject every client", got.ClientAuth)
	}
}

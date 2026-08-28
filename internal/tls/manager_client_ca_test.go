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

// caDosyasi writes a self-signed CA to disk and returns its path.
func caDosyasi(t *testing.T, cn string) string {
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
	ca := caDosyasi(t, "dgn-mtls-ca")

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
		t.Error("taban yapılandırmaya CA havuzu sızmış")
	}

	got, err := base.GetConfigForClient(&tls.ClientHelloInfo{ServerName: "mtls.test"})
	if err != nil {
		t.Fatalf("GetConfigForClient: %v", err)
	}
	if got == nil {
		t.Fatal("client_ca tanımlı domain için nil döndü — mTLS hiç uygulanmaz")
	}
	if got.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Errorf("ClientAuth = %v, want RequireAndVerifyClientCert", got.ClientAuth)
	}
	if got.ClientCAs == nil {
		t.Error("ClientCAs boş — istemci sertifikası doğrulanamaz")
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
		{Host: "a.test", SSL: config.SSLConfig{Mode: "auto", ClientCA: caDosyasi(t, "ca-a"), ClientAuth: "require"}},
		{Host: "b.test", SSL: config.SSLConfig{Mode: "auto", ClientCA: caDosyasi(t, "ca-b"), ClientAuth: "request"}},
	})
	if err := m.LoadClientCAs(); err != nil {
		t.Fatalf("LoadClientCAs: %v", err)
	}

	a, b := m.clientCAFor("a.test"), m.clientCAFor("b.test")
	if a == nil || b == nil {
		t.Fatal("havuzlardan biri yüklenmedi")
	}
	if a.Equal(b) {
		t.Error("iki domain aynı havuzu paylaşıyor — birinin CA'sı diğerinin istemcilerini doğrular")
	}

	base := m.TLSConfig()
	got, _ := base.GetConfigForClient(&tls.ClientHelloInfo{ServerName: "b.test"})
	if got == nil || got.ClientAuth != tls.VerifyClientCertIfGiven {
		t.Errorf("b.test ClientAuth yanlış: %v", got)
	}
}

// An unreadable CA must not take the other domains down with it.
func TestLoadClientCAsSurvivesBadFile(t *testing.T) {
	iyi := caDosyasi(t, "iyi-ca")
	bozuk := filepath.Join(t.TempDir(), "bozuk.pem")
	if err := os.WriteFile(bozuk, []byte("bu bir sertifika değil"), 0o644); err != nil {
		t.Fatalf("yaz: %v", err)
	}

	m := testManager(t, []config.Domain{
		{Host: "bozuk.test", SSL: config.SSLConfig{Mode: "auto", ClientCA: bozuk, ClientAuth: "require"}},
		{Host: "iyi.test", SSL: config.SSLConfig{Mode: "auto", ClientCA: iyi, ClientAuth: "require"}},
	})
	if err := m.LoadClientCAs(); err == nil {
		t.Error("bozuk CA hata döndürmedi — sessizce yutulmuş")
	}

	if m.clientCAFor("iyi.test") == nil {
		t.Error("bozuk dosya diğer domainin yüklenmesini engelledi")
	}
	// A domain whose CA failed to parse must not silently accept anonymous
	// clients under a config that claims to require certificates.
	got, _ := m.TLSConfig().GetConfigForClient(&tls.ClientHelloInfo{ServerName: "bozuk.test"})
	if got != nil && got.ClientAuth != tls.NoClientCert && got.ClientCAs == nil {
		t.Errorf("CA yokken ClientAuth=%v ayarlandı — el sıkışma her istemciyi reddeder", got.ClientAuth)
	}
}

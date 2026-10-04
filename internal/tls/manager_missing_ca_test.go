package uwastls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func missingCAHandshakeCert() *tls.Certificate {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"open.test", "protected.test"}, NotBefore: time.Unix(0, 0), NotAfter: time.Unix(4102444800, 0), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		panic(err)
	}
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}
func missingCAAnonymousHandshake(m *Manager, host string) error {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	cfg := m.TLSConfig()
	cfg.MaxVersion = tls.VersionTLS12
	server := tls.Server(a, cfg)
	client := tls.Client(b, &tls.Config{ServerName: host, InsecureSkipVerify: true, MinVersion: tls.VersionTLS12, MaxVersion: tls.VersionTLS12})
	done := make(chan error, 1)
	go func() { err := server.Handshake(); a.Close(); done <- err }()
	clientErr := client.Handshake()
	b.Close()
	serverErr := <-done
	if serverErr != nil {
		return serverErr
	}
	return clientErr
}
func TestMissingClientCAFailsClosed(t *testing.T) {
	for _, kind := range []string{"missing", "invalid"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			ca := filepath.Join(dir, "ca.pem")
			if kind == "invalid" {
				if err := os.WriteFile(ca, []byte("not PEM"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			m := NewManager(config.ACMEConfig{Storage: dir}, []config.Domain{{Host: "open.test"}, {Host: "protected.test", SSL: config.SSLConfig{ClientCA: ca, ClientAuth: "require"}}}, logger.New("error", "text"))
			c := missingCAHandshakeCert()
			m.RegisterCert("open.test", c)
			m.RegisterCert("protected.test", c)
			if err := m.LoadClientCAs(); err == nil {
				t.Fatal("bad CA must return error")
			}
			if err := missingCAAnonymousHandshake(m, "open.test"); err != nil {
				t.Fatalf("open control: %v", err)
			}
			if err := missingCAAnonymousHandshake(m, "protected.test"); err == nil {
				t.Fatal("required mTLS accepted anonymous client after CA load failure")
			}
		})
	}
}

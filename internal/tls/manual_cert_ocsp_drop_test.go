package uwastls

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// F27100: the OCSP refresh swaps a copy of a certificate into certs, but
// manualCerts kept the original pointer. LoadManualCerts only removes the
// entry it would have written (CompareAndDelete on that pointer), so once a
// manual certificate had been restapled its removal silently did nothing and a
// domain switched to ssl.mode off kept serving the old certificate (F16300
// regressed after the first renewal pass).
func TestManualCertIsDroppedAfterOCSPRefreshReplacedIt(t *testing.T) {
	dir := t.TempDir()
	f := newOCSPFixture(t, "shop.test", true)
	keyDER, err := x509.MarshalPKCS8PrivateKey(f.cert.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	var chain []byte
	for _, der := range f.cert.Certificate {
		chain = append(chain, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	certPath := filepath.Join(dir, "shop.crt")
	keyPath := filepath.Join(dir, "shop.key")
	if err := os.WriteFile(certPath, chain, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}

	m := testManager(t, []config.Domain{manualDomain("shop.test", certPath, keyPath)})
	m.LoadManualCerts()
	if served(m, "shop.test") == nil {
		t.Fatal("control: the manual certificate is not served after LoadManualCerts")
	}

	m.checkRenewals(context.Background()) // restaples: certs now holds a copy
	if f.hits.Load() == 0 {
		t.Fatal("control: the OCSP refresh pass did not run")
	}

	m.UpdateDomains([]config.Domain{{Host: "shop.test", SSL: config.SSLConfig{Mode: "off"}}})
	if got := served(m, "shop.test"); got != nil {
		t.Errorf("EXPECTED: no certificate for ssl.mode off; ACTUAL: old manual certificate still served")
	}
}

package uwastls

// F47302: an uploaded certificate pair is written under the same directory as
// the ACME store (cert.pem and key.pem, no meta.json). LoadExistingCerts loads
// every directory there, so after the domain was deleted, or switched to auto,
// or the host was given to another customer, a restart put the previous
// owner's pair back into service: it answered the new owner's handshakes until
// it expired, and the previous owner still holds its key. An uploaded pair
// belongs to the domain whose ssl.cert/ssl.key point at it, which
// LoadManualCerts loads; only pairs the ACME store wrote (it always writes
// meta.json) are reloaded here.

import (
	"crypto/tls"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

func writeUploadedPair(t *testing.T, dir, host string) {
	t.Helper()
	_, certPEM, keyPEM := generateTestCertWithExpiry(t, host, 365*24*time.Hour)
	d := filepath.Join(dir, host)
	if err := os.MkdirAll(d, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "cert.pem"), certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "key.pem"), keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
}

func servedFor(m *Manager, host string) bool {
	_, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: host})
	return err == nil
}

func TestLoadExistingCertsIgnoresUploadedPairOfOtherOwner(t *testing.T) {
	log := logger.New("error", "text")

	t.Run("host now auto-SSL: uploaded pair not reloaded", func(t *testing.T) {
		store := t.TempDir()
		writeUploadedPair(t, store, "shop.test")
		m := NewManager(config.ACMEConfig{Storage: store},
			[]config.Domain{{Host: "shop.test", SSL: config.SSLConfig{Mode: "auto"}}}, log)
		m.LoadExistingCerts()
		t.Logf("EXPECTED: no certificate for shop.test until ACME issues one\nACTUAL: served=%v", servedFor(m, "shop.test"))
		if servedFor(m, "shop.test") {
			t.Fatal("PROBLEM CONFIRMED: the previous owner's uploaded pair is served after a restart")
		}
	})
	t.Run("host no longer configured: uploaded pair not reloaded", func(t *testing.T) {
		store := t.TempDir()
		writeUploadedPair(t, store, "gone.test")
		m := NewManager(config.ACMEConfig{Storage: store},
			[]config.Domain{{Host: "other.test", SSL: config.SSLConfig{Mode: "auto"}}}, log)
		m.LoadExistingCerts()
		if _, ok := m.certs.Load("gone.test"); ok {
			t.Fatal("PROBLEM CONFIRMED: a removed domain's uploaded pair was loaded into memory")
		}
	})

	t.Run("control: manual domain still serves its uploaded pair", func(t *testing.T) {
		store := t.TempDir()
		writeUploadedPair(t, store, "own.test")
		d := filepath.Join(store, "own.test")
		m := NewManager(config.ACMEConfig{Storage: store},
			[]config.Domain{{Host: "own.test", SSL: config.SSLConfig{
				Mode: "manual", Cert: filepath.Join(d, "cert.pem"), Key: filepath.Join(d, "key.pem")}}}, log)
		m.LoadExistingCerts()
		m.LoadManualCerts()
		if !servedFor(m, "own.test") {
			t.Fatal("control failed: the manual domain's own pair is not served")
		}
	})
	t.Run("control: a pair the ACME store wrote is reloaded", func(t *testing.T) {
		store := t.TempDir()
		m := NewManager(config.ACMEConfig{Storage: store},
			[]config.Domain{{Host: "acme.test", SSL: config.SSLConfig{Mode: "auto"}}}, log)
		c, certPEM, keyPEM := generateTestCertWithExpiry(t, "acme.test", 60*24*time.Hour)
		if err := m.storage.Save("acme.test", c, keyPEM, certPEM); err != nil {
			t.Fatal(err)
		}
		m2 := NewManager(config.ACMEConfig{Storage: store},
			[]config.Domain{{Host: "acme.test", SSL: config.SSLConfig{Mode: "auto"}}}, log)
		m2.LoadExistingCerts()
		if !servedFor(m2, "acme.test") {
			t.Fatal("control failed: an ACME-issued pair was not reloaded")
		}
	})
	t.Run("control: no domains configured keeps loading everything", func(t *testing.T) {
		store := t.TempDir()
		writeUploadedPair(t, store, "legacy.test")
		m := NewManager(config.ACMEConfig{Storage: store}, nil, log)
		m.LoadExistingCerts()
		if _, ok := m.certs.Load("legacy.test"); !ok {
			t.Fatal("control failed: legacy load-all behaviour changed")
		}
	})
}

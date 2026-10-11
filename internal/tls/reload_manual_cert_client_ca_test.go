package uwastls

import (
	"os"
	"sync"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

func newManualManager(t *testing.T, d []config.Domain) *Manager {
	t.Helper()
	m := NewManager(config.ACMEConfig{Storage: t.TempDir()}, d, logger.New("error", "text"))
	m.LoadManualCerts()
	return m
}

// A reload (UpdateDomains) must pick up a manual certificate the operator
// replaced on disk; it used to be served until restart (F2770).
func TestUpdateDomainsReloadsReplacedManualCert(t *testing.T) {
	c1, k1 := writeOwnerTaggedCert(t, "V1", "manual.test")
	d := []config.Domain{{Host: "manual.test", SSL: config.SSLConfig{Mode: "manual", Cert: c1, Key: k1}}}
	m := newManualManager(t, d)
	if got := servedCN(t, m, "manual.test"); got != "V1" {
		t.Fatalf("before replacement served %q, want V1", got)
	}

	c2, k2 := writeOwnerTaggedCert(t, "V2", "manual.test")
	b, _ := os.ReadFile(c2)
	kb, _ := os.ReadFile(k2)
	if err := os.WriteFile(c1, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(k1, kb, 0o600); err != nil {
		t.Fatal(err)
	}
	m.UpdateDomains(d)
	if got := servedCN(t, m, "manual.test"); got != "V2" {
		t.Fatalf("after reload served %q, want V2", got)
	}

	// Files that no longer parse must not take the host offline: the
	// previous certificate keeps being served.
	if err := os.WriteFile(c1, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.UpdateDomains(d)
	m.UpdateDomains(d) // repeated reloads stay stable
	if got := servedCN(t, m, "manual.test"); got != "V2" {
		t.Fatalf("after a broken replacement served %q, want V2", got)
	}
}

// A manual-SSL domain added after startup gets its certificate on the same
// reload that adds it.
func TestUpdateDomainsLoadsNewManualDomain(t *testing.T) {
	ca, ak := writeOwnerTaggedCert(t, "A", "a.test")
	cb, bk := writeOwnerTaggedCert(t, "B", "b.test")
	da := []config.Domain{{Host: "a.test", SSL: config.SSLConfig{Mode: "manual", Cert: ca, Key: ak}}}
	m := newManualManager(t, da)
	m.UpdateDomains(append(da, config.Domain{Host: "b.test", SSL: config.SSLConfig{Mode: "manual", Cert: cb, Key: bk}}))
	if got := servedCN(t, m, "b.test"); got != "B" {
		t.Fatalf("b.test served %q, want B", got)
	}
	if got := servedCN(t, m, "a.test"); got != "A" {
		t.Fatalf("a.test served %q, want A", got)
	}
}

// ssl.client_ca of a domain added or changed by a reload takes effect, and a
// domain that lost it stops carrying a pool.
func TestUpdateDomainsReloadsClientCA(t *testing.T) {
	caPath := writeCAFile(t, "late-ca")
	plain := config.Domain{Host: "plain.test", SSL: config.SSLConfig{Mode: "auto"}}
	m := testManager(t, []config.Domain{plain})
	_ = m.LoadClientCAs()

	withCA := []config.Domain{plain, {Host: "mtls.test", SSL: config.SSLConfig{Mode: "auto", ClientCA: caPath, ClientAuth: "require"}}}
	m.UpdateDomains(withCA)
	if m.clientCAFor("mtls.test") == nil {
		t.Fatal("client CA of a domain added by reload was not loaded")
	}
	if m.clientCAFor("plain.test") != nil {
		t.Fatal("a domain without client_ca must not carry a pool")
	}

	m.UpdateDomains([]config.Domain{plain, {Host: "mtls.test", SSL: config.SSLConfig{Mode: "auto"}}})
	if m.clientCAFor("mtls.test") != nil {
		t.Fatal("pool survived removal of ssl.client_ca")
	}
}

// Reloads racing with handshakes stay consistent under -race.
func TestUpdateDomainsRacesHandshakes(t *testing.T) {
	c, k := writeOwnerTaggedCert(t, "RACE", "race.test")
	d := []config.Domain{{Host: "race.test", SSL: config.SSLConfig{Mode: "manual", Cert: c, Key: k}}}
	m := newManualManager(t, d)

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 20; j++ {
				m.UpdateDomains(d)
			}
		}()
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 50; j++ {
				if got := servedCN(t, m, "race.test"); got != "RACE" {
					t.Errorf("served %q during reload, want RACE", got)
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
}

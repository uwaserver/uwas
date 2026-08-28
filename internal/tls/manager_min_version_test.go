package uwastls

import (
	"crypto/tls"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// ssl.min_version was dead configuration: defaulted, validated and merged, but
// never read. TLSConfig hardcoded VersionTLS12, so a domain asking for 1.3 was
// served 1.2 and believed otherwise.

func TestMinTLSVersionFor(t *testing.T) {
	log := logger.New("error", "text")

	cases := []struct {
		istenen  string
		beklenen uint16
		ad       string
	}{
		{"1.3", tls.VersionTLS13, "1.3 yükseltiyor"},
		{"1.2", tls.VersionTLS12, "1.2 aynı"},
		{"", tls.VersionTLS12, "boş varsayılan"},
		// Below 1.2 is clamped, not honoured: validation has always accepted
		// these and the field was ignored, so honouring them literally would
		// downgrade existing deployments.
		{"1.1", tls.VersionTLS12, "1.1 sıkıştırılıyor"},
		{"1.0", tls.VersionTLS12, "1.0 sıkıştırılıyor"},
		{"çöp", tls.VersionTLS12, "tanınmayan değer güvenli tabana düşer"},
	}

	for _, c := range cases {
		t.Run(c.ad, func(t *testing.T) {
			if got := minTLSVersionFor(c.istenen, log, "x.test"); got != c.beklenen {
				t.Errorf("minTLSVersionFor(%q) = 0x%04x, want 0x%04x", c.istenen, got, c.beklenen)
			}
		})
	}
}

func testManager(t *testing.T, domains []config.Domain) *Manager {
	t.Helper()
	m := NewManager(config.ACMEConfig{Storage: t.TempDir()}, domains, logger.New("error", "text"))
	return m
}

// The ClientHello path must hand back a config carrying the domain's floor.
func TestGetConfigForClientRaisesMinVersion(t *testing.T) {
	m := testManager(t, []config.Domain{
		{Host: "tls13.test", SSL: config.SSLConfig{Mode: "auto", MinVersion: "1.3"}},
		{Host: "plain.test", SSL: config.SSLConfig{Mode: "auto"}},
	})

	base := m.TLSConfig()
	if base.MinVersion != tls.VersionTLS12 {
		t.Fatalf("taban MinVersion 0x%04x, 1.2 bekleniyordu", base.MinVersion)
	}
	if base.GetConfigForClient == nil {
		t.Fatal("GetConfigForClient ayarlanmamış — per-domain sürüm hiç uygulanmaz")
	}

	got, err := base.GetConfigForClient(&tls.ClientHelloInfo{ServerName: "tls13.test"})
	if err != nil {
		t.Fatalf("GetConfigForClient: %v", err)
	}
	if got == nil {
		t.Fatal("1.3 isteyen domain için nil döndü — taban 1.2 kalırdı")
	}
	if got.MinVersion != tls.VersionTLS13 {
		t.Errorf("MinVersion = 0x%04x, want TLS 1.3 (0x%04x)", got.MinVersion, tls.VersionTLS13)
	}
}

// A domain that configures nothing must not allocate a clone; the base config
// already carries the right floor.
func TestGetConfigForClientNilWhenNothingSet(t *testing.T) {
	m := testManager(t, []config.Domain{
		{Host: "plain.test", SSL: config.SSLConfig{Mode: "auto"}},
	})

	got, err := m.TLSConfig().GetConfigForClient(&tls.ClientHelloInfo{ServerName: "plain.test"})
	if err != nil {
		t.Fatalf("GetConfigForClient: %v", err)
	}
	if got != nil {
		t.Errorf("ayarsız domain için klon üretildi (MinVersion 0x%04x)", got.MinVersion)
	}
}

func TestGetConfigForClientUnknownHost(t *testing.T) {
	m := testManager(t, []config.Domain{
		{Host: "known.test", SSL: config.SSLConfig{Mode: "auto", MinVersion: "1.3"}},
	})

	got, err := m.TLSConfig().GetConfigForClient(&tls.ClientHelloInfo{ServerName: "başka.test"})
	if err != nil {
		t.Fatalf("GetConfigForClient: %v", err)
	}
	if got != nil {
		t.Error("tanınmayan host için taban dışı yapılandırma döndü")
	}
}

// SNI arrives as the www form for a bare apex domain; the override must still
// apply, matching how the certificate allowlist treats the same names.
func TestGetConfigForClientMatchesWWWAndAliases(t *testing.T) {
	m := testManager(t, []config.Domain{{
		Host:    "example.test",
		Aliases: []string{"alias.test"},
		SSL:     config.SSLConfig{Mode: "auto", MinVersion: "1.3"},
	}})
	base := m.TLSConfig()

	for _, host := range []string{"example.test", "www.example.test", "alias.test", "www.alias.test"} {
		got, err := base.GetConfigForClient(&tls.ClientHelloInfo{ServerName: host})
		if err != nil {
			t.Fatalf("%s: %v", host, err)
		}
		if got == nil || got.MinVersion != tls.VersionTLS13 {
			t.Errorf("%s: per-domain sürüm uygulanmadı (got %v)", host, got)
		}
	}
}

func TestClientAuthModeFor(t *testing.T) {
	cases := map[string]tls.ClientAuthType{
		"require": tls.RequireAndVerifyClientCert,
		"request": tls.VerifyClientCertIfGiven,
		"none":    tls.NoClientCert,
		"":        tls.NoClientCert,
	}
	for in, want := range cases {
		if got := clientAuthModeFor(in); got != want {
			t.Errorf("clientAuthModeFor(%q) = %v, want %v", in, got, want)
		}
	}
}

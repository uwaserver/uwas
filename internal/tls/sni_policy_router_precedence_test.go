package uwastls

import (
	"crypto/tls"
	"net"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

func sniPolicyManager(t *testing.T, domains ...config.Domain) *Manager {
	t.Helper()
	m := NewManager(config.ACMEConfig{Storage: t.TempDir()}, domains, logger.New("error", "text"))
	if err := m.LoadClientCAs(); err != nil {
		t.Fatal(err)
	}
	return m
}

func sniPolicyClientAuth(m *Manager, sni string) tls.ClientAuthType {
	out, _ := m.TLSConfig().GetConfigForClient(&tls.ClientHelloInfo{ServerName: sni})
	if out == nil {
		return tls.NoClientCert
	}
	return out.ClientAuth
}

// The SNI policy must belong to the domain the router serves for that name:
// an explicit www host beats another domain's implicit www form in either
// config order, and wildcard aliases count.
func TestSNIPolicyFollowsRouterPrecedence(t *testing.T) {
	ca := writeCAFile(t, "sni policy ca")
	mtls := config.SSLConfig{Mode: "auto", ClientCA: ca, ClientAuth: "require"}
	apexPublic := config.Domain{Host: "example.com", SSL: config.SSLConfig{Mode: "auto"}}
	wwwSecure := config.Domain{Host: "www.example.com", SSL: mtls}
	wcAlias := config.Domain{Host: "secure.test", Aliases: []string{"*.secure.test"}, SSL: mtls}

	cases := []struct {
		name    string
		domains []config.Domain
		sni     string
		want    tls.ClientAuthType
	}{
		{"explicit www owner after public apex", []config.Domain{apexPublic, wwwSecure}, "www.example.com", tls.RequireAndVerifyClientCert},
		{"explicit www owner before public apex", []config.Domain{wwwSecure, apexPublic}, "www.example.com", tls.RequireAndVerifyClientCert},
		{"public apex not taken by mTLS www listed first", []config.Domain{wwwSecure, apexPublic}, "example.com", tls.NoClientCert},
		{"wildcard alias", []config.Domain{wcAlias}, "api.secure.test", tls.RequireAndVerifyClientCert},
	}
	for _, c := range cases {
		if got := sniPolicyClientAuth(sniPolicyManager(t, c.domains...), c.sni); got != c.want {
			t.Errorf("%s: sni=%s client auth = %v, want %v", c.name, c.sni, got, c.want)
		}
	}
}

func TestSNIPolicyPublicApexNotLockedOut(t *testing.T) {
	ca := writeCAFile(t, "sni policy ca")
	m := sniPolicyManager(t,
		config.Domain{Host: "www.example.com", SSL: config.SSLConfig{Mode: "auto", ClientCA: ca, ClientAuth: "require"}},
		config.Domain{Host: "example.com", SSL: config.SSLConfig{Mode: "auto"}})
	c, err := m.generateSelfSigned("example.com")
	if err != nil {
		t.Fatal(err)
	}
	m.RegisterCert("example.com", c)

	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	cfg := m.TLSConfig()
	cfg.MaxVersion = tls.VersionTLS12
	srv := tls.Server(a, cfg)
	cli := tls.Client(b, &tls.Config{ServerName: "example.com", InsecureSkipVerify: true, MinVersion: tls.VersionTLS12, MaxVersion: tls.VersionTLS12})
	done := make(chan error, 1)
	go func() { err := srv.Handshake(); a.Close(); done <- err }()
	cerr := cli.Handshake()
	b.Close()
	if serr := <-done; serr != nil || cerr != nil {
		t.Fatalf("anonymous handshake to the public apex refused: server=%v client=%v", serr, cerr)
	}
}

package uwastls

import (
	"context"
	"crypto/tls"
	"fmt"
	"sync"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// A self-signed fallback served during a handshake must not look like an
// issued certificate: HasCert would start the HTTPS redirect (with HSTS) for a
// domain whose ACME cert is still pending, ObtainCerts would skip it, and
// client-chosen SNI names under a wildcard domain would pile up in the cert
// map and be sent to ACME by the renewal loop.
func TestSelfSignedFallbackDoesNotCountAsIssuedCert(t *testing.T) {
	domains := []config.Domain{
		{Host: "pending.example", SSL: config.SSLConfig{Mode: "auto"}},
		{Host: "*.wild.example", SSL: config.SSLConfig{Mode: "auto"}},
	}
	m := NewManager(config.ACMEConfig{Storage: t.TempDir(), Email: "ops@pending.example"},
		domains, logger.New("error", "text"))
	m.AllowSelfSigned = true
	m.retryBackoff = 1

	var mu sync.Mutex
	var asked []string
	m.acmeObtainFunc = func(ctx context.Context, names []string) (*tls.Certificate, []byte, []byte, error) {
		mu.Lock()
		asked = append(asked, names...)
		mu.Unlock()
		return nil, nil, nil, fmt.Errorf("fake ACME: refuse")
	}

	if _, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: "pending.example"}); err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	if m.HasCert("pending.example") {
		t.Error("HasCert reports the self-signed fallback as an issued certificate")
	}

	m.ObtainCerts(context.Background())
	mu.Lock()
	tried := false
	for _, n := range asked {
		if n == "pending.example" {
			tried = true
		}
	}
	asked = nil
	mu.Unlock()
	if !tried {
		t.Error("ObtainCerts skipped pending.example after a self-signed fallback")
	}

	before := 0
	m.certs.Range(func(_, _ any) bool { before++; return true })
	for i := 0; i < 20; i++ {
		_, _ = m.GetCertificate(&tls.ClientHelloInfo{ServerName: fmt.Sprintf("n%d.x.wild.example", i)})
	}
	after := 0
	m.certs.Range(func(_, _ any) bool { after++; return true })
	if after != before {
		t.Errorf("fallback certs added %d entries to the issued-cert map", after-before)
	}

	m.checkRenewals(context.Background())
	mu.Lock()
	defer mu.Unlock()
	if len(asked) != 0 {
		t.Errorf("renewal sent fallback names to ACME: %v", asked)
	}
}

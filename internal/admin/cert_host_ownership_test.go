package admin

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/uwaserver/uwas/internal/auth"
	"github.com/uwaserver/uwas/internal/config"
	uwastls "github.com/uwaserver/uwas/internal/tls"
)

// A certificate upload names its target in the URL. A host that no domain
// serves used to get a 200 "uploaded", a stray directory under the certificate
// store, and no configuration change at all.
func TestCertUploadUnknownHostRejected(t *testing.T) {
	e := newHostConflictEnv(t, []config.Domain{{Host: "site.test", Type: "static"}})
	dir := t.TempDir()
	certUploadDir = dir
	t.Cleanup(func() { certUploadDir = "/var/lib/uwas/certs" })

	cert, key := selfSignedPair(t, "other.test")
	b, _ := json.Marshal(map[string]string{"cert": cert, "key": key})
	code := e.do(http.MethodPost, "/api/v1/certs/other.test/upload", string(b), auth.RoleAdmin)
	if code != http.StatusNotFound {
		t.Fatalf("EXPECTED: 404 for a host no domain serves\nACTUAL: %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "other.test")); err == nil {
		t.Fatalf("EXPECTED: no certificate directory for an unknown host\nACTUAL: %s exists", filepath.Join(dir, "other.test"))
	}
}

// An alias is served by its domain, so a certificate uploaded for the alias
// switches that domain to manual TLS instead of reporting success and doing
// nothing.
func TestCertUploadAliasAppliesToOwningDomain(t *testing.T) {
	e := newHostConflictEnv(t, []config.Domain{{Host: "site.test", Aliases: []string{"alias.test"}, Type: "static"}})
	dir := t.TempDir()
	certUploadDir = dir
	t.Cleanup(func() { certUploadDir = "/var/lib/uwas/certs" })

	cert, key := selfSignedPair(t, "alias.test")
	b, _ := json.Marshal(map[string]string{"cert": cert, "key": key})
	if code := e.do(http.MethodPost, "/api/v1/certs/alias.test/upload", string(b), auth.RoleAdmin); code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	e.s.configMu.RLock()
	got := e.s.config.Domains[0].SSL
	e.s.configMu.RUnlock()
	want := filepath.Join(dir, "alias.test", "cert.pem")
	if got.Mode != "manual" || got.Cert != want {
		t.Fatalf("EXPECTED: owning domain switched to manual with cert %s\nACTUAL: mode=%q cert=%q", want, got.Mode, got.Cert)
	}
}

// Force renewal stores the ACME certificate in memory and on disk, replacing
// whatever served the host before. A host no domain serves, and a domain whose
// certificate the operator supplied (ssl.mode manual) or that has TLS off,
// must be refused before any ACME order starts. Without an ACME client the
// manager answers 500, so a 404/409 proves the handler stopped first.
func TestCertRenewRefusedForUnmanagedHosts(t *testing.T) {
	setMode := func(mode string) []config.Domain {
		return []config.Domain{{Host: "site.test", Aliases: []string{"alias.test"}, Type: "static", SSL: config.SSLConfig{Mode: mode}}}
	}
	// newHostConflictEnv forces ssl.mode off on the config it serves, so build
	// the managed domain list the handler sees through the env and restore it.
	cases := []struct {
		name string
		host string
		mode string
		want int
	}{
		{"unknown host", "other.test", "auto", http.StatusNotFound},
		{"manual certificate", "site.test", "manual", http.StatusConflict},
		{"tls off", "site.test", "off", http.StatusConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doms := setMode(tc.mode)
			e := newHostConflictEnv(t, doms)
			e.s.configMu.Lock()
			e.s.config.Domains[0].SSL.Mode = tc.mode
			e.s.configMu.Unlock()
			e.s.SetTLSManager(uwastls.NewManager(config.ACMEConfig{Storage: t.TempDir()}, doms, testLogger(t)))
			if code := e.do(http.MethodPost, "/api/v1/certs/"+tc.host+"/renew", "", auth.RoleAdmin); code != tc.want {
				t.Fatalf("EXPECTED: %d\nACTUAL: %d", tc.want, code)
			}
		})
	}
	// Control: an ACME-managed domain and its alias still reach the manager
	// (no ACME client in the test, so it answers 500).
	for _, host := range []string{"site.test", "alias.test"} {
		t.Run("managed "+host, func(t *testing.T) {
			doms := setMode("auto")
			e := newHostConflictEnv(t, doms)
			e.s.configMu.Lock()
			e.s.config.Domains[0].SSL.Mode = "auto"
			e.s.configMu.Unlock()
			e.s.SetTLSManager(uwastls.NewManager(config.ACMEConfig{Storage: t.TempDir()}, doms, testLogger(t)))
			if code := e.do(http.MethodPost, "/api/v1/certs/"+host+"/renew", "", auth.RoleAdmin); code != http.StatusInternalServerError {
				t.Fatalf("control: want 500 from the manager, got %d", code)
			}
		})
	}
}

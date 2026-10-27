package doctor

// F32201: the SSL Certificates and Domains Directory checks looked in
// "<config dir>/certs" and "<config dir>/domains.d" regardless of what the
// configuration says. A stock system install keeps its certificates in
// global.acme.storage (/var/lib/uwas/certs) and a config may point
// domains_dir elsewhere, so the doctor reported "No certs directory" and
// "domains.d not found" on a healthy host.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeDoctorConfig(t *testing.T, storage, domainsDir string) string {
	t.Helper()
	dir := t.TempDir()
	cfg := "global:\n  web_root: " + filepath.ToSlash(dir) + "\n  acme:\n    storage: " + filepath.ToSlash(storage) + "\n"
	if domainsDir != "" {
		cfg += "  \ndomains_dir: " + filepath.ToSlash(domainsDir) + "\n"
	}
	p := filepath.Join(dir, "uwas.yaml")
	if err := os.WriteFile(p, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSSLCertsCheckFollowsACMEStorage(t *testing.T) {
	storage := t.TempDir()
	for _, f := range []string{"example.com.crt", "example.com.key"} {
		if err := os.WriteFile(filepath.Join(storage, f), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfgPath := writeDoctorConfig(t, storage, "")

	c := checkSSLCerts(cfgPath)
	t.Logf("EXPECTED: ok, certificates counted in %s\nACTUAL: %s %q", storage, c.Status, c.Message)
	if c.Status != StatusOK || strings.Contains(c.Message, "No certs directory") {
		t.Fatalf("PROBLEM CONFIRMED: certs check ignored acme.storage: %s %q", c.Status, c.Message)
	}
}

func TestDomainsDirCheckFollowsDomainsDir(t *testing.T) {
	domains := t.TempDir()
	if err := os.WriteFile(filepath.Join(domains, "a.yaml"), []byte("host: a.test\ntype: static\nroot: /tmp\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfgPath := writeDoctorConfig(t, t.TempDir(), domains)

	c := checkDomainsDir(cfgPath)
	t.Logf("EXPECTED: ok, 1 domain(s) in %s\nACTUAL: %s %q", domains, c.Status, c.Message)
	if c.Status != StatusOK || !strings.Contains(c.Message, "1 domain(s)") {
		t.Fatalf("PROBLEM CONFIRMED: domains check ignored domains_dir: %s %q", c.Status, c.Message)
	}
}

// Control: an unreadable config keeps the old "next to the config file" probe.
func TestConfigDirsFallbackWhenConfigUnreadable(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent", "uwas.yaml")
	if c := checkSSLCerts(missing); c.Status != StatusWarn || !strings.Contains(c.Message, "No certs directory") {
		t.Fatalf("fallback certs check = %s %q", c.Status, c.Message)
	}
	if c := checkDomainsDir(missing); c.Status != StatusWarn {
		t.Fatalf("fallback domains check = %s %q", c.Status, c.Message)
	}
}

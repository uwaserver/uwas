package admin

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/migrate"
)

func cpanelSelfSigned(t *testing.T, host string, notAfter time.Time) (certPEM, keyPEM string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    time.Now().Add(-48 * time.Hour),
		NotAfter:     notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
}

func cpanelArchive(t *testing.T, files map[string]string) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if strings.HasSuffix(n, "/") {
			if err := tw.WriteHeader(&tar.Header{Name: n, Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := tw.WriteHeader(&tar.Header{Name: n, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(files[n]))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(files[n])); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	gz.Close()
	p := filepath.Join(t.TempDir(), "backup.tar.gz")
	if err := os.WriteFile(p, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func domainByHost(cfg *config.Config, host string) *config.Domain {
	for i := range cfg.Domains {
		if cfg.Domains[i].Host == host {
			return &cfg.Domains[i]
		}
	}
	return nil
}

// A certificate that came with a cPanel backup was copied under <webroot>/.certs
// but the new domain was always created with ssl.mode "auto", so the copy was
// never served: the site went to ACME (which fails until DNS moves) while a
// valid certificate sat unused. The imported pair must be wired as manual TLS.
func TestCPanelImportServesImportedCertificate(t *testing.T) {
	e := newHostConflictEnv(t, []config.Domain{{Host: "main.test", Type: "static"}})
	webRoot := e.s.config.Global.WebRoot
	cert, key := cpanelSelfSigned(t, "alice.com", time.Now().Add(90*24*time.Hour))
	arc := cpanelArchive(t, map[string]string{
		"cpmove-alice/":                           "",
		"cpmove-alice/homedir/":                   "",
		"cpmove-alice/homedir/public_html/":       "",
		"cpmove-alice/homedir/public_html/i.html": "<html></html>",
		"cpmove-alice/userdata/main":              "main_domain: alice.com\n",
		"cpmove-alice/sslcerts/alice_com_1.crt":   cert,
		"cpmove-alice/sslkeys/alice_com_1.key":    key,
	})

	res, err := migrate.ImportCPanelBackup(arc, webRoot, false)
	if err != nil {
		t.Fatal(err)
	}
	added, skipped := e.s.createDomainsFromMigrationChecked(res, webRoot)
	if len(added) != 1 || len(skipped) != 0 {
		t.Fatalf("added=%v skipped=%v", added, skipped)
	}
	d := domainByHost(e.s.config, "alice.com")
	if d == nil {
		t.Fatal("alice.com not created")
	}
	t.Logf("EXPECTED: ssl.mode=manual with the imported pair\nACTUAL: mode=%q cert=%q key=%q (ssl_certs=%d)", d.SSL.Mode, d.SSL.Cert, d.SSL.Key, res.SSLCerts)
	if d.SSL.Mode != "manual" || d.SSL.Cert == "" || d.SSL.Key == "" {
		t.Fatalf("PROBLEM CONFIRMED: imported certificate is not wired (ssl = %+v)", d.SSL)
	}
	for _, p := range []string{d.SSL.Cert, d.SSL.Key} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("configured path %s: %v", p, err)
		}
	}
	if err := e.s.persistConfig(); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(e.cfgPath); err != nil {
		t.Fatalf("persisted configuration no longer loads: %v", err)
	}
}

// Control: without a certificate in the backup the new domain keeps automatic
// TLS, and an unusable (expired) certificate does not switch it to manual.
func TestCPanelImportWithoutUsableCertKeepsAutoTLS(t *testing.T) {
	expired, expiredKey := cpanelSelfSigned(t, "alice.com", time.Now().Add(-24*time.Hour))
	cases := map[string]map[string]string{
		"no certificate": {},
		"expired": {
			"cpmove-alice/ssl/alice.com.crt": expired,
			"cpmove-alice/ssl/alice.com.key": expiredKey,
		},
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			e := newHostConflictEnv(t, []config.Domain{{Host: "main.test", Type: "static"}})
			webRoot := e.s.config.Global.WebRoot
			files := map[string]string{
				"cpmove-alice/":                           "",
				"cpmove-alice/homedir/":                   "",
				"cpmove-alice/homedir/public_html/":       "",
				"cpmove-alice/homedir/public_html/i.html": "<html></html>",
				"cpmove-alice/userdata/main":              "main_domain: alice.com\n",
			}
			for k, v := range extra {
				files[k] = v
			}
			res, err := migrate.ImportCPanelBackup(cpanelArchive(t, files), webRoot, false)
			if err != nil {
				t.Fatal(err)
			}
			e.s.createDomainsFromMigrationChecked(res, webRoot)
			d := domainByHost(e.s.config, "alice.com")
			if d == nil || d.SSL.Mode != "auto" || d.SSL.Cert != "" || d.SSL.Key != "" {
				t.Fatalf("ssl = %+v, want automatic TLS", d)
			}
		})
	}
}

// Parked domains and server aliases become aliases of the imported site, but a
// name that another site already claims is skipped with a message instead of
// producing a configuration that fails to load.
func TestCPanelImportRegistersAliasesAndSkipsClaimedOnes(t *testing.T) {
	e := newHostConflictEnv(t, []config.Domain{
		{Host: "main.test", Type: "static", Aliases: []string{"taken.test"}},
	})
	webRoot := e.s.config.Global.WebRoot
	arc := cpanelArchive(t, map[string]string{
		"cpmove-alice/":                           "",
		"cpmove-alice/homedir/":                   "",
		"cpmove-alice/homedir/public_html/":       "",
		"cpmove-alice/homedir/public_html/i.html": "<html></html>",
		"cpmove-alice/userdata/main":              "main_domain: alice.com\nparked_domains:\n- parked.test\n- taken.test\n- MAIN.test\n",
		"cpmove-alice/userdata/alice.com":         "documentroot: /home/alice/public_html\nserveralias: www.alice.com extra.alice.com\n",
	})
	res, err := migrate.ImportCPanelBackup(arc, webRoot, false)
	if err != nil {
		t.Fatal(err)
	}
	added, skipped := e.s.createDomainsFromMigrationChecked(res, webRoot)
	d := domainByHost(e.s.config, "alice.com")
	if d == nil || len(added) != 1 {
		t.Fatalf("added=%v skipped=%v", added, skipped)
	}
	sort.Strings(d.Aliases)
	t.Logf("EXPECTED: aliases=[extra.alice.com parked.test]; taken.test and MAIN.test skipped with their owner named\nACTUAL: aliases=%v skipped=%v", d.Aliases, skipped)
	if strings.Join(d.Aliases, ",") != "extra.alice.com,parked.test" {
		t.Fatalf("PROBLEM CONFIRMED: aliases = %v", d.Aliases)
	}
	joined := strings.Join(skipped, "\n")
	if !strings.Contains(joined, "taken.test") || !strings.Contains(joined, "main.test") {
		t.Errorf("skipped = %v, want messages naming taken.test and MAIN.test with their owner", skipped)
	}
	if err := e.s.persistConfig(); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(e.cfgPath); err != nil {
		t.Fatalf("PROBLEM CONFIRMED: persisted configuration no longer loads: %v", err)
	}
}

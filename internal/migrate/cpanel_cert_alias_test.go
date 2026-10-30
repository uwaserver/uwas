package migrate

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// selfSignedPair returns a certificate valid for hosts (until notAfter) and its
// private key, both PEM encoded.
func selfSignedPair(t *testing.T, hosts []string, notAfter time.Time) (certPEM, keyPEM string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: hosts[0]},
		DNSNames:     hosts,
		NotBefore:    time.Now().Add(-48 * time.Hour),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	return certPEM, keyPEM
}

// importedPairLoads checks that the certificate the import stored for host is a
// usable pair, which is what the TLS manager needs to serve it.
func importedPairLoads(t *testing.T, target, host string) error {
	t.Helper()
	dir := filepath.Join(target, ".certs", host)
	_, err := tls.LoadX509KeyPair(filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem"))
	return err
}

func domainJSON(t *testing.T, d CPanelDomain) map[string]any {
	t.Helper()
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func certArchive(t *testing.T, files map[string]string) string {
	t.Helper()
	base := map[string]string{
		"cpmove-alice/":                           "",
		"cpmove-alice/homedir/":                   "",
		"cpmove-alice/homedir/public_html/":       "",
		"cpmove-alice/homedir/public_html/i.html": "<html></html>",
		"cpmove-alice/userdata/main":              "main_domain: alice.com\n",
	}
	for k, v := range files {
		base[k] = v
	}
	p := filepath.Join(t.TempDir(), "backup.tar.gz")
	writeCPanelBackup(t, p, base)
	return p
}

// pkgacct keeps account certificates in sslcerts/ and sslkeys/ under file names
// that carry a hash, not the bare domain; the import only looked for
// ssl/<domain>.crt, so every certificate of a real backup was ignored and
// ssl_certs stayed 0.
func TestCPanelImportFindsCertsUnderSslcerts(t *testing.T) {
	cert, key := selfSignedPair(t, []string{"alice.com", "www.alice.com"}, time.Now().Add(90*24*time.Hour))
	target := t.TempDir()
	res, err := ImportCPanelBackup(certArchive(t, map[string]string{
		"cpmove-alice/sslcerts/alice_com_a1b2c3_4d5e6f.crt": cert,
		"cpmove-alice/sslkeys/alice_com_a1b2c3_4d5e6f.key":  key,
	}), target, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("EXPECTED: ssl_certs=1 and a loadable pair for alice.com\nACTUAL: ssl_certs=%d errors=%v", res.SSLCerts, res.Errors)
	if res.SSLCerts != 1 {
		t.Fatalf("PROBLEM CONFIRMED: ssl_certs = %d, want 1 (certificate under sslcerts/ ignored)", res.SSLCerts)
	}
	if err := importedPairLoads(t, target, "alice.com"); err != nil {
		t.Fatalf("PROBLEM CONFIRMED: stored pair does not load: %v", err)
	}
	if !res.Domains[0].SSL {
		t.Errorf("domain SSL flag not set: %+v", res.Domains[0])
	}
}

// apache_tls/<domain> holds the key, the certificate and the CA bundle in one
// PEM file.
func TestCPanelImportFindsCombinedApacheTLSFile(t *testing.T) {
	cert, key := selfSignedPair(t, []string{"alice.com"}, time.Now().Add(90*24*time.Hour))
	target := t.TempDir()
	res, err := ImportCPanelBackup(certArchive(t, map[string]string{
		"cpmove-alice/apache_tls/alice.com": key + cert,
	}), target, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.SSLCerts != 1 {
		t.Fatalf("PROBLEM CONFIRMED: ssl_certs = %d, want 1 (combined apache_tls file ignored); errors=%v", res.SSLCerts, res.Errors)
	}
	if err := importedPairLoads(t, target, "alice.com"); err != nil {
		t.Fatalf("PROBLEM CONFIRMED: stored pair does not load: %v", err)
	}
}

// The existing ssl/<domain>.crt layout keeps working (control).
func TestCPanelImportKeepsSslDirLayout(t *testing.T) {
	cert, key := selfSignedPair(t, []string{"alice.com"}, time.Now().Add(90*24*time.Hour))
	target := t.TempDir()
	res, err := ImportCPanelBackup(certArchive(t, map[string]string{
		"cpmove-alice/ssl/alice.com.crt": cert,
		"cpmove-alice/ssl/alice.com.key": key,
	}), target, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.SSLCerts != 1 || importedPairLoads(t, target, "alice.com") != nil {
		t.Fatalf("control failed: ssl_certs=%d errors=%v", res.SSLCerts, res.Errors)
	}
}

// An expired certificate, a certificate for another name and a certificate
// whose key is missing or different are not served: the domain falls back to
// automatic TLS and the reason is reported.
func TestCPanelImportRejectsUnusableCerts(t *testing.T) {
	good, goodKey := selfSignedPair(t, []string{"alice.com"}, time.Now().Add(90*24*time.Hour))
	expired, expiredKey := selfSignedPair(t, []string{"alice.com"}, time.Now().Add(-24*time.Hour))
	other, otherKey := selfSignedPair(t, []string{"bob.test"}, time.Now().Add(90*24*time.Hour))
	_, strayKey := selfSignedPair(t, []string{"stray.test"}, time.Now().Add(90*24*time.Hour))
	_ = good
	_ = goodKey

	cases := []struct {
		name  string
		files map[string]string
		want  string // substring of the reported reason
	}{
		{"expired", map[string]string{
			"cpmove-alice/ssl/alice.com.crt": expired, "cpmove-alice/ssl/alice.com.key": expiredKey,
		}, "expired"},
		{"certificate for another name", map[string]string{
			"cpmove-alice/ssl/alice.com.crt": other, "cpmove-alice/ssl/alice.com.key": otherKey,
		}, ""},
		{"key does not match", map[string]string{
			"cpmove-alice/ssl/alice.com.crt": good, "cpmove-alice/ssl/alice.com.key": strayKey,
		}, "key"},
		{"key missing", map[string]string{
			"cpmove-alice/ssl/alice.com.crt": good,
		}, "key"},
		{"garbage", map[string]string{
			"cpmove-alice/ssl/alice.com.crt": "CERTDATA", "cpmove-alice/ssl/alice.com.key": "KEYDATA",
		}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			target := t.TempDir()
			res, err := ImportCPanelBackup(certArchive(t, c.files), target, false)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("EXPECTED: ssl_certs=0, no stored pair\nACTUAL: ssl_certs=%d ssl=%v errors=%v", res.SSLCerts, res.Domains[0].SSL, res.Errors)
			if res.SSLCerts != 0 || res.Domains[0].SSL {
				t.Fatalf("PROBLEM CONFIRMED: unusable certificate counted as imported (ssl_certs=%d)", res.SSLCerts)
			}
			if _, err := os.Stat(filepath.Join(target, ".certs", "alice.com", "key.pem")); err == nil {
				t.Errorf("PROBLEM CONFIRMED: private key of an unusable pair was stored")
			}
			if c.want != "" && !strings.Contains(strings.ToLower(strings.Join(res.Errors, "\n")), c.want) {
				t.Errorf("errors = %v, want a message containing %q", res.Errors, c.want)
			}
		})
	}
}

// parked_domains (userdata/main) and serveralias (userdata/<vhost>) are extra
// names of a site. They were dropped, so a parked domain stopped resolving to
// the site after the move.
func TestCPanelImportCarriesParkedAndServerAliases(t *testing.T) {
	target := t.TempDir()
	res, err := ImportCPanelBackup(certArchive(t, map[string]string{
		"cpmove-alice/userdata/main": "---\ncp_user: alice\nmain_domain: alice.com\nparked_domains:\n- parked.test\n- Other-Parked.test\n" +
			"sub_domains:\n- shop.alice.com\n",
		"cpmove-alice/userdata/alice.com":        "documentroot: /home/alice/public_html\nservername: alice.com\nserveralias: www.alice.com extra.alice.com *.wild.test bad_name.test\n",
		"cpmove-alice/userdata/shop.alice.com":   "documentroot: /home/alice/public_html/shop\nservername: shop.alice.com\nserveralias: www.shop.alice.com\n",
		"cpmove-alice/homedir/public_html/shop/": "",
	}), target, false)
	if err != nil {
		t.Fatal(err)
	}
	byHost := map[string]map[string]any{}
	for _, d := range res.Domains {
		byHost[d.Domain] = domainJSON(t, d)
	}
	toStrings := func(v any) []string {
		var out []string
		if l, ok := v.([]any); ok {
			for _, x := range l {
				out = append(out, x.(string))
			}
		}
		sort.Strings(out)
		return out
	}
	got := toStrings(byHost["alice.com"]["aliases"])
	t.Logf("EXPECTED: alice.com aliases=[*.wild.test extra.alice.com other-parked.test parked.test]\nACTUAL: %v (errors=%v)", got, res.Errors)
	want := []string{"*.wild.test", "extra.alice.com", "other-parked.test", "parked.test"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("PROBLEM CONFIRMED: alice.com aliases = %v, want %v (www.alice.com is the same site by apex/www equivalence; bad_name.test is not a hostname)", got, want)
	}
	// Control: www.<host> alone adds nothing for the subdomain.
	if a := toStrings(byHost["shop.alice.com"]["aliases"]); len(a) != 0 {
		t.Errorf("shop.alice.com aliases = %v, want none", a)
	}
	if !strings.Contains(strings.Join(res.Errors, "\n"), "bad_name.test") {
		t.Errorf("errors = %v, want the invalid alias reported as skipped", res.Errors)
	}
}

// A name that is itself an imported domain (the addon domain appears in the
// serveralias of its subdomain vhost) is a site of its own, not an alias of
// another one.
func TestCPanelImportAliasNotAnotherImportedDomain(t *testing.T) {
	target := t.TempDir()
	res, err := ImportCPanelBackup(realLayoutArchive(t, "cpmove-alice"), target, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range res.Domains {
		m := domainJSON(t, d)
		if l, ok := m["aliases"].([]any); ok {
			for _, a := range l {
				for _, other := range res.Domains {
					if a == other.Domain {
						t.Errorf("PROBLEM CONFIRMED: %s lists imported domain %s as an alias", d.Domain, other.Domain)
					}
				}
			}
		}
	}
}

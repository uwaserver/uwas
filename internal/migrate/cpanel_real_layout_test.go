package migrate

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// realLayoutArchive mirrors what pkgacct writes for an account with a main
// domain, a subdomain, an addon domain and PHP-FPM enabled: an addon domain is
// created as a subdomain vhost (userdata/addon.example.com) and the real domain
// name only appears in userdata/main's addon_domains map and the vhost's
// serveralias; PHP-FPM adds <vhost>.php-fpm.yaml beside every vhost file; the
// root directory of a full-account backup is backup-<date>_<user>, not
// cpmove-<user>.
func realLayoutArchive(t *testing.T, root string) string {
	t.Helper()
	arc := filepath.Join(t.TempDir(), "backup.tar.gz")
	writeCPanelBackup(t, arc, map[string]string{
		root + "/cp/alice": "BWLIMIT=0\nDNS=example.com\nDNS1=addon.test\n",
		root + "/userdata/main": "---\naddon_domains:\n  addon.test: addon.example.com\n" +
			"cp_user: alice\nmain_domain: example.com\nparked_domains:\n- parked.test\n" +
			"sub_domains:\n- shop.example.com\n- addon.example.com\n",
		root + "/userdata/main.cache":                     "{}",
		root + "/userdata/example.com":                    "documentroot: /home/alice/public_html\nservername: example.com\nserveralias: www.example.com\n",
		root + "/userdata/example.com_SSL":                "documentroot: /home/alice/public_html\nservername: example.com\n",
		root + "/userdata/example.com.cache":              "{}",
		root + "/userdata/example.com.php-fpm.yaml":       "---\n_is_present: 1\nphp_fpm: 1\nphpversion: ea-php81\n",
		root + "/userdata/shop.example.com":               "documentroot: /home/alice/public_html/shop\nservername: shop.example.com\n",
		root + "/userdata/shop.example.com.php-fpm.yaml":  "---\n_is_present: 1\nphp_fpm: 1\nphpversion: ea-php81\n",
		root + "/userdata/addon.example.com":              "documentroot: /home/alice/addon.test\nservername: addon.example.com\nserveralias: www.addon.example.com addon.test www.addon.test\n",
		root + "/userdata/addon.example.com.php-fpm.yaml": "---\n_is_present: 1\nphp_fpm: 1\nphpversion: ea-php81\n",
		root + "/homedir/public_html/index.php":           "MAIN",
		root + "/homedir/public_html/shop/index.html":     "SHOP",
		root + "/homedir/addon.test/index.html":           "ADDON",
		root + "/mysql/alice_wp.sql":                      "CREATE TABLE t (id int);\n",
	})
	return arc
}

func importedHosts(res *CPanelResult) []string {
	var hosts []string
	for _, d := range res.Domains {
		hosts = append(hosts, d.Domain)
	}
	sort.Strings(hosts)
	return hosts
}

// Every vhost file has a <vhost>.php-fpm.yaml sibling on a PHP-FPM account. It
// was treated as another domain ("example.com.php-fpm.yaml"), producing a
// bogus site with no files and a warning for each vhost.
func TestCPanelImportIgnoresPHPFPMUserdataFiles(t *testing.T) {
	dst := t.TempDir()
	res, err := ImportCPanelBackup(realLayoutArchive(t, "cpmove-alice"), dst, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range importedHosts(res) {
		if strings.Contains(h, "php-fpm") || strings.HasSuffix(h, ".yaml") {
			t.Errorf("EXPECTED: no domain for php-fpm.yaml metadata\nACTUAL: domains=%v errors=%v", importedHosts(res), res.Errors)
			break
		}
	}
	// Control: the real vhosts are still imported with their files.
	if got := readImported(dst, "example.com", "index.php"); got != "MAIN" {
		t.Errorf("control: example.com index = %q, want MAIN", got)
	}
	if got := readImported(dst, "shop.example.com", "index.html"); got != "SHOP" {
		t.Errorf("control: shop.example.com index = %q, want SHOP", got)
	}
}

// An addon domain's vhost file is named after its subdomain form
// (addon.example.com); the real domain (addon.test) is only listed in
// userdata/main's addon_domains. Importing the subdomain form instead left the
// real addon domain without a site in UWAS.
func TestCPanelImportAddonDomainUsesRealName(t *testing.T) {
	dst := t.TempDir()
	res, err := ImportCPanelBackup(realLayoutArchive(t, "cpmove-alice"), dst, false)
	if err != nil {
		t.Fatal(err)
	}
	hosts := importedHosts(res)
	t.Logf("EXPECTED: addon.test among domains, files copied\nACTUAL: domains=%v errors=%v", hosts, res.Errors)
	found := false
	for _, h := range hosts {
		found = found || h == "addon.test"
		if h == "addon.example.com" {
			t.Errorf("PROBLEM CONFIRMED: addon imported under its subdomain form %q", h)
		}
	}
	if !found {
		t.Errorf("PROBLEM CONFIRMED: addon.test not imported: %v", hosts)
	}
	if got := readImported(dst, "addon.test", "index.html"); got != "ADDON" {
		t.Errorf("addon.test files = %q, want ADDON (errors=%v)", got, res.Errors)
	}
	for _, d := range res.Domains {
		if d.Domain == "addon.test" && d.Type != "addon" {
			t.Errorf("addon.test type = %q, want addon", d.Type)
		}
	}
	// Control: the subdomain keeps its own name and type.
	for _, d := range res.Domains {
		if d.Domain == "shop.example.com" && d.Type != "sub" {
			t.Errorf("control: shop.example.com type = %q, want sub", d.Type)
		}
	}
}

// A full-account backup's root directory is backup-<date>_<user>; the whole
// directory name was reported as the account and as every database's owner.
// userdata/main (cp_user) and cp/<user> name the real account.
func TestCPanelImportAccountNameFromBackupMetadata(t *testing.T) {
	for _, root := range []string{"backup-10.27.2026_12-00-00_alice", "cpmove-alice"} {
		t.Run(root, func(t *testing.T) {
			res, err := ImportCPanelBackup(realLayoutArchive(t, root), t.TempDir(), false)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("EXPECTED: alice\nACTUAL: %q", res.User)
			if res.User != "alice" {
				t.Errorf("PROBLEM CONFIRMED: User = %q, want alice", res.User)
			}
			if len(res.Databases) != 1 || res.Databases[0].User != "alice" {
				t.Errorf("database owner = %+v, want alice", res.Databases)
			}
		})
	}
}

// The imported domains must end up in a configuration that loads: hosts are
// unique and valid.
func TestCPanelImportRealLayoutProducesLoadableHosts(t *testing.T) {
	res, err := ImportCPanelBackup(realLayoutArchive(t, "cpmove-alice"), t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, d := range res.Domains {
		if seen[d.Domain] {
			t.Errorf("duplicate host %q", d.Domain)
		}
		seen[d.Domain] = true
		if !config.IsValidHostname(d.Domain) {
			t.Errorf("invalid host %q", d.Domain)
		}
	}
	if len(res.Domains) == 0 {
		t.Fatal("no domains imported")
	}
	if _, err := os.Stat(filepath.Join(t.TempDir())); err != nil {
		t.Fatal(err)
	}
}

func TestReadCPanelAddonDomainsForms(t *testing.T) {
	write := func(body string) string {
		p := filepath.Join(t.TempDir(), "main")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cases := []struct {
		name, body string
		want       map[string]string
	}{
		{"empty inline map", "---\naddon_domains: {}\nmain_domain: a.test\n", map[string]string{}},
		{"two addons then next key", "addon_domains:\n  one.test: one.a.test\n  \"two.test\": 'two.a.test'\nmain_domain: a.test\n", map[string]string{"one.a.test": "one.test", "two.a.test": "two.test"}},
		{"crlf", "addon_domains:\r\n  one.test: one.a.test\r\nmain_domain: a.test\r\n", map[string]string{"one.a.test": "one.test"}},
		{"no addon block", "main_domain: a.test\n", map[string]string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := readCPanelAddonDomains(write(c.body))
			if len(got) != len(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			for k, v := range c.want {
				if got[k] != v {
					t.Errorf("got %v, want %v", got, c.want)
				}
			}
		})
	}
	if got := readCPanelAddonDomains(filepath.Join(t.TempDir(), "absent")); got != nil {
		t.Errorf("missing file: got %v, want nil", got)
	}
}

func TestReadCPanelUserFallbacks(t *testing.T) {
	root := t.TempDir()
	if got := readCPanelUser(root, "fallback"); got != "fallback" {
		t.Errorf("no metadata: %q, want fallback", got)
	}
	os.MkdirAll(filepath.Join(root, "cp"), 0o755)
	os.WriteFile(filepath.Join(root, "cp", "bob"), []byte("x"), 0o644)
	if got := readCPanelUser(root, "fallback"); got != "bob" {
		t.Errorf("single cp file: %q, want bob", got)
	}
	os.WriteFile(filepath.Join(root, "cp", "carol"), []byte("x"), 0o644)
	if got := readCPanelUser(root, "fallback"); got != "fallback" {
		t.Errorf("ambiguous cp dir: %q, want fallback", got)
	}
	os.MkdirAll(filepath.Join(root, "userdata"), 0o755)
	os.WriteFile(filepath.Join(root, "userdata", "main"), []byte("cp_user: dave\nmain_domain: d.test\n"), 0o644)
	if got := readCPanelUser(root, "fallback"); got != "dave" {
		t.Errorf("cp_user: %q, want dave", got)
	}
	os.WriteFile(filepath.Join(root, "userdata", "main"), []byte("cp_user: ../evil\n"), 0o644)
	if got := readCPanelUser(root, "fallback"); got == "../evil" {
		t.Errorf("unsafe cp_user accepted: %q", got)
	}
}

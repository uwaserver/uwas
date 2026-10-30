package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func cpanelDocRootArchive(t *testing.T) string {
	t.Helper()
	arc := filepath.Join(t.TempDir(), "cpmove-alice.tar.gz")
	writeCPanelBackup(t, arc, map[string]string{
		"cpmove-alice/userdata/main":                     "main_domain: main.test\n",
		"cpmove-alice/userdata/addon.test":               "servername: addon.test\ndocumentroot: /home/alice/addon.test\n",
		"cpmove-alice/userdata/nested.test":              "servername: nested.test\ndocumentroot: /home2/alice/public_html/nested\n",
		"cpmove-alice/userdata/blog.main.test":           "servername: blog.main.test\ndocumentroot: /home/alice/public_html/blog\n",
		"cpmove-alice/homedir/public_html/index.html":    "MAIN",
		"cpmove-alice/homedir/public_html/wp-config.php": "SECRET",
		"cpmove-alice/homedir/public_html/nested/i.html": "NESTED",
		"cpmove-alice/homedir/addon.test/index.html":     "ADDON",
	})
	return arc
}

func readImported(dst, host, name string) string {
	b, err := os.ReadFile(filepath.Join(dst, host, "public_html", name))
	if err != nil {
		return ""
	}
	return string(b)
}

// An addon domain whose document root lives beside public_html
// (/home/alice/addon.test) was resolved to "alice/addon.test", so its files
// were never copied. Controls: a docroot under public_html (even on /home2)
// and the main domain keep working.
func TestCPanelImportDocRootOutsidePublicHTML(t *testing.T) {
	dst := t.TempDir()
	res, err := ImportCPanelBackup(cpanelDocRootArchive(t), dst, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := readImported(dst, "addon.test", "index.html"); got != "ADDON" {
		t.Errorf("EXPECTED: addon.test files copied\nACTUAL: index=%q errors=%v", got, res.Errors)
	}
	if got := readImported(dst, "nested.test", "i.html"); got != "NESTED" {
		t.Errorf("control: nested.test = %q, want NESTED", got)
	}
	if got := readImported(dst, "main.test", "index.html"); got != "MAIN" {
		t.Errorf("control: main.test = %q, want MAIN", got)
	}
}

// A subdomain whose document root is missing from the archive must not get a
// copy of the whole main site (wp-config.php, backups) served under its own
// hostname; the missing files are reported instead.
func TestCPanelImportMissingDocRootDoesNotCopyMainSite(t *testing.T) {
	dst := t.TempDir()
	res, err := ImportCPanelBackup(cpanelDocRootArchive(t), dst, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := readImported(dst, "blog.main.test", "wp-config.php"); got != "" {
		t.Errorf("EXPECTED: no main-site files under blog.main.test\nACTUAL: wp-config.php=%q", got)
	}
	reported := false
	for _, e := range res.Errors {
		reported = reported || strings.Contains(e, "no files for blog.main.test")
	}
	if !reported {
		t.Errorf("missing document root not reported: %v", res.Errors)
	}
}

func TestReadCPanelDocRootHomePrefixes(t *testing.T) {
	cases := map[string]string{
		"/home/alice/addon.test":       "addon.test",
		"/home2/alice/sites/app":       "sites/app",
		"/home/alice/public_html/blog": "public_html/blog",
		"/srv/www/site":                "www/site", // not a /home layout: last two components
		"/home/alice":                  "home/alice",
	}
	for docroot, want := range cases {
		p := filepath.Join(t.TempDir(), "dom")
		os.WriteFile(p, []byte("documentroot: "+docroot+"\n"), 0o644)
		if got := readCPanelDocRoot(p); got != want {
			t.Errorf("%s: got %q, want %q", docroot, got, want)
		}
	}
}

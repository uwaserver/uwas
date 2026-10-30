package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// Round 176 (F41000-F41004): `uwas migrate apache` turned PHP sites into
// static ones (every .php answers 403 after the move), kept a literal "\" as a
// ServerAlias for a continued line, ignored lower-case <virtualhost> tags,
// dropped ProxyPassMatch / Alias / Include / SSLCertificateChainFile without a
// word, and produced two domains named example.com for vhosts without a
// ServerName (config validation then rejects the whole file).

func apacheConvert(t *testing.T, src string) (string, *config.Config, error) {
	t.Helper()
	dir := t.TempDir()
	conf := filepath.Join(dir, "site.conf")
	if err := os.WriteFile(conf, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(conf)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	out := convertApacheToYAML(parseApacheConfig(f))
	p := filepath.Join(dir, "uwas.yaml")
	if err := os.WriteFile(p, []byte(out), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(p)
	return out, cfg, err
}

func TestApacheMigrationKeepsPHPSitesPHP(t *testing.T) {
	vh := func(body string) string {
		return "<VirtualHost *:80>\nServerName php.test\nDocumentRoot /var/www/php\n" + body + "</VirtualHost>\n"
	}
	cases := map[string]struct{ body, wantFPM string }{
		"fpm socket": {"<FilesMatch \\.php$>\n SetHandler \"proxy:unix:/run/php/php8.3-fpm.sock|fcgi://localhost\"\n</FilesMatch>\n", "unix:/run/php/php8.3-fpm.sock"},
		"fpm tcp":    {"SetHandler \"proxy:fcgi://127.0.0.1:9000\"\n", "127.0.0.1:9000"},
		"mod_php":    {"AddHandler application/x-httpd-php .php\n", ""},
		"addtype":    {"AddType application/x-httpd-php .php\n", ""},
	}
	for name, c := range cases {
		out, cfg, err := apacheConvert(t, vh(c.body))
		if err != nil {
			t.Fatalf("%s: converted config does not load: %v\n%s", name, err, out)
		}
		if got := cfg.Domains[0].Type; got != "php" {
			t.Errorf("%s: type = %q, want php (every .php answers 403 on a static site)\n%s", name, got, out)
			continue
		}
		if c.wantFPM != "" && cfg.Domains[0].PHP.FPMAddress != c.wantFPM {
			t.Errorf("%s: fpm_address = %q, want %q\n%s", name, cfg.Domains[0].PHP.FPMAddress, c.wantFPM, out)
		}
	}

	// Controls: a plain site stays static, a proxy site stays a proxy even if
	// the file also mentions PHP, and "SetHandler none" is not PHP.
	_, cfg, err := apacheConvert(t, vh(""))
	if err != nil || cfg.Domains[0].Type != "static" {
		t.Fatalf("control (plain site): type=%v err=%v", cfg.Domains[0].Type, err)
	}
	_, cfg, err = apacheConvert(t, vh("SetHandler none\n"))
	if err != nil || cfg.Domains[0].Type != "static" {
		t.Fatalf("control (SetHandler none): type=%v err=%v", cfg.Domains[0].Type, err)
	}
	_, cfg, err = apacheConvert(t, vh("ProxyPass / http://127.0.0.1:8080/\nAddHandler application/x-httpd-php .php\n"))
	if err != nil || cfg.Domains[0].Type != "proxy" {
		t.Fatalf("control (proxy wins): type=%v err=%v", cfg.Domains[0].Type, err)
	}
}

func TestApacheMigrationContinuationLinesAndTagCase(t *testing.T) {
	_, cfg, err := apacheConvert(t, "<VirtualHost *:80>\nServerName c.test\nServerAlias a.c.test \\\n    b.c.test\nDocumentRoot /var/www/c\n</VirtualHost>\n")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(cfg.Domains[0].Aliases, ",")
	if got != "a.c.test,b.c.test" {
		t.Errorf("aliases = %q, want a.c.test,b.c.test (a continued ServerAlias line)", got)
	}

	out, cfg, err := apacheConvert(t, "<virtualhost *:80>\nservername lc.test\ndocumentroot /var/www/lc\n</virtualhost>\n")
	if err != nil || len(cfg.Domains) != 1 || cfg.Domains[0].Host != "lc.test" {
		t.Errorf("lower-case <virtualhost> block lost: cfg=%v err=%v\n%s", cfg, err, out)
	}

	// Control: the usual spelling keeps working.
	_, cfg, err = apacheConvert(t, "<VirtualHost *:80>\nServerName ok.test\nDocumentRoot /var/www/ok\n</VirtualHost>\n")
	if err != nil || len(cfg.Domains) != 1 || cfg.Domains[0].Host != "ok.test" {
		t.Fatalf("control: cfg=%v err=%v", cfg, err)
	}
}

func TestApacheMigrationReportsDroppedDirectives(t *testing.T) {
	cases := map[string]string{
		"ProxyPassMatch":          "ProxyPassMatch ^/api/(.*)$ http://127.0.0.1:9000/$1",
		"Alias":                   "Alias /static /var/www/shared/static",
		"AliasMatch":              "AliasMatch ^/img/(.*)$ /var/www/img/$1",
		"ScriptAlias":             "ScriptAlias /cgi-bin/ /usr/lib/cgi-bin/",
		"Include":                 "Include /etc/apache2/extra/site.conf",
		"IncludeOptional":         "IncludeOptional /etc/apache2/extra/*.conf",
		"SSLCertificateChainFile": "SSLCertificateChainFile /etc/ssl/chain.crt",
	}
	for name, line := range cases {
		out := convertApacheForTest(t, line+"\n")
		if !strings.HasPrefix(out, "# WARNING: not migrated") || !strings.Contains(out, "b.test: "+line) {
			t.Errorf("%s: dropped without a warning:\n%s", name, out)
		}
	}

	// Include outside any vhost is just as invisible.
	out, _, _ := apacheConvert(t, "Include /etc/apache2/sites-available/*.conf\n<VirtualHost *:80>\nServerName i.test\nDocumentRoot /var/www/i\n</VirtualHost>\n")
	if !strings.Contains(out, "# WARNING:") || !strings.Contains(out, "Include /etc/apache2/sites-available/*.conf") {
		t.Errorf("top-level Include dropped without a warning:\n%s", out)
	}

	// Controls: ServerAlias and ProxyPass are migrated, so they stay quiet.
	if out := convertApacheForTest(t, "ServerAlias www.b.test\nProxyPass /api http://127.0.0.1:9000\n"); strings.Contains(out, "WARNING") {
		t.Errorf("migrated directives produced a warning:\n%s", out)
	}
}

func TestApacheMigrationVHostsWithoutServerNameStayLoadable(t *testing.T) {
	out, cfg, err := apacheConvert(t, "<VirtualHost *:80>\nDocumentRoot /var/www/a\n</VirtualHost>\n<VirtualHost *:8080>\nDocumentRoot /var/www/b\n</VirtualHost>\n")
	if err != nil {
		t.Fatalf("two vhosts without ServerName: config does not load: %v\n%s", err, out)
	}
	if len(cfg.Domains) != 2 {
		t.Fatalf("domains = %d, want 2\n%s", len(cfg.Domains), out)
	}
	if !strings.Contains(out, "# WARNING:") {
		t.Errorf("placeholder host used without a warning:\n%s", out)
	}
}

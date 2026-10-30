package migrate

// F40600: nginx accepts quoted arguments ("/var/www/my site" is the only way
// to give a path with a space). The converter kept the quotes, so the migrated
// root, certificate paths and host names carried literal quote characters and
// matched nothing.

import (
	"strings"
	"testing"
)

func TestNginxQuotedValuesAreUnquoted(t *testing.T) {
	cfgs, err := ParseNginx(strings.NewReader(`
server {
    listen 443 ssl;
    server_name "q.test" 'www.q.test';
    root "/var/www/q site";
    index "index.html" 'index.php';
    ssl_certificate "/etc/ssl/q site/cert.pem";
    ssl_certificate_key '/etc/ssl/q site/key.pem';
}
`))
	if err != nil || len(cfgs) != 1 {
		t.Fatalf("parse: %v %d", err, len(cfgs))
	}
	c := cfgs[0]
	t.Logf("ACTUAL: names=%q root=%q index=%q cert=%q key=%q", c.ServerNames, c.Root, c.IndexFiles, c.SSLCert, c.SSLKey)
	if strings.Join(c.ServerNames, "|") != "q.test|www.q.test" ||
		c.Root != "/var/www/q site" ||
		strings.Join(c.IndexFiles, "|") != "index.html|index.php" ||
		c.SSLCert != "/etc/ssl/q site/cert.pem" || c.SSLKey != "/etc/ssl/q site/key.pem" {
		t.Fatal("PROBLEM CONFIRMED: quotes kept in migrated values")
	}
}

func TestNginxUnquotedValuesUnchanged(t *testing.T) {
	cfgs, _ := ParseNginx(strings.NewReader("server {\nserver_name a.test;\nroot /var/www/a;\nindex index.html;\n}\n"))
	c := cfgs[0]
	if c.ServerNames[0] != "a.test" || c.Root != "/var/www/a" || c.IndexFiles[0] != "index.html" {
		t.Errorf("plain values changed: %+v", c)
	}
	// A lone quote or a quote inside a value is data, not a delimiter.
	if got := nginxUnquote(`"`); got != `"` {
		t.Errorf("lone quote = %q", got)
	}
	if got := nginxUnquote(`a"b`); got != `a"b` {
		t.Errorf("inner quote = %q", got)
	}
	if got := nginxUnquote(`"a\"b"`); got != `a"b` {
		t.Errorf("escaped quote = %q", got)
	}
}

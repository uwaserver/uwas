package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApacheMigrationQuotedArguments(t *testing.T) {
	for _, tc := range []struct{ line, want string }{
		{`DocumentRoot "/srv/My Site"`, "/srv/My Site"},
		{`DocumentRoot '/srv/My Site'`, "/srv/My Site"},
		{"\tDocumentRoot\t\"/srv/My Site\"\t", "/srv/My Site"},
		{"DocumentRoot /srv/site ignored", "/srv/site"},
		{`DocumentRoot ""`, ""},
		{"SingleWord", ""},
		{"", ""},
		{`DocumentRoot "/srv/a\\b Site"`, `/srv/a\b Site`},
		{`DocumentRoot "/srv/a\"b Site"`, `/srv/a"b Site`},
		{`DocumentRoot '/srv/a\'b Site'`, "/srv/a'b Site"},
		{`DocumentRoot "/srv/a\nb Site"`, `/srv/a\nb Site`},
	} {
		if got := extractApacheValue(tc.line); got != tc.want {
			t.Fatalf("%q got %q want %q", tc.line, got, tc.want)
		}
	}
	path := filepath.Join(t.TempDir(), "apache.conf")
	for _, quote := range []string{`"`, "'"} {
		data := "<VirtualHost *:443>\nServerName " + quote + "example.test" + quote + "\nDocumentRoot " + quote + "/srv/My Site" + quote + "\nSSLEngine on\nSSLCertificateFile " + quote + "/srv/My Certs/site.crt" + quote + "\nSSLCertificateKeyFile " + quote + "/srv/My Certs/site.key" + quote + "\n</VirtualHost>\n"
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		v := parseApacheConfig(f)
		f.Close()
		if len(v) != 1 || v[0].serverName != "example.test" || v[0].documentRoot != "/srv/My Site" || v[0].sslCertFile != "/srv/My Certs/site.crt" || v[0].sslKeyFile != "/srv/My Certs/site.key" || !v[0].sslEngine {
			t.Fatal(v)
		}
		out := convertApacheToYAML(v)
		for _, want := range []string{"host: example.test", "root: /srv/My Site", "cert: /srv/My Certs/site.crt", "key: /srv/My Certs/site.key"} {
			if !strings.Contains(out, want) {
				t.Fatal("conversion lost scalar", want, out)
			}
		}
	}
	fmt.Println("FIX VERIFIED")
}

package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// Round 176 (F41100-F41102): the nginx converter turned a whole static site
// into a redirect to /new/ because ONE location returned 301, proxied the
// whole site for a regex or exact proxy location, and gave every
// "server_name _" server the host example.com, which config validation
// rejects for the whole file.

func nginxLoad(t *testing.T, src string) (string, []string, *config.Config, error) {
	t.Helper()
	out, warns, err := NginxToYAMLWithWarnings(strings.NewReader(src))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	p := filepath.Join(t.TempDir(), "uwas.yaml")
	if err := os.WriteFile(p, []byte(out), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, lerr := config.Load(p)
	return out, warns, cfg, lerr
}

func TestNginxLocationRedirectDoesNotRedirectTheWholeSite(t *testing.T) {
	out, _, cfg, err := nginxLoad(t, "server {\n server_name a.test;\n root /var/www/a;\n location /old/ { return 301 /new/; }\n}\n")
	if err != nil {
		t.Fatalf("load: %v\n%s", err, out)
	}
	d := cfg.Domains[0]
	if d.Type != "static" {
		t.Fatalf("type = %q, want static: one redirecting location must not redirect every request\n%s", d.Type, out)
	}
	if len(d.Locations) != 1 || d.Locations[0].Match != "/old/" || d.Locations[0].Redirect != "/new/" || d.Locations[0].RedirectCode != 301 {
		t.Errorf("locations = %+v, want one /old/ -> /new/ 301\n%s", d.Locations, out)
	}

	// A redirect that needs a request variable cannot be carried; it is
	// reported, and the site still is not turned into a redirect.
	out, warns, cfg, err := nginxLoad(t, "server {\n server_name v.test;\n root /var/www/v;\n location /old/ { return 301 https://v.test$request_uri; }\n}\n")
	if err != nil {
		t.Fatalf("load: %v\n%s", err, out)
	}
	if cfg.Domains[0].Type != "static" || !strings.Contains(strings.Join(warns, "\n"), "return 301 https://v.test$request_uri") {
		t.Errorf("variable redirect: type=%q warnings=%q\n%s", cfg.Domains[0].Type, warns, out)
	}

	// Controls: a catch-all location and a server-level return still make a
	// redirect domain.
	for name, src := range map[string]string{
		"catch-all":    "server {\n server_name c.test;\n location / { return 301 https://c.example/; }\n}\n",
		"server level": "server {\n server_name s.test;\n return 301 https://s.example/;\n}\n",
	} {
		out, _, cfg, err := nginxLoad(t, src)
		if err != nil || cfg.Domains[0].Type != "redirect" {
			t.Errorf("control %s: type=%v err=%v\n%s", name, cfg.Domains[0].Type, err, out)
		}
	}
}

func TestNginxRegexAndExactProxyLocationsStayPathScoped(t *testing.T) {
	cases := map[string]struct{ body, match string }{
		"regex":            {"location ~ ^/api/ { proxy_pass http://127.0.0.1:9000; }\n", "~^/api/"},
		"case-insensitive": {"location ~* \\.json$ { proxy_pass http://127.0.0.1:9000; }\n", "~*\\.json$"},
		"exact":            {"location = /ping { proxy_pass http://127.0.0.1:9000; }\n", "~^/ping$"},
	}
	for name, c := range cases {
		out, _, cfg, err := nginxLoad(t, "server {\n server_name r.test;\n root /var/www/r;\n"+c.body+"}\n")
		if err != nil {
			t.Fatalf("%s: load: %v\n%s", name, err, out)
		}
		d := cfg.Domains[0]
		if d.Type != "static" {
			t.Errorf("%s: type = %q, want static (the whole site went to the API)\n%s", name, d.Type, out)
			continue
		}
		if len(d.Locations) != 1 || d.Locations[0].Match != c.match || d.Locations[0].ProxyPass != "http://127.0.0.1:9000" || d.Locations[0].StripPrefix {
			t.Errorf("%s: locations = %+v, want match %q, no strip\n%s", name, d.Locations, c.match, out)
		}
	}

	// Control: a proxied "/" is a proxy site.
	out, _, cfg, err := nginxLoad(t, "server {\n server_name p.test;\n root /var/www/p;\n location / { proxy_pass http://127.0.0.1:3000; }\n}\n")
	if err != nil || cfg.Domains[0].Type != "proxy" {
		t.Errorf("control: type=%v err=%v\n%s", cfg.Domains[0].Type, err, out)
	}
}

func TestNginxServerNameUnderscoreStaysLoadable(t *testing.T) {
	out, warns, cfg, err := nginxLoad(t, "server {\n listen 80 default_server;\n server_name _;\n root /var/www/d;\n}\nserver {\n listen 8080 default_server;\n server_name _;\n root /var/www/e;\n}\n")
	if err != nil {
		t.Fatalf("two catch-all servers: config does not load: %v\n%s", err, out)
	}
	if len(cfg.Domains) != 2 || cfg.Domains[0].Host == cfg.Domains[1].Host {
		t.Fatalf("hosts = %+v, want two distinct placeholders\n%s", cfg.Domains, out)
	}
	if !strings.Contains(strings.Join(warns, "\n"), "placeholder") {
		t.Errorf("placeholder hosts without a warning: %q", warns)
	}

	// Control: named servers are untouched and warning-free.
	_, warns, cfg, err = nginxLoad(t, "server {\n server_name a.test;\n root /var/www/a;\n}\n")
	if err != nil || cfg.Domains[0].Host != "a.test" || len(warns) != 0 {
		t.Errorf("control: host=%v warns=%q err=%v", cfg.Domains[0].Host, warns, err)
	}
}

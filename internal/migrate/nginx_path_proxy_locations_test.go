package migrate

// F40602: "root /var/www/site; location /api/ { proxy_pass … }" is a static
// site with one proxied path. The converter made the whole domain a proxy
// (every page and asset went to the API backend, root was dropped) or, on a PHP
// site, dropped the proxied path without a word. The Apache converter already
// uses path-scoped "locations" for this (F8601); the nginx one now does too.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

func TestNginxPathProxyKeepsSiteAndScopesProxy(t *testing.T) {
	d := proxyDomain(t, `
server {
    listen 80;
    server_name a.test;
    root /var/www/a;
    location /api/ {
        proxy_pass http://127.0.0.1:3000;
    }
    location /v1/ {
        proxy_pass http://127.0.0.1:4000/;
    }
    location /legacy/ {
        proxy_pass http://127.0.0.1:5000/app/;
    }
}
`)
	t.Logf("ACTUAL: type=%s root=%q upstreams=%v locations=%+v", d.Type, d.Root, addrs(d), d.Locations)
	if d.Type != "static" || d.Root != "/var/www/a" || len(d.Proxy.Upstreams) != 0 {
		t.Fatalf("PROBLEM CONFIRMED: site turned into %s (root %q, upstreams %v)", d.Type, d.Root, addrs(d))
	}
	want := []config.LocationConfig{
		{Match: "/api/", ProxyPass: "http://127.0.0.1:3000"},
		{Match: "/v1/", ProxyPass: "http://127.0.0.1:4000", StripPrefix: true},
		{Match: "/legacy/", ProxyPass: "http://127.0.0.1:5000/app", StripPrefix: true},
	}
	if len(d.Locations) != len(want) {
		t.Fatalf("locations = %+v", d.Locations)
	}
	for i, w := range want {
		g := d.Locations[i]
		if g.Match != w.Match || g.ProxyPass != w.ProxyPass || g.StripPrefix != w.StripPrefix {
			t.Errorf("location %d = %+v, want %+v", i, g, w)
		}
	}
}

func TestNginxPathProxyOnPHPSite(t *testing.T) {
	d := proxyDomain(t, `
server {
    listen 80;
    server_name a.test;
    root /var/www/a;
    location /ws/ {
        proxy_pass http://127.0.0.1:6001;
    }
    location ~ \.php$ {
        fastcgi_pass unix:/run/php/php-fpm.sock;
    }
}
`)
	if d.Type != "php" || len(d.Locations) != 1 || d.Locations[0].Match != "/ws/" {
		t.Fatalf("php site with a proxied path: type=%s locations=%+v", d.Type, d.Locations)
	}
}

func TestNginxPathProxyControlsKeepOldShape(t *testing.T) {
	// The root location proxied, a server-level proxy_pass and a site with no
	// root are still whole-domain proxies. (A regex or exact proxy location on
	// a site with a root is path-scoped too since F41101.)
	for name, ng := range map[string]string{
		"root location": "server {\nserver_name a.test;\nroot /var/www/a;\nlocation / {\nproxy_pass http://127.0.0.1:3000;\n}\n}\n",
		"server level":  "server {\nserver_name a.test;\nroot /var/www/a;\nproxy_pass http://127.0.0.1:3000;\n}\n",
		"no root":       "server {\nserver_name a.test;\nlocation /api/ {\nproxy_pass http://127.0.0.1:3000;\n}\n}\n",
	} {
		d := proxyDomain(t, ng)
		if d.Type != "proxy" || len(d.Locations) != 0 || len(d.Proxy.Upstreams) != 1 {
			t.Errorf("%s: type=%s locations=%+v upstreams=%v, want a plain proxy domain", name, d.Type, d.Locations, addrs(d))
		}
	}
}

func TestNginxPathProxyMigratedConfigLoads(t *testing.T) {
	y, _, err := NginxToYAMLWithWarnings(strings.NewReader(`
server {
    listen 80;
    server_name a.test;
    root /var/www/a;
    location /api/ {
        proxy_pass http://127.0.0.1:3000/;
    }
}
`))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "uwas.yaml")
	body := strings.Replace(y, "# Converted from Nginx config by UWAS\n", "", 1)
	if err := os.WriteFile(path, []byte("global:\n  admin:\n    enabled: false\n"+body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load: %v\n%s", err, y)
	}
	got := cfg.Domains[0]
	if got.Type != "static" || got.Root != "/var/www/a" || len(got.Locations) != 1 ||
		got.Locations[0].ProxyPass != "http://127.0.0.1:3000" || !got.Locations[0].StripPrefix {
		t.Errorf("after load: type=%s root=%q locations=%+v\n%s", got.Type, got.Root, got.Locations, y)
	}
}

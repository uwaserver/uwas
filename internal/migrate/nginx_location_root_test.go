package migrate

import (
	"strings"
	"testing"
)

// F42004: a `root` or `alias` inside a location was dropped, so on a proxied
// site every static path (/static/, /files/) was sent to the backend instead
// of being served from disk, with no warning. UWAS strips the matched prefix
// before joining a location root (nginx's alias semantics), so an nginx root
// needs the prefix appended.

func TestNginxLocationRootAndAliasAreCarried(t *testing.T) {
	out, warns, cfg, err := nginxLoad(t, "server {\n server_name app.test;\n location / { proxy_pass http://127.0.0.1:3000; }\n location /static/ { root /var/www/app; }\n location /files/ { alias /srv/files/; }\n}\n")
	if err != nil {
		t.Fatalf("converted config does not load: %v\n%s", err, out)
	}
	d := cfg.Domains[0]
	if d.Type != "proxy" || len(d.Proxy.Upstreams) != 1 {
		t.Fatalf("type=%s upstreams=%v, want the proxy kept\n%s", d.Type, d.Proxy.Upstreams, out)
	}
	got := map[string]string{}
	for _, l := range d.Locations {
		got[l.Match] = l.Root
	}
	if got["/static/"] != "/var/www/app/static" || got["/files/"] != "/srv/files" {
		t.Errorf("EXPECTED: /static/ -> /var/www/app/static, /files/ -> /srv/files\nACTUAL: %v\n%s", got, out)
	}
	if len(warns) != 0 {
		t.Errorf("unexpected warnings: %v", warns)
	}
}

// A static site keeps its own root and gains the extra location; a regex
// location with a root keeps the full path, so the root maps directly.
func TestNginxLocationRootOnStaticSiteAndRegex(t *testing.T) {
	out, _, cfg, err := nginxLoad(t, "server {\n server_name s.test;\n root /var/www/s;\n location /dl/ { root /data; }\n location ~* \\.(jpg|png)$ { root /var/www/images; }\n}\n")
	if err != nil {
		t.Fatalf("converted config does not load: %v\n%s", err, out)
	}
	d := cfg.Domains[0]
	if d.Type != "static" || d.Root != "/var/www/s" {
		t.Fatalf("type=%s root=%s, want the static site kept\n%s", d.Type, d.Root, out)
	}
	got := map[string]string{}
	for _, l := range d.Locations {
		got[l.Match] = l.Root
	}
	if got["/dl/"] != "/data/dl" {
		t.Errorf("/dl/ root = %q, want /data/dl\n%s", got["/dl/"], out)
	}
	if got["~*\\.(jpg|png)$"] != "/var/www/images" {
		t.Errorf("regex location root = %q, want /var/www/images\n%s", got["~*\\.(jpg|png)$"], out)
	}
}

// An alias on a regex location needs the capture group; it is reported, not
// guessed.
func TestNginxLocationAliasOnRegexIsReported(t *testing.T) {
	out, warns, _, err := nginxLoad(t, "server {\n server_name r.test;\n root /var/www/r;\n location ~ ^/img/(.*)$ { alias /srv/img/$1; }\n}\n")
	if err != nil {
		t.Fatalf("converted config does not load: %v\n%s", err, out)
	}
	if !strings.Contains(strings.Join(warns, "\n"), "alias") {
		t.Errorf("alias on a regex location not reported: %v\n%s", warns, out)
	}
}

// Control: a location with neither root nor alias adds nothing.
func TestNginxLocationWithoutRootAddsNothing(t *testing.T) {
	out, _, cfg, err := nginxLoad(t, "server {\n server_name c.test;\n root /var/www/c;\n location /x/ { expires 1d; }\n}\n")
	if err != nil {
		t.Fatalf("load: %v\n%s", err, out)
	}
	if len(cfg.Domains[0].Locations) != 0 {
		t.Errorf("locations = %+v, want none", cfg.Domains[0].Locations)
	}
}

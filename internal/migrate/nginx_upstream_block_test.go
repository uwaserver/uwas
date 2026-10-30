package migrate

// F40601: a named "upstream" block was dropped, so "proxy_pass http://backend;"
// became a proxy to the host name "backend" (it never resolves) and the
// block's servers, weights and balancing method were lost without a word.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

func proxyDomain(t *testing.T, ng string) config.Domain {
	t.Helper()
	cfgs, err := ParseNginx(strings.NewReader(ng))
	if err != nil {
		t.Fatal(err)
	}
	ds := ConvertToUWAS(cfgs)
	if len(ds) != 1 {
		t.Fatalf("got %d domains, want 1", len(ds))
	}
	return ds[0]
}

func addrs(d config.Domain) []string {
	var out []string
	for _, u := range d.Proxy.Upstreams {
		out = append(out, u.Address)
	}
	return out
}

func TestNginxUpstreamBlockIsResolved(t *testing.T) {
	d := proxyDomain(t, `
upstream backend {
    least_conn;
    server 127.0.0.1:3000 weight=3;
    server 127.0.0.1:3001;
    server 127.0.0.1:3002 backup;
    server 127.0.0.1:3003 down;
    server unix:/run/app.sock;
}
server {
    listen 80;
    server_name p.test;
    location / {
        proxy_pass http://backend;
    }
}
`)
	got := addrs(d)
	want := []string{"http://127.0.0.1:3000", "http://127.0.0.1:3001"}
	t.Logf("EXPECTED: %v\nACTUAL: %v", want, got)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("PROBLEM CONFIRMED: upstream block not resolved: %v, want %v", got, want)
	}
	if d.Proxy.Upstreams[0].Weight != 3 || d.Proxy.Upstreams[1].Weight != 1 {
		t.Errorf("weights = %d,%d want 3,1", d.Proxy.Upstreams[0].Weight, d.Proxy.Upstreams[1].Weight)
	}
	if d.Proxy.Algorithm != "least-conn" {
		t.Errorf("algorithm = %q, want least-conn", d.Proxy.Algorithm)
	}
}

func TestNginxUpstreamBlockForms(t *testing.T) {
	// One-line block, inside http {}, URI suffix kept, https scheme kept.
	d := proxyDomain(t, `
http {
    upstream api { server 10.1.2.3:8443; server 10.1.2.4:8443; }
    server {
        listen 80;
        server_name p.test;
        location / {
            proxy_pass https://api/v1/;
        }
    }
}
`)
	got := strings.Join(addrs(d), ",")
	want := "https://10.1.2.3:8443/v1/,https://10.1.2.4:8443/v1/"
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestNginxUpstreamControls(t *testing.T) {
	// A direct address and a name that is not an upstream group stay as written.
	d := proxyDomain(t, `
upstream backend { server 127.0.0.1:3000; }
server {
    listen 80;
    server_name p.test;
    location / {
        proxy_pass http://127.0.0.1:4000;
    }
}
`)
	if got := strings.Join(addrs(d), ","); got != "http://127.0.0.1:4000" {
		t.Errorf("direct address changed: %s", got)
	}
	d = proxyDomain(t, `
server {
    listen 80;
    server_name p.test;
    location / {
        proxy_pass http://app.internal:9000;
    }
}
`)
	if got := strings.Join(addrs(d), ","); got != "http://app.internal:9000" {
		t.Errorf("host name changed: %s", got)
	}
}

func TestNginxUpstreamWeightAndAlgorithmEmittedAsYAML(t *testing.T) {
	y, _, err := NginxToYAMLWithWarnings(strings.NewReader(`
upstream backend {
    ip_hash;
    server 127.0.0.1:3000 weight=2;
    server 127.0.0.1:3001;
}
server {
    listen 80;
    server_name p.test;
    location / { proxy_pass http://backend; }
}
`))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"algorithm: ip-hash", "weight: 2", "address: http://127.0.0.1:3001"} {
		if !strings.Contains(y, want) {
			t.Errorf("YAML lacks %q:\n%s", want, y)
		}
	}
}

// The converted file must also load and validate: the resolved servers are
// plain TCP addresses, so the migrated site comes up pointing at the real
// backends, with the weights and method intact.
func TestNginxUpstreamBlockMigratedConfigLoads(t *testing.T) {
	y, _, err := NginxToYAMLWithWarnings(strings.NewReader(`
upstream backend {
    least_conn;
    server 127.0.0.1:3000 weight=3;
    server 127.0.0.1:3001;
}
server {
    listen 80;
    server_name p.test;
    location / { proxy_pass http://backend; }
}
`))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "uwas.yaml")
	if err := os.WriteFile(path, []byte("global:\n  admin:\n    enabled: false\n"+strings.Replace(y, "# Converted from Nginx config by UWAS\n", "", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load: %v\n%s", err, y)
	}
	p := cfg.Domains[0].Proxy
	if len(p.Upstreams) != 2 || p.Upstreams[0].Weight != 3 || p.Algorithm != "least-conn" {
		t.Errorf("proxy after load = %+v", p)
	}
}

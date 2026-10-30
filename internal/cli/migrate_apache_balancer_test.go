package cli

import (
	"strings"
	"testing"
)

// F42005: `ProxyPass / balancer://cluster/` was copied as a literal
// balancer:// upstream, which config validation rejects ("unsupported URL
// scheme"), so the whole converted file failed to load. The cluster's
// BalancerMember entries are now carried over as upstreams.

func TestApacheBalancerMembersBecomeUpstreams(t *testing.T) {
	src := "<VirtualHost *:80>\nServerName app.test\nProxyPass / balancer://cluster/\nProxyPassReverse / balancer://cluster/\n</VirtualHost>\n" +
		// defined after the ProxyPass that uses it, like Apache allows
		"<Proxy balancer://cluster>\n BalancerMember http://10.0.0.1:8080 loadfactor=3\n BalancerMember http://10.0.0.2:8080\n ProxySet lbmethod=byrequests\n</Proxy>\n"
	out, cfg, err := apacheConvert(t, src)
	if err != nil {
		t.Fatalf("EXPECTED: converted config loads\nACTUAL: %v\n%s", err, out)
	}
	ups := cfg.Domains[0].Proxy.Upstreams
	if len(ups) != 2 || ups[0].Address != "http://10.0.0.1:8080" || ups[0].Weight != 3 || ups[1].Address != "http://10.0.0.2:8080" {
		t.Fatalf("upstreams = %+v, want 10.0.0.1:8080 (weight 3) and 10.0.0.2:8080\n%s", ups, out)
	}
}

func TestApacheBalancerInsideVHostAndPathScoped(t *testing.T) {
	src := "<VirtualHost *:80>\nServerName app.test\nDocumentRoot /var/www/app\n" +
		"<Proxy \"balancer://api\">\n BalancerMember http://10.0.0.5:9000\n BalancerMember http://10.0.0.6:9000\n</Proxy>\n" +
		"ProxyPass /api balancer://api\n</VirtualHost>\n"
	out, cfg, err := apacheConvert(t, src)
	if err != nil {
		t.Fatalf("converted config does not load: %v\n%s", err, out)
	}
	d := cfg.Domains[0]
	if d.Type == "proxy" || len(d.Locations) != 1 || d.Locations[0].ProxyPass != "http://10.0.0.5:9000" {
		t.Fatalf("locations = %+v (type %s), want one /api location proxied to the first member\n%s", d.Locations, d.Type, out)
	}
	if !strings.Contains(out, "only http://10.0.0.5:9000 was kept") {
		t.Errorf("dropping the second member is not reported:\n%s", out)
	}
}

// A cluster name with no <Proxy> block is reported, and the file still loads.
func TestApacheBalancerUndefinedClusterIsReported(t *testing.T) {
	out, _, err := apacheConvert(t, "<VirtualHost *:80>\nServerName app.test\nDocumentRoot /var/www/app\nProxyPass / balancer://ghost/\n</VirtualHost>\n")
	if err != nil {
		t.Fatalf("converted config does not load: %v\n%s", err, out)
	}
	if !strings.Contains(out, "balancer://ghost") {
		t.Errorf("undefined balancer not reported:\n%s", out)
	}
}

// Control: a plain backend and a non-balancer <Proxy *> section are unchanged.
func TestApacheBalancerControlPlainProxyUnchanged(t *testing.T) {
	out, cfg, err := apacheConvert(t, "<VirtualHost *:80>\nServerName plain.test\n<Proxy *>\n Require all granted\n</Proxy>\nProxyPass / http://127.0.0.1:3000/\n</VirtualHost>\n")
	if err != nil {
		t.Fatalf("converted config does not load: %v\n%s", err, out)
	}
	ups := cfg.Domains[0].Proxy.Upstreams
	if len(ups) != 1 || ups[0].Address != "http://127.0.0.1:3000/" || ups[0].Weight != 0 {
		t.Errorf("upstreams = %+v, want the single plain backend", ups)
	}
}

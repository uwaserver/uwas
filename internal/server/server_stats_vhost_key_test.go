package server

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// Per-domain analytics and metrics must be keyed by the vhost that served the
// request, not by the client-chosen Host header. Keyed by Host, every
// wildcard subdomain or arbitrary HTTPS Host allocated its own record
// (~400 KB each for analytics), and alias/www traffic was filed apart from
// the domain that owns it.
func TestRequestStatsKeyedByResolvedVHost(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newMinimalServer(&config.Config{
		Global: config.GlobalConfig{WebRoot: filepath.Dir(root)},
		Domains: []config.Domain{
			{Host: "tenant.com", Aliases: []string{"alias-of-tenant.com"}, Type: "static", Root: root, SSL: config.SSLConfig{Mode: "off"}},
			{Host: "*.wild.com", Type: "static", Root: root, SSL: config.SSLConfig{Mode: "off"}},
		},
	})
	do := func(host string, overTLS bool) {
		r := httptest.NewRequest(http.MethodGet, "/index.html", nil)
		r.Host = host
		if overTLS {
			r.TLS = &tls.ConnectionState{ServerName: "tenant.com"}
		}
		s.handleRequest(httptest.NewRecorder(), r)
	}
	for i := 0; i < 30; i++ {
		do(fmt.Sprintf("s%d.wild.com", i), false)
		do(fmt.Sprintf("probe%d.invalid", i), true)
	}
	do("alias-of-tenant.com", false)
	do("www.tenant.com", false)

	if n := len(s.analytics.GetAll()); n != 2 {
		t.Errorf("analytics records = %d, want 2 (tenant.com, *.wild.com)", n)
	}
	snap := s.metrics.DomainStatsSnapshot()
	if len(snap) != 2 {
		t.Errorf("metrics domain records = %d, want 2", len(snap))
	}
	if st := snap["*.wild.com"]; st == nil || st["requests"] != 30 {
		t.Errorf("*.wild.com metrics = %v, want 30 requests", st)
	}
	// 30 HTTPS probes served by the fallback + alias + www variant.
	if st := snap["tenant.com"]; st == nil || st["requests"] != 32 {
		t.Errorf("tenant.com metrics = %v, want 32 requests", st)
	}
}

package admin

// Regression test for F680/F681: with the patched handleBulkDomainImport, importing never adds a
// self-redirecting duplicate, apex and www both route to the imported site,
// www/apex variants of an existing or same-request host are skipped, and the
// resulting config always validates.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/metrics"
	"github.com/uwaserver/uwas/internal/router"
)

func bulkImportTestServer(t *testing.T) (*Server, string) {
	dir := t.TempDir()
	cfg := &config.Config{
		Global:  config.GlobalConfig{Admin: config.AdminConfig{Listen: "127.0.0.1:0"}, WebRoot: dir},
		Domains: []config.Domain{{Host: "existing.org", Type: "static", Root: dir, SSL: config.SSLConfig{Mode: "auto"}}},
	}
	return New(cfg, logger.New("error", "text"), metrics.New()), dir
}

func v680Post(s *Server, dir string, hosts ...string) (added, skipped []string, code int) {
	var body struct {
		Domains []map[string]string `json:"domains"`
	}
	for _, h := range hosts {
		body.Domains = append(body.Domains, map[string]string{"host": h, "type": "static", "root": dir})
	}
	b, _ := json.Marshal(body)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, withAdminContext(httptest.NewRequest("POST", "/api/v1/domains/import", strings.NewReader(string(b)))))
	var out struct {
		Added   []string `json:"added"`
		Skipped []string `json:"skipped"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out.Added, out.Skipped, rec.Code
}

func v680State(s *Server) ([]config.Domain, error) {
	s.configMu.RLock()
	domains := append([]config.Domain(nil), s.config.Domains...)
	s.configMu.RUnlock()
	vc := config.Config{
		Global:  config.GlobalConfig{LogLevel: "info", LogFormat: "json", Admin: config.AdminConfig{Listen: "127.0.0.1:9443"}, WebRoot: "/var/www"},
		Domains: domains,
	}
	return domains, config.ValidateWithDefaults(&vc)
}

func TestBulkImportNoSelfRedirectDuplicate(t *testing.T) {
	ok, bad := 0, 0
	check := func(name string, cond bool, detail string) {
		if cond {
			ok++
			fmt.Printf("OK       %s\n", name)
		} else {
			bad++
			fmt.Printf("MISMATCH %s: %s\n", name, detail)
		}
	}
	routesStatic := func(domains []config.Domain, want string, hosts ...string) bool {
		r := router.NewVHostRouter(domains)
		for _, h := range hosts {
			d := r.Lookup(h)
			if d == nil || d.Type != "static" || !strings.EqualFold(d.Host, want) {
				return false
			}
		}
		return true
	}

	// 1. original reproduction, apex + subdomain, each twice (repeat is skipped)
	for _, h := range []string{"newsite.com", "blog.example.net"} {
		s, dir := bulkImportTestServer(t)
		added, _, code := v680Post(s, dir, h)
		domains, err := v680State(s)
		check("import "+h+" status/added", code == http.StatusOK && len(added) == 1, fmt.Sprintf("code=%d added=%v", code, added))
		check("import "+h+" one domain, no redirect", len(domains) == 2 && domains[1].Type == "static", fmt.Sprintf("%+v", domains))
		check("import "+h+" config valid", err == nil, fmt.Sprint(err))
		check("import "+h+" apex+www route to site", routesStatic(domains, h, h, "www."+h), "router mismatch")
		added2, skipped2, _ := v680Post(s, dir, h)
		d2, err2 := v680State(s)
		check("re-import "+h+" skipped", len(added2) == 0 && len(skipped2) == 1 && len(d2) == 2 && err2 == nil, fmt.Sprintf("added=%v skipped=%v n=%d err=%v", added2, skipped2, len(d2), err2))
	}

	// 2. apex + www in one request -> www skipped
	{
		s, dir := bulkImportTestServer(t)
		added, skipped, _ := v680Post(s, dir, "pair.com", "www.pair.com")
		domains, err := v680State(s)
		check("apex+www same request", len(added) == 1 && len(skipped) == 1 && len(domains) == 2 && err == nil, fmt.Sprintf("added=%v skipped=%v err=%v", added, skipped, err))
	}
	// 3. www first (mixed case), then apex -> apex skipped, both names route
	{
		s, dir := bulkImportTestServer(t)
		added, skipped, _ := v680Post(s, dir, "WWW.Mixed.com", "mixed.com")
		domains, err := v680State(s)
		check("www-first mixed case", len(added) == 1 && len(skipped) == 1 && err == nil && routesStatic(domains, "www.mixed.com", "mixed.com", "www.mixed.com"), fmt.Sprintf("added=%v skipped=%v err=%v", added, skipped, err))
	}
	// 4. www variant of a pre-existing domain -> skipped
	{
		s, dir := bulkImportTestServer(t)
		added, skipped, _ := v680Post(s, dir, "www.existing.org")
		_, err := v680State(s)
		check("www of existing skipped", len(added) == 0 && len(skipped) == 1 && err == nil, fmt.Sprintf("added=%v skipped=%v err=%v", added, skipped, err))
	}
	// 5. empty host skipped, valid neighbour still added
	{
		s, dir := bulkImportTestServer(t)
		added, skipped, _ := v680Post(s, dir, "  ", "ok.com")
		_, err := v680State(s)
		check("empty host skipped", len(added) == 1 && len(skipped) == 1 && err == nil, fmt.Sprintf("added=%v skipped=%v err=%v", added, skipped, err))
	}
	// 6. 8 concurrent imports of the same host released together
	{
		s, dir := bulkImportTestServer(t)
		start := make(chan struct{})
		var wg sync.WaitGroup
		var mu sync.Mutex
		total := 0
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				a, _, _ := v680Post(s, dir, "race.com")
				mu.Lock()
				total += len(a)
				mu.Unlock()
			}()
		}
		close(start)
		wg.Wait()
		domains, err := v680State(s)
		check("concurrent import adds once", total == 1 && len(domains) == 2 && err == nil, fmt.Sprintf("total=%d n=%d err=%v", total, len(domains), err))
	}

	fmt.Printf("OK=%d MISMATCH=%d\n", ok, bad)
	if bad > 0 {
		t.Fatalf("FIX NOT VERIFIED")
	}
	fmt.Println("FIX VERIFIED")
}

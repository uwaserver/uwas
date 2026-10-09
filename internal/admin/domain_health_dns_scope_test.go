package admin

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/auth"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/dnsmanager"
)

// zoneWalkProvider resolves a name to its closest configured zone, like the
// real providers' FindZoneByDomain.
type zoneWalkProvider struct {
	zones   map[string]string
	records map[string][]dnsmanager.Record
}

func (p *zoneWalkProvider) ListZones() ([]dnsmanager.Zone, error) { return nil, nil }
func (p *zoneWalkProvider) ListRecords(id string) ([]dnsmanager.Record, error) {
	return append([]dnsmanager.Record(nil), p.records[id]...), nil
}
func (p *zoneWalkProvider) CreateRecord(string, dnsmanager.Record) (*dnsmanager.Record, error) {
	return nil, fmt.Errorf("unused")
}
func (p *zoneWalkProvider) UpdateRecord(string, string, dnsmanager.Record) (*dnsmanager.Record, error) {
	return nil, fmt.Errorf("unused")
}
func (p *zoneWalkProvider) DeleteRecord(string, string) error { return fmt.Errorf("unused") }
func (p *zoneWalkProvider) FindZoneByDomain(d string) (*dnsmanager.Zone, error) {
	parts := strings.Split(strings.ToLower(d), ".")
	for i := 0; i+1 < len(parts); i++ {
		if id, ok := p.zones[strings.Join(parts[i:], ".")]; ok {
			return &dnsmanager.Zone{ID: id, Name: strings.Join(parts[i:], ".")}, nil
		}
	}
	return nil, fmt.Errorf("zone not found for %s", d)
}

func multiUserTestServer(t *testing.T, domains ...config.Domain) *Server {
	t.Helper()
	s := testServerFromConfig(t, &config.Config{
		Global: config.GlobalConfig{
			Admin:   config.AdminConfig{Listen: "127.0.0.1:0", APIKey: "k-0123456789abcdef0123456789abcdef"},
			WebRoot: t.TempDir(), Users: config.UsersConfig{Enabled: true},
		},
		Domains: domains,
	})
	return s
}

// TestDNSRecordsSubdomainTenantSeesOnlyItsSubtree: a subdomain resolves to its
// parent zone, so a non-admin who manages only shop.example.com must not get
// the rest of the shared zone (other tenants' hosts, DKIM, ...).
func TestDNSRecordsSubdomainTenantSeesOnlyItsSubtree(t *testing.T) {
	prov := &zoneWalkProvider{
		zones: map[string]string{"example.com": "z1"},
		records: map[string][]dnsmanager.Record{"z1": {
			{Type: "A", Name: "example.com"}, {Type: "A", Name: "shop.example.com"},
			{Type: "TXT", Name: "_acme-challenge.shop.example.com"}, {Type: "A", Name: "blog.example.com"},
			{Type: "A", Name: "xshop.example.com"},
		}},
	}
	old := testDNSProviderHook
	testDNSProviderHook = func() dnsmanager.Provider { return prov }
	defer func() { testDNSProviderHook = old }()
	s := multiUserTestServer(t)
	s.ensureAuthManagerFromConfig()

	names := func(u *auth.User) []string {
		r := httptest.NewRequest("GET", "/api/v1/dns/shop.example.com/records", nil)
		r = r.WithContext(auth.WithUser(r.Context(), u))
		rec := httptest.NewRecorder()
		s.mux.ServeHTTP(rec, r)
		var body struct{ Records []dnsmanager.Record }
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		var out []string
		for _, r := range body.Records {
			out = append(out, r.Name)
		}
		sort.Strings(out)
		return out
	}
	got := names(&auth.User{Username: "t", Role: auth.RoleUser, Domains: []string{"shop.example.com"}})
	if want := "_acme-challenge.shop.example.com,shop.example.com"; strings.Join(got, ",") != want {
		t.Fatalf("tenant records = %v, want %s", got, want)
	}
	if got := names(&auth.User{Username: "a", Role: auth.RoleAdmin}); len(got) != 5 {
		t.Fatalf("admin records = %v, want whole zone", got)
	}
}

//go:noinline
func parkAfterRead(release <-chan struct{}) { <-release }

// waitParked polls goroutine stacks (no happens-before edge) until a
// goroutine sits in parkAfterRead, so -race sees the next write as
// unordered with the parked goroutine's earlier reads.
func waitParked(t *testing.T) {
	buf := make([]byte, 1<<20)
	for i := 0; i < 2000000; i++ {
		if n := runtime.Stack(buf, true); strings.Contains(string(buf[:n]), "parkAfterRead(") {
			return
		}
		runtime.Gosched()
	}
	t.Error("goroutine never parked")
}

// TestDomainDebugDoesNotHoldConfigPointer: Delete splices s.config.Domains in
// place; the debug handler must not read the entry after RUnlock (-race).
func TestDomainDebugDoesNotHoldConfigPointer(t *testing.T) {
	s := testServerFromConfig(t, &config.Config{
		Global: config.GlobalConfig{Admin: config.AdminConfig{Listen: "127.0.0.1:0"}, WebRoot: t.TempDir()},
		Domains: []config.Domain{
			{Host: "a.example.com", Type: "static", Root: t.TempDir()},
			{Host: "b.example.com", Type: "static", Root: t.TempDir()},
			{Host: "c.example.com", Type: "static", Root: t.TempDir()},
		},
	})
	release, finished := make(chan struct{}), make(chan struct{})
	var code int
	go func() {
		rec := httptest.NewRecorder()
		s.mux.ServeHTTP(rec, withAdminContext(httptest.NewRequest("GET", "/api/v1/domains/b.example.com/debug", nil)))
		code = rec.Code
		parkAfterRead(release)
		close(finished)
	}()
	waitParked(t)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, withAdminContext(httptest.NewRequest("DELETE", "/api/v1/domains/a.example.com?confirm=true", nil)))
	close(release)
	<-finished
	if rec.Code != 200 || code != 200 {
		t.Fatalf("delete=%d debug=%d", rec.Code, code)
	}
}

// TestDomainHealthReadsAuthMgrThroughGetter: the health handler must read the
// auth manager via getAuthMgr; a plain field read races the lazy init (-race).
func TestDomainHealthReadsAuthMgrThroughGetter(t *testing.T) {
	s := multiUserTestServer(t)
	s.setAuthMgr(nil)
	release, wDone, rDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		s.ensureAuthManagerFromConfig()
		parkAfterRead(release)
		close(wDone)
	}()
	go func() {
		defer close(rDone)
		waitParked(t)
		r := httptest.NewRequest("GET", "/api/v1/domains/health", nil)
		r = r.WithContext(auth.WithUser(r.Context(), &auth.User{Username: "u", Role: auth.RoleUser, Domains: []string{"a.example.com"}}))
		s.handleDomainHealth(httptest.NewRecorder(), r)
	}()
	<-rDone
	close(release)
	<-wDone
	if s.getAuthMgr() == nil {
		t.Fatal("auth manager not initialised")
	}
}

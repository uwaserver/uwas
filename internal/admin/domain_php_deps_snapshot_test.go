package admin

import (
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"

	"github.com/uwaserver/uwas/internal/auth"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/domainutil"
)

// TestConfigDomainsSnapshotSurvivesInPlaceWriters pins that the domain
// handler's ConfigDomains result is detached from s.config.Domains: Delete's
// in-place splice, SetDomainFPMAddress and the alias filter must not change
// a snapshot a reader is still iterating.
func TestConfigDomainsSnapshotSurvivesInPlaceWriters(t *testing.T) {
	s := testServerFromConfig(t, &config.Config{
		Global: config.GlobalConfig{Admin: config.AdminConfig{Listen: "127.0.0.1:0"}, WebRoot: t.TempDir()},
		Domains: []config.Domain{
			{Host: "a.example.com", Type: "static"},
			{Host: "b.example.com", Type: "static", Aliases: []string{"x.example.com", "y.example.com"}},
			{Host: "c.example.com", Type: "static"},
		},
	})

	paused := make(chan struct{})
	resume := make(chan struct{})
	seen := make(chan []config.Domain, 1)
	go func() {
		var got []config.Domain
		for i, d := range (&domainDeps{s: s}).ConfigDomains() {
			if i == 0 {
				close(paused)
				<-resume
			}
			got = append(got, d)
		}
		seen <- got
	}()
	<-paused

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, withAdminContext(httptest.NewRequest("DELETE", "/api/v1/domains/a.example.com?confirm=true", nil)))
	if rec.Code != 200 {
		t.Fatalf("delete status = %d: %s", rec.Code, rec.Body.String())
	}
	(&phpDeps{s: s}).SetDomainFPMAddress("c.example.com", "127.0.0.1:9000")
	s.configMu.Lock()
	s.config.Domains[0].Aliases = domainutil.RemoveDomainAlias(s.config.Domains[0].Aliases, "x.example.com")
	s.configMu.Unlock()
	close(resume)

	got := <-seen
	var hosts []string
	for _, d := range got {
		hosts = append(hosts, d.Host)
	}
	if want := []string{"a.example.com", "b.example.com", "c.example.com"}; !reflect.DeepEqual(hosts, want) {
		t.Fatalf("snapshot hosts = %v, want %v", hosts, want)
	}
	if got[1].Aliases[0] != "x.example.com" || got[2].PHP.FPMAddress != "" {
		t.Fatalf("snapshot mutated: aliases=%v fpm=%q", got[1].Aliases, got[2].PHP.FPMAddress)
	}
}

// TestPHPDepsCanManageDomainLazyAuthInit pins that phpDeps reads the auth
// manager through getAuthMgr; under -race a plain field read races the
// lazy init done by ensureAuthManagerFromConfig.
func TestPHPDepsCanManageDomainLazyAuthInit(t *testing.T) {
	s := testServerFromConfig(t, &config.Config{Global: config.GlobalConfig{
		Admin:   config.AdminConfig{Listen: "127.0.0.1:0", APIKey: "k-0123456789abcdef0123456789abcdef"},
		WebRoot: t.TempDir(),
		Users:   config.UsersConfig{Enabled: true},
	}})
	s.setAuthMgr(nil)

	r := httptest.NewRequest("GET", "/api/v1/php/domains", nil)
	r = r.WithContext(auth.WithUser(r.Context(), &auth.User{Username: "u", Role: auth.RoleUser, Domains: []string{"a.example.com"}}))
	d := &phpDeps{s: s}

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; _ = d.CanManageDomain(r, "b.example.com") }()
	}
	wg.Add(1)
	go func() { defer wg.Done(); <-start; s.ensureAuthManagerFromConfig() }()
	close(start)
	wg.Wait()

	if s.getAuthMgr() == nil {
		t.Fatal("auth manager was not initialised")
	}
	if d.CanManageDomain(r, "b.example.com") {
		t.Fatal("non-admin allowed a foreign domain after auth init")
	}
	if !d.CanManageDomain(r, "a.example.com") {
		t.Fatal("non-admin denied its own domain")
	}
}

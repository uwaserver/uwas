package admin

import (
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/uwaserver/uwas/internal/auth"
	"github.com/uwaserver/uwas/internal/config"
)

// TestAuthManagerLazyInitRace drives the lazy auth-manager initialization
// (the public bootstrap path's ensureAuthManagerFromConfig) concurrently with
// the per-request authorization path (requirePermission). Before authMu
// existed, the writer's plain `s.authMgr = mgr` raced the readers' plain
// field loads (handlers_auth.go:325 vs :155). Run under -race.
func TestAuthManagerLazyInitRace(t *testing.T) {
	s := &Server{config: &config.Config{}}
	s.config.Global.Users.Enabled = true
	s.config.Global.WebRoot = t.TempDir()

	user := &auth.User{ID: "u1", Username: "u1", Role: auth.RoleUser, Enabled: true}

	var wg sync.WaitGroup
	start := make(chan struct{})

	// Writers: the lazy init performed by handleAuthBootstrap.
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			s.ensureAuthManagerFromConfig()
		}()
	}
	// Readers: the authorization gate every authenticated request passes.
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			req := httptest.NewRequest("POST", "/api/v1/test", nil)
			req = req.WithContext(auth.WithUser(req.Context(), user))
			s.requirePermission(httptest.NewRecorder(), req, auth.PermDomainRead)
		}()
	}

	close(start)
	wg.Wait()

	// After the join, this read is properly synchronized (happens-before via
	// the WaitGroup) — it only asserts that the init actually happened.
	if s.authMgr == nil {
		t.Fatal("auth manager was not initialized")
	}
}

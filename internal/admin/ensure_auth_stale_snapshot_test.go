package admin

import (
	"testing"

	"github.com/uwaserver/uwas/internal/auth"
	"github.com/uwaserver/uwas/internal/config"
)

// TestEnsureAuthManagerAppliesSnapshotUnderConfigLock: F66600. The api_key
// snapshot was applied to the live manager after configMu was released, so a
// slower caller (the public bootstrap request, or a settings PUT) holding an
// older snapshot applied it over a newer rotation: the rotated-out key kept
// authenticating and the configured one was refused. Gated order: caller A is
// held right after its snapshot; a rotation that can take the config lock
// while A is held is the overlap that lost.
func TestEnsureAuthManagerAppliesSnapshotUnderConfigLock(t *testing.T) {
	const oldKey, newKey = "key-one-11111111111111", "key-two-22222222222222"
	for _, initMgr := range []bool{true, false} {
		s := &Server{config: &config.Config{}}
		s.config.Global.Users.Enabled = true
		s.config.Global.WebRoot = t.TempDir()
		s.config.Global.Admin.APIKey = "key-zero-000000000000"
		if initMgr {
			s.ensureAuthManagerFromConfig()
		}
		s.configMu.Lock()
		s.config.Global.Admin.APIKey = oldKey
		s.configMu.Unlock()

		reached, release := make(chan struct{}), make(chan struct{})
		first := true
		testEnsureAuthHook = func() {
			if first {
				first = false
				close(reached)
				<-release
			}
		}
		aDone := make(chan struct{})
		go func() { defer close(aDone); s.ensureAuthManagerFromConfig() }()
		<-reached

		rotate := func() {
			s.config.Global.Admin.APIKey = newKey
			s.configMu.Unlock()
			s.ensureAuthManagerFromConfig()
		}
		overlapped := s.configMu.TryLock()
		if overlapped {
			rotate()
		}
		close(release)
		<-aDone
		if !overlapped {
			s.configMu.Lock()
			rotate()
		}
		testEnsureAuthHook = nil

		mgr, ok := s.getAuthMgr().(*auth.Manager)
		if !ok {
			t.Fatalf("initMgr=%v: no auth manager", initMgr)
		}
		if _, err := mgr.AuthenticateAPIKey(newKey); err != nil {
			t.Errorf("initMgr=%v: configured key refused (overlapped=%v): %v", initMgr, overlapped, err)
		}
		if _, err := mgr.AuthenticateAPIKey(oldKey); err == nil {
			t.Errorf("initMgr=%v: rotated-out key still authenticates (overlapped=%v)", initMgr, overlapped)
		}
	}
}

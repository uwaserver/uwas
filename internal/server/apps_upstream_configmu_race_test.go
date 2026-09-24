package server

// Race regression test for the missing configMu guard around s.config.Domains
// in startRegisteredApps. Run with `-race`:
//   go test -race -v -count=1 -run TestStartRegisteredAppsConfigMu -timeout=60s ./internal/server/...
//
// Pre-fix expectation: FAIL — the Go race detector flags the unlocked
// pointer-deref in startRegisteredApps:93 racing the writer.
//
// Post-fix expectation: PASS — startRegisteredApps takes configMu.RLock()
// before touching s.config.Domains, so the concurrent writer sees no race.

import (
	"sync"
	"testing"
)

// hammerConfigMuRace hammers a goroutine calling the fixed
// startRegisteredApps() against a concurrent writer that replaces *s.config
// under configMu.Lock().  With the fix in place, startRegisteredApps
// holds configMu.RLock() for the entire s.config.Domains read, so the
// writer's pointer swap and the reader's domain snapshot are ordered by
// the lock and no race is reported.
func TestStartRegisteredAppsConfigMu(t *testing.T) {
	s, _ := bootServerWithApp(t)

	// Concurrent writer: mirror the exact sequence at server_reload.go:95-97.
	writer := func() {
		s.configMu.Lock()
		defer s.configMu.Unlock()
		newCfg := *s.config
		newCfg.Domains = nil // mutate so the writer is observable
		*s.config = newCfg
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// One writer goroutine hammering config replacement.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				writer()
			}
		}
	}()

	// The reader under test: call the actual startRegisteredApps(), which
	// with the fix takes configMu.RLock() before reading s.config.Domains.
	// If the fix is missing, the race detector flags the unlocked read.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				s.startRegisteredApps()
			}
		}
	}()

	// Run long enough for the race detector (GOMAXPROCS*N cycles) to
	// observe simultaneous unlock read / lock write if the fix is absent.
	for i := 0; i < 50_000; i++ {
		writer()
		s.startRegisteredApps()
	}
	close(stop)
	wg.Wait()
}

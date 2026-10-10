package admin

import (
	"sync"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/metrics"
)

// TestCloseKeepsLoginRateLimitUsable pins F716: Close nilled the rlMu-guarded
// rate-limit maps under auditMu, so a failed login after (or during) Close
// wrote into a nil map and panicked, and raced in-flight logins.
func TestCloseKeepsLoginRateLimitUsable(t *testing.T) {
	newSrv := func() *Server {
		cfg := &config.Config{Global: config.GlobalConfig{Admin: config.AdminConfig{Listen: "127.0.0.1:0", APIKey: "f716-key-0123456789abcdef"}}}
		return New(cfg, logger.New("error", "text"), metrics.New())
	}

	s := newSrv()
	s.Close()
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("failed login after Close panicked: %v", r)
			}
		}()
		s.recordAuthFailure("203.0.113.9", "alice")
		s.checkRateLimit("203.0.113.9", "alice")
	}()

	// Logins racing Close, released together; -race must stay quiet.
	s2 := newSrv()
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("login racing Close panicked: %v", r)
				}
			}()
			<-start
			s2.recordAuthFailure("198.51.100.1", "bob")
		}()
	}
	wg.Add(1)
	go func() { defer wg.Done(); <-start; s2.Close() }()
	close(start)
	wg.Wait()
}

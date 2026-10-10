package admin

import (
	"sync"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// F585: cfDeps.AddDomainIfAbsent must check and append under one configMu
// hold, so concurrent zone imports add a host exactly once.
func TestCFDepsAddDomainIfAbsentConcurrent(t *testing.T) {
	s := &Server{config: &config.Config{Domains: []config.Domain{{Host: "Existing.example.com"}}}}
	d := &cfDeps{s: s}
	if d.AddDomainIfAbsent(config.Domain{Host: "existing.example.com"}) {
		t.Fatal("existing host (different case) was added again")
	}

	const n = 16
	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if d.AddDomainIfAbsent(config.Domain{Host: "race.example.com"}) {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()

	s.configMu.RLock()
	defer s.configMu.RUnlock()
	count := 0
	for _, x := range s.config.Domains {
		if x.Host == "race.example.com" {
			count++
		}
	}
	if wins != 1 || count != 1 {
		t.Fatalf("wins=%d count=%d, want 1/1", wins, count)
	}
}

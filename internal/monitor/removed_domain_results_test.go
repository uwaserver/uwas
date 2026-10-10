package monitor

// Regression test: UpdateDomains drops results of removed domains, a re-added
// domain starts fresh, and a check in flight when its domain is removed does
// not bring the result back. Gated with channels; no sleeps.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

func TestUpdateDomainsDropsRemovedDomainResults(t *testing.T) {
	var gateMu sync.Mutex
	var entered chan struct{}
	var release chan struct{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gateMu.Lock()
		e, rel := entered, release
		gateMu.Unlock()
		if e != nil && strings.HasPrefix(r.Host, "127.0.0.1") {
			close(e)
			<-rel
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	port := srv.URL[strings.LastIndex(srv.URL, ":"):]
	a := config.Domain{Host: "127.0.0.1" + port, SSL: config.SSLConfig{Mode: "off"}}
	b := config.Domain{Host: "localhost" + port, SSL: config.SSLConfig{Mode: "off"}}
	ctx := context.Background()

	ok, bad := 0, 0
	check := func(name string, cond bool, detail string) {
		if cond {
			ok++
			fmt.Printf("OK %s\n", name)
		} else {
			bad++
			fmt.Printf("MISMATCH %s: %s\n", name, detail)
		}
	}
	has := func(m *Monitor, host string) (HealthResult, bool) {
		for _, r := range m.Results() {
			if r.Host == host {
				return r, true
			}
		}
		return HealthResult{}, false
	}

	// 1. Removed domain dropped; repeated twice.
	for i := 0; i < 2; i++ {
		m := New([]config.Domain{a, b}, testLogger())
		m.sweep(ctx)
		m.UpdateDomains([]config.Domain{b})
		_, ga := has(m, a.Host)
		_, gb := has(m, b.Host)
		check(fmt.Sprintf("removed-dropped#%d", i), !ga && gb, fmt.Sprintf("a=%v b=%v", ga, gb))
	}

	// 2. Unchanged list keeps results and history.
	{
		m := New([]config.Domain{a, b}, testLogger())
		m.sweep(ctx)
		m.sweep(ctx)
		m.UpdateDomains([]config.Domain{a, b})
		ra, ga := has(m, a.Host)
		check("unchanged-keeps", ga && len(ra.Checks) == 2, fmt.Sprintf("present=%v checks=%d", ga, len(ra.Checks)))
	}

	// 3. Re-added starts fresh (no inherited failure streak).
	{
		m := New([]config.Domain{a}, testLogger())
		m.sweep(ctx)
		m.sweep(ctx)
		m.UpdateDomains(nil)
		m.UpdateDomains([]config.Domain{a})
		m.sweep(ctx)
		ra, _ := has(m, a.Host)
		check("readded-fresh", len(ra.Checks) == 1 && ra.consecutiveFail == 1, fmt.Sprintf("checks=%d fails=%d", len(ra.Checks), ra.consecutiveFail))
	}

	// 4. Empty list clears everything.
	{
		m := New([]config.Domain{a, b}, testLogger())
		m.sweep(ctx)
		m.UpdateDomains(nil)
		check("empty-clears", len(m.Results()) == 0, fmt.Sprintf("results=%d", len(m.Results())))
	}

	// 5. Gated: a check in flight when its domain is removed is not stored.
	{
		m := New([]config.Domain{a, b}, testLogger())
		gateMu.Lock()
		entered, release = make(chan struct{}), make(chan struct{})
		e, rel := entered, release
		gateMu.Unlock()
		done := make(chan struct{})
		go func() { m.checkDomain(ctx, a); close(done) }()
		<-e
		m.UpdateDomains([]config.Domain{b})
		close(rel)
		<-done
		gateMu.Lock()
		entered, release = nil, nil
		gateMu.Unlock()
		_, ga := has(m, a.Host)
		check("inflight-not-resurrected", !ga, "removed host stored after in-flight check")
	}

	// 6. Gated control: in-flight check of a still-configured domain is stored
	//    even when the list changed meanwhile.
	{
		m := New([]config.Domain{a, b}, testLogger())
		gateMu.Lock()
		entered, release = make(chan struct{}), make(chan struct{})
		e, rel := entered, release
		gateMu.Unlock()
		done := make(chan struct{})
		go func() { m.checkDomain(ctx, a); close(done) }()
		<-e
		m.UpdateDomains([]config.Domain{a})
		close(rel)
		<-done
		gateMu.Lock()
		entered, release = nil, nil
		gateMu.Unlock()
		_, ga := has(m, a.Host)
		check("inflight-kept-when-configured", ga, "configured host dropped")
	}

	// 7. Direct check with no list change still records (existing semantics).
	{
		m := New(nil, testLogger())
		m.checkDomain(ctx, b)
		_, gb := has(m, b.Host)
		check("direct-check-records", gb, "not recorded")
	}

	// 8. Concurrency: 16 sweeps and 16 updates released together.
	{
		m := New([]config.Domain{a, b}, testLogger())
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < 16; i++ {
			wg.Add(2)
			go func() { defer wg.Done(); <-start; m.sweep(ctx) }()
			go func(i int) {
				defer wg.Done()
				<-start
				if i%2 == 0 {
					m.UpdateDomains([]config.Domain{b})
				} else {
					m.UpdateDomains([]config.Domain{a, b})
				}
			}(i)
		}
		close(start)
		wg.Wait()
		m.UpdateDomains([]config.Domain{b})
		_, ga := has(m, a.Host)
		check("concurrent-final-state", !ga, "a present after final update")
	}

	fmt.Printf("OK=%d MISMATCH=%d\n", ok, bad)
	if bad > 0 {
		t.Fatalf("FIX NOT VERIFIED")
	}
	fmt.Println("FIX VERIFIED")
}

package cloudflare

// Regression test for F585: two overlapping zone imports both passed the
// ExistingDomains snapshot check and each appended the same host. The handler
// must re-check through AddDomainIfAbsent when the adapter provides it.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

type zdDeps struct {
	*tsDeps
	mu      sync.Mutex
	domains []config.Domain
}

func (d *zdDeps) FetchDNSRecords(string, string) ([]DNSRecord, error) {
	return []DNSRecord{{Type: "A", Name: "dup.example.com"}}, nil
}
func (d *zdDeps) ExistingDomains() map[string]bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	m := map[string]bool{}
	for _, x := range d.domains {
		m[strings.ToLower(x.Host)] = true
	}
	return m
}
func (d *zdDeps) AddDomain(dom config.Domain) {
	d.mu.Lock()
	d.domains = append(d.domains, dom)
	d.mu.Unlock()
}
func (d *zdDeps) AddDomainIfAbsent(dom config.Domain) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, x := range d.domains {
		if strings.EqualFold(x.Host, dom.Host) {
			return false
		}
	}
	d.domains = append(d.domains, dom)
	return true
}

func TestZoneImportConcurrentImportsAddHostOnce(t *testing.T) {
	d := &zdDeps{tsDeps: newTSDeps(&State{Connected: true, Token: "t", AccountID: "a"})}
	h := New(d)

	const n = 2
	arrived := make(chan struct{}, n)
	release := make(chan struct{})
	orig := MkdirAllFn
	t.Cleanup(func() { MkdirAllFn = orig })
	// MkdirAllFn runs after the snapshot check and before the append, so
	// holding both imports here forces the stale-snapshot interleaving.
	MkdirAllFn = func(string, os.FileMode) error {
		arrived <- struct{}{}
		<-release
		return nil
	}

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.ZoneImport(httptest.NewRecorder(), tsReq(http.MethodPost, `{}`, "z1"))
		}()
	}
	for i := 0; i < n; i++ {
		<-arrived
	}
	close(release)
	wg.Wait()

	d.mu.Lock()
	defer d.mu.Unlock()
	count := 0
	for _, x := range d.domains {
		if x.Host == "dup.example.com" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("dup.example.com added %d times by overlapping imports, want 1", count)
	}
}

package router

import (
	"fmt"
	"sync"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// Regression test for F830: derived keys never outrank explicit keys; a www/apex variant
// outranks the bare key of a port-qualified host in either config order; the
// port-qualified domain keeps its own port; existing precedence is unchanged.
func TestPortBareKeyYieldsToWWWVariant(t *testing.T) {
	A := func(h string, al ...string) config.Domain { return config.Domain{Host: h, Aliases: al, Root: "A"} }
	B := func(h string, al ...string) config.Domain { return config.Domain{Host: h, Aliases: al, Root: "B"} }
	ok, bad := 0, 0
	check := func(label string, r *VHostRouter, host, want string, wantCfg bool) {
		for i := 0; i < 2; i++ {
			d, cfg := r.LookupWithStatus(host)
			got := "<nil>"
			if d != nil {
				got = d.Root
			}
			if got != want || cfg != wantCfg {
				bad++
				fmt.Printf("MISMATCH %s host=%q want=%s/%v got=%s/%v\n", label, host, want, wantCfg, got, cfg)
				return
			}
		}
		ok++
	}
	both := func(label string, doms []config.Domain, fn func(string, *VHostRouter)) {
		fn(label+" fwd", NewVHostRouter(doms))
		rev := make([]config.Domain, len(doms))
		for i := range doms {
			rev[len(doms)-1-i] = doms[i]
		}
		fn(label+" rev", NewVHostRouter(rev))
	}

	// Reproduction: www/apex variant vs port-derived bare key, both orders.
	both("www-port+apex", []config.Domain{B("www.example.com:8080"), A("example.com")}, func(l string, r *VHostRouter) {
		check(l, r, "www.example.com", "A", true)
		check(l, r, "www.example.com:443", "A", true)
		check(l, r, "WWW.Example.com.", "A", true)
		check(l, r, "www.example.com:8080", "B", true)
		check(l, r, "example.com", "A", true)
	})
	both("apex-port+www", []config.Domain{B("example.com:8080"), A("www.example.com")}, func(l string, r *VHostRouter) {
		check(l, r, "example.com", "A", true)
		check(l, r, "example.com:443", "A", true)
		check(l, r, "example.com:8080", "B", true)
		check(l, r, "www.example.com", "A", true)
	})
	// F227 still holds: explicit bare host beats the port-derived key.
	both("F227", []config.Domain{A("example.com"), B("example.com:8080")}, func(l string, r *VHostRouter) {
		check(l, r, "example.com", "A", true)
		check(l, r, "example.com:443", "A", true)
		check(l, r, "example.com:8080", "B", true)
		check(l, r, "www.example.com", "A", true)
	})
	// Explicit alias beats both derived kinds.
	both("alias", []config.Domain{A("www.example.com:8080"), B("other.test", "www.example.com")}, func(l string, r *VHostRouter) {
		check(l, r, "www.example.com", "B", true)
		check(l, r, "www.example.com:8080", "A", true)
	})
	// Unclaimed: a port-only domain is still reachable without the port.
	check("port-only", NewVHostRouter([]config.Domain{B("example.com:8080")}), "example.com", "B", true)
	check("port-only", NewVHostRouter([]config.Domain{B("example.com:8080")}), "example.com:9", "B", true)
	// Two port hosts with the same bare host: first listed keeps the bare key.
	r2 := NewVHostRouter([]config.Domain{A("example.com:8080"), B("example.com:9090")})
	check("two-ports", r2, "example.com", "A", true)
	check("two-ports", r2, "example.com:9090", "B", true)
	// Implicit vs explicit www/apex unchanged (explicit wins, both orders).
	both("explicit-www", []config.Domain{A("www.x.test"), B("x.test")}, func(l string, r *VHostRouter) {
		check(l, r, "x.test", "B", true)
		check(l, r, "www.x.test", "A", true)
	})
	// Wildcard loses to a derived www variant only where it did before.
	both("wildcard", []config.Domain{A("example.com"), B("*.example.com")}, func(l string, r *VHostRouter) {
		check(l, r, "www.example.com", "A", true)
		check(l, r, "shop.example.com", "B", true)
	})
	// Unknown host still falls back unconfigured.
	r3 := NewVHostRouter([]config.Domain{B("www.example.com:8080"), A("example.com")})
	check("unknown", r3, "nope.test", "B", false)
	// Empty host/alias edge case.
	check("empty", NewVHostRouter([]config.Domain{{Host: " . ", Aliases: []string{"real.com"}, Root: "A"}}), "real.com", "A", true)

	// Update swaps precedence atomically; concurrent lookups see old or new.
	r := NewVHostRouter([]config.Domain{A("example.com")})
	newDoms := []config.Domain{B("www.example.com:8080"), A("example.com")}
	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	wrong := 0
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if d := r.Lookup("www.example.com"); d == nil || d.Root != "A" {
				mu.Lock()
				wrong++
				mu.Unlock()
			}
		}()
	}
	wg.Add(1)
	go func() { defer wg.Done(); <-start; r.Update(newDoms) }()
	close(start)
	wg.Wait()
	if wrong != 0 {
		bad++
		fmt.Printf("MISMATCH concurrent: %d lookups not A\n", wrong)
	} else {
		ok++
	}
	check("after-update", r, "www.example.com", "A", true)
	check("after-update", r, "www.example.com:8080", "B", true)

	fmt.Printf("OK=%d MISMATCH=%d\n", ok, bad)
	if bad != 0 {
		t.Fatal("FIX NOT VERIFIED")
	}
	fmt.Println("FIX VERIFIED")
}

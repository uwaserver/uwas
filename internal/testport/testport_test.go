package testport

import (
	"net"
	"sync"
	"testing"
)

// The reserved address refuses connections and a server can still take it.
func TestDeadRefusesAndFreeIsBindable(t *testing.T) {
	addr := Dead(t)
	if c, err := net.Dial("tcp", addr); err == nil {
		c.Close()
		t.Fatalf("Dial(%s) succeeded, want connection refused", addr)
	}

	free := FreeAddr(t)
	ln, err := net.Listen("tcp", free)
	if err != nil {
		t.Fatalf("a server must be able to bind the reserved port: %v", err)
	}
	defer ln.Close()
	c, err := net.Dial("tcp", free)
	if err != nil {
		t.Fatalf("server on %s not reachable: %v", free, err)
	}
	c.Close()
}

// Other binders never get a reserved port, unlike listen-then-close.
func TestReservedPortIsNotReissued(t *testing.T) {
	const rounds, claimants, per = 100, 16, 30
	for r := 0; r < rounds; r++ {
		port := portOf(reserve(t))
		var mu sync.Mutex
		stolen := false
		var wg sync.WaitGroup
		for i := 0; i < claimants; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				var held []net.Listener
				for j := 0; j < per; j++ {
					ln, err := net.Listen("tcp", "127.0.0.1:0")
					if err != nil {
						continue
					}
					held = append(held, ln)
					if ln.Addr().(*net.TCPAddr).Port == port {
						mu.Lock()
						stolen = true
						mu.Unlock()
					}
				}
				for _, l := range held {
					l.Close()
				}
			}()
		}
		wg.Wait()
		if stolen {
			t.Fatalf("round %d: reserved port %d was handed to another listener", r, port)
		}
	}
}

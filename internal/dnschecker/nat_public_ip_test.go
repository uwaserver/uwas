package dnschecker

// F46600: behind 1:1 NAT (EC2, GCP, Azure, Oracle ...) the server's public
// address is on no interface. Check compared the resolved records with the
// interface addresses only, so a correctly configured domain was reported as
// "does not point here" and the page recommended the private interface address
// as the A record.

import (
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

func natStubs(t *testing.T, resolved, iface string) {
	t.Helper()
	saveAndRestore(t)
	lookupHost = func(string) ([]string, error) { return []string{resolved}, nil }
	lookupCNAME = func(h string) (string, error) { return h + ".", nil }
	lookupMX = func(string) ([]*net.MX, error) { return nil, nil }
	lookupNS = func(string) ([]*net.NS, error) { return nil, nil }
	lookupTXT = func(string) ([]string, error) { return nil, nil }
	interfaceAddrs = func() ([]net.Addr, error) {
		return []net.Addr{&net.IPNet{IP: net.ParseIP(iface), Mask: net.CIDRMask(24, 32)}}, nil
	}
}

func TestCheckBehindNATUsesPublicIP(t *testing.T) {
	natStubs(t, "203.0.113.7", "10.0.0.5")
	publicIP = func() string { return "203.0.113.7" }

	r := Check("site.test")
	if !r.PointsHere {
		t.Errorf("points_here = false, want true (server_ips=%v)", r.ServerIPs)
	}
	if len(r.ServerIPs) != 2 || r.ServerIPs[0] != "203.0.113.7" || r.ServerIPs[1] != "10.0.0.5" {
		t.Errorf("server_ips = %v, want the public address first, then the interface one", r.ServerIPs)
	}
}

// The recommendation must name the public address even when the lookup fails,
// which is when the dashboard shows its "How to fix" guidance.
func TestCheckBehindNATLookupFailureStillLeadsWithPublicIP(t *testing.T) {
	natStubs(t, "203.0.113.7", "10.0.0.5")
	lookupHost = func(string) ([]string, error) { return nil, errors.New("no such host") }
	publicIP = func() string { return "203.0.113.7" }

	r := Check("new.test")
	if r.Error == "" || r.PointsHere {
		t.Fatalf("lookup failure not reported: %+v", r)
	}
	if len(r.ServerIPs) == 0 || r.ServerIPs[0] != "203.0.113.7" {
		t.Errorf("server_ips = %v, want the public address first", r.ServerIPs)
	}
}

// A host whose interface carries a public address needs no outside lookup.
func TestCheckPublicInterfaceMakesNoExternalLookup(t *testing.T) {
	natStubs(t, "198.51.100.20", "198.51.100.20")
	var calls atomic.Int64
	publicIP = func() string { calls.Add(1); return "192.0.2.1" }

	r := Check("direct.test")
	if !r.PointsHere || len(r.ServerIPs) != 1 {
		t.Errorf("direct host: %+v", r)
	}
	if calls.Load() != 0 {
		t.Errorf("public IP looked up %d times for a host with a public interface", calls.Load())
	}
}

// Behind NAT a domain that resolves elsewhere is still not pointing here.
func TestCheckBehindNATResolvesElsewhere(t *testing.T) {
	natStubs(t, "192.0.2.99", "10.0.0.5")
	publicIP = func() string { return "203.0.113.7" }

	if r := Check("elsewhere.test"); r.PointsHere {
		t.Errorf("points_here = true for a domain resolving to %v", r.A)
	}
}

// CGNAT (RFC 6598) is not public: a host with only that address is behind NAT.
func TestCheckCGNATInterfaceCountsAsNAT(t *testing.T) {
	natStubs(t, "203.0.113.7", "100.64.1.5")
	publicIP = func() string { return "203.0.113.7" }

	if r := Check("cgnat.test"); !r.PointsHere {
		t.Errorf("points_here = false behind CGNAT (server_ips=%v)", r.ServerIPs)
	}
}

// An unreachable or garbage answer leaves the interface addresses as before.
func TestCheckPublicIPUnavailableOrGarbage(t *testing.T) {
	for name, answer := range map[string]string{"empty": "", "html": "<html>429</html>", "text": "not an ip"} {
		t.Run(name, func(t *testing.T) {
			natStubs(t, "203.0.113.7", "10.0.0.5")
			publicIP = func() string { return answer }
			r := Check("site.test")
			if r.PointsHere || len(r.ServerIPs) != 1 || r.ServerIPs[0] != "10.0.0.5" {
				t.Errorf("answer %q changed the result: %+v", answer, r)
			}
		})
	}
}

// The detection call is bounded and cached: a hung service neither stalls Check
// nor is retried on every check.
func TestCheckPublicIPLookupBoundedAndCached(t *testing.T) {
	natStubs(t, "203.0.113.7", "10.0.0.5")
	publicIPTimeout = 20 * time.Millisecond
	t.Cleanup(func() { publicIPTimeout = 3 * time.Second })
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	var calls atomic.Int64
	publicIP = func() string { calls.Add(1); <-release; return "203.0.113.7" }

	for i := 0; i < 3; i++ {
		done := make(chan Result, 1)
		go func() { done <- Check("site.test") }()
		select {
		case r := <-done:
			if r.PointsHere {
				t.Errorf("check %d: points_here with no public address", i)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("check %d stalled on the hung detection call", i)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("hung detection retried: %d calls for 3 checks, want 1 (miss is cached)", got)
	}
}

func TestCheckPublicIPSuccessIsCached(t *testing.T) {
	natStubs(t, "203.0.113.7", "10.0.0.5")
	var calls atomic.Int64
	publicIP = func() string { calls.Add(1); return "203.0.113.7" }

	for i := 0; i < 4; i++ {
		if r := Check("site.test"); !r.PointsHere {
			t.Fatalf("check %d: points_here = false", i)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("public IP looked up %d times for 4 checks, want 1", got)
	}
}

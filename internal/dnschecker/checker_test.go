package dnschecker

import (
	"fmt"
	"net"
	"testing"
	"time"
)

func TestCheckLocalhost(t *testing.T) {
	r := Check("localhost")

	if r.Domain != "localhost" {
		t.Errorf("expected domain 'localhost', got %q", r.Domain)
	}

	// localhost should resolve to at least one address (127.0.0.1 or ::1)
	if r.Error != "" && len(r.A) == 0 && len(r.AAAA) == 0 {
		t.Logf("DNS lookup for localhost returned error (may be OK in some environments): %s", r.Error)
	}
}

func TestGetServerIPs(t *testing.T) {
	ips := getServerIPs()
	// On most systems, there should be at least one non-loopback IP.
	// However, in some CI/container environments there may be none.
	if len(ips) == 0 {
		t.Log("getServerIPs returned no IPs (may be OK in restricted environments)")
	}
	for _, ip := range ips {
		if ip == "127.0.0.1" || ip == "::1" {
			t.Errorf("getServerIPs should not return loopback address %q", ip)
		}
	}
}

func TestResultStruct(t *testing.T) {
	r := Result{
		Domain:     "example.com",
		A:          []string{"93.184.216.34"},
		AAAA:       []string{"2606:2800:220:1:248:1893:25c8:1946"},
		CNAME:      "",
		MX:         []string{"mx.example.com (priority 10)"},
		NS:         []string{"ns1.example.com"},
		TXT:        []string{"v=spf1 -all"},
		PointsHere: false,
		ServerIPs:  []string{"10.0.0.1"},
	}

	if r.Domain != "example.com" {
		t.Errorf("expected Domain 'example.com', got %q", r.Domain)
	}
	if len(r.A) != 1 {
		t.Errorf("expected 1 A record, got %d", len(r.A))
	}
	if len(r.AAAA) != 1 {
		t.Errorf("expected 1 AAAA record, got %d", len(r.AAAA))
	}
	if r.PointsHere {
		t.Error("expected PointsHere=false")
	}
}

func TestCheckInvalidDomain(t *testing.T) {
	r := Check("this-domain-definitely-does-not-exist-uwas-test.invalid")

	if r.Error == "" {
		t.Log("expected an error for invalid domain, but lookup succeeded (DNS may be doing wildcard resolution)")
	}
}

// --- Mock-based tests for full coverage ---

// saveAndRestore saves all hook variables and returns a function that restores them.
func saveAndRestore(t *testing.T) {
	origHost := lookupHost
	origCNAME := lookupCNAME
	origMX := lookupMX
	origNS := lookupNS
	origTXT := lookupTXT
	origAddrs := interfaceAddrs
	origTimeout := txtTimeout
	t.Cleanup(func() {
		lookupHost = origHost
		lookupCNAME = origCNAME
		lookupMX = origMX
		lookupNS = origNS
		lookupTXT = origTXT
		interfaceAddrs = origAddrs
		txtTimeout = origTimeout
	})
}

func TestCheckWithMockDNS_IPv4AndIPv6(t *testing.T) {
	saveAndRestore(t)

	interfaceAddrs = func() ([]net.Addr, error) {
		return []net.Addr{
			&net.IPNet{IP: net.ParseIP("10.0.0.1"), Mask: net.CIDRMask(24, 32)},
		}, nil
	}
	lookupHost = func(host string) ([]string, error) {
		return []string{"10.0.0.1", "2001:db8::1"}, nil
	}
	lookupCNAME = func(host string) (string, error) {
		return "alias.example.com.", nil
	}
	lookupMX = func(name string) ([]*net.MX, error) {
		return []*net.MX{{Host: "mail.example.com.", Pref: 10}}, nil
	}
	lookupNS = func(name string) ([]*net.NS, error) {
		return []*net.NS{{Host: "ns1.example.com."}}, nil
	}
	lookupTXT = func(name string) ([]string, error) {
		return []string{"v=spf1 include:example.com ~all"}, nil
	}

	r := Check("example.com")

	if r.Domain != "example.com" {
		t.Errorf("Domain = %q", r.Domain)
	}
	if r.Error != "" {
		t.Errorf("unexpected error: %s", r.Error)
	}
	if len(r.A) != 1 || r.A[0] != "10.0.0.1" {
		t.Errorf("A = %v, want [10.0.0.1]", r.A)
	}
	if len(r.AAAA) != 1 || r.AAAA[0] != "2001:db8::1" {
		t.Errorf("AAAA = %v, want [2001:db8::1]", r.AAAA)
	}
	if !r.PointsHere {
		t.Error("expected PointsHere=true since 10.0.0.1 is a server IP")
	}
	if r.CNAME != "alias.example.com" {
		t.Errorf("CNAME = %q, want alias.example.com", r.CNAME)
	}
	if len(r.MX) != 1 {
		t.Errorf("MX count = %d, want 1", len(r.MX))
	}
	if len(r.NS) != 1 || r.NS[0] != "ns1.example.com" {
		t.Errorf("NS = %v", r.NS)
	}
	if len(r.TXT) != 1 {
		t.Errorf("TXT count = %d, want 1", len(r.TXT))
	}
}

func TestCheckLookupHostError(t *testing.T) {
	saveAndRestore(t)

	interfaceAddrs = func() ([]net.Addr, error) {
		return nil, nil
	}
	lookupHost = func(host string) ([]string, error) {
		return nil, fmt.Errorf("no such host")
	}

	r := Check("bad.example.com")
	if r.Error == "" {
		t.Error("expected error when LookupHost fails")
	}
	if len(r.A) != 0 {
		t.Errorf("A should be empty, got %v", r.A)
	}
}

func TestCheckCNAMEMatchesDomain(t *testing.T) {
	saveAndRestore(t)

	interfaceAddrs = func() ([]net.Addr, error) {
		return nil, nil
	}
	lookupHost = func(host string) ([]string, error) {
		return []string{"1.2.3.4"}, nil
	}
	// CNAME equals domain+"." — should NOT be set
	lookupCNAME = func(host string) (string, error) {
		return "test.example.com.", nil
	}
	lookupMX = func(name string) ([]*net.MX, error) {
		return nil, fmt.Errorf("no MX")
	}
	lookupNS = func(name string) ([]*net.NS, error) {
		return nil, fmt.Errorf("no NS")
	}
	lookupTXT = func(name string) ([]string, error) {
		return nil, fmt.Errorf("no TXT")
	}

	r := Check("test.example.com")
	if r.CNAME != "" {
		t.Errorf("CNAME should be empty when it matches domain, got %q", r.CNAME)
	}
	if len(r.MX) != 0 {
		t.Errorf("MX should be empty, got %v", r.MX)
	}
	if len(r.NS) != 0 {
		t.Errorf("NS should be empty, got %v", r.NS)
	}
}

func TestCheckCNAMEError(t *testing.T) {
	saveAndRestore(t)

	interfaceAddrs = func() ([]net.Addr, error) {
		return nil, nil
	}
	lookupHost = func(host string) ([]string, error) {
		return []string{"1.2.3.4"}, nil
	}
	lookupCNAME = func(host string) (string, error) {
		return "", fmt.Errorf("CNAME error")
	}
	lookupMX = func(name string) ([]*net.MX, error) {
		return nil, nil
	}
	lookupNS = func(name string) ([]*net.NS, error) {
		return nil, nil
	}
	lookupTXT = func(name string) ([]string, error) {
		return nil, nil
	}

	r := Check("example.com")
	if r.CNAME != "" {
		t.Errorf("CNAME should be empty on error, got %q", r.CNAME)
	}
}

func TestCheckTXTTimeout(t *testing.T) {
	saveAndRestore(t)

	interfaceAddrs = func() ([]net.Addr, error) {
		return nil, nil
	}
	lookupHost = func(host string) ([]string, error) {
		return []string{"1.2.3.4"}, nil
	}
	lookupCNAME = func(host string) (string, error) {
		return host + ".", nil
	}
	lookupMX = func(name string) ([]*net.MX, error) {
		return nil, nil
	}
	lookupNS = func(name string) ([]*net.NS, error) {
		return nil, nil
	}
	// TXT lookup takes too long
	lookupTXT = func(name string) ([]string, error) {
		time.Sleep(200 * time.Millisecond)
		return []string{"too late"}, nil
	}
	txtTimeout = 10 * time.Millisecond

	r := Check("example.com")
	// TXT should be nil because the timeout fires before the lookup completes
	if len(r.TXT) != 0 {
		t.Errorf("TXT should be empty on timeout, got %v", r.TXT)
	}
}

func TestCheckNotPointsHere(t *testing.T) {
	saveAndRestore(t)

	interfaceAddrs = func() ([]net.Addr, error) {
		return []net.Addr{
			&net.IPNet{IP: net.ParseIP("10.0.0.1"), Mask: net.CIDRMask(24, 32)},
		}, nil
	}
	lookupHost = func(host string) ([]string, error) {
		return []string{"99.99.99.99"}, nil
	}
	lookupCNAME = func(host string) (string, error) {
		return host + ".", nil
	}
	lookupMX = func(name string) ([]*net.MX, error) {
		return nil, nil
	}
	lookupNS = func(name string) ([]*net.NS, error) {
		return nil, nil
	}
	lookupTXT = func(name string) ([]string, error) {
		return nil, nil
	}

	r := Check("other.example.com")
	if r.PointsHere {
		t.Error("PointsHere should be false when IPs don't match")
	}
}

func TestCheckParseIPNil(t *testing.T) {
	saveAndRestore(t)

	interfaceAddrs = func() ([]net.Addr, error) {
		return nil, nil
	}
	// Return something that's not a valid IP
	lookupHost = func(host string) ([]string, error) {
		return []string{"not-an-ip", "1.2.3.4"}, nil
	}
	lookupCNAME = func(host string) (string, error) {
		return host + ".", nil
	}
	lookupMX = func(name string) ([]*net.MX, error) {
		return nil, nil
	}
	lookupNS = func(name string) ([]*net.NS, error) {
		return nil, nil
	}
	lookupTXT = func(name string) ([]string, error) {
		return nil, nil
	}

	r := Check("example.com")
	// "not-an-ip" should be skipped (parsed == nil), only "1.2.3.4" should appear
	if len(r.A) != 1 || r.A[0] != "1.2.3.4" {
		t.Errorf("A = %v, want [1.2.3.4]", r.A)
	}
}

func TestGetServerIPsError(t *testing.T) {
	saveAndRestore(t)

	interfaceAddrs = func() ([]net.Addr, error) {
		return nil, fmt.Errorf("interface error")
	}

	ips := getServerIPs()
	if ips != nil {
		t.Errorf("expected nil IPs on error, got %v", ips)
	}
}

func TestGetServerIPsIPAddr(t *testing.T) {
	saveAndRestore(t)

	// Return a *net.IPAddr to cover that type-switch branch
	interfaceAddrs = func() ([]net.Addr, error) {
		return []net.Addr{
			&net.IPAddr{IP: net.ParseIP("192.168.1.100")},
			&net.IPAddr{IP: net.ParseIP("127.0.0.1")},       // loopback, should be skipped
			&net.IPAddr{IP: net.ParseIP("fe80::1")},          // link-local, should be skipped
		}, nil
	}

	ips := getServerIPs()
	if len(ips) != 1 || ips[0] != "192.168.1.100" {
		t.Errorf("ips = %v, want [192.168.1.100]", ips)
	}
}

func TestGetServerIPsNilIP(t *testing.T) {
	saveAndRestore(t)

	// Return an addr type that isn't *net.IPNet or *net.IPAddr — ip stays nil
	interfaceAddrs = func() ([]net.Addr, error) {
		return []net.Addr{
			&net.UnixAddr{Name: "/tmp/test.sock", Net: "unix"},
		}, nil
	}

	ips := getServerIPs()
	if len(ips) != 0 {
		t.Errorf("expected no IPs for non-IP address, got %v", ips)
	}
}

func TestGetServerIPsMixed(t *testing.T) {
	saveAndRestore(t)

	_, ipnet, _ := net.ParseCIDR("172.16.0.5/16")
	interfaceAddrs = func() ([]net.Addr, error) {
		return []net.Addr{
			ipnet,
			&net.IPAddr{IP: net.ParseIP("192.168.1.1")},
		}, nil
	}

	ips := getServerIPs()
	if len(ips) != 2 {
		t.Errorf("expected 2 IPs, got %v", ips)
	}
}

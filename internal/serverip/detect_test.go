package serverip

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// saveAndRestore saves the current hook values and restores them via t.Cleanup.
func saveAndRestore(t *testing.T) {
	t.Helper()
	origNetInterfaces := netInterfaces
	origIfaceAddrs := ifaceAddrs
	origHttpGet := httpGet
	origURLs := publicIPURLs
	t.Cleanup(func() {
		netInterfaces = origNetInterfaces
		ifaceAddrs = origIfaceAddrs
		httpGet = origHttpGet
		publicIPURLs = origURLs
	})
}

// --- Test DetectAll ---

func TestDetectAll_RealSystem(t *testing.T) {
	ips := DetectAll()

	if len(ips) == 0 {
		t.Log("DetectAll returned no IPs (may be OK in restricted environments)")
		return
	}

	for _, info := range ips {
		ip := net.ParseIP(info.IP)
		if ip == nil {
			t.Errorf("DetectAll returned invalid IP: %q", info.IP)
			continue
		}
		if ip.IsLoopback() {
			t.Errorf("DetectAll should not return loopback address: %q", info.IP)
		}
		if info.Version != 4 && info.Version != 6 {
			t.Errorf("IP version should be 4 or 6, got %d for %q", info.Version, info.IP)
		}
		if info.Interface == "" {
			t.Errorf("Interface name should not be empty for IP %q", info.IP)
		}
	}
}

func TestDetectAll_InterfacesError(t *testing.T) {
	saveAndRestore(t)
	netInterfaces = func() ([]net.Interface, error) {
		return nil, errors.New("mock error")
	}

	ips := DetectAll()
	if ips != nil {
		t.Errorf("expected nil when Interfaces fails, got %v", ips)
	}
}

func TestDetectAll_NoInterfaces(t *testing.T) {
	saveAndRestore(t)
	netInterfaces = func() ([]net.Interface, error) {
		return []net.Interface{}, nil
	}

	ips := DetectAll()
	if len(ips) != 0 {
		t.Errorf("expected empty result for no interfaces, got %d IPs", len(ips))
	}
}

func TestDetectAll_LoopbackSkipped(t *testing.T) {
	saveAndRestore(t)
	netInterfaces = func() ([]net.Interface, error) {
		return []net.Interface{
			{
				Index: 1,
				Name:  "lo",
				Flags: net.FlagLoopback | net.FlagUp,
			},
		}, nil
	}

	ips := DetectAll()
	if len(ips) != 0 {
		t.Errorf("expected loopback to be skipped, got %d IPs", len(ips))
	}
}

func TestDetectAll_DownInterfaceSkipped(t *testing.T) {
	saveAndRestore(t)
	netInterfaces = func() ([]net.Interface, error) {
		return []net.Interface{
			{
				Index: 1,
				Name:  "eth0",
				Flags: 0, // not FlagUp
			},
		}, nil
	}

	ips := DetectAll()
	if len(ips) != 0 {
		t.Errorf("expected down interface to be skipped, got %d IPs", len(ips))
	}
}

func TestDetectAll_AddrsError(t *testing.T) {
	saveAndRestore(t)
	netInterfaces = func() ([]net.Interface, error) {
		return []net.Interface{
			{
				Index: 1,
				Name:  "eth0",
				Flags: net.FlagUp,
			},
		}, nil
	}
	ifaceAddrs = func(iface *net.Interface) ([]net.Addr, error) {
		return nil, errors.New("addrs error")
	}

	ips := DetectAll()
	if len(ips) != 0 {
		t.Errorf("expected 0 IPs when Addrs fails, got %d", len(ips))
	}
}

func TestDetectAll_IPNetAddr(t *testing.T) {
	saveAndRestore(t)
	netInterfaces = func() ([]net.Interface, error) {
		return []net.Interface{
			{Index: 1, Name: "eth0", Flags: net.FlagUp},
		}, nil
	}
	ifaceAddrs = func(iface *net.Interface) ([]net.Addr, error) {
		return []net.Addr{
			&net.IPNet{IP: net.ParseIP("10.0.0.1"), Mask: net.CIDRMask(24, 32)},
		}, nil
	}

	ips := DetectAll()
	if len(ips) != 1 {
		t.Fatalf("expected 1 IP, got %d", len(ips))
	}
	if ips[0].IP != "10.0.0.1" {
		t.Errorf("expected 10.0.0.1, got %q", ips[0].IP)
	}
	if ips[0].Version != 4 {
		t.Errorf("expected version 4, got %d", ips[0].Version)
	}
	if ips[0].Interface != "eth0" {
		t.Errorf("expected interface eth0, got %q", ips[0].Interface)
	}
	if !ips[0].Primary {
		t.Error("expected primary=true for the only IPv4")
	}
}

func TestDetectAll_IPAddrType(t *testing.T) {
	saveAndRestore(t)
	netInterfaces = func() ([]net.Interface, error) {
		return []net.Interface{
			{Index: 1, Name: "eth0", Flags: net.FlagUp},
		}, nil
	}
	ifaceAddrs = func(iface *net.Interface) ([]net.Addr, error) {
		return []net.Addr{
			&net.IPAddr{IP: net.ParseIP("192.168.1.50")},
		}, nil
	}

	ips := DetectAll()
	if len(ips) != 1 {
		t.Fatalf("expected 1 IP from IPAddr type, got %d", len(ips))
	}
	if ips[0].IP != "192.168.1.50" {
		t.Errorf("expected 192.168.1.50, got %q", ips[0].IP)
	}
	if ips[0].Version != 4 {
		t.Errorf("expected version 4, got %d", ips[0].Version)
	}
}

func TestDetectAll_IPv6Detection(t *testing.T) {
	saveAndRestore(t)
	netInterfaces = func() ([]net.Interface, error) {
		return []net.Interface{
			{Index: 1, Name: "eth0", Flags: net.FlagUp},
		}, nil
	}
	ifaceAddrs = func(iface *net.Interface) ([]net.Addr, error) {
		return []net.Addr{
			&net.IPNet{IP: net.ParseIP("2001:db8::1"), Mask: net.CIDRMask(64, 128)},
		}, nil
	}

	ips := DetectAll()
	if len(ips) != 1 {
		t.Fatalf("expected 1 IP, got %d", len(ips))
	}
	if ips[0].Version != 6 {
		t.Errorf("expected version 6, got %d", ips[0].Version)
	}
	if ips[0].Primary {
		t.Error("IPv6-only should not be marked primary")
	}
}

func TestDetectAll_LoopbackIPFiltered(t *testing.T) {
	saveAndRestore(t)
	netInterfaces = func() ([]net.Interface, error) {
		return []net.Interface{
			{Index: 1, Name: "eth0", Flags: net.FlagUp},
		}, nil
	}
	ifaceAddrs = func(iface *net.Interface) ([]net.Addr, error) {
		return []net.Addr{
			&net.IPNet{IP: net.ParseIP("127.0.0.1"), Mask: net.CIDRMask(8, 32)},
		}, nil
	}

	ips := DetectAll()
	if len(ips) != 0 {
		t.Errorf("expected loopback IP to be filtered, got %d IPs", len(ips))
	}
}

func TestDetectAll_LinkLocalUnicastFiltered(t *testing.T) {
	saveAndRestore(t)
	netInterfaces = func() ([]net.Interface, error) {
		return []net.Interface{
			{Index: 1, Name: "eth0", Flags: net.FlagUp},
		}, nil
	}
	ifaceAddrs = func(iface *net.Interface) ([]net.Addr, error) {
		return []net.Addr{
			&net.IPNet{IP: net.ParseIP("169.254.1.1"), Mask: net.CIDRMask(16, 32)},
		}, nil
	}

	ips := DetectAll()
	if len(ips) != 0 {
		t.Errorf("expected link-local unicast to be filtered, got %d IPs", len(ips))
	}
}

func TestDetectAll_LinkLocalMulticastFiltered(t *testing.T) {
	saveAndRestore(t)
	netInterfaces = func() ([]net.Interface, error) {
		return []net.Interface{
			{Index: 1, Name: "eth0", Flags: net.FlagUp},
		}, nil
	}
	ifaceAddrs = func(iface *net.Interface) ([]net.Addr, error) {
		return []net.Addr{
			&net.IPNet{IP: net.ParseIP("ff02::1"), Mask: net.CIDRMask(16, 128)},
		}, nil
	}

	ips := DetectAll()
	if len(ips) != 0 {
		t.Errorf("expected link-local multicast to be filtered, got %d IPs", len(ips))
	}
}

func TestDetectAll_NilIPFiltered(t *testing.T) {
	saveAndRestore(t)
	netInterfaces = func() ([]net.Interface, error) {
		return []net.Interface{
			{Index: 1, Name: "eth0", Flags: net.FlagUp},
		}, nil
	}
	ifaceAddrs = func(iface *net.Interface) ([]net.Addr, error) {
		// Return an addr type that doesn't match IPNet or IPAddr
		return []net.Addr{
			&net.TCPAddr{IP: net.ParseIP("10.0.0.1"), Port: 80},
		}, nil
	}

	ips := DetectAll()
	// net.TCPAddr is neither *net.IPNet nor *net.IPAddr, so ip stays nil → filtered
	if len(ips) != 0 {
		t.Errorf("expected unrecognized addr type to be filtered, got %d IPs", len(ips))
	}
}

func TestDetectAll_MixedIPv4IPv6(t *testing.T) {
	saveAndRestore(t)
	netInterfaces = func() ([]net.Interface, error) {
		return []net.Interface{
			{Index: 1, Name: "eth0", Flags: net.FlagUp},
		}, nil
	}
	ifaceAddrs = func(iface *net.Interface) ([]net.Addr, error) {
		return []net.Addr{
			&net.IPNet{IP: net.ParseIP("2001:db8::1"), Mask: net.CIDRMask(64, 128)},
			&net.IPNet{IP: net.ParseIP("10.0.0.1"), Mask: net.CIDRMask(24, 32)},
			&net.IPNet{IP: net.ParseIP("2001:db8::2"), Mask: net.CIDRMask(64, 128)},
			&net.IPNet{IP: net.ParseIP("10.0.0.2"), Mask: net.CIDRMask(24, 32)},
		}, nil
	}

	ips := DetectAll()
	if len(ips) != 4 {
		t.Fatalf("expected 4 IPs, got %d", len(ips))
	}

	// Primary should be the first IPv4
	if !ips[0].Primary {
		t.Error("first IP should be primary")
	}
	if ips[0].IP != "10.0.0.1" {
		t.Errorf("primary IP should be 10.0.0.1, got %q", ips[0].IP)
	}

	// Only one primary
	primaryCount := 0
	for _, ip := range ips {
		if ip.Primary {
			primaryCount++
		}
	}
	if primaryCount != 1 {
		t.Errorf("expected exactly 1 primary, got %d", primaryCount)
	}

	// IPv4 before IPv6 (after primary)
	seenV6 := false
	for _, ip := range ips {
		if ip.Version == 6 {
			seenV6 = true
		}
		if ip.Version == 4 && seenV6 {
			t.Error("IPv4 should come before IPv6 in sort order")
		}
	}
}

func TestDetectAll_MultipleInterfaces(t *testing.T) {
	saveAndRestore(t)
	netInterfaces = func() ([]net.Interface, error) {
		return []net.Interface{
			{Index: 1, Name: "eth0", Flags: net.FlagUp},
			{Index: 2, Name: "eth1", Flags: net.FlagUp},
		}, nil
	}
	ifaceAddrs = func(iface *net.Interface) ([]net.Addr, error) {
		switch iface.Name {
		case "eth0":
			return []net.Addr{
				&net.IPNet{IP: net.ParseIP("10.0.0.1"), Mask: net.CIDRMask(24, 32)},
			}, nil
		case "eth1":
			return []net.Addr{
				&net.IPNet{IP: net.ParseIP("192.168.1.1"), Mask: net.CIDRMask(24, 32)},
			}, nil
		}
		return nil, nil
	}

	ips := DetectAll()
	if len(ips) != 2 {
		t.Fatalf("expected 2 IPs from 2 interfaces, got %d", len(ips))
	}

	// Check interface names are preserved
	names := map[string]bool{}
	for _, ip := range ips {
		names[ip.Interface] = true
	}
	if !names["eth0"] || !names["eth1"] {
		t.Errorf("expected both eth0 and eth1, got interfaces: %v", names)
	}
}

func TestDetectAll_MixedValidInvalidAddrs(t *testing.T) {
	saveAndRestore(t)
	netInterfaces = func() ([]net.Interface, error) {
		return []net.Interface{
			{Index: 1, Name: "eth0", Flags: net.FlagUp},
		}, nil
	}
	ifaceAddrs = func(iface *net.Interface) ([]net.Addr, error) {
		return []net.Addr{
			&net.IPNet{IP: net.ParseIP("127.0.0.1"), Mask: net.CIDRMask(8, 32)},   // loopback - filtered
			&net.IPNet{IP: net.ParseIP("169.254.1.1"), Mask: net.CIDRMask(16, 32)}, // link-local - filtered
			&net.IPNet{IP: net.ParseIP("10.0.0.1"), Mask: net.CIDRMask(24, 32)},    // valid
			&net.IPNet{IP: net.ParseIP("fe80::1"), Mask: net.CIDRMask(10, 128)},     // link-local v6 - filtered
			&net.IPNet{IP: net.ParseIP("2001:db8::1"), Mask: net.CIDRMask(64, 128)}, // valid
		}, nil
	}

	ips := DetectAll()
	if len(ips) != 2 {
		t.Fatalf("expected 2 valid IPs after filtering, got %d", len(ips))
	}
}

func TestDetectAll_OnlyIPv6_NoPrimary(t *testing.T) {
	saveAndRestore(t)
	netInterfaces = func() ([]net.Interface, error) {
		return []net.Interface{
			{Index: 1, Name: "eth0", Flags: net.FlagUp},
		}, nil
	}
	ifaceAddrs = func(iface *net.Interface) ([]net.Addr, error) {
		return []net.Addr{
			&net.IPNet{IP: net.ParseIP("2001:db8::1"), Mask: net.CIDRMask(64, 128)},
			&net.IPNet{IP: net.ParseIP("2001:db8::2"), Mask: net.CIDRMask(64, 128)},
		}, nil
	}

	ips := DetectAll()
	if len(ips) != 2 {
		t.Fatalf("expected 2 IPs, got %d", len(ips))
	}
	for _, ip := range ips {
		if ip.Primary {
			t.Error("no IP should be marked primary when only IPv6 is available")
		}
	}
}

func TestDetectAll_PrimaryMarking(t *testing.T) {
	ips := DetectAll()
	if len(ips) == 0 {
		t.Skip("no IPs detected, skipping primary marking test")
	}

	primaryCount := 0
	for _, info := range ips {
		if info.Primary {
			primaryCount++
			if info.Version != 4 {
				t.Errorf("primary IP should be IPv4, got version %d", info.Version)
			}
		}
	}

	if primaryCount > 1 {
		t.Errorf("expected at most 1 primary IP, got %d", primaryCount)
	}
}

func TestDetectAll_Sorted(t *testing.T) {
	ips := DetectAll()
	if len(ips) < 2 {
		t.Skip("need at least 2 IPs to test sorting")
	}

	foundNonPrimary := false
	for _, info := range ips {
		if !info.Primary {
			foundNonPrimary = true
		}
		if info.Primary && foundNonPrimary {
			t.Error("primary IP should come before non-primary IPs in sorted output")
		}
	}
}

// --- Test PrimaryIPv4 ---

func TestPrimaryIPv4_RealSystem(t *testing.T) {
	ip := PrimaryIPv4()

	if ip == "" {
		t.Log("PrimaryIPv4 returned empty (may be OK in restricted environments)")
		return
	}

	parsed := net.ParseIP(ip)
	if parsed == nil {
		t.Errorf("PrimaryIPv4 returned invalid IP: %q", ip)
		return
	}
	if parsed.To4() == nil {
		t.Errorf("PrimaryIPv4 returned non-IPv4 address: %q", ip)
	}
}

func TestPrimaryIPv4_NoInterfaces(t *testing.T) {
	saveAndRestore(t)
	netInterfaces = func() ([]net.Interface, error) {
		return nil, errors.New("no interfaces")
	}

	ip := PrimaryIPv4()
	if ip != "" {
		t.Errorf("expected empty string when no interfaces, got %q", ip)
	}
}

func TestPrimaryIPv4_OnlyIPv6(t *testing.T) {
	saveAndRestore(t)
	netInterfaces = func() ([]net.Interface, error) {
		return []net.Interface{
			{Index: 1, Name: "eth0", Flags: net.FlagUp},
		}, nil
	}
	ifaceAddrs = func(iface *net.Interface) ([]net.Addr, error) {
		return []net.Addr{
			&net.IPNet{IP: net.ParseIP("2001:db8::1"), Mask: net.CIDRMask(64, 128)},
		}, nil
	}

	ip := PrimaryIPv4()
	if ip != "" {
		t.Errorf("expected empty for IPv6-only, got %q", ip)
	}
}

func TestPrimaryIPv4_ReturnsFirstIPv4(t *testing.T) {
	saveAndRestore(t)
	netInterfaces = func() ([]net.Interface, error) {
		return []net.Interface{
			{Index: 1, Name: "eth0", Flags: net.FlagUp},
		}, nil
	}
	ifaceAddrs = func(iface *net.Interface) ([]net.Addr, error) {
		return []net.Addr{
			&net.IPNet{IP: net.ParseIP("2001:db8::1"), Mask: net.CIDRMask(64, 128)},
			&net.IPNet{IP: net.ParseIP("10.0.0.1"), Mask: net.CIDRMask(24, 32)},
			&net.IPNet{IP: net.ParseIP("10.0.0.2"), Mask: net.CIDRMask(24, 32)},
		}, nil
	}

	ip := PrimaryIPv4()
	if ip != "10.0.0.1" {
		t.Errorf("expected '10.0.0.1', got %q", ip)
	}
}

// --- Test PublicIP ---

func TestPublicIP_FirstURLSucceeds(t *testing.T) {
	saveAndRestore(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "203.0.113.1\n")
	}))
	defer srv.Close()

	publicIPURLs = []string{srv.URL}
	httpGet = func(client *http.Client, url string) (*http.Response, error) {
		return client.Get(url)
	}

	ip := PublicIP()
	if ip != "203.0.113.1" {
		t.Errorf("expected '203.0.113.1', got %q", ip)
	}
}

func TestPublicIP_FirstFailsSecondSucceeds(t *testing.T) {
	saveAndRestore(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "198.51.100.42")
	}))
	defer srv.Close()

	callCount := 0
	httpGet = func(client *http.Client, url string) (*http.Response, error) {
		callCount++
		if callCount == 1 {
			return nil, errors.New("connection refused")
		}
		return client.Get(srv.URL)
	}

	publicIPURLs = []string{"http://fail.invalid", srv.URL}

	ip := PublicIP()
	if ip != "198.51.100.42" {
		t.Errorf("expected '198.51.100.42', got %q", ip)
	}
}

func TestPublicIP_AllURLsFail_FallbackToLocal(t *testing.T) {
	saveAndRestore(t)

	httpGet = func(client *http.Client, url string) (*http.Response, error) {
		return nil, errors.New("connection refused")
	}
	publicIPURLs = []string{"http://a.invalid", "http://b.invalid"}

	// Set up a known local IP for deterministic fallback
	netInterfaces = func() ([]net.Interface, error) {
		return []net.Interface{
			{Index: 1, Name: "eth0", Flags: net.FlagUp},
		}, nil
	}
	ifaceAddrs = func(iface *net.Interface) ([]net.Addr, error) {
		return []net.Addr{
			&net.IPNet{IP: net.ParseIP("10.99.99.1"), Mask: net.CIDRMask(24, 32)},
		}, nil
	}

	ip := PublicIP()
	if ip != "10.99.99.1" {
		t.Errorf("expected fallback to local '10.99.99.1', got %q", ip)
	}
}

func TestPublicIP_AllURLsFail_NoInterfaces(t *testing.T) {
	saveAndRestore(t)

	httpGet = func(client *http.Client, url string) (*http.Response, error) {
		return nil, errors.New("connection refused")
	}
	publicIPURLs = []string{"http://a.invalid"}

	netInterfaces = func() ([]net.Interface, error) {
		return nil, errors.New("no interfaces")
	}

	ip := PublicIP()
	if ip != "" {
		t.Errorf("expected empty string when everything fails, got %q", ip)
	}
}

func TestPublicIP_InvalidIPResponse(t *testing.T) {
	saveAndRestore(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "not-an-ip-address")
	}))
	defer srv.Close()

	publicIPURLs = []string{srv.URL}
	httpGet = func(client *http.Client, url string) (*http.Response, error) {
		return client.Get(url)
	}

	netInterfaces = func() ([]net.Interface, error) {
		return nil, errors.New("no interfaces")
	}

	ip := PublicIP()
	if ip != "" {
		t.Errorf("expected empty for invalid IP response with no fallback, got %q", ip)
	}
}

func TestPublicIP_IPv6Response(t *testing.T) {
	saveAndRestore(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "2001:db8::1\n")
	}))
	defer srv.Close()

	publicIPURLs = []string{srv.URL}
	httpGet = func(client *http.Client, url string) (*http.Response, error) {
		return client.Get(url)
	}

	ip := PublicIP()
	if ip != "2001:db8::1" {
		t.Errorf("expected '2001:db8::1', got %q", ip)
	}
}

func TestPublicIP_WhitespaceInResponse(t *testing.T) {
	saveAndRestore(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "  10.0.0.1  \n")
	}))
	defer srv.Close()

	publicIPURLs = []string{srv.URL}
	httpGet = func(client *http.Client, url string) (*http.Response, error) {
		return client.Get(url)
	}

	ip := PublicIP()
	if ip != "10.0.0.1" {
		t.Errorf("expected '10.0.0.1' after trimming, got %q", ip)
	}
}

func TestPublicIP_EmptyBody(t *testing.T) {
	saveAndRestore(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return empty body
	}))
	defer srv.Close()

	publicIPURLs = []string{srv.URL}
	httpGet = func(client *http.Client, url string) (*http.Response, error) {
		return client.Get(url)
	}

	netInterfaces = func() ([]net.Interface, error) {
		return nil, errors.New("no interfaces")
	}

	ip := PublicIP()
	if ip != "" {
		t.Errorf("expected empty for empty body response, got %q", ip)
	}
}

func TestPublicIP_FirstInvalidSecondValid(t *testing.T) {
	saveAndRestore(t)

	callCount := 0
	srvInvalid := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "garbage-data")
	}))
	defer srvInvalid.Close()

	srvValid := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "192.0.2.1")
	}))
	defer srvValid.Close()

	urls := []string{srvInvalid.URL, srvValid.URL}
	publicIPURLs = urls
	httpGet = func(client *http.Client, url string) (*http.Response, error) {
		callCount++
		return client.Get(url)
	}

	ip := PublicIP()
	if ip != "192.0.2.1" {
		t.Errorf("expected '192.0.2.1', got %q", ip)
	}
	if callCount != 2 {
		t.Errorf("expected 2 HTTP calls (first invalid, second valid), got %d", callCount)
	}
}

func TestPublicIP_MultipleURLsAllInvalid(t *testing.T) {
	saveAndRestore(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "not-valid")
	}))
	defer srv.Close()

	publicIPURLs = []string{srv.URL, srv.URL, srv.URL}
	httpGet = func(client *http.Client, url string) (*http.Response, error) {
		return client.Get(url)
	}

	netInterfaces = func() ([]net.Interface, error) {
		return nil, errors.New("no interfaces")
	}

	ip := PublicIP()
	if ip != "" {
		t.Errorf("expected empty when all URLs return invalid IPs, got %q", ip)
	}
}

func TestPublicIP_EmptyURLList(t *testing.T) {
	saveAndRestore(t)

	publicIPURLs = []string{}
	netInterfaces = func() ([]net.Interface, error) {
		return nil, errors.New("no interfaces")
	}

	ip := PublicIP()
	if ip != "" {
		t.Errorf("expected empty with no URLs and no interfaces, got %q", ip)
	}
}

func TestPublicIP_Non200Status(t *testing.T) {
	saveAndRestore(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "10.0.0.1")
	}))
	defer srv.Close()

	publicIPURLs = []string{srv.URL}
	httpGet = func(client *http.Client, url string) (*http.Response, error) {
		return client.Get(url)
	}

	// The code doesn't check status codes — it reads the body regardless.
	ip := PublicIP()
	if ip != "10.0.0.1" {
		t.Errorf("expected '10.0.0.1' (body read regardless of status), got %q", ip)
	}
}

func TestPublicIP_LongResponse(t *testing.T) {
	saveAndRestore(t)

	longIP := "203.0.113.50"
	padding := strings.Repeat("x", 100)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, longIP+padding)
	}))
	defer srv.Close()

	publicIPURLs = []string{srv.URL}
	httpGet = func(client *http.Client, url string) (*http.Response, error) {
		return client.Get(url)
	}

	netInterfaces = func() ([]net.Interface, error) {
		return nil, errors.New("no interfaces")
	}

	ip := PublicIP()
	// The first 64 bytes include "203.0.113.50xxxx..." which is not a valid IP.
	if ip != "" {
		t.Errorf("expected empty for long non-IP response with no fallback, got %q", ip)
	}
}

func TestPublicIP_ExactlyFitsIn64Bytes(t *testing.T) {
	saveAndRestore(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "192.168.255.255")
	}))
	defer srv.Close()

	publicIPURLs = []string{srv.URL}
	httpGet = func(client *http.Client, url string) (*http.Response, error) {
		return client.Get(url)
	}

	ip := PublicIP()
	if ip != "192.168.255.255" {
		t.Errorf("expected '192.168.255.255', got %q", ip)
	}
}

func TestPublicIP_MixedFailures(t *testing.T) {
	saveAndRestore(t)

	srvGood := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "100.64.0.1")
	}))
	defer srvGood.Close()

	callIndex := 0
	httpGet = func(client *http.Client, url string) (*http.Response, error) {
		callIndex++
		switch callIndex {
		case 1:
			return nil, errors.New("dns error")
		case 2:
			return nil, errors.New("timeout")
		default:
			return client.Get(srvGood.URL)
		}
	}

	publicIPURLs = []string{"http://a.invalid", "http://b.invalid", srvGood.URL}

	ip := PublicIP()
	if ip != "100.64.0.1" {
		t.Errorf("expected '100.64.0.1', got %q", ip)
	}
}

// --- Test IPInfo struct ---

func TestIPInfoStruct(t *testing.T) {
	info := IPInfo{
		IP:        "192.168.1.1",
		Version:   4,
		Interface: "eth0",
		Primary:   true,
	}

	if info.IP != "192.168.1.1" {
		t.Errorf("expected IP '192.168.1.1', got %q", info.IP)
	}
	if info.Version != 4 {
		t.Errorf("expected Version 4, got %d", info.Version)
	}
	if info.Interface != "eth0" {
		t.Errorf("expected Interface 'eth0', got %q", info.Interface)
	}
	if !info.Primary {
		t.Error("expected Primary=true")
	}
}

func TestIPInfoStruct_IPv6(t *testing.T) {
	info := IPInfo{
		IP:        "2001:db8::1",
		Version:   6,
		Interface: "eth0",
		Primary:   false,
	}

	if info.Version != 6 {
		t.Errorf("expected Version 6, got %d", info.Version)
	}
	if info.Primary {
		t.Error("expected Primary=false for IPv6")
	}
}

// --- Test edge cases via real system paths ---

func TestDetectAll_LinkLocalFiltered(t *testing.T) {
	ips := DetectAll()
	for _, info := range ips {
		ip := net.ParseIP(info.IP)
		if ip == nil {
			t.Errorf("invalid IP in results: %q", info.IP)
			continue
		}
		if ip.IsLinkLocalUnicast() {
			t.Errorf("link-local unicast should be filtered: %q", info.IP)
		}
		if ip.IsLinkLocalMulticast() {
			t.Errorf("link-local multicast should be filtered: %q", info.IP)
		}
	}
}

func TestDetectAll_VersionCorrectness(t *testing.T) {
	ips := DetectAll()
	for _, info := range ips {
		ip := net.ParseIP(info.IP)
		if ip == nil {
			continue
		}
		if ip.To4() != nil && info.Version != 4 {
			t.Errorf("IP %q should have Version=4, got %d", info.IP, info.Version)
		}
		if ip.To4() == nil && info.Version != 6 {
			t.Errorf("IP %q should have Version=6, got %d", info.IP, info.Version)
		}
	}
}

// --- Test default hooks ---

func TestDefaultHooks(t *testing.T) {
	// Verify default netInterfaces hook works (delegates to net.Interfaces)
	ifaces, err := netInterfaces()
	if err != nil {
		t.Logf("netInterfaces returned error: %v (may be OK)", err)
		return
	}
	if ifaces == nil {
		t.Log("netInterfaces returned nil interfaces (may be OK)")
	}
}

func TestDefaultHttpGetHook(t *testing.T) {
	// Verify the default httpGet hook works by calling a local test server.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	client := &http.Client{}
	resp, err := httpGet(client, srv.URL)
	if err != nil {
		t.Fatalf("default httpGet failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestDefaultIfaceAddrsHook(t *testing.T) {
	// Verify the default ifaceAddrs hook calls iface.Addrs().
	// We use a real interface from the system.
	ifaces, err := net.Interfaces()
	if err != nil || len(ifaces) == 0 {
		t.Skip("no interfaces available for ifaceAddrs test")
	}

	addrs, err := ifaceAddrs(&ifaces[0])
	// err can be nil or non-nil depending on the interface; just check no panic.
	_ = addrs
	_ = err
}

// --- Test sorting logic with controlled data ---

func TestSortingLogic(t *testing.T) {
	tests := []struct {
		name     string
		input    []IPInfo
		expected []IPInfo
	}{
		{
			name: "primary first then v4 then v6",
			input: []IPInfo{
				{IP: "2001:db8::1", Version: 6, Interface: "eth0"},
				{IP: "10.0.0.2", Version: 4, Interface: "eth0"},
				{IP: "10.0.0.1", Version: 4, Interface: "eth0", Primary: true},
			},
			expected: []IPInfo{
				{IP: "10.0.0.1", Version: 4, Interface: "eth0", Primary: true},
				{IP: "10.0.0.2", Version: 4, Interface: "eth0"},
				{IP: "2001:db8::1", Version: 6, Interface: "eth0"},
			},
		},
		{
			name: "only ipv6 no primary",
			input: []IPInfo{
				{IP: "2001:db8::2", Version: 6, Interface: "eth0"},
				{IP: "2001:db8::1", Version: 6, Interface: "eth0"},
			},
			expected: []IPInfo{
				{IP: "2001:db8::2", Version: 6, Interface: "eth0"},
				{IP: "2001:db8::1", Version: 6, Interface: "eth0"},
			},
		},
		{
			name:     "empty list",
			input:    []IPInfo{},
			expected: []IPInfo{},
		},
		{
			name: "single item",
			input: []IPInfo{
				{IP: "10.0.0.1", Version: 4, Interface: "eth0", Primary: true},
			},
			expected: []IPInfo{
				{IP: "10.0.0.1", Version: 4, Interface: "eth0", Primary: true},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Apply the same sort as DetectAll
			sortIPInfos(tt.input)
			if len(tt.input) != len(tt.expected) {
				t.Fatalf("length mismatch: got %d, want %d", len(tt.input), len(tt.expected))
			}
			for i := range tt.input {
				if tt.input[i] != tt.expected[i] {
					t.Errorf("index %d: got %+v, want %+v", i, tt.input[i], tt.expected[i])
				}
			}
		})
	}
}

// sortIPInfos replicates the sort logic from DetectAll for unit testing.
func sortIPInfos(ips []IPInfo) {
	for i := 0; i < len(ips); i++ {
		for j := i + 1; j < len(ips); j++ {
			swap := false
			if ips[j].Primary && !ips[i].Primary {
				swap = true
			} else if ips[i].Primary == ips[j].Primary && ips[j].Version < ips[i].Version {
				swap = true
			}
			if swap {
				ips[i], ips[j] = ips[j], ips[i]
			}
		}
	}
}

package serverip

import (
	"net"
	"testing"
)

func withFakeIfaces(t *testing.T, ifs map[string][]string, order []string) func() {
	oldIf, oldAddrs := netInterfaces, ifaceAddrs
	netInterfaces = func() ([]net.Interface, error) {
		var out []net.Interface
		for _, n := range order {
			out = append(out, net.Interface{Name: n, Flags: net.FlagUp})
		}
		return out, nil
	}
	ifaceAddrs = func(i *net.Interface) ([]net.Addr, error) {
		var out []net.Addr
		for _, a := range ifs[i.Name] {
			out = append(out, &net.IPNet{IP: net.ParseIP(a), Mask: net.CIDRMask(24, 32)})
		}
		return out, nil
	}
	return func() { netInterfaces, ifaceAddrs = oldIf, oldAddrs }
}

func TestPrimaryIPv4PrefersPublicOverBridgeAndVPN(t *testing.T) {
	cases := []struct {
		name  string
		ifs   map[string][]string
		order []string
		want  string
	}{
		{"private bridge first, public later", map[string][]string{"docker0": {"172.17.0.1"}, "eth0": {"203.0.113.5"}}, []string{"docker0", "eth0"}, "203.0.113.5"},
		{"cgnat vpn first", map[string][]string{"tailscale0": {"100.64.0.7"}, "eth0": {"198.51.100.9"}}, []string{"tailscale0", "eth0"}, "198.51.100.9"},
		{"public already first", map[string][]string{"eth0": {"203.0.113.5"}, "docker0": {"172.17.0.1"}}, []string{"eth0", "docker0"}, "203.0.113.5"},
		{"behind NAT: only private, first wins", map[string][]string{"eth0": {"10.0.0.4"}, "docker0": {"172.17.0.1"}}, []string{"eth0", "docker0"}, "10.0.0.4"},
		{"ipv6 only", map[string][]string{"eth0": {"2001:db8::1"}}, []string{"eth0"}, ""},
		{"no interfaces", map[string][]string{}, nil, ""},
	}
	for _, c := range cases {
		restore := withFakeIfaces(t, c.ifs, c.order)
		got := PrimaryIPv4()
		restore()
		if got != c.want {
			t.Fatalf("%s: PrimaryIPv4() = %q, want %q", c.name, got, c.want)
		}
	}
}

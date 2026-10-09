// Package serverip detects and manages server IP addresses.
package serverip

import (
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Testable hooks — overridden in tests to avoid real network calls.
var (
	netInterfaces = net.Interfaces
	ifaceAddrs    = func(iface *net.Interface) ([]net.Addr, error) {
		return iface.Addrs()
	}
	httpGet = func(client *http.Client, url string) (*http.Response, error) {
		return client.Get(url)
	}
	publicIPURLs = []string{
		"https://api.ipify.org",
		"https://ifconfig.me/ip",
		"https://icanhazip.com",
	}
)

// IPInfo represents a server IP address.
type IPInfo struct {
	IP        string `json:"ip"`
	Version   int    `json:"version"` // 4 or 6
	Interface string `json:"interface"`
	Primary   bool   `json:"primary"`
}

// DetectAll returns all non-loopback IPs on this server.
func DetectAll() []IPInfo {
	var ips []IPInfo

	ifaces, err := netInterfaces()
	if err != nil {
		return nil
	}

	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}

		addrs, err := ifaceAddrs(&iface)
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}

			if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
				continue
			}

			ver := 4
			if ip.To4() == nil {
				ver = 6
			}

			ips = append(ips, IPInfo{
				IP:        ip.String(),
				Version:   ver,
				Interface: iface.Name,
			})
		}
	}

	// Mark first IPv4 as primary
	for i := range ips {
		if ips[i].Version == 4 {
			ips[i].Primary = true
			break
		}
	}

	// Sort: primary first, then IPv4, then IPv6
	sort.Slice(ips, func(i, j int) bool {
		if ips[i].Primary != ips[j].Primary {
			return ips[i].Primary
		}
		return ips[i].Version < ips[j].Version
	})

	return ips
}

// PrimaryIPv4 returns the server's primary public IPv4.
func PrimaryIPv4() string {
	for _, ip := range DetectAll() {
		if ip.Version == 4 {
			return ip.IP
		}
	}
	return ""
}

// PublicIP tries to detect the public IP via an external service.
func PublicIP() string {
	client := &http.Client{Timeout: 5 * time.Second}

	for _, url := range publicIPURLs {
		resp, err := httpGet(client, url)
		if err != nil {
			continue
		}
		// Read the whole (tiny) body — a single Read can return a partial
		// response, truncating the IP and failing the parse.
		body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
		resp.Body.Close()
		if err != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
			continue
		}
		ip := strings.TrimSpace(string(body))
		if net.ParseIP(ip) != nil {
			return ip
		}
	}

	// Fallback to local detection. Only a globally routable address can stand
	// in for the public IP: a private or CGNAT interface address (the norm
	// behind NAT) would be published as the domain's A record by DNS sync.
	for _, info := range DetectAll() {
		if info.Version == 4 && isPublicAddr(net.ParseIP(info.IP)) {
			return info.IP
		}
	}
	return ""
}

// cgnatNet is the RFC 6598 shared address space, which net.IP.IsPrivate omits.
var cgnatNet = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

func isPublicAddr(ip net.IP) bool {
	return ip != nil && ip.IsGlobalUnicast() && !ip.IsPrivate() && !cgnatNet.Contains(ip)
}

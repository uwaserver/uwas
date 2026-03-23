// Package serverip detects and manages server IP addresses.
package serverip

import (
	"net"
	"net/http"
	"sort"
	"strings"
	"time"
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

	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}

		addrs, err := iface.Addrs()
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

	for _, url := range []string{
		"https://api.ipify.org",
		"https://ifconfig.me/ip",
		"https://icanhazip.com",
	} {
		resp, err := client.Get(url)
		if err != nil {
			continue
		}
		defer resp.Body.Close()
		buf := make([]byte, 64)
		n, _ := resp.Body.Read(buf)
		ip := strings.TrimSpace(string(buf[:n]))
		if net.ParseIP(ip) != nil {
			return ip
		}
	}

	// Fallback to local detection
	return PrimaryIPv4()
}

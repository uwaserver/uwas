// Package dnschecker verifies DNS records for domains.
package dnschecker

import (
	"fmt"
	"net"
	"strings"
	"time"
)

// Testable hooks for DNS and network lookups.
var (
	lookupHost   = net.LookupHost
	lookupCNAME  = net.LookupCNAME
	lookupMX     = net.LookupMX
	lookupNS     = net.LookupNS
	lookupTXT    = net.LookupTXT
	interfaceAddrs = net.InterfaceAddrs
	txtTimeout     = 3 * time.Second
)

// Result contains DNS check results for a domain.
type Result struct {
	Domain     string   `json:"domain"`
	A          []string `json:"a"`
	AAAA       []string `json:"aaaa"`
	CNAME      string   `json:"cname,omitempty"`
	MX         []string `json:"mx,omitempty"`
	NS         []string `json:"ns,omitempty"`
	TXT        []string `json:"txt,omitempty"`
	PointsHere bool     `json:"points_here"`
	ServerIPs  []string `json:"server_ips"`
	Error      string   `json:"error,omitempty"`
}

// Check performs DNS lookups for a domain and compares with server IPs.
func Check(domain string) Result {
	r := Result{Domain: domain}

	// Get server's own IPs
	r.ServerIPs = getServerIPs()

	// Resolve A records
	ips, err := lookupHost(domain)
	if err != nil {
		r.Error = fmt.Sprintf("DNS lookup failed: %s", err)
		return r
	}

	for _, ip := range ips {
		parsed := net.ParseIP(ip)
		if parsed == nil {
			continue
		}
		if parsed.To4() != nil {
			r.A = append(r.A, ip)
		} else {
			r.AAAA = append(r.AAAA, ip)
		}
	}

	// Check if any resolved IP matches server IPs
	serverIPSet := make(map[string]bool)
	for _, sip := range r.ServerIPs {
		serverIPSet[sip] = true
	}
	for _, ip := range ips {
		if serverIPSet[ip] {
			r.PointsHere = true
			break
		}
	}

	// CNAME
	cname, err := lookupCNAME(domain)
	if err == nil && cname != domain+"." {
		r.CNAME = strings.TrimSuffix(cname, ".")
	}

	// MX
	mxRecords, err := lookupMX(domain)
	if err == nil {
		for _, mx := range mxRecords {
			r.MX = append(r.MX, fmt.Sprintf("%s (priority %d)", strings.TrimSuffix(mx.Host, "."), mx.Pref))
		}
	}

	// NS
	nsRecords, err := lookupNS(domain)
	if err == nil {
		for _, ns := range nsRecords {
			r.NS = append(r.NS, strings.TrimSuffix(ns.Host, "."))
		}
	}

	// TXT (with timeout)
	txtCh := make(chan []string, 1)
	go func() {
		txt, _ := lookupTXT(domain)
		txtCh <- txt
	}()
	select {
	case txt := <-txtCh:
		r.TXT = txt
	case <-time.After(txtTimeout):
	}

	return r
}

// getServerIPs returns this server's non-loopback IPs.
func getServerIPs() []string {
	var ips []string
	addrs, err := interfaceAddrs()
	if err != nil {
		return nil
	}
	for _, addr := range addrs {
		var ip net.IP
		switch v := addr.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			continue
		}
		ips = append(ips, ip.String())
	}
	return ips
}

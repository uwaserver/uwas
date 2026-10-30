// Package dnschecker verifies DNS records for domains.
package dnschecker

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/uwaserver/uwas/internal/serverip"
)

// Testable hooks for DNS and network lookups.
var (
	lookupHost     = net.LookupHost
	lookupCNAME    = net.LookupCNAME
	lookupMX       = net.LookupMX
	lookupNS       = net.LookupNS
	lookupTXT      = net.LookupTXT
	interfaceAddrs = net.InterfaceAddrs
	txtTimeout     = 3 * time.Second

	// publicIP reports the address the internet reaches this server at; it is
	// only asked when no interface carries a public address (behind NAT).
	publicIP        = serverip.PublicIP
	publicIPTimeout = 3 * time.Second
)

// Cache of the externally detected public IP, so a DNS page that is checked
// repeatedly does not call out to the detection services every time. A miss is
// cached briefly so an offline server does not stall every check.
var publicIPCache struct {
	mu sync.Mutex
	ip string
	at time.Time
}

const (
	publicIPTTL     = 10 * time.Minute
	publicIPMissTTL = time.Minute
)

// serverPublicIP returns the externally detected public IP, or "" when it
// cannot be determined within publicIPTimeout.
func serverPublicIP() string {
	publicIPCache.mu.Lock()
	defer publicIPCache.mu.Unlock()

	ttl := publicIPTTL
	if publicIPCache.ip == "" {
		ttl = publicIPMissTTL
	}
	if !publicIPCache.at.IsZero() && time.Since(publicIPCache.at) < ttl {
		return publicIPCache.ip
	}

	ch := make(chan string, 1)
	go func() { ch <- publicIP() }()
	var ip string
	select {
	case ip = <-ch:
	case <-time.After(publicIPTimeout):
	}
	if parsed := net.ParseIP(strings.TrimSpace(ip)); parsed != nil {
		ip = parsed.String()
	} else {
		ip = ""
	}
	publicIPCache.ip, publicIPCache.at = ip, time.Now()
	return ip
}

// hasPublicAddr reports whether any of ips is a globally routable address.
func hasPublicAddr(ips []string) bool {
	for _, s := range ips {
		if serverip.IsPublicAddr(net.ParseIP(s)) {
			return true
		}
	}
	return false
}

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
	// Behind NAT (EC2, GCP, Azure, Oracle ...) the public address is on no
	// interface: comparing against the interface addresses alone reported a
	// correctly configured domain as "does not point here" and recommended the
	// private address as the A record. The externally visible address leads the
	// list, since that is the one a record must name.
	if !hasPublicAddr(r.ServerIPs) {
		if pub := serverPublicIP(); pub != "" {
			r.ServerIPs = append([]string{pub}, r.ServerIPs...)
		}
	}

	// Use mockable lookup (net.LookupHost respects context internally)
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
		parsed := net.ParseIP(ip)
		if parsed != nil && serverIPSet[parsed.String()] {
			r.PointsHere = true
			break
		}
	}

	// CNAME
	cname, err := lookupCNAME(domain)
	// Compare ignoring case and a trailing dot on either side: the resolver
	// returns the canonical FQDN, so "example.com." (or mixed case) would
	// otherwise report the domain as its own CNAME.
	if err == nil && !strings.EqualFold(strings.TrimSuffix(cname, "."), strings.TrimSuffix(domain, ".")) {
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
	// Use a context with deadline so the goroutine is cancelled on timeout.
	// Without cancellation, the goroutine leaks: it stays blocked on the DNS
	// lookup for its full duration even after Check() returns.
	ctx, cancel := context.WithTimeout(context.Background(), txtTimeout)
	defer cancel()
	txtCh := make(chan []string, 1)
	go func() {
		txt, _ := lookupTXT(domain)
		select {
		case txtCh <- txt:
		case <-ctx.Done():
		}
	}()
	select {
	case txt := <-txtCh:
		r.TXT = txt
	case <-ctx.Done():
		// timeout reached — goroutine's select above fired ctx.Done() too,
		// cancelling its DNS call and freeing its resources.
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

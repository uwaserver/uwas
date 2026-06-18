package cloudflare

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	IPv4ListURL = "https://www.cloudflare.com/ips-v4"
	IPv6ListURL = "https://www.cloudflare.com/ips-v6"
)

// IPSet caches parsed Cloudflare CIDR ranges for request-time origin checks.
type IPSet struct {
	mu          sync.RWMutex
	fingerprint string
	nets        []*net.IPNet
}

func NewIPSet() *IPSet {
	return &IPSet{}
}

func (s *IPSet) Contains(ip string, cidrs []string) bool {
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil {
		return false
	}
	nets := s.netsFor(cidrs)
	for _, n := range nets {
		if n.Contains(parsed) {
			return true
		}
	}
	return false
}

func (s *IPSet) netsFor(cidrs []string) []*net.IPNet {
	fp := fingerprintCIDRs(cidrs)
	s.mu.RLock()
	if fp == s.fingerprint {
		nets := s.nets
		s.mu.RUnlock()
		return nets
	}
	s.mu.RUnlock()

	normalized, _ := NormalizeCIDRs(cidrs)
	nets := make([]*net.IPNet, 0, len(normalized))
	for _, cidr := range normalized {
		_, n, err := net.ParseCIDR(cidr)
		if err == nil {
			nets = append(nets, n)
		}
	}

	s.mu.Lock()
	s.fingerprint = fp
	s.nets = nets
	s.mu.Unlock()
	return nets
}

func NormalizeCIDRs(values []string) ([]string, error) {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, raw := range values {
		for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
			return r == '\n' || r == '\r' || r == ',' || r == ';' || r == '\t' || r == ' '
		}) {
			part = strings.TrimSpace(part)
			if part == "" || strings.HasPrefix(part, "#") {
				continue
			}
			cidr, err := normalizeCIDR(part)
			if err != nil {
				return nil, err
			}
			if _, ok := seen[cidr]; ok {
				continue
			}
			seen[cidr] = struct{}{}
			out = append(out, cidr)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i] < out[j]
	})
	return out, nil
}

func normalizeCIDR(value string) (string, error) {
	if !strings.Contains(value, "/") {
		ip := net.ParseIP(value)
		if ip == nil {
			return "", fmt.Errorf("invalid IP/CIDR %q", value)
		}
		if ip.To4() != nil {
			value += "/32"
		} else {
			value += "/128"
		}
	}
	ip, network, err := net.ParseCIDR(value)
	if err != nil {
		return "", fmt.Errorf("invalid IP/CIDR %q", value)
	}
	network.IP = ip.Mask(network.Mask)
	return network.String(), nil
}

func FetchIPRanges(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	v4, err := fetchIPRangeURL(ctx, IPv4ListURL)
	if err != nil {
		return nil, err
	}
	v6, err := fetchIPRangeURL(ctx, IPv6ListURL)
	if err != nil {
		return nil, err
	}
	ranges := append(v4, v6...)
	return NormalizeCIDRs(ranges)
}

func fetchIPRangeURL(ctx context.Context, url string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: status %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(body)), nil
}

func fingerprintCIDRs(cidrs []string) string {
	return strings.Join(cidrs, "\x00")
}

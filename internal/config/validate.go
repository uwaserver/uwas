package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Validate performs structural and semantic validation on a Config.
func Validate(cfg *Config) error {
	var errs []string

	// Global validation
	switch cfg.Global.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		errs = append(errs, fmt.Sprintf("invalid log_level: %q (must be debug, info, warn, error)", cfg.Global.LogLevel))
	}

	switch cfg.Global.LogFormat {
	case "json", "text", "clf":
	default:
		errs = append(errs, fmt.Sprintf("invalid log_format: %q (must be json, text, clf)", cfg.Global.LogFormat))
	}

	// Listen address validation
	if cfg.Global.HTTPListen != "" {
		validateListenAddr(cfg.Global.HTTPListen, "global.http_listen", &errs)
	}
	if cfg.Global.HTTPSListen != "" {
		validateListenAddr(cfg.Global.HTTPSListen, "global.https_listen", &errs)
	}
	if cfg.Global.Admin.Listen != "" {
		validateListenAddr(cfg.Global.Admin.Listen, "global.admin.listen", &errs)
	}
	if cfg.Global.MCP.Listen != "" {
		validateListenAddr(cfg.Global.MCP.Listen, "global.mcp.listen", &errs)
	}

	// Rate limit validation (global fallback)
	if cfg.Global.RateLimit.Requests != 0 || cfg.Global.RateLimit.Window.Duration != 0 {
		if cfg.Global.RateLimit.Requests <= 0 {
			errs = append(errs, "global.rate_limit.requests must be > 0 when rate limiting is configured")
		}
		if cfg.Global.RateLimit.Window.Duration <= 0 {
			errs = append(errs, "global.rate_limit.window must be > 0 when rate limiting is configured")
		}
	}

	// Trusted proxies validation (CIDR notation)
	for i, cidr := range cfg.Global.TrustedProxies {
		_, _, err := net.ParseCIDR(cidr)
		if err != nil {
			// Also allow plain IPs (not CIDR)
			if ip := net.ParseIP(cidr); ip == nil {
				errs = append(errs, fmt.Sprintf("global.trusted_proxies[%d]: invalid CIDR or IP %q: %v", i, cidr, err))
			}
		}
	}

	// ACME email validation
	if cfg.Global.ACME.Email != "" {
		if !strings.Contains(cfg.Global.ACME.Email, "@") {
			errs = append(errs, fmt.Sprintf("global.acme.email: invalid email %q (must contain @)", cfg.Global.ACME.Email))
		}
	}

	// Cache TTL validation
	if cfg.Global.Cache.DefaultTTL < 0 {
		errs = append(errs, fmt.Sprintf("global.cache.default_ttl: must be >= 0, got %d", cfg.Global.Cache.DefaultTTL))
	}
	if cfg.Global.Cache.GraceTTL < 0 {
		errs = append(errs, fmt.Sprintf("global.cache.grace_ttl: must be >= 0, got %d", cfg.Global.Cache.GraceTTL))
	}

	// Backup validation
	if cfg.Global.Backup.Enabled {
		switch cfg.Global.Backup.Provider {
		case "local", "s3", "sftp":
		default:
			errs = append(errs, fmt.Sprintf("global.backup.provider: invalid provider %q (must be local, s3, sftp)", cfg.Global.Backup.Provider))
		}

		if cfg.Global.Backup.Keep <= 0 {
			errs = append(errs, fmt.Sprintf("global.backup.keep: must be > 0, got %d", cfg.Global.Backup.Keep))
		}

		// S3-specific validation
		if cfg.Global.Backup.Provider == "s3" {
			if cfg.Global.Backup.S3.Bucket == "" {
				errs = append(errs, "global.backup.s3.bucket: required when provider is s3")
			}
		}

		// SFTP-specific validation
		if cfg.Global.Backup.Provider == "sftp" {
			if cfg.Global.Backup.SFTP.Host == "" {
				errs = append(errs, "global.backup.sftp.host: required when provider is sftp")
			}
			if cfg.Global.Backup.SFTP.User == "" {
				errs = append(errs, "global.backup.sftp.user: required when provider is sftp")
			}
		}
	}

	// Domain validation
	hosts := make(map[string]bool)
	for i, d := range cfg.Domains {
		prefix := fmt.Sprintf("domains[%d]", i)

		if d.Host == "" {
			errs = append(errs, fmt.Sprintf("%s: host is required", prefix))
			continue
		}

		if hosts[d.Host] {
			errs = append(errs, fmt.Sprintf("%s: duplicate host %q", prefix, d.Host))
		}
		hosts[d.Host] = true

		for _, alias := range d.Aliases {
			if hosts[alias] {
				errs = append(errs, fmt.Sprintf("%s: duplicate alias %q", prefix, alias))
			}
			hosts[alias] = true
		}

		switch d.Type {
		case "static", "php", "proxy", "app", "redirect":
		default:
			errs = append(errs, fmt.Sprintf("%s: invalid type %q (must be static, php, proxy, app, redirect)", prefix, d.Type))
		}

		switch d.SSL.Mode {
		case "auto", "manual", "off":
		default:
			errs = append(errs, fmt.Sprintf("%s: invalid ssl.mode %q (must be auto, manual, off)", prefix, d.SSL.Mode))
		}

		if d.SSL.Mode == "manual" {
			if d.SSL.Cert == "" {
				errs = append(errs, fmt.Sprintf("%s: ssl.cert required when mode is manual", prefix))
			} else {
				if _, err := os.Stat(d.SSL.Cert); err != nil {
					errs = append(errs, fmt.Sprintf("%s: ssl.cert file %q not found: %v", prefix, d.SSL.Cert, err))
				}
			}
			if d.SSL.Key == "" {
				errs = append(errs, fmt.Sprintf("%s: ssl.key required when mode is manual", prefix))
			} else {
				if _, err := os.Stat(d.SSL.Key); err != nil {
					errs = append(errs, fmt.Sprintf("%s: ssl.key file %q not found: %v", prefix, d.SSL.Key, err))
				}
			}
		}

		// TLS min_version validation
		if d.SSL.MinVersion != "" {
			switch d.SSL.MinVersion {
			case "1.0", "1.1", "1.2", "1.3":
			default:
				errs = append(errs, fmt.Sprintf("%s: invalid ssl.min_version %q (must be 1.0, 1.1, 1.2, 1.3)", prefix, d.SSL.MinVersion))
			}
		}

		// Root: auto-fill from web_root if empty, only error if still empty
		if (d.Type == "static" || d.Type == "php") && d.Root == "" {
			if cfg.Global.WebRoot != "" {
				cfg.Domains[i].Root = filepath.Join(cfg.Global.WebRoot, d.Host, "public_html")
			} else {
				errs = append(errs, fmt.Sprintf("%s: root is required for type %q", prefix, d.Type))
			}
		}

		// Proxy validation
		if d.Type == "proxy" {
			if len(d.Proxy.Upstreams) == 0 {
				errs = append(errs, fmt.Sprintf("%s: proxy.upstreams required for type proxy", prefix))
			} else {
				seen := make(map[string]bool)
				for j, u := range d.Proxy.Upstreams {
					uprefix := fmt.Sprintf("%s.proxy.upstreams[%d]", prefix, j)
					if u.Address == "" {
						errs = append(errs, fmt.Sprintf("%s: address is required", uprefix))
					} else {
						parsed, err := url.Parse(u.Address)
						if err != nil || parsed.Host == "" {
							errs = append(errs, fmt.Sprintf("%s: invalid URL %q", uprefix, u.Address))
						} else if isCloudMetadataHost(parsed.Hostname()) {
							errs = append(errs, fmt.Sprintf("%s: cloud metadata endpoint blocked (%s)", uprefix, parsed.Hostname()))
						}
						if seen[u.Address] {
							errs = append(errs, fmt.Sprintf("%s: duplicate upstream address %q", uprefix, u.Address))
						}
						seen[u.Address] = true
					}
					if u.Weight < 0 {
						errs = append(errs, fmt.Sprintf("%s: weight must be >= 0, got %d", uprefix, u.Weight))
					}
				}
			}

			// Proxy algorithm validation
			if d.Proxy.Algorithm != "" {
				switch d.Proxy.Algorithm {
				case "round-robin", "least-conn", "weighted", "random", "sticky":
				default:
					errs = append(errs, fmt.Sprintf("%s: invalid proxy.algorithm %q (must be round-robin, least-conn, weighted, random, sticky)", prefix, d.Proxy.Algorithm))
				}
			}

			// Canary weight validation
			if d.Proxy.Canary.Enabled {
				if d.Proxy.Canary.Weight < 0 || d.Proxy.Canary.Weight > 100 {
					errs = append(errs, fmt.Sprintf("%s: proxy.canary.weight must be 0-100, got %d", prefix, d.Proxy.Canary.Weight))
				}
			}

			// Mirror percent validation
			if d.Proxy.Mirror.Enabled {
				if d.Proxy.Mirror.Percent < 0 || d.Proxy.Mirror.Percent > 100 {
					errs = append(errs, fmt.Sprintf("%s: proxy.mirror.percent must be 0-100, got %d", prefix, d.Proxy.Mirror.Percent))
				}
			}
		}

		// Redirect validation
		if d.Type == "redirect" {
			if d.Redirect.Target == "" {
				errs = append(errs, fmt.Sprintf("%s: redirect.target required for type redirect", prefix))
			}
			if d.Redirect.Status != 0 {
				switch d.Redirect.Status {
				case 301, 302, 307, 308:
				default:
					errs = append(errs, fmt.Sprintf("%s: invalid redirect.status %d (must be 301, 302, 307, 308)", prefix, d.Redirect.Status))
				}
			}
		}

		// Rate limit validation
		if d.Security.RateLimit.Requests != 0 || d.Security.RateLimit.Window.Duration != 0 {
			if d.Security.RateLimit.Requests <= 0 {
				errs = append(errs, fmt.Sprintf("%s: security.rate_limit.requests must be > 0 when rate limiting is configured, got %d", prefix, d.Security.RateLimit.Requests))
			}
			if d.Security.RateLimit.Window.Duration <= 0 {
				errs = append(errs, fmt.Sprintf("%s: security.rate_limit.window must be > 0 when rate limiting is configured", prefix))
			}
		}

		// Rewrite rules validation (regex must compile)
		for j, rw := range d.Rewrites {
			if rw.Match != "" {
				if _, err := regexp.Compile(rw.Match); err != nil {
					errs = append(errs, fmt.Sprintf("%s.rewrites[%d]: invalid regex in match %q: %v", prefix, j, rw.Match, err))
				}
			}
		}

		// Compression algorithm validation
		if d.Compression.Enabled {
			for j, alg := range d.Compression.Algorithms {
				switch alg {
				case "gzip", "br":
				default:
					errs = append(errs, fmt.Sprintf("%s.compression.algorithms[%d]: invalid algorithm %q (must be gzip, br)", prefix, j, alg))
				}
			}
		}

		// Image optimization format validation
		if d.ImageOptimization.Enabled {
			for j, f := range d.ImageOptimization.Formats {
				switch f {
				case "webp", "avif":
				default:
					errs = append(errs, fmt.Sprintf("%s.image_optimization.formats[%d]: invalid format %q (must be webp, avif)", prefix, j, f))
				}
			}
		}

		// Domain-level cache TTL validation
		if d.Cache.TTL < 0 {
			errs = append(errs, fmt.Sprintf("%s: cache.ttl must be >= 0, got %d", prefix, d.Cache.TTL))
		}
		for j, rule := range d.Cache.Rules {
			if rule.TTL < 0 {
				errs = append(errs, fmt.Sprintf("%s.cache.rules[%d]: ttl must be >= 0, got %d", prefix, j, rule.TTL))
			}
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("config validation failed:\n  %s", strings.Join(errs, "\n  "))
	}
	return nil
}

// validateListenAddr checks that a listen address is a valid host:port with port in range.
func validateListenAddr(addr, field string, errs *[]string) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("%s: invalid listen address %q: %v", field, addr, err))
		return
	}
	// Host can be empty (meaning all interfaces), but if specified must be valid
	if host != "" {
		if ip := net.ParseIP(host); ip == nil {
			// Not an IP; could be a hostname - just do a basic sanity check
			if strings.ContainsAny(host, " \t/\\") {
				*errs = append(*errs, fmt.Sprintf("%s: invalid host in address %q", field, addr))
			}
		}
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("%s: invalid port in address %q: %v", field, addr, err))
		return
	}
	if port < 1 || port > 65535 {
		*errs = append(*errs, fmt.Sprintf("%s: port must be 1-65535, got %d", field, port))
	}
}

// isCloudMetadataHost returns true for cloud provider metadata endpoints
// that should never be used as proxy upstreams (SSRF prevention).
func isCloudMetadataHost(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	// AWS/GCP/Azure instance metadata
	return ip.Equal(net.ParseIP("169.254.169.254")) ||
		ip.Equal(net.ParseIP("fd00:ec2::254"))
}

var loopbackIPBlocks = []*net.IPNet{
	mustParseCIDR("127.0.0.0/8"),
	mustParseCIDR("::1/128"),
}

var privateIPBlocks = []*net.IPNet{
	mustParseCIDR("10.0.0.0/8"),
	mustParseCIDR("172.16.0.0/12"),
	mustParseCIDR("192.168.0.0/16"),
	mustParseCIDR("fc00::/7"),
}

var cloudMetadataIPBlocks = []*net.IPNet{
	mustParseCIDR("169.254.169.254/32"),
	mustParseCIDR("fd00:ec2::254/128"),
}

var linkLocalIPBlocks = []*net.IPNet{
	mustParseCIDR("169.254.0.0/16"),
	mustParseCIDR("fe80::/10"),
}

var documentationIPBlocks = []*net.IPNet{
	mustParseCIDR("192.0.2.0/24"),
	mustParseCIDR("198.51.100.0/24"),
	mustParseCIDR("203.0.2.0/24"),
}

// blockedIPBlocks contains CIDR ranges that must never be reached by external
// callbacks such as webhooks (SSRF prevention). Order matters: more specific
// ranges first.
var blockedIPBlocks = concatIPBlocks(
	cloudMetadataIPBlocks,
	loopbackIPBlocks,
	privateIPBlocks,
	linkLocalIPBlocks,
	documentationIPBlocks,
)

func concatIPBlocks(groups ...[]*net.IPNet) []*net.IPNet {
	var total int
	for _, group := range groups {
		total += len(group)
	}
	blocks := make([]*net.IPNet, 0, total)
	for _, group := range groups {
		blocks = append(blocks, group...)
	}
	return blocks
}

func mustParseCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic("config: invalid CIDR " + s + ": " + err.Error())
	}
	return n
}

func isIPBlocked(ip net.IP) bool {
	for _, block := range blockedIPBlocks {
		if block.Contains(ip) {
			return true
		}
	}
	return false
}

type urlSafetyPolicy struct {
	allowLoopback bool
	allowPrivate  bool
}

func ipBlockedReason(ip net.IP, policy urlSafetyPolicy) string {
	if inAnyIPBlock(ip, cloudMetadataIPBlocks) {
		return "cloud metadata endpoint"
	}
	if inAnyIPBlock(ip, loopbackIPBlocks) && !policy.allowLoopback {
		return "loopback address"
	}
	if inAnyIPBlock(ip, privateIPBlocks) && !policy.allowPrivate {
		return "private address"
	}
	if inAnyIPBlock(ip, linkLocalIPBlocks) {
		return "link-local address"
	}
	if inAnyIPBlock(ip, documentationIPBlocks) {
		return "documentation address"
	}
	return ""
}

func inAnyIPBlock(ip net.IP, blocks []*net.IPNet) bool {
	for _, block := range blocks {
		if block.Contains(ip) {
			return true
		}
	}
	return false
}

// isLoopback returns true if the host is a loopback address (localhost, 127.0.0.0/8, ::1).
// This is used to allow test servers that run on loopback addresses.
func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// IsLoopback returns true if the rawURL points to a loopback address.
// This is safe to call for test URLs that use httptest.NewServer on localhost.
// It does NOT perform DNS resolution, so it only detects literal loopback IPs.
func IsLoopback(rawURL string) bool {
	if rawURL == "" {
		return false
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return isLoopback(u.Hostname())
}

// IsWebhookURLSafe checks whether a URL resolves to an address that external
// callbacks must not reach, including loopback, private, link-local, metadata,
// and documentation ranges.
func IsWebhookURLSafe(rawURL string) error {
	return isURLSafe(rawURL, urlSafetyPolicy{})
}

// IsProxyUpstreamSafe checks whether a reverse proxy upstream is safe when
// private upstreams are disabled. Loopback is allowed because UWAS commonly
// proxies local application processes, but private/link-local/metadata ranges
// remain blocked unless the domain explicitly enables private upstreams.
func IsProxyUpstreamSafe(rawURL string) error {
	return isURLSafe(rawURL, urlSafetyPolicy{allowLoopback: true})
}

// IsPrivateProxyUpstreamSafe allows private upstream addresses for domains that
// explicitly opt into them, while still blocking metadata, link-local, and
// documentation ranges.
func IsPrivateProxyUpstreamSafe(rawURL string) error {
	return isURLSafe(rawURL, urlSafetyPolicy{allowLoopback: true, allowPrivate: true})
}

// IsSSRFSafe is kept for older callers. New code should use IsWebhookURLSafe
// or IsProxyUpstreamSafe so the intended trust boundary is explicit.
func IsSSRFSafe(rawURL string) error {
	return IsWebhookURLSafe(rawURL)
}

func isURLSafe(rawURL string, policy urlSafetyPolicy) error {
	if rawURL == "" {
		return nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("URL has no host")
	}

	// Check if the hostname itself is a blocked IP
	if ip := net.ParseIP(host); ip != nil {
		if reason := ipBlockedReason(ip, policy); reason != "" {
			return fmt.Errorf("URL host %q is a blocked %s (SSRF)", host, reason)
		}
		return nil
	}

	// For hostnames (not IPs), check if it's a known loopback name before DNS lookup.
	if policy.allowLoopback && isLoopback(host) {
		return nil
	}

	// Resolve the hostname and check all resolved IPs
	ips, err := net.LookupIP(host)
	if err != nil {
		// Cannot resolve — allow it; DNS may be temporarily unavailable.
		// The actual HTTP request will fail anyway if the host is unreachable.
		return nil
	}
	for _, ip := range ips {
		if reason := ipBlockedReason(ip, policy); reason != "" {
			return fmt.Errorf("URL host %q resolves to blocked %s %q (SSRF)", host, reason, ip.String())
		}
	}
	return nil
}

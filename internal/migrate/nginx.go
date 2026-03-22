// Package migrate converts third-party web server configs to UWAS domain YAML.
package migrate

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/uwaserver/uwas/internal/config"
)

// NginxConfig holds parsed data from a single Nginx server block.
type NginxConfig struct {
	ServerNames []string // from server_name directive
	Root        string
	ListenPorts []string // raw listen values: "80", "443 ssl", etc.
	SSLCert     string
	SSLKey      string
	IndexFiles  []string
	Locations   []NginxLocation
	ProxyPass   string // server-level proxy_pass
	Return      string // server-level return directive (e.g. "301 https://example.com$request_uri")
	TryFiles    []string
}

// NginxLocation holds parsed data from a location block.
type NginxLocation struct {
	Path       string
	Modifier   string // ~, ~*, =, or empty
	ProxyPass  string
	FastCGI    string // fastcgi_pass value
	TryFiles   []string
	Return     string
}

// ParseNginx reads Nginx config from a reader and returns parsed server blocks.
// This is a pragmatic line-by-line parser that handles common patterns, not a
// full Nginx config parser.
func ParseNginx(reader io.Reader) ([]NginxConfig, error) {
	scanner := bufio.NewScanner(reader)
	var configs []NginxConfig
	var current *NginxConfig
	var currentLoc *NginxLocation

	inServer := false
	inLocation := false
	braceDepth := 0

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip comments and empty lines
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		openBraces := strings.Count(line, "{")
		closeBraces := strings.Count(line, "}")

		// Detect server block start
		if !inServer && strings.HasPrefix(line, "server") && strings.Contains(line, "{") {
			inServer = true
			braceDepth = 1
			cfg := NginxConfig{}
			current = &cfg
			continue
		}

		if !inServer {
			// Outside any server block — skip
			continue
		}

		// Update brace depth
		braceDepth += openBraces - closeBraces

		// End of server block
		if braceDepth <= 0 {
			inServer = false
			inLocation = false
			if current != nil {
				configs = append(configs, *current)
				current = nil
			}
			continue
		}

		// Detect location block
		if !inLocation && strings.HasPrefix(line, "location") && strings.Contains(line, "{") {
			inLocation = true
			mod, path := extractLocationModifierAndPath(line)
			loc := NginxLocation{Path: path, Modifier: mod}
			currentLoc = &loc
			continue
		}

		// Inside location block
		if inLocation {
			if closeBraces > 0 && openBraces == 0 {
				// End of location block
				inLocation = false
				if current != nil && currentLoc != nil {
					current.Locations = append(current.Locations, *currentLoc)
				}
				currentLoc = nil
				continue
			}

			if currentLoc != nil {
				if val := extractDirective(line, "proxy_pass"); val != "" {
					currentLoc.ProxyPass = val
				}
				if val := extractDirective(line, "fastcgi_pass"); val != "" {
					currentLoc.FastCGI = val
				}
				if val := extractDirective(line, "try_files"); val != "" {
					currentLoc.TryFiles = strings.Fields(val)
				}
				if val := extractDirective(line, "return"); val != "" {
					currentLoc.Return = val
				}
			}
			continue
		}

		// Server-level directives
		if val := extractDirective(line, "server_name"); val != "" {
			current.ServerNames = strings.Fields(val)
		}
		if val := extractDirective(line, "root"); val != "" {
			current.Root = val
		}
		if val := extractDirective(line, "listen"); val != "" {
			current.ListenPorts = append(current.ListenPorts, val)
		}
		if val := extractDirective(line, "index"); val != "" {
			current.IndexFiles = strings.Fields(val)
		}
		// ssl_certificate_key must be checked before ssl_certificate to avoid
		// false match (ssl_certificate is a prefix of ssl_certificate_key).
		if val := extractDirective(line, "ssl_certificate_key"); val != "" {
			current.SSLKey = val
		} else if val := extractDirective(line, "ssl_certificate"); val != "" {
			current.SSLCert = val
		}
		if val := extractDirective(line, "proxy_pass"); val != "" {
			current.ProxyPass = val
		}
		if val := extractDirective(line, "return"); val != "" {
			current.Return = val
		}
		if val := extractDirective(line, "try_files"); val != "" {
			current.TryFiles = strings.Fields(val)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading nginx config: %w", err)
	}

	return configs, nil
}

// ConvertToUWAS converts parsed NginxConfig blocks to UWAS Domain configs.
func ConvertToUWAS(configs []NginxConfig) []config.Domain {
	domains := make([]config.Domain, 0, len(configs))

	for _, nc := range configs {
		d := config.Domain{}

		// Host and aliases
		if len(nc.ServerNames) > 0 {
			first := nc.ServerNames[0]
			if first == "_" {
				d.Host = "example.com"
			} else {
				d.Host = first
			}
			if len(nc.ServerNames) > 1 {
				d.Aliases = nc.ServerNames[1:]
			}
		} else {
			d.Host = "example.com"
		}

		// Root
		d.Root = nc.Root

		// Index files
		if len(nc.IndexFiles) > 0 {
			d.IndexFiles = nc.IndexFiles
		}

		// SSL
		hasSSLListen := false
		for _, lp := range nc.ListenPorts {
			if strings.Contains(lp, "ssl") || strings.Contains(lp, "443") {
				hasSSLListen = true
				break
			}
		}

		if nc.SSLCert != "" {
			d.SSL = config.SSLConfig{
				Mode: "manual",
				Cert: nc.SSLCert,
				Key:  nc.SSLKey,
			}
		} else if hasSSLListen {
			d.SSL = config.SSLConfig{
				Mode: "auto",
			}
		}

		// Determine type based on directives
		domainType := determineDomainType(nc)
		d.Type = domainType

		switch domainType {
		case "php":
			d.PHP = buildPHPConfig(nc)
		case "proxy":
			d.Proxy = buildProxyConfig(nc)
		case "redirect":
			d.Redirect = buildRedirectConfig(nc)
		}

		// try_files / SPA mode
		tryFiles := collectTryFiles(nc)
		if len(tryFiles) > 0 {
			// Detect SPA mode: try_files ending with /index.php or /index.html
			// (possibly with query string like /index.php?$args)
			last := tryFiles[len(tryFiles)-1]
			baseLast := strings.SplitN(last, "?", 2)[0]
			if baseLast == "/index.html" || baseLast == "/index.php" {
				d.SPAMode = true
			}
			d.TryFiles = tryFiles
		}

		domains = append(domains, d)
	}

	return domains
}

// NginxToYAML is a convenience function that parses Nginx config, converts to
// UWAS domains, and marshals to minimal, clean YAML (no zero-value fields).
func NginxToYAML(reader io.Reader) (string, error) {
	configs, err := ParseNginx(reader)
	if err != nil {
		return "", err
	}
	if len(configs) == 0 {
		return "", fmt.Errorf("no server blocks found")
	}

	domains := ConvertToUWAS(configs)
	return domainsToYAML(domains), nil
}

// domainsToYAML generates clean, minimal YAML output for converted domains.
func domainsToYAML(domains []config.Domain) string {
	var b strings.Builder
	b.WriteString("# Converted from Nginx config by UWAS\n")
	b.WriteString("domains:\n")

	for _, d := range domains {
		fmt.Fprintf(&b, "  - host: %s\n", d.Host)
		fmt.Fprintf(&b, "    type: %s\n", d.Type)
		if d.Root != "" {
			fmt.Fprintf(&b, "    root: %s\n", d.Root)
		}
		if len(d.Aliases) > 0 {
			b.WriteString("    aliases:\n")
			for _, a := range d.Aliases {
				fmt.Fprintf(&b, "      - %s\n", a)
			}
		}
		if len(d.IndexFiles) > 0 {
			b.WriteString("    index_files:\n")
			for _, f := range d.IndexFiles {
				fmt.Fprintf(&b, "      - %s\n", f)
			}
		}
		if d.SSL.Mode != "" {
			b.WriteString("    ssl:\n")
			fmt.Fprintf(&b, "      mode: %s\n", d.SSL.Mode)
			if d.SSL.Cert != "" {
				fmt.Fprintf(&b, "      cert: %s\n", d.SSL.Cert)
			}
			if d.SSL.Key != "" {
				fmt.Fprintf(&b, "      key: %s\n", d.SSL.Key)
			}
		}
		if d.Type == "php" && d.PHP.FPMAddress != "" {
			b.WriteString("    php:\n")
			fmt.Fprintf(&b, "      fpm_address: %s\n", d.PHP.FPMAddress)
			if len(d.PHP.IndexFiles) > 0 {
				b.WriteString("      index_files:\n")
				for _, f := range d.PHP.IndexFiles {
					fmt.Fprintf(&b, "        - %s\n", f)
				}
			}
		}
		if d.Type == "proxy" && len(d.Proxy.Upstreams) > 0 {
			b.WriteString("    proxy:\n")
			b.WriteString("      upstreams:\n")
			for _, u := range d.Proxy.Upstreams {
				fmt.Fprintf(&b, "        - address: %s\n", u.Address)
			}
		}
		if d.Type == "redirect" && d.Redirect.Target != "" {
			b.WriteString("    redirect:\n")
			fmt.Fprintf(&b, "      target: %s\n", d.Redirect.Target)
			if d.Redirect.Status > 0 {
				fmt.Fprintf(&b, "      status: %d\n", d.Redirect.Status)
			}
			if d.Redirect.PreservePath {
				b.WriteString("      preserve_path: true\n")
			}
		}
		if d.SPAMode {
			b.WriteString("    spa_mode: true\n")
		}
		if len(d.TryFiles) > 0 {
			b.WriteString("    try_files:\n")
			for _, f := range d.TryFiles {
				fmt.Fprintf(&b, "      - %s\n", f)
			}
		}
		b.WriteString("\n")
	}

	return b.String()
}

// --- internal helpers ---

// extractDirective extracts the value of a simple Nginx directive.
// E.g., extractDirective("server_name example.com;", "server_name") => "example.com"
func extractDirective(line, directive string) string {
	line = strings.TrimSpace(line)
	prefix := directive + " "
	if !strings.HasPrefix(line, prefix) {
		return ""
	}
	val := strings.TrimPrefix(line, prefix)
	val = strings.TrimSuffix(val, ";")
	return strings.TrimSpace(val)
}

var locationRegexp = regexp.MustCompile(`^location\s+(?:(~\*?|=)\s+)?(\S+)\s*\{`)

func extractLocationModifierAndPath(line string) (modifier, path string) {
	m := locationRegexp.FindStringSubmatch(line)
	if len(m) >= 3 {
		return m[1], m[2]
	}
	return "", "/"
}

// determineDomainType inspects the parsed Nginx config and returns the UWAS
// domain type: "php", "proxy", "redirect", or "static".
func determineDomainType(nc NginxConfig) string {
	// Check for redirect (return 301/302)
	if nc.Return != "" {
		code, _ := parseReturnDirective(nc.Return)
		if code == 301 || code == 302 {
			return "redirect"
		}
	}

	// Check for proxy_pass at server level
	if nc.ProxyPass != "" {
		return "proxy"
	}

	// Check locations for PHP / proxy / redirect indicators
	hasPHP := false
	hasProxy := false
	hasRedirectLoc := false

	for _, loc := range nc.Locations {
		if loc.FastCGI != "" {
			hasPHP = true
		}
		if loc.Modifier == "~" && strings.Contains(loc.Path, `\.php`) {
			hasPHP = true
		}
		if loc.ProxyPass != "" {
			hasProxy = true
		}
		if loc.Return != "" {
			code, _ := parseReturnDirective(loc.Return)
			if code == 301 || code == 302 {
				hasRedirectLoc = true
			}
		}
	}

	// Also check index files for PHP hint
	for _, idx := range nc.IndexFiles {
		if strings.HasSuffix(idx, ".php") {
			hasPHP = true
			break
		}
	}

	if hasPHP {
		return "php"
	}
	if hasProxy {
		return "proxy"
	}
	if hasRedirectLoc && !hasPHP && !hasProxy {
		// Only mark as redirect if there is nothing else going on
		return "redirect"
	}
	return "static"
}

// parseReturnDirective parses "301 https://..." into status code and target.
func parseReturnDirective(val string) (int, string) {
	parts := strings.Fields(val)
	if len(parts) < 2 {
		return 0, ""
	}
	var code int
	if _, err := fmt.Sscanf(parts[0], "%d", &code); err != nil {
		return 0, ""
	}
	return code, parts[1]
}

func buildPHPConfig(nc NginxConfig) config.PHPConfig {
	php := config.PHPConfig{}

	// Find FPM address from fastcgi_pass in locations
	for _, loc := range nc.Locations {
		if loc.FastCGI != "" {
			php.FPMAddress = loc.FastCGI
			break
		}
	}

	// Gather PHP index files
	var phpIdx []string
	for _, idx := range nc.IndexFiles {
		if strings.HasSuffix(idx, ".php") {
			phpIdx = append(phpIdx, idx)
		}
	}
	if len(phpIdx) > 0 {
		php.IndexFiles = phpIdx
	}

	return php
}

func buildProxyConfig(nc NginxConfig) config.ProxyConfig {
	proxy := config.ProxyConfig{}

	// Collect all proxy_pass targets
	seen := make(map[string]bool)
	addUpstream := func(addr string) {
		if addr != "" && !seen[addr] {
			seen[addr] = true
			proxy.Upstreams = append(proxy.Upstreams, config.Upstream{
				Address: addr,
				Weight:  1,
			})
		}
	}

	if nc.ProxyPass != "" {
		addUpstream(nc.ProxyPass)
	}
	for _, loc := range nc.Locations {
		if loc.ProxyPass != "" {
			addUpstream(loc.ProxyPass)
		}
	}

	return proxy
}

func buildRedirectConfig(nc NginxConfig) config.RedirectConfig {
	redirect := config.RedirectConfig{}

	// Check server-level return first
	if nc.Return != "" {
		code, target := parseReturnDirective(nc.Return)
		if code == 301 || code == 302 {
			redirect.Status = code
			redirect.Target = target
			// If the target contains $request_uri or $uri, preserve path
			if strings.Contains(target, "$request_uri") || strings.Contains(target, "$uri") {
				redirect.PreservePath = true
				// Clean the target of nginx variables for UWAS
				redirect.Target = strings.ReplaceAll(redirect.Target, "$request_uri", "")
				redirect.Target = strings.ReplaceAll(redirect.Target, "$uri", "")
			}
			return redirect
		}
	}

	// Check location-level returns
	for _, loc := range nc.Locations {
		if loc.Return != "" {
			code, target := parseReturnDirective(loc.Return)
			if code == 301 || code == 302 {
				redirect.Status = code
				redirect.Target = target
				if strings.Contains(target, "$request_uri") || strings.Contains(target, "$uri") {
					redirect.PreservePath = true
					redirect.Target = strings.ReplaceAll(redirect.Target, "$request_uri", "")
					redirect.Target = strings.ReplaceAll(redirect.Target, "$uri", "")
				}
				return redirect
			}
		}
	}

	return redirect
}

// collectTryFiles gathers try_files from server level and location blocks.
func collectTryFiles(nc NginxConfig) []string {
	// Prefer server-level try_files
	if len(nc.TryFiles) > 0 {
		return nc.TryFiles
	}
	// Fall back to the root location's try_files
	for _, loc := range nc.Locations {
		if loc.Path == "/" && len(loc.TryFiles) > 0 {
			return loc.TryFiles
		}
	}
	return nil
}

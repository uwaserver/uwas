// Package migrate converts third-party web server configs to UWAS domain YAML.
package migrate

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

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
	// Warnings lists access-control directives the converter cannot express;
	// they are dropped from the output, so the operator must be told (F8900).
	Warnings []string

	// upstreams holds the "upstream NAME { … }" groups defined in the file, so
	// a "proxy_pass http://NAME" resolves to the group's servers (F40601).
	upstreams map[string]nginxUpstreamGroup
}

// nginxUpstreamGroup is one parsed "upstream" block: the servers that take
// traffic (down and backup servers are left out) and its balancing method.
type nginxUpstreamGroup struct {
	Servers   []nginxUpstreamServer
	Algorithm string
}

type nginxUpstreamServer struct {
	Address string
	Weight  int
}

// NginxLocation holds parsed data from a location block.
type NginxLocation struct {
	Path      string
	Modifier  string // ~, ~*, =, or empty
	ProxyPass string
	FastCGI   string // fastcgi_pass value
	TryFiles  []string
	Return    string
	DenyAll   bool   // "deny all;" - the path must not be served
	Root      string // root inside the location (F42004)
	Alias     string // alias inside the location (F42004)
	Warnings  []string
}

// ParseNginx reads Nginx config from a reader and returns parsed server blocks.
// This is a pragmatic line-by-line parser that handles common patterns, not a
// full Nginx config parser.
func ParseNginx(reader io.Reader) ([]NginxConfig, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("reading nginx config: %w", err)
	}
	groups := parseNginxUpstreams(string(data))
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	var configs []NginxConfig
	var current *NginxConfig
	var currentLoc *NginxLocation

	inServer := false
	inLocation := false
	braceDepth := 0
	ifDepth := 0 // >0 while inside an `if (...) { … }` block

	for scanner.Scan() {
		line := strings.TrimSpace(stripNginxComment(scanner.Text()))

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
			// A one-line block ("server { listen 80; return 301 …; }") closes
			// itself on this line. Parse the directives before the closing
			// brace and complete the block here, or the block never closed:
			// every following line merged into it, later servers were
			// absorbed, and the incomplete config was dropped at EOF (the
			// same-line shape F8602 fixed for location blocks).
			if closeBraces > 0 {
				if start, end := strings.Index(line, "{"), strings.LastIndex(line, "}"); end > start {
					for _, part := range strings.Split(line[start+1:end], ";") {
						parseNginxServerDirective(current, part)
					}
				}
				configs = append(configs, *current)
				inServer = false
				current = nil
			}
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
			// A location still open here means the line closed BOTH blocks
			// at once ("…; } }").
			serverLevel := !inLocation
			inServer = false
			inLocation = false
			if current != nil {
				if serverLevel {
					// The server's last directive may share the closing-brace
					// line ("try_files $uri /index.html; }") — parse what
					// precedes the brace before closing, or it was silently
					// dropped (the same-line shapes F8602 and the location-end
					// fix covered for location blocks).
					if end := strings.Index(line, "}"); end > 0 {
						for _, part := range strings.Split(line[:end], ";") {
							parseNginxServerDirective(current, part)
						}
					}
				} else if currentLoc != nil {
					// The pending location's directives precede its own
					// closing brace on this line — parse and flush it, or
					// the whole location was silently lost.
					if end := strings.Index(line, "}"); end > 0 {
						for _, part := range strings.Split(line[:end], ";") {
							parseNginxLocationDirective(currentLoc, part)
						}
					}
					current.Locations = append(current.Locations, *currentLoc)
					current.Warnings = append(current.Warnings, locationWarnings(*currentLoc)...)
					currentLoc = nil
				}
				configs = append(configs, *current)
				current = nil
			}
			continue
		}

		// An `if (...) { … }` block is conditional: its directives (typically
		// `return 301 …`) must not be read as the server's own, which turned
		// the whole site into a redirect to itself (F54602). Skip the block
		// and say so.
		if ifDepth > 0 {
			ifDepth += openBraces - closeBraces
			continue
		}
		if nginxIfLine(line) && openBraces > 0 {
			if current != nil {
				current.Warnings = append(current.Warnings, "if block \""+line+"\" is conditional and was not converted")
			}
			if openBraces > closeBraces {
				ifDepth = openBraces - closeBraces
			}
			continue
		}

		// Detect location block
		if !inLocation && strings.HasPrefix(line, "location") && strings.Contains(line, "{") {
			mod, path := extractLocationModifierAndPath(line)
			loc := NginxLocation{Path: path, Modifier: mod}
			if closeBraces > 0 {
				// A one-line block ("location = /f { access_log off; }") is
				// complete here; staying "inside" it swallowed every
				// following server directive and location (F8602).
				if end := strings.LastIndex(line, "}"); end > strings.Index(line, "{") {
					for _, part := range strings.Split(line[strings.Index(line, "{")+1:end], ";") {
						parseNginxLocationDirective(&loc, part)
					}
				}
				if current != nil {
					current.Locations = append(current.Locations, loc)
					current.Warnings = append(current.Warnings, locationWarnings(loc)...)
				}
				continue
			}
			inLocation = true
			currentLoc = &loc
			continue
		}

		// Inside location block
		if inLocation {
			if closeBraces > 0 && openBraces == 0 {
				// The block's last directive may share the closing-brace line
				// ("proxy_pass http://backend; }", "deny all; }") — parse what
				// precedes the brace before closing, or that directive was
				// silently dropped (the same-line shape F8602 fixed for the
				// block's start).
				if end := strings.Index(line, "}"); end > 0 && currentLoc != nil {
					for _, part := range strings.Split(line[:end], ";") {
						parseNginxLocationDirective(currentLoc, part)
					}
				}
				// End of location block
				inLocation = false
				if current != nil && currentLoc != nil {
					current.Locations = append(current.Locations, *currentLoc)
					current.Warnings = append(current.Warnings, locationWarnings(*currentLoc)...)
				}
				currentLoc = nil
				continue
			}

			if currentLoc != nil {
				parseNginxLocationDirective(currentLoc, line)
			}
			continue
		}

		// Server-level directives
		parseNginxServerDirective(current, line)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading nginx config: %w", err)
	}

	if len(groups) > 0 {
		for i := range configs {
			configs[i].upstreams = groups
		}
	}
	return configs, nil
}

// ConvertToUWAS converts parsed NginxConfig blocks to UWAS Domain configs.
func ConvertToUWAS(configs []NginxConfig) []config.Domain {
	domains := make([]config.Domain, 0, len(configs))
	placeholders := 0

	for _, nc := range configs {
		d := config.Domain{}

		// Host and aliases. A catch-all or nameless server gets a placeholder
		// host; each one needs its own, or config validation rejects the file
		// for the duplicate (F41102).
		if len(nc.ServerNames) > 0 && nc.ServerNames[0] != "_" {
			d.Host = nc.ServerNames[0]
			if len(nc.ServerNames) > 1 {
				d.Aliases = nc.ServerNames[1:]
			}
		} else {
			placeholders++
			d.Host = nginxPlaceholderHost(placeholders)
			if len(nc.ServerNames) > 1 {
				d.Aliases = nc.ServerNames[1:]
			}
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

		// Determine type based on directives. A site that keeps its own content
		// and proxies only some paths stays static/php with path-scoped
		// proxies (F40602).
		domainType := determineDomainType(nc)
		if locs, rest, ok := pathScopedProxies(nc); ok {
			if t := determineDomainType(rest); t == "static" || t == "php" {
				domainType = t
				d.Locations = locs
			}
		}
		d.Type = domainType
		if domainType != "redirect" {
			for _, loc := range nc.Locations {
				if lc, ok, _ := locationRedirectConfig(loc); ok {
					d.Locations = append(d.Locations, lc)
				}
			}
			for _, loc := range nc.Locations {
				if lc, ok, _ := locationRootConfig(loc); ok {
					d.Locations = append(d.Locations, lc)
				}
			}
		}

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
			// Detect SPA mode: try_files ending with /index.html. A /index.php
			// front controller is not SPA mode: spa_mode makes the static
			// handler fall back to /index.html and ignore try_files (F3800).
			last := tryFiles[len(tryFiles)-1]
			baseLast := strings.SplitN(last, "?", 2)[0]
			if baseLast == "/index.html" {
				d.SPAMode = true
			}
			d.TryFiles = tryFiles
		}

		// "location /private/ { deny all; }": nothing under the path may be
		// served. blocked_paths is a substring match, so a prefix, exact or ^~
		// path carries over; a regex location has no equivalent and is not
		// translated.
		for _, loc := range nc.Locations {
			if loc.DenyAll && loc.Modifier != "~" && loc.Modifier != "~*" && loc.Path != "" && loc.Path != "/" {
				d.Security.BlockedPaths = append(d.Security.BlockedPaths, loc.Path)
			}
		}

		domains = append(domains, d)
	}

	return dropShadowedHTTPSRedirects(domains)
}

// pathScopedProxies handles "root …; location /api/ { proxy_pass … }": a
// server with its own content whose proxied locations are all plain prefix
// paths. It returns the path-scoped proxies and the server with those proxy
// targets removed, so the domain type follows what is left (static or php).
// ok is false for anything else (a proxied "/" or regex location, a
// server-level proxy_pass, no root): those stay whole-domain proxies.
func pathScopedProxies(nc NginxConfig) (locs []config.LocationConfig, rest NginxConfig, ok bool) {
	if nc.Root == "" || nc.ProxyPass != "" {
		return nil, nc, false
	}
	rest = nc
	rest.Locations = make([]NginxLocation, len(nc.Locations))
	copy(rest.Locations, nc.Locations)
	for i, loc := range nc.Locations {
		if loc.ProxyPass == "" {
			continue
		}
		lc, found := proxyLocationConfig(nc, loc)
		if !found {
			return nil, nc, false
		}
		locs = append(locs, lc)
		rest.Locations[i].ProxyPass = ""
	}
	return locs, rest, len(locs) > 0
}

// nginxLocationMatch turns a location's modifier and path into a UWAS location
// match: a prefix path as is, an exact "= /x" as the anchored regex ~^/x$, and a
// regex ("~" / "~*") with the same marker UWAS uses. isPrefix reports a plain
// prefix path; ok is false for a catch-all "/" (it names the whole site) and
// for anything that is not a path.
func nginxLocationMatch(loc NginxLocation) (match string, isPrefix, ok bool) {
	switch loc.Modifier {
	case "", "^~":
		if !strings.HasPrefix(loc.Path, "/") || loc.Path == "/" {
			return "", false, false
		}
		return loc.Path, true, true
	case "=":
		if !strings.HasPrefix(loc.Path, "/") {
			return "", false, false
		}
		return "~^" + regexp.QuoteMeta(loc.Path) + "$", false, true
	case "~", "~*":
		if loc.Path == "" {
			return "", false, false
		}
		return loc.Modifier + loc.Path, false, true
	}
	return "", false, false
}

// proxyLocationConfig builds the path-scoped proxy of one location. A regex or
// exact location cannot carry a proxy_pass URI (nginx forbids it for regex and
// UWAS strips only prefixes), so those must name a bare backend.
func proxyLocationConfig(nc NginxConfig, loc NginxLocation) (config.LocationConfig, bool) {
	match, isPrefix, ok := nginxLocationMatch(loc)
	if !ok {
		return config.LocationConfig{}, false
	}
	target, strip := locationProxyTarget(nc, loc.ProxyPass)
	if !isPrefix && strip {
		return config.LocationConfig{}, false
	}
	return config.LocationConfig{Match: match, ProxyPass: target, StripPrefix: strip}, true
}

// locationRootConfig turns "location /static/ { root /var/www/app; }" or an
// alias location into a path-scoped static root. UWAS strips the matched
// prefix of a prefix location before joining it to the location root, which is
// nginx's alias semantics; nginx's root appends the whole URI, so the prefix is
// added to the root. A regex or exact location keeps the full path in UWAS, so
// a root maps directly. An alias on a regex or exact location needs the
// capture group and cannot be carried; the third result says so (F42004).
func locationRootConfig(loc NginxLocation) (lc config.LocationConfig, ok bool, unsupported string) {
	if loc.ProxyPass != "" || loc.Return != "" || loc.FastCGI != "" || (loc.Root == "" && loc.Alias == "") {
		return lc, false, ""
	}
	match, isPrefix, matchOK := nginxLocationMatch(loc)
	if !matchOK {
		return lc, false, ""
	}
	switch {
	case loc.Alias != "" && !isPrefix:
		return lc, false, "location " + loc.Modifier + " " + loc.Path + " uses alias, which cannot be carried for a regex or exact location; not migrated"
	case loc.Alias != "":
		return config.LocationConfig{Match: match, Root: strings.TrimRight(loc.Alias, "/")}, true, ""
	case isPrefix:
		return config.LocationConfig{Match: match, Root: strings.TrimRight(loc.Root, "/") + strings.TrimSuffix(loc.Path, "/")}, true, ""
	}
	return config.LocationConfig{Match: match, Root: loc.Root}, true, ""
}

// locationRedirectConfig turns "location /old/ { return 301 /new/; }" into a
// path-scoped redirect. Only a location that is not the catch-all can be one
// (the catch-all makes the whole site a redirect). The target must be fixed:
// one that uses a request variable cannot be carried, and the third result
// says so, so the caller can report it (F41100).
func locationRedirectConfig(loc NginxLocation) (lc config.LocationConfig, ok bool, unsupported string) {
	if loc.Return == "" {
		return lc, false, ""
	}
	code, target := parseReturnDirective(loc.Return)
	switch code {
	case 301, 302, 307, 308:
	default:
		return lc, false, ""
	}
	match, _, found := nginxLocationMatch(loc)
	if !found {
		return lc, false, "" // the catch-all: the site itself is the redirect
	}
	if strings.Contains(target, "$") {
		return lc, false, "location " + loc.Path + ": return " + strings.TrimSpace(loc.Return)
	}
	return config.LocationConfig{Match: match, Redirect: target, RedirectCode: code}, true, ""
}

// locationProxyTarget converts a location's proxy_pass to the UWAS form: the
// base URL the request path is appended to, and whether the location prefix is
// stripped first. nginx replaces the matched prefix by the URI part of
// proxy_pass ("http://h/" or "http://h/app/") and passes the request path
// unchanged when there is none. A name that is an upstream group resolves to
// its first server (a location proxy has a single backend).
func locationProxyTarget(nc NginxConfig, raw string) (target string, strip bool) {
	scheme, rest, ok := strings.Cut(raw, "://")
	if !ok {
		return raw, false
	}
	host, uri := rest, ""
	if i := strings.Index(rest, "/"); i >= 0 {
		host, uri = rest[:i], rest[i:]
	}
	if g, found := nc.upstreams[host]; found && len(g.Servers) > 0 {
		host = g.Servers[0].Address
	}
	if uri == "" {
		return scheme + "://" + host, false
	}
	return scheme + "://" + host + strings.TrimRight(uri, "/"), true
}

// dropShadowedHTTPSRedirects removes the plain-HTTP "return 301 https://..."
// server of a site whose HTTPS server is also migrated: two domains with one
// host fail config validation, and a domain with TLS already redirects HTTP
// to HTTPS (F8604).
func dropShadowedHTTPSRedirects(domains []config.Domain) []config.Domain {
	served := map[string]bool{}
	for _, d := range domains {
		if d.Type != "redirect" {
			served[strings.ToLower(d.Host)] = true
		}
	}
	out := domains[:0:0]
	for _, d := range domains {
		if d.Type == "redirect" && served[strings.ToLower(d.Host)] &&
			strings.HasPrefix(strings.ToLower(d.Redirect.Target), "https://") {
			continue
		}
		out = append(out, d)
	}
	return out
}

// nginxPlaceholderHost is the host given to the n-th server that has no usable
// server_name.
func nginxPlaceholderHost(n int) string {
	if n <= 1 {
		return "example.com"
	}
	return fmt.Sprintf("example-%d.com", n)
}

// NginxToYAML is a convenience function that parses Nginx config, converts to
// UWAS domains, and marshals to minimal, clean YAML (no zero-value fields).
func NginxToYAML(reader io.Reader) (string, error) {
	yaml, warnings, err := NginxToYAMLWithWarnings(reader)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, w := range warnings {
		b.WriteString("# WARNING: not migrated, the site is NOT protected by it: " + w + "\n")
	}
	return b.String() + yaml, nil
}

// NginxToYAMLWithWarnings is NginxToYAML plus the access-control directives
// that could not be translated and were dropped (F8900). Such a site is more
// open after migration than before, so callers must show the list.
func NginxToYAMLWithWarnings(reader io.Reader) (string, []string, error) {
	configs, err := ParseNginx(reader)
	if err != nil {
		return "", nil, err
	}
	if len(configs) == 0 {
		return "", nil, fmt.Errorf("no server blocks found")
	}

	var warnings []string
	placeholders := 0
	for _, nc := range configs {
		name := "_"
		if len(nc.ServerNames) > 0 {
			name = nc.ServerNames[0]
		}
		for _, w := range nc.Warnings {
			warnings = append(warnings, name+": "+w)
		}
		for _, loc := range nc.Locations {
			if _, _, unsupported := locationRedirectConfig(loc); unsupported != "" {
				warnings = append(warnings, name+": "+unsupported)
			}
			if _, _, unsupported := locationRootConfig(loc); unsupported != "" {
				warnings = append(warnings, name+": "+unsupported)
			}
		}
		if name == "_" {
			placeholders++
			warnings = append(warnings, name+": no usable server_name; the host was set to the placeholder "+nginxPlaceholderHost(placeholders)+", edit it")
		}
	}
	domains := ConvertToUWAS(configs)
	return domainsToYAML(domains), warnings, nil
}

// domainsToYAML generates clean, minimal YAML output for converted domains.
func domainsToYAML(domains []config.Domain) string {
	var b strings.Builder
	b.WriteString("# Converted from Nginx config by UWAS\n")
	b.WriteString("domains:\n")

	for _, d := range domains {
		fmt.Fprintf(&b, "  - host: %s\n", yamlScalar(d.Host))
		fmt.Fprintf(&b, "    type: %s\n", yamlScalar(d.Type))
		if d.Root != "" {
			fmt.Fprintf(&b, "    root: %s\n", yamlScalar(d.Root))
		}
		if len(d.Aliases) > 0 {
			b.WriteString("    aliases:\n")
			for _, a := range d.Aliases {
				fmt.Fprintf(&b, "      - %s\n", yamlScalar(a))
			}
		}
		if len(d.IndexFiles) > 0 {
			b.WriteString("    index_files:\n")
			for _, f := range d.IndexFiles {
				fmt.Fprintf(&b, "      - %s\n", yamlScalar(f))
			}
		}
		if d.SSL.Mode != "" {
			b.WriteString("    ssl:\n")
			fmt.Fprintf(&b, "      mode: %s\n", yamlScalar(d.SSL.Mode))
			if d.SSL.Cert != "" {
				fmt.Fprintf(&b, "      cert: %s\n", yamlScalar(d.SSL.Cert))
			}
			if d.SSL.Key != "" {
				fmt.Fprintf(&b, "      key: %s\n", yamlScalar(d.SSL.Key))
			}
		}
		if d.Type == "php" && d.PHP.FPMAddress != "" {
			b.WriteString("    php:\n")
			fmt.Fprintf(&b, "      fpm_address: %s\n", yamlScalar(d.PHP.FPMAddress))
			if len(d.PHP.IndexFiles) > 0 {
				b.WriteString("      index_files:\n")
				for _, f := range d.PHP.IndexFiles {
					fmt.Fprintf(&b, "        - %s\n", yamlScalar(f))
				}
			}
		}
		if d.Type == "proxy" && len(d.Proxy.Upstreams) > 0 {
			b.WriteString("    proxy:\n")
			if d.Proxy.Algorithm != "" {
				fmt.Fprintf(&b, "      algorithm: %s\n", yamlScalar(d.Proxy.Algorithm))
			}
			b.WriteString("      upstreams:\n")
			for _, u := range d.Proxy.Upstreams {
				fmt.Fprintf(&b, "        - address: %s\n", yamlScalar(u.Address))
				if u.Weight > 1 {
					fmt.Fprintf(&b, "          weight: %d\n", u.Weight)
				}
			}
		}
		if d.Type == "redirect" && d.Redirect.Target != "" {
			b.WriteString("    redirect:\n")
			fmt.Fprintf(&b, "      target: %s\n", yamlScalar(d.Redirect.Target))
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
				fmt.Fprintf(&b, "      - %s\n", yamlScalar(f))
			}
		}
		if len(d.Locations) > 0 {
			b.WriteString("    locations:\n")
			for _, l := range d.Locations {
				fmt.Fprintf(&b, "      - match: %s\n", yamlScalar(l.Match))
				if l.ProxyPass != "" {
					fmt.Fprintf(&b, "        proxy_pass: %s\n", yamlScalar(l.ProxyPass))
				}
				if l.StripPrefix {
					b.WriteString("        strip_prefix: true\n")
				}
				if l.Root != "" {
					fmt.Fprintf(&b, "        root: %s\n", yamlScalar(l.Root))
				}
				if l.Redirect != "" {
					fmt.Fprintf(&b, "        redirect: %s\n", yamlScalar(l.Redirect))
					fmt.Fprintf(&b, "        redirect_code: %d\n", l.RedirectCode)
				}
			}
		}
		if len(d.Security.BlockedPaths) > 0 {
			b.WriteString("    security:\n      blocked_paths:\n")
			for _, p := range d.Security.BlockedPaths {
				fmt.Fprintf(&b, "        - %s\n", yamlScalar(p))
			}
		}
		b.WriteString("\n")
	}

	return b.String()
}

// yamlScalar returns s as a YAML scalar that parses back to exactly s. A
// value is written plain only when it round-trips as the same string; anything
// YAML would read differently (a leading "*" alias marker as in a wildcard
// server_name, a " #" comment, ": ", quotes, true/123/null) is written
// double-quoted, whose escapes are a superset of Go's strconv quoting.
func yamlScalar(s string) string {
	if s != "" && !strings.ContainsAny(s, "\"'\\\t\r\n") {
		var m map[string]any
		if yaml.Unmarshal([]byte("v: "+s), &m) == nil {
			if v, ok := m["v"].(string); ok && v == s {
				return s
			}
		}
	}
	return strconv.Quote(s)
}

// YAMLScalar is yamlScalar for converters outside this package (F8310).
func YAMLScalar(s string) string { return yamlScalar(s) }

// --- internal helpers ---

var nginxUpstreamStart = regexp.MustCompile(`^upstream\s+(\S+)\s*\{(.*)$`)

// parseNginxUpstreams collects the "upstream NAME { server …; }" blocks of a
// config (at any nesting, one-line or multi-line). A server marked down or
// backup, and a unix: socket (the UWAS proxy dials TCP), is not carried over.
func parseNginxUpstreams(text string) map[string]nginxUpstreamGroup {
	groups := map[string]nginxUpstreamGroup{}
	cur := ""
	var g nginxUpstreamGroup
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := strings.TrimSpace(stripNginxComment(sc.Text()))
		if cur == "" {
			m := nginxUpstreamStart.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			cur = nginxUnquote(m[1])
			g = nginxUpstreamGroup{}
			line = m[2]
		}
		closed := false
		if i := strings.Index(line, "}"); i >= 0 {
			line, closed = line[:i], true
		}
		for _, part := range strings.Split(line, ";") {
			f := strings.Fields(part)
			if len(f) == 0 {
				continue
			}
			switch f[0] {
			case "least_conn":
				g.Algorithm = "least-conn"
			case "ip_hash":
				g.Algorithm = "ip-hash"
			case "server":
				if srv, ok := parseNginxUpstreamServer(f[1:]); ok {
					g.Servers = append(g.Servers, srv)
				}
			}
		}
		if closed {
			groups[cur] = g
			cur = ""
		}
	}
	return groups
}

func parseNginxUpstreamServer(f []string) (nginxUpstreamServer, bool) {
	if len(f) == 0 {
		return nginxUpstreamServer{}, false
	}
	srv := nginxUpstreamServer{Address: nginxUnquote(f[0]), Weight: 1}
	if strings.HasPrefix(srv.Address, "unix:") {
		return nginxUpstreamServer{}, false
	}
	for _, p := range f[1:] {
		switch {
		case p == "down" || p == "backup":
			return nginxUpstreamServer{}, false
		case strings.HasPrefix(p, "weight="):
			if w, err := strconv.Atoi(strings.TrimPrefix(p, "weight=")); err == nil && w > 0 {
				srv.Weight = w
			}
		}
	}
	return srv, true
}

// nginxUnquote strips one pair of matching quotes from an nginx argument. A
// quote that is not the first and last character of the value is data.
func nginxUnquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		q := string(s[0])
		return strings.ReplaceAll(s[1:len(s)-1], "\\"+q, q)
	}
	return s
}

func unquoteAll(fields []string) []string {
	for i, f := range fields {
		fields[i] = nginxUnquote(f)
	}
	return fields
}

// parseNginxServerDirective records the server-level directives that the
// converter understands, plus the access-control directives it must warn
// about. Used by the main loop and by one-line server blocks.
// nginxIfLine reports whether a line opens an `if (...)` block.
func nginxIfLine(line string) bool {
	if !strings.HasPrefix(line, "if") {
		return false
	}
	return strings.HasPrefix(strings.TrimLeft(line[2:], " \t"), "(")
}

func parseNginxServerDirective(cfg *NginxConfig, line string) {
	if w := untranslatedAccessDirective(line); w != "" {
		cfg.Warnings = append(cfg.Warnings, "server "+w)
	}
	if val := extractDirective(line, "server_name"); val != "" {
		cfg.ServerNames = cfg.ServerNames[:0]
		for _, n := range unquoteAll(strings.Fields(val)) {
			// A regex name can never match as a literal host or alias (F54603).
			if strings.HasPrefix(n, "~") {
				cfg.Warnings = append(cfg.Warnings, "server_name regex "+n+" is not supported and was skipped")
				continue
			}
			cfg.ServerNames = append(cfg.ServerNames, n)
		}
	}
	if val := extractDirective(line, "include"); val != "" {
		cfg.Warnings = append(cfg.Warnings, "include "+val+" is not followed; directives from it were not converted")
	}
	if val := extractDirective(line, "root"); val != "" {
		cfg.Root = nginxUnquote(val)
	}
	if val := extractDirective(line, "listen"); val != "" {
		cfg.ListenPorts = append(cfg.ListenPorts, val)
	}
	if val := extractDirective(line, "index"); val != "" {
		cfg.IndexFiles = unquoteAll(strings.Fields(val))
	}
	// ssl_certificate_key must be checked before ssl_certificate to avoid
	// false match (ssl_certificate is a prefix of ssl_certificate_key).
	if val := extractDirective(line, "ssl_certificate_key"); val != "" {
		cfg.SSLKey = nginxUnquote(val)
	} else if val := extractDirective(line, "ssl_certificate"); val != "" {
		cfg.SSLCert = nginxUnquote(val)
	}
	if val := extractDirective(line, "proxy_pass"); val != "" {
		cfg.ProxyPass = nginxUnquote(val)
	}
	if val := extractDirective(line, "return"); val != "" {
		cfg.Return = val
	}
	if val := extractDirective(line, "try_files"); val != "" {
		cfg.TryFiles = unquoteAll(strings.Fields(val))
	}
}

// parseNginxLocationDirective records the directives of a location block that
// the converter understands.
func parseNginxLocationDirective(loc *NginxLocation, line string) {
	if w := untranslatedAccessDirective(line); w != "" {
		loc.Warnings = append(loc.Warnings, w)
	}
	if val := extractDirective(line, "proxy_pass"); val != "" {
		loc.ProxyPass = nginxUnquote(val)
	}
	if val := extractDirective(line, "fastcgi_pass"); val != "" {
		loc.FastCGI = nginxUnquote(val)
	}
	if val := extractDirective(line, "try_files"); val != "" {
		loc.TryFiles = unquoteAll(strings.Fields(val))
	}
	if val := extractDirective(line, "return"); val != "" {
		loc.Return = val
	}
	if val := extractDirective(line, "deny"); nginxUnquote(val) == "all" {
		loc.DenyAll = true
	}
	if val := extractDirective(line, "root"); val != "" {
		loc.Root = nginxUnquote(val)
	}
	if val := extractDirective(line, "alias"); val != "" {
		loc.Alias = nginxUnquote(val)
	}
}

// untranslatedAccessDirective names an access-control directive that has no
// UWAS equivalent in the migrated config (auth_basic, allow/deny lists,
// auth_request, satisfy, internal). It returns "" for anything else. "deny all"
// is judged separately: a prefix location carries over as blocked_paths.
func untranslatedAccessDirective(line string) string {
	line = strings.TrimSpace(line)
	for _, d := range []string{"auth_basic_user_file", "auth_basic", "auth_request", "satisfy", "allow"} {
		if val := extractDirective(line, d); val != "" {
			if d == "auth_basic" && val == "off" {
				return ""
			}
			return d + " " + val
		}
	}
	if val := extractDirective(line, "deny"); val != "" && val != "all" {
		return "deny " + val
	}
	if strings.TrimSuffix(line, ";") == "internal" {
		return "internal"
	}
	return ""
}

// locationWarnings reports what a location block asks for that is not carried
// over: its untranslated access directives and a "deny all" the converter
// cannot turn into blocked_paths (a regex location, or the site root).
func locationWarnings(loc NginxLocation) []string {
	var out []string
	for _, w := range loc.Warnings {
		out = append(out, fmt.Sprintf("location %s: %s", loc.Path, w))
	}
	if loc.DenyAll && (loc.Modifier == "~" || loc.Modifier == "~*" || loc.Path == "" || loc.Path == "/") {
		out = append(out, fmt.Sprintf("location %s %s: deny all", loc.Modifier, loc.Path))
	}
	return out
}

// stripNginxComment drops a trailing "# comment" from a config line. Nginx
// starts a comment at a "#" that begins a token (line start or after
// whitespace or ";"), outside quotes; "#" inside a value such as a URL
// fragment stays. Without this "listen 80; # http" kept "; # http" in the
// value, and a "}" or "{" inside a comment shifted the block depth.
func stripNginxComment(line string) string {
	var quote byte
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '#' && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t' || line[i-1] == ';'):
			return line[:i]
		}
	}
	return line
}

// extractDirective extracts the value of a simple Nginx directive.
// E.g., extractDirective("server_name example.com;", "server_name") => "example.com"
func extractDirective(line, directive string) string {
	line = strings.TrimSpace(line)
	val, ok := strings.CutPrefix(line, directive)
	if !ok || len(val) == 0 || (val[0] != ' ' && val[0] != '\t') {
		return ""
	}
	val = strings.TrimSuffix(val, ";")
	return strings.TrimSpace(val)
}

var locationRegexp = regexp.MustCompile(`^location\s+(?:(~\*?|=|\^~)\s+)?(\S+)\s*\{`)

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
		if loc.Return != "" && (loc.Modifier == "" || loc.Modifier == "^~") && loc.Path == "/" {
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
	return code, nginxUnquote(parts[1])
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
	addUpstream := func(addr string, weight int) {
		if addr != "" && !seen[addr] {
			seen[addr] = true
			proxy.Upstreams = append(proxy.Upstreams, config.Upstream{
				Address: addr,
				Weight:  weight,
			})
		}
	}
	// A proxy_pass naming an "upstream" block expands to the block's servers
	// (F40601); anything else is a direct address and stays as written.
	add := func(target string) {
		scheme, rest, ok := strings.Cut(target, "://")
		if ok {
			host, suffix := rest, ""
			if i := strings.Index(rest, "/"); i >= 0 {
				host, suffix = rest[:i], rest[i:]
			}
			if g, found := nc.upstreams[host]; found && len(g.Servers) > 0 {
				for _, srv := range g.Servers {
					addUpstream(scheme+"://"+srv.Address+suffix, srv.Weight)
				}
				if proxy.Algorithm == "" {
					proxy.Algorithm = g.Algorithm
				}
				return
			}
		}
		addUpstream(target, 1)
	}

	if nc.ProxyPass != "" {
		add(nc.ProxyPass)
	}
	for _, loc := range nc.Locations {
		if loc.ProxyPass != "" {
			add(loc.ProxyPass)
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
